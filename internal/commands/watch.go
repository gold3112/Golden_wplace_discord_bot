package commands

import (
	"fmt"
	"log"
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

type watchCreateInput struct {
	Label       string
	Type        models.WatchType
	Origin      string
	Threshold   int
	TemplateURL string
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
					{Name: "origin", Description: "Origin 座標 (例: 1818-806-989-358)", Type: discordgo.ApplicationCommandOptionString, Required: true},
					{Name: "threshold", Description: "通知閾値(px)", Type: discordgo.ApplicationCommandOptionInteger, Required: false},
					{Name: "template_url", Description: "テンプレート画像URL", Type: discordgo.ApplicationCommandOptionString, Required: false},
				},
			},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "status", Description: "自分の監視ステータスを表示"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "pause", Description: "監視を一時停止"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "resume", Description: "監視を再開"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "delete", Description: "監視を削除"},
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
			Label:       getOptionString(sub.Options, "label"),
			Type:        models.WatchType(strings.ToLower(getOptionString(sub.Options, "type"))),
			Origin:      getOptionString(sub.Options, "origin"),
			Threshold:   int(getOptionInt(sub.Options, "threshold")),
			TemplateURL: getOptionString(sub.Options, "template_url"),
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
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "origin", Label: "Origin 座標", Style: discordgo.TextInputShort, Required: true, Placeholder: "1818-806-989-358"},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "threshold", Label: "通知閾値(px)", Style: discordgo.TextInputShort, Required: false, Placeholder: "5"},
				}},
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "template_url", Label: "テンプレート画像URL", Style: discordgo.TextInputParagraph, Required: false},
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

	threshold := 0
	if raw := strings.TrimSpace(getModalValue(ic, "threshold")); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			threshold = v
		}
	}

	input := watchCreateInput{
		Label:       getModalValue(ic, "label"),
		Type:        models.WatchType(strings.ToLower(getModalValue(ic, "type"))),
		Origin:      getModalValue(ic, "origin"),
		Threshold:   threshold,
		TemplateURL: getModalValue(ic, "template_url"),
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
	origin := strings.TrimSpace(input.Origin)
	if origin == "" {
		respondEphemeral(s, ic, "Origin 座標を入力してください。")
		return
	}

	watchType := models.WatchType(strings.ToLower(string(input.Type)))
	if watchType != models.WatchTypeProgress && watchType != models.WatchTypeVandal {
		watchType = models.WatchTypeProgress
	}

	threshold := input.Threshold
	if threshold <= 0 {
		threshold = 5
	}
	if threshold > 500 {
		threshold = 500
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
			{ID: user.ID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory},
		},
	})
	if err != nil {
		log.Printf("failed to create channel: %v", err)
		respondEphemeral(s, ic, "チャンネル作成に失敗しました。権限を確認してください。")
		return
	}

	now := time.Now().UTC()
	watch := &models.Watch{
		ID:                 watchID,
		Label:              label,
		OwnerID:            user.ID,
		GuildID:            ic.GuildID,
		ChannelID:          channel.ID,
		Type:               watchType,
		Origin:             origin,
		Template:           strings.TrimSpace(input.TemplateURL),
		ThresholdPixels:    threshold,
		Status:             models.WatchStatusActive,
		CreatedAt:          now,
		NextScheduledCheck: now.Add(5 * time.Minute),
	}

	if err := w.storage.AddWatch(watch); err != nil {
		respondEphemeral(s, ic, "監視設定の保存に失敗しました。")
		return
	}

	w.manager.ScheduleWatch(watch)

	intro := fmt.Sprintf("👋 %s さん用の監視チャンネルです。\n1. テンプレート画像を添付\n2. 必要なら追加情報を記入\n監視は5分間隔で実行されます。", user.Mention())
	_, _ = s.ChannelMessageSend(channel.ID, intro)

	respondEphemeral(s, ic, fmt.Sprintf("監視チャンネル <#%s> を作成しました。", channel.ID))
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

	status := fmt.Sprintf("状態: %s\n閾値: %dpx\n最終チェック: %s\n次回予定: %s\nチャンネル: <#%s>",
		watch.Status,
		watch.ThresholdPixels,
		formatTime(watch.LastCheckedAt),
		next,
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

	watch.Status = models.WatchStatusDeleted
	if err := w.storage.UpdateWatch(watch); err != nil {
		respondEphemeral(s, ic, "削除に失敗しました。")
		return
	}
	w.manager.RemoveWatch(watch.ID)

	if _, err := s.ChannelDelete(watch.ChannelID); err != nil {
		log.Printf("failed to delete channel %s: %v", watch.ChannelID, err)
	}
	respondEphemeral(s, ic, "監視を削除しました。")
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
