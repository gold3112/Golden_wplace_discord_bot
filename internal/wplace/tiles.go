package wplace

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"golden_wplace_discord_bot/internal/utils"
)

const tileCacheTTL = 2 * time.Minute

var tileHTTPClient = &http.Client{
	Timeout: 12 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   32,
		MaxConnsPerHost:       32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type tileCacheEntry struct {
	data      []byte
	expiresAt time.Time
}

var tileCache struct {
	mu    sync.RWMutex
	items map[string]tileCacheEntry
}

var (
	tileURLFormat string
	urlFormatMu   sync.RWMutex
)

func init() {
	tileCache.items = make(map[string]tileCacheEntry)
	detectTileURLFormat()
}

func detectTileURLFormat() {
	formats := []string{
		"https://backend.wplace.live/tile/%d/%d.png",
		"https://backend.wplace.live/files/s0/tiles/%d/%d.png",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, format := range formats {
		testURL := fmt.Sprintf(format, 0, 0)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, testURL, nil)
		if err != nil {
			continue
		}

		resp, err := tileHTTPClient.Do(req)
		if err != nil {
			log.Printf("Tile URL format test failed for %s: %v", format, err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			tileURLFormat = format
			log.Printf("✅ Detected working tile URL format: %s", format)
			return
		}
		log.Printf("Tile URL format %s returned status %d", format, resp.StatusCode)
	}

	tileURLFormat = formats[0]
	log.Printf("⚠️ No working tile URL format detected, using default: %s", tileURLFormat)
}

func GetTileURLFormat() string {
	urlFormatMu.RLock()
	defer urlFormatMu.RUnlock()
	return tileURLFormat
}

func DownloadTile(ctx context.Context, limiter *utils.RateLimiter, tileX, tileY int) ([]byte, error) {
	return downloadTile(ctx, limiter, tileX, tileY, true)
}

func DownloadTileNoCache(ctx context.Context, limiter *utils.RateLimiter, tileX, tileY int) ([]byte, error) {
	return downloadTile(ctx, limiter, tileX, tileY, false)
}

func downloadTile(ctx context.Context, limiter *utils.RateLimiter, tileX, tileY int, useCache bool) ([]byte, error) {
	cacheBust := time.Now().UnixNano() % 10000000
	urlFormatMu.RLock()
	format := tileURLFormat
	urlFormatMu.RUnlock()
	url := fmt.Sprintf(format+"?t=%d", tileX, tileY, cacheBust)
	cacheKey := fmt.Sprintf("%d-%d", tileX, tileY)
	if useCache {
		if data, ok := getTileFromCache(cacheKey); ok {
			return data, nil
		}
	}

	doReq := func() (interface{}, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		resp, err := tileHTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("HTTP GET failed for %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("failed to download tile %d-%d (URL: %s), status: %s", tileX, tileY, url, resp.Status)
		}
		return io.ReadAll(resp.Body)
	}

	var (
		val interface{}
		err error
	)
	if limiter != nil {
		val, err = limiter.Do(ctx, "backend.wplace.live", doReq)
	} else {
		val, err = doReq()
	}
	if err != nil {
		return nil, err
	}
	data, ok := val.([]byte)
	if !ok {
		return nil, fmt.Errorf("unexpected response type for tile %d-%d", tileX, tileY)
	}
	if useCache && len(data) > 0 {
		storeTileCache(cacheKey, data)
	}
	return data, nil
}

func DownloadTilesGrid(
	ctx context.Context,
	limiter *utils.RateLimiter,
	minX, minY, cols, rows, maxConcurrent int,
) ([][]byte, error) {
	return downloadTilesGrid(ctx, limiter, minX, minY, cols, rows, maxConcurrent, true)
}

func DownloadTilesGridNoCache(
	ctx context.Context,
	limiter *utils.RateLimiter,
	minX, minY, cols, rows, maxConcurrent int,
) ([][]byte, error) {
	return downloadTilesGrid(ctx, limiter, minX, minY, cols, rows, maxConcurrent, false)
}

func downloadTilesGrid(
	ctx context.Context,
	limiter *utils.RateLimiter,
	minX, minY, cols, rows, maxConcurrent int,
	useCache bool,
) ([][]byte, error) {
	if cols <= 0 || rows <= 0 {
		return nil, fmt.Errorf("invalid grid size: %dx%d", cols, rows)
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}

	total := cols * rows
	tiles := make([][]byte, total)
	var mu sync.Mutex
	var firstErr error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type tileJob struct {
		tileX int
		tileY int
		index int
	}

	workers := maxConcurrent
	if workers > total {
		workers = total
	}

	jobs := make(chan tileJob, total)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for job := range jobs {
			data, err := downloadTile(ctx, limiter, job.tileX, job.tileY, useCache)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				return
			}
			mu.Lock()
			tiles[job.index] = data
			mu.Unlock()
		}
	}

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker()
	}

	index := 0
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			jobs <- tileJob{
				tileX: minX + x,
				tileY: minY + y,
				index: index,
			}
			index++
		}
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	return tiles, nil
}

func CombineTilesCroppedImage(tiles [][]byte, cols, rows, cropX, cropY, cropWidth, cropHeight int) (image.Image, error) {
	if len(tiles) != cols*rows {
		return nil, fmt.Errorf("tile count mismatch: have %d, expected %d", len(tiles), cols*rows)
	}

	tileSize := 256
	fullWidth := cols * tileSize
	fullHeight := rows * tileSize

	fullImg := image.NewRGBA(image.Rect(0, 0, fullWidth, fullHeight))

	for idx, data := range tiles {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to decode tile %d: %w", idx, err)
		}

		x := (idx % cols) * tileSize
		y := (idx / cols) * tileSize
		rect := image.Rect(x, y, x+tileSize, y+tileSize)
		draw.Draw(fullImg, rect, img, image.Point{}, draw.Src)
	}

	cropRect := image.Rect(cropX, cropY, cropX+cropWidth, cropY+cropHeight)
	if !cropRect.In(fullImg.Bounds()) {
		return nil, fmt.Errorf("crop rect %v out of bounds %v", cropRect, fullImg.Bounds())
	}

	cropped := image.NewRGBA(image.Rect(0, 0, cropWidth, cropHeight))
	draw.Draw(cropped, cropped.Bounds(), fullImg, image.Point{X: cropX, Y: cropY}, draw.Src)
	return cropped, nil
}

func getTileFromCache(key string) ([]byte, bool) {
	tileCache.mu.RLock()
	entry, ok := tileCache.items[key]
	tileCache.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		tileCache.mu.Lock()
		delete(tileCache.items, key)
		tileCache.mu.Unlock()
		return nil, false
	}
	return entry.data, true
}

func storeTileCache(key string, data []byte) {
	tileCache.mu.Lock()
	tileCache.items[key] = tileCacheEntry{data: data, expiresAt: time.Now().Add(tileCacheTTL)}
	tileCache.mu.Unlock()
}
