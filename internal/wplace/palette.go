package wplace

import (
	"image"
	"image/color"
	"math"
)

// PaletteColor represents a Wplace palette color
type PaletteColor struct {
	ID  int
	RGB color.NRGBA
}

// WplacePalette is the official 64-color palette from README_EN.md
var WplacePalette = []PaletteColor{
	{0, color.NRGBA{0, 0, 0, 0}},         // Transparent
	{1, color.NRGBA{0, 0, 0, 255}},       // Black
	{2, color.NRGBA{60, 60, 60, 255}},    // Dark Gray
	{3, color.NRGBA{120, 120, 120, 255}}, // Gray
	{4, color.NRGBA{210, 210, 210, 255}}, // Light Gray
	{5, color.NRGBA{255, 255, 255, 255}}, // White
	{6, color.NRGBA{96, 0, 24, 255}},     // Maroon
	{7, color.NRGBA{237, 28, 36, 255}},    // Red
	{8, color.NRGBA{255, 127, 39, 255}},   // Orange
	{9, color.NRGBA{246, 170, 9, 255}},    // Gold
	{10, color.NRGBA{249, 221, 59, 255}},  // Yellow
	{11, color.NRGBA{255, 250, 188, 255}}, // Pale Yellow
	{12, color.NRGBA{14, 185, 104, 255}},  // Green
	{13, color.NRGBA{19, 230, 123, 255}},  // Light Green
	{14, color.NRGBA{135, 255, 94, 255}},  // Lime
	{15, color.NRGBA{12, 129, 110, 255}},  // Teal
	{16, color.NRGBA{16, 174, 166, 255}},  // Cyan-Teal
	{17, color.NRGBA{19, 225, 190, 255}},  // Aqua
	{18, color.NRGBA{40, 80, 158, 255}},   // Blue
	{19, color.NRGBA{64, 147, 228, 255}},  // Sky Blue
	{20, color.NRGBA{96, 247, 242, 255}},  // Pale Blue
	{21, color.NRGBA{107, 80, 246, 255}},  // Indigo
	{22, color.NRGBA{153, 177, 251, 255}}, // Lavender Blue
	{23, color.NRGBA{120, 12, 153, 255}},  // Purple
	{24, color.NRGBA{170, 56, 185, 255}},  // Magenta
	{25, color.NRGBA{224, 159, 249, 255}}, // Pink-Purple
	{26, color.NRGBA{203, 0, 122, 255}},   // Dark Pink
	{27, color.NRGBA{236, 31, 128, 255}},  // Pink
	{28, color.NRGBA{243, 141, 169, 255}}, // Light Pink
	{29, color.NRGBA{104, 70, 52, 255}},   // Brown
	{30, color.NRGBA{149, 104, 42, 255}},  // Light Brown
	{31, color.NRGBA{248, 178, 119, 255}}, // Tan
	{32, color.NRGBA{170, 170, 170, 255}}, // Paid Gray
	{33, color.NRGBA{165, 14, 30, 255}},   // Paid Crimson
	{34, color.NRGBA{250, 128, 114, 255}}, // Salmon
	{35, color.NRGBA{228, 92, 26, 255}},   // Burnt Orange
	{36, color.NRGBA{214, 181, 148, 255}}, // Sand
	{37, color.NRGBA{156, 132, 49, 255}},  // Dark Yellow
	{38, color.NRGBA{197, 173, 49, 255}},  // Mustard
	{39, color.NRGBA{232, 212, 95, 255}},  // Straw
	{40, color.NRGBA{74, 107, 58, 255}},   // Olive
	{41, color.NRGBA{90, 148, 74, 255}},   // Leaf Green
	{42, color.NRGBA{132, 197, 115, 255}}, // Sage
	{43, color.NRGBA{15, 121, 159, 255}},  // Deep Sea
	{44, color.NRGBA{187, 250, 242, 255}}, // Mint
	{45, color.NRGBA{125, 199, 255, 255}}, // Azure
	{46, color.NRGBA{77, 49, 184, 255}},   // Paid Violet
	{47, color.NRGBA{74, 66, 132, 255}},   // Slate Blue
	{48, color.NRGBA{122, 113, 196, 255}}, // Periwinkle
	{49, color.NRGBA{181, 174, 241, 255}}, // Pastel Purple
	{50, color.NRGBA{219, 164, 99, 255}},  // Copper
	{51, color.NRGBA{209, 128, 81, 255}},  // Sienna
	{52, color.NRGBA{255, 197, 165, 255}}, // Peach
	{53, color.NRGBA{155, 82, 73, 255}},   // Paid Maroon
	{54, color.NRGBA{209, 128, 120, 255}}, // Rose
	{55, color.NRGBA{250, 182, 164, 255}}, // Flesh
	{56, color.NRGBA{123, 99, 82, 255}},   // Dark Sand
	{57, color.NRGBA{156, 132, 107, 255}}, // Clay
	{58, color.NRGBA{51, 57, 65, 255}},    // Gunmetal
	{59, color.NRGBA{109, 117, 141, 255}}, // Slate Gray
	{60, color.NRGBA{179, 185, 209, 255}}, // Steel
	{61, color.NRGBA{109, 100, 63, 255}},  // Khaki
	{62, color.NRGBA{148, 140, 107, 255}}, // Moss
	{63, color.NRGBA{205, 197, 158, 255}}, // Parchment
}

// GetNearestPaletteColor finds the closest Wplace palette color for a given color.
func GetNearestPaletteColor(c color.Color) color.NRGBA {
	r, g, b, a := c.RGBA()
	// Skip transparent or near-transparent
	if a < 32768 { // 50%
		return color.NRGBA{0, 0, 0, 0}
	}

	r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)

	minDist := math.MaxFloat64
	var nearest color.NRGBA

	// Skip ID 0 (Transparent) in distance calculation
	for i := 1; i < len(WplacePalette); i++ {
		pc := WplacePalette[i].RGB
		dist := colorDistance(r8, g8, b8, pc.R, pc.G, pc.B)
		if dist < minDist {
			minDist = dist
			nearest = pc
		}
	}

	return nearest
}

// HasNonPaletteColors 画像内に公式パレット外の色が含まれているかチェックし、補正後の画像も返す
func HasNonPaletteColors(img *image.NRGBA) (bool, *image.NRGBA) {
	hasInvalid := false
	bounds := img.Bounds()
	out := image.NewNRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := img.NRGBAAt(x, y)
			// 半透明ピクセルはWplaceでは扱えないため、128未満なら透明、128以上なら不透明として扱う
			if c.A < 128 {
				out.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 0})
				continue
			}

			// パレット色との最小距離を計算
			nearest := GetNearestPaletteColor(c)
			dist := colorDistance(c.R, c.G, c.B, nearest.R, nearest.G, nearest.B)

			// 距離が一定以下なら、圧縮等の誤差とみなして OK とする
			// 100 は各成分が 5〜10 程度ズレていても許容する範囲
			if dist < 100 {
				out.SetNRGBA(x, y, nearest) // 誤差を修正してセット
			} else {
				hasInvalid = true
				out.SetNRGBA(x, y, nearest)
			}
		}
	}
	return hasInvalid, out
}

func colorToUint32(c color.NRGBA) uint32 {
	return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}

func colorDistance(r1, g1, b1, r2, g2, b2 uint8) float64 {
	dr := float64(r1) - float64(r2)
	dg := float64(g1) - float64(g2)
	db := float64(b1) - float64(b2)
	return dr*dr + dg*dg + db*db
}
