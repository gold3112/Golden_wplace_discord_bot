package commands

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golden_wplace_discord_bot/internal/models"
	"golden_wplace_discord_bot/internal/storage"
	"golden_wplace_discord_bot/internal/utils"
	"golden_wplace_discord_bot/internal/watchmanager"

	"github.com/bwmarrin/discordgo"
)

const (
	watchPanelButtonID = "watch_create_button"
	watchModalID       = "watch_create_modal"
)

const defaultThresholdPercent = models.DefaultThresholdPercent

type watchCreateInput struct {
	Label string
	Type  models.WatchType
}

// WatchCommands watch系スラッシュコマンド
type WatchCommands struct {
	storage *storage.Storage
	manager *watchmanager.Manager
}

// NewWatchCommands コンストラクタ
func NewWatchCommands(storage *storage.Storage, manager *watchmanager.Manager) *WatchCommands {
	return &WatchCommands{storage: storage, manager: manager}
}

// Register スラッシュコマンド登録
func (w *WatchCommands) Register(session *discordgo.Session, appID string) error {
	watchCmd := &discordgo.ApplicationCommand{
		Name:        "watch",
		Description: "Golden watch utilities",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "create",
				Description: "監視チャンネルを作成",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "label", Description: "監視の表示名", Type: discordgo.ApplicationCommandOptionString, Required: true},
					{Name: "type", Description: "監視タイプ", Type: discordgo.ApplicationCommandOptionString, Required: true, Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "progress", Value: string(models.WatchTypeProgress)},
						{Name: "vandal", Value: string(models.WatchTypeVandal)},
					}},
				},
			},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "status", Description: "自分の監視ステータスを表示"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "pause", Description: "監視を一時停止"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "resume", Description: "監視を再開"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "delete", Description: "監視を削除"},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "settings",
				Description: "監視設定を変更",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "threshold", Description: "通知閾値(10%%刻み)", Type: discordgo.ApplicationCommandOptionInteger, Required: true},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "mod_delete",
				Description: "管理者が指定ユーザーの監視を削除",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "削除対象ユーザー", Type: discordgo.ApplicationCommandOptionUser, Required: true},
				},
			},
		},
	}

	panelCmd := &discordgo.ApplicationCommand{
		Name:        "createmonitor",
		Description: "監視リクエストパネルを設置 (管理者用)",
	}

	for _, cmd := range []*discordgo.ApplicationCommand{watchCmd, panelCmd} {
		if _, err := session.ApplicationCommandCreate(appID, "", cmd); err != nil {
			return err
		}
	}
	return nil
}

// HandleInteraction watchコマンドディスパッチ
func (w *WatchCommands) HandleInteraction(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	switch ic.Type {
	case discordgo.InteractionApplicationCommand:
		w.handleApplicationCommand(s, ic)
	case discordgo.InteractionMessageComponent:
		w.handleComponentInteraction(s, ic)
	case discordgo.InteractionModalSubmit:
		w.handleModalSubmit(s, ic)
	}
}

func (w *WatchCommands) handleApplicationCommand(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	data := ic.ApplicationCommandData()
	switch data.Name {
	case "watch":
		w.handleWatchCommand(s, ic, data)
	case "createmonitor":
		w.handleCreateMonitorCommand(s, ic)
	}
}

func (w *WatchCommands) handleWatchCommand(s *discordgo.Session, ic *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if len(data.Options) == 0 {
		return
	}

	sub := data.Options[0]
	switch sub.Name {
	case "create":
		input := watchCreateInput{
			Label: getOptionString(sub.Options, "label"),
			Type:  models.WatchType(strings.ToLower(getOptionString(sub.Options, "type"))),
		}
		w.processCreateRequest(s, ic, input)
	case "status":
		w.handleStatus(s, ic)
	case "pause":
		w.handlePause(s, ic)
	case "resume":
		w.handleResume(s, ic)
	case "delete":
		w.handleDelete(s, ic)
	case "settings":
		w.handleSettings(s, ic, sub.Options)
	case "mod_delete":
		w.handleModeratorDelete(s, ic, sub.Options)
	}
}

// HandleMessageCreate 監視セットアップ中のメッセージを処理
func (w *WatchCommands) HandleMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	if mc.Author == nil || mc.Author.Bot {
		return
	}
	if mc.GuildID == "" {
		return
	}
	watch, err := w.storage.GetWatchByChannel(mc.GuildID, mc.ChannelID)
	if err != nil || watch == nil {
		return
	}
	if watch.OwnerID != mc.Author.ID {
		return
	}
	if watch.Status == models.WatchStatusDeleted {
		return
	}

	if watch.Origin == "" {
		w.handleOriginInput(s, mc, watch)
		return
	}
	if watch.Template == "" {
		w.handleTemplateInput(s, mc, watch)
		return
	}

	if looksLikeThresholdCommand(mc.Content) {
		w.handleThresholdMessage(s, mc, watch)
	}
}

func (w *WatchCommands) handleCreateMonitorCommand(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	if ic.GuildID == "" {
		respondEphemeral(s, ic, "ギルド内でのみ利用できます。")
		return
	}
	if !hasPermission(ic.Member, discordgo.PermissionManageChannels) {
		respondEphemeral(s, ic, "このコマンドを使うにはチャンネル管理権限が必要です。")
		return
	}

	panel := &discordgo.MessageSend{
		Content: "🎨 **Golden Watch Panel**\nボタンを押して監視リクエストを開始してください。",
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "監視を申し込む",
						Style:    discordgo.PrimaryButton,
						CustomID: watchPanelButtonID,
					},
				},
			},
		},
	}

	if _, err := s.ChannelMessageSendComplex(ic.ChannelID, panel); err != nil {
		log.Printf("failed to send panel message: %v", err)
		respondEphemeral(s, ic, "パネルの設置に失敗しました。権限を確認してください。")
		return
	}

	respondEphemeral(s, ic, "監視リクエストパネルを設置しました。")
}

func (w *WatchCommands) handleComponentInteraction(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	data := ic.MessageComponentData()
	switch data.CustomID {
	case watchPanelButtonID:
		w.presentCreateModal(s, ic)
	}
}

func (w *WatchCommands) presentCreateModal(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	if ic.GuildID == "" {
		respondEphemeral(s, ic, "ギルド内でのみ利用できます。")
		return
	}

	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	existing, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil {
		respondEphemeral(s, ic, "監視状況の取得に失敗しました。")
		return
	}
	if existing != nil && existing.Status != models.WatchStatusDeleted {
		respondEphemeral(s, ic, "既に監視チャンネルが存在します。/watch statusで確認してください。")
		return
	}

	modal := &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: watchModalID,
			Title:    "監視リクエスト",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "label", Label: "監視の名前", Style: discordgo.TextInputShort, Required: true, Placeholder: "例: Golden Logo"},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "type", Label: "タイプ (progress / vandal)", Style: discordgo.TextInputShort, Required: true, Value: string(models.WatchTypeProgress)},
				}},
			},
		},
	}

	_ = s.InteractionRespond(ic.Interaction, modal)
}

func (w *WatchCommands) handleModalSubmit(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	data := ic.ModalSubmitData()
	if data.CustomID != watchModalID {
		return
	}

	input := watchCreateInput{
		Label: getModalValue(ic, "label"),
		Type:  models.WatchType(strings.ToLower(getModalValue(ic, "type"))),
	}

	w.processCreateRequest(s, ic, input)
}

func (w *WatchCommands) processCreateRequest(s *discordgo.Session, ic *discordgo.InteractionCreate, input watchCreateInput) {
	if ic.GuildID == "" {
		respondEphemeral(s, ic, "ギルド内でのみ利用できます。")
		return
	}

	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	label := strings.TrimSpace(input.Label)
	if label == "" {
		respondEphemeral(s, ic, "監視名を入力してください。")
		return
	}

	watchType := models.WatchType(strings.ToLower(string(input.Type)))
	if watchType != models.WatchTypeProgress && watchType != models.WatchTypeVandal {
		watchType = models.WatchTypeProgress
	}

	existing, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil {
		respondEphemeral(s, ic, "設定読み込みに失敗しました。")
		return
	}
	if existing != nil && existing.Status != models.WatchStatusDeleted {
		respondEphemeral(s, ic, "既に監視チャンネルが存在します。/watch statusで確認してください。")
		return
	}

	watchID := utils.GenerateWatchID(user.ID)

	channelName := utils.SlugifyChannelName(label)

	channel, err := s.GuildChannelCreateComplex(ic.GuildID, discordgo.GuildChannelCreateData{
		Name: channelName,
		Type: discordgo.ChannelTypeGuildText,
		PermissionOverwrites: []*discordgo.PermissionOverwrite{
			{ID: ic.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			{ID: user.ID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles},
		},
	})
	if err != nil {
		log.Printf("failed to create channel: %v", err)
		respondEphemeral(s, ic, "チャンネル作成に失敗しました。権限を確認してください。")
		return
	}

	now := time.Now().UTC()
	watch := &models.Watch{
		ID:               watchID,
		Label:            label,
		OwnerID:          user.ID,
		GuildID:          ic.GuildID,
		ChannelID:        channel.ID,
		Type:             watchType,
		Origin:           "",
		Template:         "",
		ThresholdPixels:  0,
		ThresholdPercent: defaultThresholdPercent,
		Status:           models.WatchStatusPending,
		CreatedAt:        now,
	}

	if err := w.storage.AddWatch(watch); err != nil {
		respondEphemeral(s, ic, "監視設定の保存に失敗しました。")
		return
	}

	intro := fmt.Sprintf("👋 %s さんの監視チャンネルを作成しました。\n\n**ステップ1**: このチャンネルで `1234-567-890-123` のようなフォーマットで座標を送信してください。\n**ステップ2**: 座標が登録されたら、テンプレート画像(PNG)をこのチャンネルにアップロードしてください。\n\n両方完了すると監視が自動的に開始されます。閾値はデフォルトで10%%刻みです。", user.Mention())
	_, _ = s.ChannelMessageSend(channel.ID, intro)

	respondEphemeral(s, ic, fmt.Sprintf("監視チャンネル <#%s> を作成しました。", channel.ID))
}

func (w *WatchCommands) handleOriginInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	text := strings.TrimSpace(mc.Content)
	if text == "" {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "座標は `タイルX-タイルY-ピクセルX-ピクセルY` 形式で入力してください。例: `1818-806-989-358`")
		return
	}
	if _, err := utils.ParseOrigin(text); err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("座標の形式が正しくありません: %v", err))
		return
	}
	watch.Origin = text
	if err := w.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to update watch origin: %v", err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, "座標の保存中にエラーが発生しました。少し待って再度お試しください。")
		return
	}
	_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ 座標を登録しました。次にテンプレート画像(PNG)をこのチャンネルにアップロードしてください。")
}

func (w *WatchCommands) handleTemplateInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	attachment := firstImageAttachment(mc.Message.Attachments)
	if attachment == nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "テンプレート画像(PNG)を添付してください。")
		return
	}
	filename, err := w.saveTemplateFromAttachment(watch.GuildID, watch.ID, attachment)
	if err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("テンプレート画像の保存に失敗しました: %v", err))
		return
	}
	watch.Template = filename

	if watch.Origin != "" {
		watch.Status = models.WatchStatusActive
		watch.NextScheduledCheck = time.Now().Add(5 * time.Minute)
	} else {
		watch.Status = models.WatchStatusPending
	}

	if err := w.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to update watch template: %v", err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, "テンプレートの保存中にエラーが発生しました。")
		return
	}

	if watch.Status == models.WatchStatusActive {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("✅ テンプレート画像を登録しました。現在の通知閾値は %.0f%% です。監視を開始します！", watch.ThresholdPercent))
		w.manager.ScheduleWatch(watch)
		w.manager.TriggerImmediateRun(watch)
	} else {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ テンプレート画像を登録しました。続いて座標を入力してください。")
	}
}

func (w *WatchCommands) handleThresholdMessage(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	value, ok := parseThresholdValue(mc.Content)
	if !ok {
		return
	}
	if err := w.setThresholdPercent(watch, value); err != nil {
		log.Printf("failed to set threshold: %v", err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, err.Error())
		return
	}
	_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("🔧 通知閾値を %.0f%% に更新しました。", watch.ThresholdPercent))
}

func (w *WatchCommands) setThresholdPercent(watch *models.Watch, value int) error {
	if value%10 != 0 {
		return fmt.Errorf("通知閾値は10%%刻みで指定してください（例: 10, 20, 30...）")
	}
	if value < 10 || value > 100 {
		return fmt.Errorf("通知閾値は10%%〜100%%の範囲で指定してください")
	}
	watch.ThresholdPercent = float64(value)
	if err := w.storage.UpdateWatch(watch); err != nil {
		return err
	}
	return nil
}

var thresholdNumberPattern = regexp.MustCompile(`\d+`)

func parseThresholdValue(content string) (int, bool) {
	match := thresholdNumberPattern.FindString(content)
	if match == "" {
		return 0, false
	}
	val, err := strconv.Atoi(match)
	if err != nil {
		return 0, false
	}
	return val, true
}

func looksLikeThresholdCommand(content string) bool {
	clean := strings.ToLower(strings.TrimSpace(content))
	if strings.HasPrefix(clean, "threshold") || strings.HasPrefix(clean, "閾値") {
		return true
	}
	return strings.HasSuffix(clean, "%")
}

func (w *WatchCommands) handleStatus(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	watch, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil || watch == nil || watch.Status == models.WatchStatusDeleted {
		respondEphemeral(s, ic, "監視が見つかりません。/watch create で作成してください。")
		return
	}

	next := "未スケジュール"
	if !watch.NextScheduledCheck.IsZero() {
		next = watch.NextScheduledCheck.Local().Format(time.RFC1123)
	}

	originState := watch.Origin
	if originState == "" {
		originState = "未登録"
	}
	templateState := "登録済み"
	if watch.Template == "" {
		templateState = "未登録"
	}

	thresholdState := "未設定"
	if watch.ThresholdPercent > 0 {
		thresholdState = fmt.Sprintf("%.0f%%", watch.ThresholdPercent)
	}

	status := fmt.Sprintf("状態: %s\n閾値: %s\n最終チェック: %s\n次回予定: %s\n座標: %s\nテンプレート: %s\nチャンネル: <#%s>",
		watch.Status,
		thresholdState,
		formatTime(watch.LastCheckedAt),
		next,
		originState,
		templateState,
		watch.ChannelID,
	)
	respondEphemeral(s, ic, status)
}

func (w *WatchCommands) handlePause(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	watch, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil || watch == nil {
		respondEphemeral(s, ic, "監視が見つかりません。")
		return
	}
	if watch.Status == models.WatchStatusPending {
		respondEphemeral(s, ic, "セットアップが完了していません。座標とテンプレートを登録してください。")
		return
	}
	if watch.Status != models.WatchStatusActive {
		respondEphemeral(s, ic, "既に停止状態です。")
		return
	}

	watch.Status = models.WatchStatusPaused
	if err := w.storage.UpdateWatch(watch); err != nil {
		respondEphemeral(s, ic, "更新に失敗しました。")
		return
	}
	w.manager.PauseWatch(watch)
	respondEphemeral(s, ic, "監視を一時停止しました。/watch resumeで再開できます。")
}

func (w *WatchCommands) handleResume(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	watch, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil || watch == nil {
		respondEphemeral(s, ic, "監視が見つかりません。")
		return
	}
	if watch.Status == models.WatchStatusPending {
		respondEphemeral(s, ic, "セットアップが完了していません。座標とテンプレートを登録してください。")
		return
	}
	if watch.Status != models.WatchStatusPaused {
		respondEphemeral(s, ic, "一時停止中のみ再開できます。")
		return
	}

	watch.Status = models.WatchStatusActive
	watch.NextScheduledCheck = time.Now().Add(5 * time.Minute)
	if err := w.storage.UpdateWatch(watch); err != nil {
		respondEphemeral(s, ic, "更新に失敗しました。")
		return
	}
	w.manager.ScheduleWatch(watch)
	respondEphemeral(s, ic, "監視を再開しました。")
}

func (w *WatchCommands) handleSettings(s *discordgo.Session, ic *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	watch, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil || watch == nil {
		respondEphemeral(s, ic, "監視が見つかりません。")
		return
	}
	threshold := int(getOptionInt(options, "threshold"))
	if err := w.setThresholdPercent(watch, threshold); err != nil {
		respondEphemeral(s, ic, fmt.Sprintf("閾値の更新に失敗しました: %v", err))
		return
	}
	respondEphemeral(s, ic, fmt.Sprintf("通知閾値を %d%% に更新しました。", threshold))
}

func (w *WatchCommands) handleDelete(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	user := interactionUser(ic)
	if user == nil {
		respondEphemeral(s, ic, "ユーザー情報を取得できません。")
		return
	}

	watch, err := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if err != nil || watch == nil {
		respondEphemeral(s, ic, "監視が見つかりません。")
		return
	}

	if err := w.deleteWatchAndCleanup(s, watch); err != nil {
		respondEphemeral(s, ic, fmt.Sprintf("削除に失敗しました: %v", err))
		return
	}
	respondEphemeral(s, ic, "監視を削除しました。")
}

func (w *WatchCommands) handleModeratorDelete(s *discordgo.Session, ic *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if ic.Member == nil || !hasPermission(ic.Member, discordgo.PermissionManageChannels) {
		respondEphemeral(s, ic, "このコマンドを実行するにはチャンネル管理権限が必要です。")
		return
	}

	targetID := getOptionUserID(options, "user")
	if targetID == "" {
		respondEphemeral(s, ic, "削除対象のユーザーを指定してください。")
		return
	}

	watch, err := w.storage.GetUserWatch(ic.GuildID, targetID)
	if err != nil || watch == nil {
		respondEphemeral(s, ic, "指定ユーザーの監視が見つかりません。")
		return
	}

	if err := w.deleteWatchAndCleanup(s, watch); err != nil {
		respondEphemeral(s, ic, fmt.Sprintf("削除に失敗しました: %v", err))
		return
	}

	respondEphemeral(s, ic, fmt.Sprintf("ユーザー <@%s> の監視を削除しました。", targetID))
}

func (w *WatchCommands) deleteWatchAndCleanup(s *discordgo.Session, watch *models.Watch) error {
	if watch == nil {
		return fmt.Errorf("watch is nil")
	}

	templateName := watch.Template
	channelID := watch.ChannelID

	watch.Status = models.WatchStatusDeleted
	watch.Template = ""
	watch.Origin = ""
	watch.NextScheduledCheck = time.Time{}

	if err := w.storage.UpdateWatch(watch); err != nil {
		return err
	}

	w.manager.RemoveWatch(watch.ID)

	if templateName != "" {
		if err := w.storage.DeleteTemplateImage(watch.GuildID, templateName); err != nil {
			log.Printf("failed to delete template %s: %v", templateName, err)
		}
	}

	if channelID != "" {
		if _, err := s.ChannelDelete(channelID); err != nil {
			log.Printf("failed to delete channel %s: %v", channelID, err)
		}
	}

	return nil
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "未チェック"
	}
	return t.Local().Format(time.RFC1123)
}

func getOptionString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range opts {
		if opt.Name == name {
			return opt.StringValue()
		}
	}
	return ""
}

func getOptionInt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) int64 {
	for _, opt := range opts {
		if opt.Name == name {
			return opt.IntValue()
		}
	}
	return 0
}

func getOptionUserID(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range opts {
		if opt.Name == name {
			if user := opt.UserValue(nil); user != nil {
				return user.ID
			}
		}
	}
	return ""
}

func getModalValue(ic *discordgo.InteractionCreate, id string) string {
	data := ic.ModalSubmitData()
	for _, comp := range data.Components {
		if row, ok := comp.(*discordgo.ActionsRow); ok {
			for _, inner := range row.Components {
				if input, ok := inner.(*discordgo.TextInput); ok && input.CustomID == id {
					return input.Value
				}
			}
		}
	}
	return ""
}

func respondEphemeral(s *discordgo.Session, ic *discordgo.InteractionCreate, content string) {
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral},
	})
}

func hasPermission(member *discordgo.Member, perm int64) bool {
	if member == nil {
		return false
	}
	return member.Permissions&perm != 0
}

func interactionUser(ic *discordgo.InteractionCreate) *discordgo.User {
	if ic.Member != nil && ic.Member.User != nil {
		return ic.Member.User
	}
	return ic.User
}

const maxTemplateBytes = 8 << 20 // 8MB

var templateHTTPClient = &http.Client{Timeout: 30 * time.Second}

func firstImageAttachment(attachments []*discordgo.MessageAttachment) *discordgo.MessageAttachment {
	for _, a := range attachments {
		if a == nil {
			continue
		}
		if a.ContentType == "" || strings.HasPrefix(a.ContentType, "image/") {
			return a
		}
	}
	return nil
}

func (w *WatchCommands) saveTemplateFromAttachment(guildID, watchID string, attachment *discordgo.MessageAttachment) (string, error) {
	if attachment == nil {
		return "", fmt.Errorf("添付ファイルが見つかりません")
	}
	if attachment.ContentType != "" && !strings.HasPrefix(attachment.ContentType, "image/") {
		return "", fmt.Errorf("画像ファイルを添付してください")
	}
	if attachment.Size > maxTemplateBytes {
		return "", fmt.Errorf("画像サイズが大きすぎます (最大%.1fMB)", float64(maxTemplateBytes)/(1<<20))
	}
	url := attachment.ProxyURL
	if url == "" {
		url = attachment.URL
	}
	if url == "" {
		return "", fmt.Errorf("添付ファイルのURLを取得できません")
	}
	resp, err := templateHTTPClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("テンプレート画像の取得に失敗しました: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("テンプレート画像の取得に失敗しました (status %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTemplateBytes))
	if err != nil {
		return "", fmt.Errorf("テンプレート画像の読み込みに失敗しました: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("画像のデコードに失敗しました")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", fmt.Errorf("PNGエンコードに失敗しました")
	}
	filename := fmt.Sprintf("%s.png", watchID)
	if err := w.storage.SaveTemplateImage(guildID, filename, buf.Bytes()); err != nil {
		return "", err
	}
	return filename, nil
}
