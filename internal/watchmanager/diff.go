package watchmanager

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"os"
	"time"

	"golden_wplace_discord_bot/internal/models"
	"golden_wplace_discord_bot/internal/utils"
	"golden_wplace_discord_bot/internal/wplace"
)

func (m *Manager) evaluateWatch(watch *models.Watch) (*wplace.Result, error) {
	if watch == nil {
		return nil, fmt.Errorf("watch is nil")
	}
	if watch.Template == "" {
		return nil, fmt.Errorf("template not set")
	}
	coord, err := utils.ParseOrigin(watch.Origin)
	if err != nil {
		return nil, err
	}
	templatePath := m.storage.GetTemplateImagePath(watch.GuildID, watch.Template)
	templateImg, opaqueCount, err := loadTemplateNRGBA(templatePath)
	if err != nil {
		return nil, err
	}

	width := templateImg.Bounds().Dx()
	height := templateImg.Bounds().Dy()
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("template dimensions invalid")
	}

	startTileX := coord.TileX + coord.PixelX/utils.WplaceTileSize
	startTileY := coord.TileY + coord.PixelY/utils.WplaceTileSize
	startPixelX := coord.PixelX % utils.WplaceTileSize
	startPixelY := coord.PixelY % utils.WplaceTileSize
	endPixelX := startPixelX + width
	endPixelY := startPixelY + height
	tilesX := (endPixelX + utils.WplaceTileSize - 1) / utils.WplaceTileSize
	tilesY := (endPixelY + utils.WplaceTileSize - 1) / utils.WplaceTileSize
	if startTileX < 0 || startTileY < 0 || startTileX+tilesX-1 >= utils.WplaceTilesPerEdge || startTileY+tilesY-1 >= utils.WplaceTilesPerEdge {
		return nil, fmt.Errorf("origin out of range: %s", watch.Origin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tiles, err := wplace.DownloadTilesGridNoCache(ctx, m.limiter, startTileX, startTileY, tilesX, tilesY, 8)
	if err != nil {
		return nil, err
	}
	cropRect := image.Rect(startPixelX, startPixelY, startPixelX+width, startPixelY+height)
	liveImg, err := wplace.CombineTilesCroppedImage(tiles, utils.WplaceTileSize, utils.WplaceTileSize, tilesX, tilesY, cropRect)
	if err != nil {
		return nil, err
	}

	diffPixels := compareImages(templateImg, liveImg)
	diffPercent := 0.0
	if opaqueCount > 0 {
		diffPercent = float64(diffPixels) * 100 / float64(opaqueCount)
	}

	center := utils.WatchAreaCenter(coord, width, height)
	zoom := utils.ZoomFromImageSize(width, height)
	snapshot := utils.BuildWplaceURL(center.Lng, center.Lat, zoom)

	return &wplace.Result{
		DiffPixels:     diffPixels,
		DiffPercentage: diffPercent,
		SnapshotURL:    snapshot,
	}, nil
}

func loadTemplateNRGBA(path string) (*image.NRGBA, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("template not found: %s", path)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to decode template: %w", err)
	}
	nrgba := toNRGBA(img)
	return nrgba, countOpaque(nrgba), nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out
}

func countOpaque(img *image.NRGBA) int {
	if img == nil {
		return 0
	}
	count := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			idx := y*img.Stride + x*4 + 3
			if img.Pix[idx] != 0 {
				count++
			}
		}
	}
	return count
}

func compareImages(templateImg, live *image.NRGBA) int {
	if templateImg == nil || live == nil {
		return 0
	}
	if templateImg.Bounds() != live.Bounds() {
		return countOpaque(templateImg)
	}
	diff := 0
	for y := 0; y < templateImg.Bounds().Dy(); y++ {
		for x := 0; x < templateImg.Bounds().Dx(); x++ {
			ti := y*templateImg.Stride + x*4
			if templateImg.Pix[ti+3] == 0 {
				continue
			}
			li := y*live.Stride + x*4
			if templateImg.Pix[ti] != live.Pix[li] || templateImg.Pix[ti+1] != live.Pix[li+1] || templateImg.Pix[ti+2] != live.Pix[li+2] {
				diff++
			}
		}
	}
	return diff
}
