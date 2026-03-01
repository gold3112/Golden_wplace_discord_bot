package utils

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	// WplaceZoom base zoom level used to compute tile count.
	WplaceZoom = 11
	// WplaceTileSize is the number of pixels per tile on Wplace.
	WplaceTileSize = 1000
	// WplaceTilesPerEdge is 2^zoom.
	WplaceTilesPerEdge = 1 << WplaceZoom // 2048
	// WplaceHighDetailZoom centers on a single pixel for deep links.
	WplaceHighDetailZoom = 21.17
)

// Coordinate represents a tile/pixel coordinate in Wplace.
type Coordinate struct {
	TileX  int
	TileY  int
	PixelX int
	PixelY int
}

// LngLat represents a longitude/latitude pair.
type LngLat struct {
	Lng float64
	Lat float64
}

// ParseOrigin parses "tileX-tileY-pixelX-pixelY" strings.
func ParseOrigin(value string) (*Coordinate, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid origin format: %s", value)
	}
	vals := make([]int, 4)
	for i, part := range parts {
		v, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid origin value: %s", value)
		}
		vals[i] = v
	}
	return &Coordinate{TileX: vals[0], TileY: vals[1], PixelX: vals[2], PixelY: vals[3]}, nil
}

// BuildWplaceURL builds a wplace.live link centered at lng/lat/zoom.
func BuildWplaceURL(lng, lat, zoom float64) string {
	return fmt.Sprintf("https://wplace.live/?lat=%.6f&lng=%.6f&zoom=%.2f", lat, lng, zoom)
}

// TilePixelCenterToLngLat converts tile/pixel to lng/lat at pixel center.
func TilePixelCenterToLngLat(tileX, tileY, pixelX, pixelY int) *LngLat {
	n := float64(WplaceTilesPerEdge)
	x := float64(tileX) + (float64(pixelX)+0.5)/float64(WplaceTileSize)
	y := float64(tileY) + (float64(pixelY)+0.5)/float64(WplaceTileSize)
	lng := x/n*360 - 180
	latRad := math.Atan(math.Sinh(math.Pi * (1 - 2*y/n)))
	lat := latRad * 180 / math.Pi
	return &LngLat{Lng: lng, Lat: lat}
}

// WatchAreaCenter returns lng/lat for the center of a watch rectangle.
func WatchAreaCenter(origin *Coordinate, width, height int) *LngLat {
	if origin == nil {
		return &LngLat{}
	}
	centerAbsX := float64(origin.TileX*WplaceTileSize+origin.PixelX) + float64(width)/2
	centerAbsY := float64(origin.TileY*WplaceTileSize+origin.PixelY) + float64(height)/2
	centerTileX := int(centerAbsX) / WplaceTileSize
	centerTileY := int(centerAbsY) / WplaceTileSize
	centerPixelX := int(centerAbsX) % WplaceTileSize
	centerPixelY := int(centerAbsY) % WplaceTileSize
	return TilePixelCenterToLngLat(centerTileX, centerTileY, centerPixelX, centerPixelY)
}

const (
	webMercatorTileSize   = 256.0
	defaultViewportWidth  = 1280.0
	defaultViewportHeight = 720.0
	uiWidthFactor         = 0.82
	uiHeightFactor        = 0.90
	zoomBias              = -0.43
	minCanvasZoom         = 10.7
	maxSafeZoom           = 22.0
)

// ZoomFromImageSize returns a canvas-safe zoom suitable for wplace.live links.
func ZoomFromImageSize(width, height int) float64 {
	if width <= 0 || height <= 0 {
		return minCanvasZoom
	}
	worldCanvasPx := float64(WplaceTilesPerEdge * WplaceTileSize)
	fracW := float64(width) / worldCanvasPx
	fracH := float64(height) / worldCanvasPx
	if fracW <= 0 || fracH <= 0 {
		return minCanvasZoom
	}
	usableW := defaultViewportWidth * uiWidthFactor
	usableH := defaultViewportHeight * uiHeightFactor
	if usableW <= 0 || usableH <= 0 {
		return minCanvasZoom
	}
	zoomW := math.Log2(usableW / (webMercatorTileSize * fracW))
	zoomH := math.Log2(usableH / (webMercatorTileSize * fracH))
	zoom := math.Min(zoomW, zoomH) + zoomBias
	if math.IsNaN(zoom) || math.IsInf(zoom, 0) {
		return minCanvasZoom
	}
	if zoom < minCanvasZoom {
		return minCanvasZoom
	}
	if zoom > maxSafeZoom {
		return maxSafeZoom
	}
	return zoom
}
