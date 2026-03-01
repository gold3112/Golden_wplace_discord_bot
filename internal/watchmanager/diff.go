package watchmanager

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	_ "image/gif"
	_ "image/jpeg"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
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
	if tilesX*tilesY > models.MaxWatchTiles {
		return nil, fmt.Errorf("watch covers too many tiles (%d > %d)", tilesX*tilesY, models.MaxWatchTiles)
	}

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

	maskedLive := applyTemplateAlphaMask(templateImg, liveImg)
	diffPixels, diffMask := buildDiffMask(templateImg, liveImg)

	livePNG, err := encodePNG(maskedLive)
	if err != nil {
		return nil, err
	}
	diffPNG, err := encodePNG(diffMask)
	if err != nil {
		return nil, err
	}
	previewPNG, err := buildCombinedPreview(maskedLive, diffMask)
	if err != nil {
		return nil, err
	}

	diffPercent := 0.0
	if opaqueCount > 0 {
		diffPercent = float64(diffPixels) * 100 / float64(opaqueCount)
	}

	center := utils.WatchAreaCenter(coord, width, height)
	zoom := utils.ZoomFromImageSize(width, height)
	snapshot := utils.BuildWplaceURL(center.Lng, center.Lat, zoom)
	fullsize := fmt.Sprintf("%d-%d-%d-%d-%d-%d", coord.TileX, coord.TileY, coord.PixelX, coord.PixelY, width, height)

	return &wplace.Result{
		DiffPixels:     diffPixels,
		DiffPercentage: diffPercent,
		SnapshotURL:    snapshot,
		LivePNG:        livePNG,
		DiffPNG:        diffPNG,
		PreviewPNG:     previewPNG,
		TemplateWidth:  width,
		TemplateHeight: height,
		TemplateOpaque: opaqueCount,
		FullsizeKey:    fullsize,
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

func applyTemplateAlphaMask(templateImg *image.NRGBA, live *image.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(templateImg.Bounds())
	if live == nil {
		return out
	}
	for y := 0; y < templateImg.Bounds().Dy(); y++ {
		for x := 0; x < templateImg.Bounds().Dx(); x++ {
			ti := y*templateImg.Stride + x*4
			if templateImg.Pix[ti+3] == 0 {
				continue
			}
			li := y*live.Stride + x*4
			oi := y*out.Stride + x*4
			out.Pix[oi] = live.Pix[li]
			out.Pix[oi+1] = live.Pix[li+1]
			out.Pix[oi+2] = live.Pix[li+2]
			out.Pix[oi+3] = 255
		}
	}
	return out
}

func buildDiffMask(templateImg *image.NRGBA, live *image.NRGBA) (int, *image.NRGBA) {
	mask := image.NewNRGBA(templateImg.Bounds())
	if live == nil || live.Bounds() != templateImg.Bounds() {
		fillOpaqueMask(mask, templateImg)
		return countOpaque(templateImg), mask
	}
	diff := 0
	diffColor := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	for y := 0; y < templateImg.Bounds().Dy(); y++ {
		for x := 0; x < templateImg.Bounds().Dx(); x++ {
			ti := y*templateImg.Stride + x*4
			if templateImg.Pix[ti+3] == 0 {
				continue
			}
			li := y*live.Stride + x*4
			if templateImg.Pix[ti] != live.Pix[li] || templateImg.Pix[ti+1] != live.Pix[li+1] || templateImg.Pix[ti+2] != live.Pix[li+2] {
				mask.SetNRGBA(x, y, diffColor)
				diff++
			}
		}
	}
	return diff, mask
}

func fillOpaqueMask(mask *image.NRGBA, templateImg *image.NRGBA) {
	diffColor := color.NRGBA{R: 255, G: 0, B: 0, A: 255}
	for y := 0; y < templateImg.Bounds().Dy(); y++ {
		for x := 0; x < templateImg.Bounds().Dx(); x++ {
			idx := y*templateImg.Stride + x*4 + 3
			if templateImg.Pix[idx] != 0 {
				mask.SetNRGBA(x, y, diffColor)
			}
		}
	}
}

func buildCombinedPreview(live image.Image, diff image.Image) ([]byte, error) {
	lw := live.Bounds().Dx()
	lh := live.Bounds().Dy()
	o := image.NewNRGBA(image.Rect(0, 0, lw*2, lh))
	draw.Draw(o, image.Rect(0, 0, lw, lh), live, live.Bounds().Min, draw.Src)
	draw.Draw(o, image.Rect(lw, 0, lw*2, lh), diff, diff.Bounds().Min, draw.Src)
	return encodePNG(o)
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
