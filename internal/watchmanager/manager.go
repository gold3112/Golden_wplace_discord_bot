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

	"github.com/bwmarrin/discordgo"
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
	session      *discordgo.Session
	mu           sync.Mutex
	tasks        map[string]*watchTask
	notifyStates map[string]*notificationState
	watchMu      map[string]*sync.Mutex // Watch IDごとの排他ロック
	started      bool
	OnWatchRemoved func(watchID string) // 削除時のコールバック
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
		watchMu:      make(map[string]*sync.Mutex),
	}
}

func (m *Manager) getWatchMutex(id string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mu, ok := m.watchMu[id]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	m.watchMu[id] = mu
	return mu
}

// StartExisting 保存済みの監視を起動
func (m *Manager) StartExisting(s *discordgo.Session) error {
	m.mu.Lock()
	m.session = s
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.mu.Unlock()

	// 期限切れ監視の定期クリーンアップを開始
	go m.startCleanupLoop()

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
			// チャンネルの存在確認
			_, err := s.Channel(watch.ChannelID)
			if err != nil {
				// チャンネルが見つからない（削除された）場合
				log.Printf("Channel %s for watch %s not found. Cleaning up...", watch.ChannelID, watch.ID)
				_ = m.storage.DeleteTemplateImage(watch.GuildID, watch.Template)
				_ = m.storage.RemoveWatchRecord(watch.GuildID, watch.ID)
				continue
			}
			m.scheduleLocked(watch)
		}
	}
	return nil
}

func (m *Manager) startCleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanupExpiredWatches()
	}
}

func (m *Manager) cleanupExpiredWatches() {
	guildIDs, err := m.storage.ListGuildIDs()
	if err != nil {
		return
	}

	var expiredIDs []string
	for _, guildID := range guildIDs {
		data, err := m.storage.LoadGuildWatches(guildID)
		if err != nil {
			continue
		}

		for _, watch := range data.Watches {
			// 5分経過した Pending 監視を削除
			if watch.Status == models.WatchStatusPending && time.Since(watch.CreatedAt) > 5*time.Minute {
				log.Printf("Watch %s (Pending) expired. Cleaning up...", watch.ID)
				expiredIDs = append(expiredIDs, watch.ID)
				
				// 1. テンプレート画像削除
				_ = m.storage.DeleteTemplateImage(watch.GuildID, watch.Template)
				
				// 2. 内部レコード削除
				_ = m.storage.RemoveWatchRecord(watch.GuildID, watch.ID)

				// 4. コールバック実行 (キャッシュ掃除など)
				if m.OnWatchRemoved != nil {
					m.OnWatchRemoved(watch.ID)
				}

				// 3. チャンネル処理
				m.mu.Lock()
				sess := m.session
				m.mu.Unlock()

				if sess != nil {
					if !watch.IsExternalChannel {
						// ボットが作ったチャンネルなら削除
						_, _ = sess.ChannelDelete(watch.ChannelID)
					} else {
						// 既存チャンネルなら案内を送って終了
						_, _ = sess.ChannelMessageSend(watch.ChannelID, "⚠️ セットアップの制限時間（5分）が経過したため、初期化を解除しました。再度監視を行う場合は `/watch init` を実行してください。")
					}
				}
			}
		}
	}

	// メモリキャッシュ(setupCache)の掃除
	if len(expiredIDs) > 0 {
		// commands パッケージの関数を呼び出す（循環参照に注意が必要なため、cmd/bot/main.go で調整するか、ここで直接呼べるか確認が必要）
		// ここでは一旦コメントアウトし、ARCHITECTURE に基づき commands 側でクリーンアップが必要であることを認識します
	}
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

	// Watch ID ごとに排他ロックを取得
	mu := m.getWatchMutex(watch.ID)
	mu.Lock()
	defer mu.Unlock()

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

	// 初回実行時（通知履歴がない場合）
	if watch.TotalNotifications == 0 {
		// 初回の状態を「通知済み」として記録し、アラートは送らない
		m.mu.Lock()
		m.notifyStates[watch.ID] = &notificationState{LastTier: currentTier, WasZero: isZero}
		m.mu.Unlock()
		// 次回から正常に判定されるよう、1を返して通知済みフラグ(TotalNotifications)を立てる
		return 1
	}

	if wasZero && !isZero {
		if err := m.notifier.NotifyRecovery(watch, result); err != nil {
			log.Printf("notify recovery failed for %s: %v", watch.ID, err)
		} else {
			sent++
		}
		// 0からの復帰時はTier通知をスキップ（NotifyRecoveryで事足りるため）
		goto finalize
	}
	if !wasZero && isZero {
		if err := m.notifier.NotifyCompletion(watch, result); err != nil {
			log.Printf("notify completion failed for %s: %v", watch.ID, err)
		} else {
			sent++
		}
		// 0到達時はTier通知をスキップ
		goto finalize
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

finalize:
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
		threshold := watch.ThresholdPercent
		if threshold <= 0 {
			threshold = models.DefaultThresholdPercent
		}
		// 保存されている前回の差分率から、通知済みのTierを復元する
		m.notifyStates[watch.ID] = &notificationState{
			LastTier: notifications.CalculateTier(watch.LastDiffPercentage, threshold),
			WasZero:  notifications.IsZeroDiff(watch.LastDiffPercentage),
		}
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
