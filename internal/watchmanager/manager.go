package watchmanager

import (
	"log"
	"math/rand"
	"sync"
	"time"

	"golden_wplace_discord_bot/internal/models"
	"golden_wplace_discord_bot/internal/notifications"
	"golden_wplace_discord_bot/internal/storage"
	"golden_wplace_discord_bot/internal/utils"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Manager 監視スケジューラー
type Manager struct {
	storage  *storage.Storage
	notifier *notifications.Notifier
	limiter  *utils.RateLimiter
	interval time.Duration
	mu       sync.Mutex
	tasks    map[string]*watchTask
	started  bool
}

type watchTask struct {
	watch   *models.Watch
	manager *Manager
	stopCh  chan struct{}
	mu      sync.Mutex
}

// NewManager 新しいManagerを作成
func NewManager(storage *storage.Storage, notifier *notifications.Notifier, limiter *utils.RateLimiter, interval time.Duration) *Manager {
	return &Manager{
		storage:  storage,
		notifier: notifier,
		limiter:  limiter,
		interval: interval,
		tasks:    make(map[string]*watchTask),
	}
}

// StartExisting 保存済みの監視を起動
func (m *Manager) StartExisting() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return nil
	}
	m.started = true

	guildIDs, err := m.storage.ListGuildIDs()
	if err != nil {
		return err
	}

	for _, guildID := range guildIDs {
		watches, err := m.storage.GetActiveWatches(guildID)
		if err != nil {
			log.Printf("failed to load watches for guild %s: %v", guildID, err)
			continue
		}
		for _, watch := range watches {
			m.scheduleLocked(watch)
		}
	}
	return nil
}

// ScheduleWatch 新規監視をスケジュール
func (m *Manager) ScheduleWatch(watch *models.Watch) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduleLocked(watch)
}

func (m *Manager) scheduleLocked(watch *models.Watch) {
	if watch == nil || watch.Status != models.WatchStatusActive {
		return
	}

	if task, ok := m.tasks[watch.ID]; ok {
		task.updateWatch(watch)
		return
	}

	task := &watchTask{
		watch:   watch,
		manager: m,
		stopCh:  make(chan struct{}),
	}
	m.tasks[watch.ID] = task
	go task.run()
}

// PauseWatch 監視を一時停止
func (m *Manager) PauseWatch(watch *models.Watch) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task, ok := m.tasks[watch.ID]; ok {
		task.stop()
		delete(m.tasks, watch.ID)
	}
}

// RemoveWatch 監視を停止
func (m *Manager) RemoveWatch(watchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task, ok := m.tasks[watchID]; ok {
		task.stop()
		delete(m.tasks, watchID)
	}
}

// Stop 全タスク停止
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, task := range m.tasks {
		task.stop()
		delete(m.tasks, id)
	}
}

func (t *watchTask) run() {
	initialDelay := time.Duration(rand.Intn(180)) * time.Second
	if initialDelay == 0 {
		initialDelay = 5 * time.Second
	}
	t.resetNext(initialDelay)
	timer := time.NewTimer(initialDelay)

	for {
		select {
		case <-timer.C:
			t.execute()
			next := t.manager.interval + time.Duration(rand.Intn(60))*time.Second
			t.resetNext(next)
			timer.Reset(next)
		case <-t.stopCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (t *watchTask) resetNext(d time.Duration) {
	next := time.Now().Add(d)
	t.watch.NextScheduledCheck = next
	if err := t.manager.storage.UpdateWatch(t.watch); err != nil {
		log.Printf("failed to update next schedule for %s: %v", t.watch.ID, err)
	}
}

func (t *watchTask) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	select {
	case <-t.stopCh:
		return
	default:
		close(t.stopCh)
	}
}

func (t *watchTask) updateWatch(watch *models.Watch) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.watch = watch
}

func (t *watchTask) execute() {
	t.mu.Lock()
	watch := t.watch
	t.mu.Unlock()

	result, err := t.manager.evaluateWatch(watch)
	now := time.Now()
	watch.LastCheckedAt = &now
	watch.TotalChecks++

	if err != nil {
		log.Printf("watch %s check failed: %v", watch.ID, err)
		_ = t.manager.storage.UpdateWatch(watch)
		return
	}

	watch.LastDiffPixels = result.DiffPixels
	watch.LastDiffPercentage = result.DiffPercentage

	if result.DiffPixels >= watch.ThresholdPixels {
		watch.TotalNotifications++
		if t.manager.notifier != nil {
			if err := t.manager.notifier.NotifyDiff(watch, result); err != nil {
				log.Printf("notify failed for watch %s: %v", watch.ID, err)
			}
		}
	}

	if err := t.manager.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to persist watch %s: %v", watch.ID, err)
	}
}
