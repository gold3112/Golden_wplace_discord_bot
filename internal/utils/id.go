package utils

import (
	"crypto/rand"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var channelSlugRegex = regexp.MustCompile(`[^a-z0-9-]+`)

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
	slug := strings.ToLower(label)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = channelSlugRegex.ReplaceAllString(slug, "")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "watch"
	}
	if len(slug) > 90 {
		slug = slug[:90]
	}
	return fmt.Sprintf("%s-%s", slug, RandomSuffix())
}
