package models

import "time"

const DefaultThresholdPercent = 10.0
const MaxWatchTiles = 8
const MaxWatchPixels = 10000000 // 10MP (e.g. 4000x2500)

// WatchType 監視タイプ
type WatchType string

const (
	WatchTypeProgress WatchType = "progress" // 進捗監視
	WatchTypeVandal   WatchType = "vandal"   // 荒らし監視
)

// WatchStatus 監視状態
type WatchStatus string

const (
	WatchStatusPending WatchStatus = "pending"
	WatchStatusActive  WatchStatus = "active"
	WatchStatusPaused  WatchStatus = "paused"
	WatchStatusDeleted WatchStatus = "deleted"
)

// WatchVisibility 公開設定
type WatchVisibility string

const (
	WatchVisibilityPublic  WatchVisibility = "public"
	WatchVisibilityPrivate WatchVisibility = "private"
)

// Watch 監視設定
type Watch struct {
	ID                 string          `json:"id"`
	Label              string          `json:"label"`
	OwnerID            string          `json:"owner_id"`
	GuildID            string          `json:"guild_id"`
	ChannelID          string          `json:"channel_id"`
	Type               WatchType       `json:"type"`
	Visibility         WatchVisibility `json:"visibility"` // 公開・非公開
	PaletteFix         bool            `json:"palette_fix"` // パレット補正
	PaletteFixSet      bool            `json:"palette_fix_set"` // パレット補正設定済みフラグ
	Origin             string          `json:"origin"`      // "1818-806-989-358" format
	Template           string          `json:"template"`
	ThresholdPixels    int         `json:"threshold_pixels"`
	ThresholdPercent   float64     `json:"threshold_percent"`
	Status             WatchStatus `json:"status"`
	CreatedAt          time.Time   `json:"created_at"`
	LastCheckedAt      *time.Time  `json:"last_checked_at,omitempty"`
	LastDiffPixels     int         `json:"last_diff_pixels"`
	LastDiffPercentage float64     `json:"last_diff_percentage"`
	TotalChecks        int         `json:"total_checks"`
	TotalNotifications int         `json:"total_notifications"`
	NextScheduledCheck time.Time   `json:"next_scheduled_check"`
}

// WatchEvent 監視イベント履歴
type WatchEvent struct {
	Timestamp      time.Time `json:"timestamp"`
	DiffPixels     int       `json:"diff_pixels"`
	DiffPercentage float64   `json:"diff_percentage"`
	SnapshotPath   string    `json:"snapshot_path,omitempty"`
	NotificationID string    `json:"notification_id,omitempty"`
}

// GuildWatches ギルド内の全監視
type GuildWatches struct {
	GuildID string   `json:"guild_id"`
	Watches []*Watch `json:"watches"`
}

// SetupSession セットアップセッション
type SetupSession struct {
	UserID          string    `json:"user_id"`
	GuildID         string    `json:"guild_id"`
	ChannelID       string    `json:"channel_id"`
	Label           string    `json:"label,omitempty"`
	Type            WatchType `json:"type,omitempty"`
	Origin          string    `json:"origin,omitempty"`
	TemplateURL     string    `json:"template_url,omitempty"`
	ThresholdPixels int       `json:"threshold_pixels"`
	Step            int       `json:"step"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}
