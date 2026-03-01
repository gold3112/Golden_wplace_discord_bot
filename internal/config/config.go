package config

import (
	"log"
	"os"
)

// Config アプリケーション設定
type Config struct {
	Token             string
	WplaceAPIBase     string
	MonitorInterval   int // 分単位
	WatchCategoryName string
}

// Load 環境変数から設定を読み込む
func Load() *Config {
	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN is required")
	}

	apiBase := os.Getenv("WPLACE_API_BASE")
	if apiBase == "" {
		apiBase = "https://backend.wplace.live"
	}

	categoryName := os.Getenv("WATCH_CATEGORY_NAME")
	if categoryName == "" {
		categoryName = "Golden Watches"
	}

	return &Config{
		Token:             token,
		WplaceAPIBase:     apiBase,
		MonitorInterval:   1,
		WatchCategoryName: categoryName,
	}
}
