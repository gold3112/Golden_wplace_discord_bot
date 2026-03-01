package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golden_wplace_discord_bot/internal/models"
)

// Storage データ永続化層
type Storage struct {
	dataDir string
	mu      sync.RWMutex
}

// NewStorage 新しいStorageを作成
func NewStorage(dataDir string) *Storage {
	_ = os.MkdirAll(filepath.Join(dataDir, "guilds"), 0755)
	return &Storage{
		dataDir: dataDir,
	}
}

// GetGuildDataDir ギルドのデータディレクトリを取得
func (s *Storage) GetGuildDataDir(guildID string) string {
	return filepath.Join(s.dataDir, "guilds", guildID)
}

// ListGuildIDs 保存されているギルドID一覧を取得
func (s *Storage) ListGuildIDs() ([]string, error) {
	guildsDir := filepath.Join(s.dataDir, "guilds")
	entries, err := os.ReadDir(guildsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to read guilds dir: %w", err)
	}

	var ids []string
	for _, entry := range entries {
		if entry.IsDir() {
			ids = append(ids, entry.Name())
		}
	}
	return ids, nil
}

// GetGuildWatchesPath ギルドの監視設定ファイルパスを取得
func (s *Storage) GetGuildWatchesPath(guildID string) string {
	return filepath.Join(s.GetGuildDataDir(guildID), "watches.json")
}

// LoadGuildWatches ギルドの監視設定を読み込む
func (s *Storage) LoadGuildWatches(guildID string) (*models.GuildWatches, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path := s.GetGuildWatchesPath(guildID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &models.GuildWatches{
				GuildID: guildID,
				Watches: []*models.Watch{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read watches: %w", err)
	}

	var guildWatches models.GuildWatches
	if err := json.Unmarshal(data, &guildWatches); err != nil {
		return nil, fmt.Errorf("failed to unmarshal watches: %w", err)
	}

	return &guildWatches, nil
}

// SaveGuildWatches ギルドの監視設定を保存
func (s *Storage) SaveGuildWatches(guildWatches *models.GuildWatches) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	guildDir := s.GetGuildDataDir(guildWatches.GuildID)
	if err := os.MkdirAll(guildDir, 0755); err != nil {
		return fmt.Errorf("failed to create guild dir: %w", err)
	}

	path := s.GetGuildWatchesPath(guildWatches.GuildID)
	data, err := json.MarshalIndent(guildWatches, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal watches: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write watches: %w", err)
	}

	return nil
}

// GetUserWatch ユーザーの監視を取得
func (s *Storage) GetUserWatch(guildID, userID string) (*models.Watch, error) {
	guildWatches, err := s.LoadGuildWatches(guildID)
	if err != nil {
		return nil, err
	}

	for _, watch := range guildWatches.Watches {
		if watch.OwnerID == userID && watch.Status != models.WatchStatusDeleted {
			return watch, nil
		}
	}

	return nil, nil
}

// GetWatchByChannel チャンネルIDから監視を取得
func (s *Storage) GetWatchByChannel(guildID, channelID string) (*models.Watch, error) {
	guildWatches, err := s.LoadGuildWatches(guildID)
	if err != nil {
		return nil, err
	}

	for _, watch := range guildWatches.Watches {
		if watch.ChannelID == channelID && watch.Status != models.WatchStatusDeleted {
			return watch, nil
		}
	}

	return nil, nil
}

// GetWatchByLabel ラベルから監視を取得
func (s *Storage) GetWatchByLabel(guildID, label string) (*models.Watch, error) {
	if label == "" {
		return nil, nil
	}

	guildWatches, err := s.LoadGuildWatches(guildID)
	if err != nil {
		return nil, err
	}

	for _, watch := range guildWatches.Watches {
		if watch.Status == models.WatchStatusDeleted {
			continue
		}
		if strings.EqualFold(watch.Label, label) {
			return watch, nil
		}
	}

	return nil, nil
}

// AddWatch 監視を追加
func (s *Storage) AddWatch(watch *models.Watch) error {
	guildWatches, err := s.LoadGuildWatches(watch.GuildID)
	if err != nil {
		return err
	}

	guildWatches.Watches = append(guildWatches.Watches, watch)
	return s.SaveGuildWatches(guildWatches)
}

// UpdateWatch 監視を更新
func (s *Storage) UpdateWatch(watch *models.Watch) error {
	guildWatches, err := s.LoadGuildWatches(watch.GuildID)
	if err != nil {
		return err
	}

	for i, w := range guildWatches.Watches {
		if w.ID == watch.ID {
			guildWatches.Watches[i] = watch
			return s.SaveGuildWatches(guildWatches)
		}
	}

	return fmt.Errorf("watch not found: %s", watch.ID)
}

// DeleteWatch 監視を削除（論理削除）
func (s *Storage) DeleteWatch(guildID, watchID string) error {
	guildWatches, err := s.LoadGuildWatches(guildID)
	if err != nil {
		return err
	}

	for i, w := range guildWatches.Watches {
		if w.ID == watchID {
			guildWatches.Watches[i].Status = models.WatchStatusDeleted
			return s.SaveGuildWatches(guildWatches)
		}
	}

	return fmt.Errorf("watch not found: %s", watchID)
}

// GetActiveWatches アクティブな監視を取得
func (s *Storage) GetActiveWatches(guildID string) ([]*models.Watch, error) {
	guildWatches, err := s.LoadGuildWatches(guildID)
	if err != nil {
		return nil, err
	}

	var active []*models.Watch
	for _, watch := range guildWatches.Watches {
		if watch.Status == models.WatchStatusActive {
			active = append(active, watch)
		}
	}

	return active, nil
}

// GetTemplateImagePath テンプレート画像パスを取得
func (s *Storage) GetTemplateImagePath(guildID, filename string) string {
	return filepath.Join(s.GetGuildDataDir(guildID), "template_img", filename)
}

// SaveTemplateImage テンプレート画像を保存
func (s *Storage) SaveTemplateImage(guildID, filename string, data []byte) error {
	dir := filepath.Join(s.GetGuildDataDir(guildID), "template_img")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create template_img dir: %w", err)
	}

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write template image: %w", err)
	}

	return nil
}

// DeleteTemplateImage テンプレート画像を削除
func (s *Storage) DeleteTemplateImage(guildID, filename string) error {
	if filename == "" {
		return nil
	}
	path := s.GetTemplateImagePath(guildID, filename)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to delete template image: %w", err)
	}
	return nil
}
