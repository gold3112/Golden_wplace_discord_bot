package wplace

import (
	"context"
	"net/http"
	"time"

	"golden_wplace_discord_bot/internal/models"
)

// Result 監視結果
type Result struct {
	DiffPixels     int
	DiffPercentage float64
	SnapshotURL    string
}

// Client Wplace APIクライアント
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient 新しいクライアント
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// CheckWatch 監視対象をチェック（TODO: 実装）
func (c *Client) CheckWatch(ctx context.Context, watch *models.Watch) (*Result, error) {
	// TODO: 実際の差分取得処理を実装
	return &Result{DiffPixels: 0, DiffPercentage: 0}, nil
}
