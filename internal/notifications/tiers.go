package notifications

import (
	"fmt"
	"math"
)

// Tier 通知段階
type Tier int

const (
	TierNone Tier = iota
	Tier10
	Tier20
	Tier30
	Tier40
	Tier50
	Tier60
	Tier70
	Tier80
	Tier90
	Tier100
)

// CalculateTier 差分率からTierを算出する
func CalculateTier(diffValue, threshold float64) Tier {
	if diffValue < threshold {
		return TierNone
	}
	switch {
	case diffValue >= 100:
		return Tier100
	case diffValue >= 90:
		return Tier90
	case diffValue >= 80:
		return Tier80
	case diffValue >= 70:
		return Tier70
	case diffValue >= 60:
		return Tier60
	case diffValue >= 50:
		return Tier50
	case diffValue >= 40:
		return Tier40
	case diffValue >= 30:
		return Tier30
	case diffValue >= 20:
		return Tier20
	default:
		return Tier10
	}
}

// IsZeroDiff 差分ゼロ判定
func IsZeroDiff(value float64) bool {
	const epsilon = 0.005
	return math.Abs(value) <= epsilon
}

// GetTierColor Tierに応じた色コード
func GetTierColor(tier Tier) int {
	switch tier {
	case Tier100:
		return 0x7F0000
	case Tier90:
		return 0xB22222
	case Tier80:
		return 0xDC143C
	case Tier70:
		return 0xFF3030
	case Tier60:
		return 0xFF4500
	case Tier50:
		return 0xFF0000
	case Tier40:
		return 0xFF4500
	case Tier30:
		return 0xFFA500
	case Tier20:
		return 0xFFD700
	case Tier10:
		return 0xFFFF00
	default:
		return 0x808080
	}
}

// TierRangeLabel Tierの範囲テキスト
func TierRangeLabel(tier Tier, threshold float64) string {
	switch tier {
	case Tier100:
		return "100%台"
	case Tier90:
		return "90%台"
	case Tier80:
		return "80%台"
	case Tier70:
		return "70%台"
	case Tier60:
		return "60%台"
	case Tier50:
		return "50%以上"
	case Tier40:
		return "40%台"
	case Tier30:
		return "30%台"
	case Tier20:
		return "20%台"
	case Tier10:
		return "10%台"
	default:
		return fmt.Sprintf("%.0f%%未満", threshold)
	}
}
