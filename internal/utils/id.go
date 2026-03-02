package utils

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var channelInvalidRegex = regexp.MustCompile(`[\\/:\*\?"<>\|.,;!@#$%^&()\[\]{}=+~` + "`" + `]+`)

// GenerateWatchID watch IDを生成
func GenerateWatchID(userID string) string {
	suffix := userID
	if len(userID) > 4 {
		suffix = userID[len(userID)-4:]
	}
	return fmt.Sprintf("watch-%s-%d", suffix, time.Now().Unix())
}

// RandomSuffix ランダムな4桁を生成
func RandomSuffix() string {
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		return "0000"
	}
	return fmt.Sprintf("%04x", b)
}

// SlugifyChannelName Discordチャンネル名に使えるスラッグを生成
func SlugifyChannelName(label string) string {
	// Discordのチャンネル名は現在Unicode（日本語など）を広くサポートしています。
	// ここでは記号などを除外する程度に留めます。
	slug := strings.ToLower(label)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = channelInvalidRegex.ReplaceAllString(slug, "")
	slug = strings.Trim(slug, "-")
	
	if slug == "" {
		slug = "watch"
	}
	
	// 長さ制限 (Discordの上限は100文字だが余裕を持たせる)
	if len([]rune(slug)) > 80 {
		runes := []rune(slug)
		slug = string(runes[:80])
	}
	
	return fmt.Sprintf("%s-%s", slug, RandomSuffix())
}
