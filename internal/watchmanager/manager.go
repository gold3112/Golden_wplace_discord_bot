package watchmanager

import (
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"golden_wplace_discord_bot/internal/models"
	"golden_wplace_discord_bot/internal/notifications"
	"golden_wplace_discord_bot/internal/storage"
	"golden_wplace_discord_bot/internal/utils"
	"golden_wplace_discord_bot/internal/wplace"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

// Manager 監視スケジューラー
type Manager struct {
	storage      *storage.Storage
	notifier     *notifications.Notifier
	limiter      *utils.RateLimiter
	interval     time.Duration
	mu           sync.Mutex
	tasks        map[string]*watchTask
	notifyStates map[string]*notificationState
	started      bool
}

type notificationState struct {
	LastTier notifications.Tier
	WasZero  bool
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
		storage:      storage,
		notifier:     notifier,
		limiter:      limiter,
		interval:     interval,
		tasks:        make(map[string]*watchTask),
		notifyStates: make(map[string]*notificationState),
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
	if watch.Origin == "" || watch.Template == "" {
		return
	}

	m.ensureThresholdDefault(watch)
	m.ensureNotificationState(watch)

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
	delete(m.notifyStates, watch.ID)
}

// RemoveWatch 監視を停止
func (m *Manager) RemoveWatch(watchID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if task, ok := m.tasks[watchID]; ok {
		task.stop()
		delete(m.tasks, watchID)
	}
	delete(m.notifyStates, watchID)
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
	initialDelay := time.Duration(rand.Intn(60)) * time.Second
	if initialDelay == 0 {
		initialDelay = 5 * time.Second
	}
	t.resetNext(initialDelay)
	timer := time.NewTimer(initialDelay)

	for {
		select {
		case <-timer.C:
			t.execute()
			next := t.manager.interval + time.Duration(rand.Intn(30))*time.Second
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

func (m *Manager) performWatchCheck(watch *models.Watch) {
	if _, err := m.runWatchEvaluation(watch, true); err != nil {
		if watch != nil {
			log.Printf("watch %s check failed: %v", watch.ID, err)
		} else {
			log.Printf("watch check failed: %v", err)
		}
	}
}

func (m *Manager) TriggerImmediateRun(watch *models.Watch) {
	if watch == nil {
		return
	}
	go m.performWatchCheck(watch)
}

func (m *Manager) RunWatchNow(watch *models.Watch) (*wplace.Result, error) {
	return m.runWatchEvaluation(watch, false)
}

func (m *Manager) runWatchEvaluation(watch *models.Watch, notify bool) (*wplace.Result, error) {
	if watch == nil {
		return nil, fmt.Errorf("watch is nil")
	}
	result, err := m.evaluateWatch(watch)
	m.finalizeWatchResult(watch, result, err, notify)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (m *Manager) finalizeWatchResult(watch *models.Watch, result *wplace.Result, evalErr error, notify bool) {
	if watch == nil {
		return
	}
	now := time.Now()
	watch.LastCheckedAt = &now
	watch.TotalChecks++

	if evalErr == nil && result != nil {
		watch.LastDiffPixels = result.DiffPixels
		watch.LastDiffPercentage = result.DiffPercentage
		if notify && m.notifier != nil {
			watch.TotalNotifications += m.dispatchNotifications(watch, result)
		}
	}

	if err := m.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to persist watch %s: %v", watch.ID, err)
	}
}

func (m *Manager) dispatchNotifications(watch *models.Watch, result *wplace.Result) int {
	if watch == nil || result == nil || m.notifier == nil {
		return 0
	}

	thresholdPercent := calculateThresholdPercent(watch, result.TemplateOpaque)

	m.mu.Lock()
	state, ok := m.notifyStates[watch.ID]
	if !ok {
		state = &notificationState{LastTier: notifications.TierNone, WasZero: notifications.IsZeroDiff(watch.LastDiffPercentage)}
		m.notifyStates[watch.ID] = state
	}
	lastTier := state.LastTier
	wasZero := state.WasZero
	m.mu.Unlock()

	currentTier := notifications.CalculateTier(result.DiffPercentage, thresholdPercent)
	isZero := notifications.IsZeroDiff(result.DiffPercentage)

	sent := 0

	if wasZero && !isZero {
		if err := m.notifier.NotifyRecovery(watch, result); err != nil {
			log.Printf("notify recovery failed for %s: %v", watch.ID, err)
		} else {
			sent++
		}
	}
	if !wasZero && isZero {
		if err := m.notifier.NotifyCompletion(watch, result); err != nil {
			log.Printf("notify completion failed for %s: %v", watch.ID, err)
		} else {
			sent++
		}
	}

	if currentTier > lastTier && currentTier > notifications.TierNone {
		if err := m.notifier.NotifyIncrease(watch, result, currentTier); err != nil {
			log.Printf("notify increase failed for %s: %v", watch.ID, err)
		} else {
			sent++
		}
	} else if currentTier < lastTier && lastTier > notifications.TierNone {
		tierForMessage := currentTier
		if tierForMessage == notifications.TierNone {
			tierForMessage = notifications.Tier10
		}
		if err := m.notifier.NotifyDecrease(watch, result, tierForMessage, thresholdPercent); err != nil {
			log.Printf("notify decrease failed for %s: %v", watch.ID, err)
		} else {
			sent++
		}
	}

	m.mu.Lock()
	if state, ok := m.notifyStates[watch.ID]; ok {
		state.LastTier = currentTier
		state.WasZero = isZero
	} else {
		m.notifyStates[watch.ID] = &notificationState{LastTier: currentTier, WasZero: isZero}
	}
	m.mu.Unlock()

	return sent
}

func calculateThresholdPercent(watch *models.Watch, opaque int) float64 {
	if watch == nil {
		return models.DefaultThresholdPercent
	}
	if watch.ThresholdPercent > 0 {
		return watch.ThresholdPercent
	}
	if opaque <= 0 || watch.ThresholdPixels <= 0 {
		return models.DefaultThresholdPercent
	}
	percent := float64(watch.ThresholdPixels) * 100 / float64(opaque)
	if percent < 10 {
		percent = 10
	}
	if percent > 100 {
		percent = 100
	}
	return percent
}

func (m *Manager) ensureNotificationState(watch *models.Watch) {
	if watch == nil {
		return
	}
	if _, ok := m.notifyStates[watch.ID]; !ok {
		m.notifyStates[watch.ID] = &notificationState{LastTier: notifications.TierNone, WasZero: notifications.IsZeroDiff(watch.LastDiffPercentage)}
	}
}

func (m *Manager) ensureThresholdDefault(watch *models.Watch) {
	if watch == nil {
		return
	}
	if watch.ThresholdPercent > 0 {
		return
	}
	watch.ThresholdPercent = models.DefaultThresholdPercent
	if err := m.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to persist default threshold for %s: %v", watch.ID, err)
	}
}

func (t *watchTask) execute() {
	t.mu.Lock()
	watch := t.watch
	t.mu.Unlock()

	t.manager.performWatchCheck(watch)
}
