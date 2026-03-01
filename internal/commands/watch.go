package commands

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	_ "image/gif"
	_ "image/jpeg"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golden_wplace_discord_bot/internal/config"
	"golden_wplace_discord_bot/internal/models"
	"golden_wplace_discord_bot/internal/notifications"
	"golden_wplace_discord_bot/internal/storage"
	"golden_wplace_discord_bot/internal/utils"
	"golden_wplace_discord_bot/internal/watchmanager"
	"golden_wplace_discord_bot/internal/wplace"

	"github.com/bwmarrin/discordgo"
)

const (
	watchPanelButtonID = "watch_create_button"
	watchModalID       = "watch_create_modal"
)

const defaultThresholdPercent = models.DefaultThresholdPercent
const quickCommandPrefix = "w!"

type watchCreateInput struct {
	Label string
	Type  models.WatchType
}

// WatchCommands watch系スラッシュコマンド
type WatchCommands struct {
	storage *storage.Storage
	manager *watchmanager.Manager
	config  *config.Config
}

// NewWatchCommands コンストラクタ
func NewWatchCommands(storage *storage.Storage, manager *watchmanager.Manager, cfg *config.Config) *WatchCommands {
	return &WatchCommands{storage: storage, manager: manager, config: cfg}
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
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "now", Description: "このチャンネルの監視を即時取得"},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "settings",
				Description: "監視設定を変更",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "origin", Description: "新しい座標 (例: 1818-806-989-358)", Type: discordgo.ApplicationCommandOptionString, Required: false},
					{Name: "template", Description: "新しいテンプレート画像 (PNG/WebP/JPEG)", Type: discordgo.ApplicationCommandOptionAttachment, Required: false},
					{Name: "visibility", Description: "公開設定 (public / private)", Type: discordgo.ApplicationCommandOptionString, Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "public (公開)", Value: "public"},
						{Name: "private (非公開)", Value: "private"},
					}},
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

	quickCmd := &discordgo.ApplicationCommand{
		Name:        "w",
		Description: "監視ショートカットコマンド",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "now", Description: "このチャンネルの監視を即時取得"},
		},
	}

	for _, cmd := range []*discordgo.ApplicationCommand{watchCmd, panelCmd, quickCmd} {
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
	case "w":
		w.handleQuickAliasCommand(s, ic, data)
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
	case "now":
		w.handleNowSlash(s, ic)
	case "settings":
		w.handleSettings(s, ic, sub.Options)
	case "mod_delete":
		w.handleModeratorDelete(s, ic, sub.Options)
	}
}

func (w *WatchCommands) handleQuickAliasCommand(s *discordgo.Session, ic *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) {
	if len(data.Options) == 0 {
		return
	}

	sub := data.Options[0]
	if sub.Name == "now" {
		w.handleNowSlash(s, ic)
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
	if w.tryHandleQuickTextCommand(s, mc) {
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
	if watch.Visibility == "" {
		w.handleVisibilityInput(s, mc, watch)
		return
	}

	if looksLikeThresholdCommand(mc.Content) {
		w.handleThresholdMessage(s, mc, watch)
	}
}

func (w *WatchCommands) HandleChannelDelete(s *discordgo.Session, cd *discordgo.ChannelDelete) {
	if cd == nil || cd.Channel == nil {
		return
	}
	channel := cd.Channel
	if channel.GuildID == "" {
		return
	}
	watch, err := w.storage.GetWatchByChannel(channel.GuildID, channel.ID)
	if err != nil || watch == nil {
		return
	}
	if err := w.cleanupWatchResources(s, watch, false); err != nil {
		log.Printf("cleanup after channel delete failed: %v", err)
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

	watchTypeInput := strings.ToLower(strings.TrimSpace(string(input.Type)))
	var watchType models.WatchType

	if strings.HasPrefix(watchTypeInput, "v") {
		watchType = models.WatchTypeVandal
	} else {
		// デフォルトまたは "p" から始まる場合は Progress
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

	categoryID, err := w.getOrCreateCategory(s, ic.GuildID)
	if err != nil {
		log.Printf("category error: %v", err)
	}
	log.Printf("using category %s for new channel %s", categoryID, channelName)

	channel, err := s.GuildChannelCreateComplex(ic.GuildID, discordgo.GuildChannelCreateData{
		Name:     channelName,
		Type:     discordgo.ChannelTypeGuildText,
		ParentID: categoryID,
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

	typeLabel := "📈 Progress (進捗追跡)"
	if watchType == models.WatchTypeVandal {
		typeLabel = "🚨 Vandal (荒らし検知)"
	}

	intro := fmt.Sprintf("👋 %s さんの監視チャンネルを作成しました。\nタイプ: **%s**\n\n**ステップ1**: このチャンネルで `1234-567-890-123` のようなフォーマットで座標を送信してください。\n**ステップ2**: 座標が登録されたら、テンプレート画像(PNG/WebP/JPEG)をアップロードしてください。\n**ステップ3**: 最後に、チャンネルを全体公開(Public)にするか、非公開(Private)のままにするかを選んでください。\n\nすべて完了すると監視が自動的に開始されます。", user.Mention(), typeLabel)
	_, _ = s.ChannelMessageSend(channel.ID, intro)

	respondEphemeral(s, ic, fmt.Sprintf("監視チャンネル <#%s> を作成しました。", channel.ID))
}

func (w *WatchCommands) getOrCreateCategory(s *discordgo.Session, guildID string) (string, error) {
	if w.config == nil || w.config.WatchCategoryName == "" {
		return "", nil
	}

	channels, err := s.GuildChannels(guildID)
	if err != nil {
		return "", err
	}

	for _, ch := range channels {
		if ch.Type == discordgo.ChannelTypeGuildCategory && strings.EqualFold(ch.Name, w.config.WatchCategoryName) {
			return ch.ID, nil
		}
	}

	category, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name: w.config.WatchCategoryName,
		Type: discordgo.ChannelTypeGuildCategory,
	})
	if err != nil {
		return "", err
	}
	return category.ID, nil
}

// ReorganizeChannels 既存の全監視チャンネルをカテゴリへ整理
func (w *WatchCommands) ReorganizeChannels(s *discordgo.Session) error {
	guildIDs, err := w.storage.ListGuildIDs()
	if err != nil {
		return err
	}

	for _, guildID := range guildIDs {
		categoryID, err := w.getOrCreateCategory(s, guildID)
		if err != nil || categoryID == "" {
			log.Printf("skipping reorganization for guild %s: category error or not configured", guildID)
			continue
		}

		guildData, err := w.storage.LoadGuildWatches(guildID)
		if err != nil {
			continue
		}

		for _, watch := range guildData.Watches {
			if watch.ChannelID == "" || watch.Status == models.WatchStatusDeleted {
				continue
			}

			ch, err := s.Channel(watch.ChannelID)
			if err != nil {
				continue
			}

			// カテゴリが未設定、または別のカテゴリにいる場合は移動
			if ch.ParentID != categoryID {
				log.Printf("reorganizing channel %s (%s) to category %s", ch.Name, ch.ID, categoryID)
				_, err = s.ChannelEditComplex(watch.ChannelID, &discordgo.ChannelEdit{
					ParentID: categoryID,
				})
				if err != nil {
					log.Printf("failed to move channel %s: %v", watch.ChannelID, err)
				}
			}
		}
	}
	return nil
}

func (w *WatchCommands) handleOriginInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	text := strings.TrimSpace(mc.Content)
	if text == "" {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "座標は `タイルX-タイルY-ピクセルX-ピクセルY` 形式で入力してください。例: `1818-806-989-358`")
		return
	}
	coord, err := utils.ParseOrigin(text)
	if err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("座標の形式が正しくありません: %v", err))
		return
	}

	// テンプレートが既にある場合はタイル数をチェック
	if watch.Template != "" {
		templatePath := w.storage.GetTemplateImagePath(watch.GuildID, watch.Template)
		width, height, err := getImageDimensions(templatePath)
		if err == nil {
			tiles := utils.CountRequiredTiles(coord, width, height)
			if tiles > models.MaxWatchTiles {
				_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("❌ 監視範囲が広すぎます (%dタイル)。最大 %d タイルまで許可されています。\n範囲を狭めるか、座標をタイルの境界に寄せるなどして調整してください。", tiles, models.MaxWatchTiles))
				return
			}
		}
	}

	watch.Origin = text
	if err := w.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to update watch origin: %v", err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, "座標の保存中にエラーが発生しました。少し待って再度お試しください。")
		return
	}

	if watch.Template == "" {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ 座標を登録しました。次にテンプレート画像(PNG/WebP/JPEG)をアップロードしてください。")
	} else if watch.Visibility == "" {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ 座標を登録しました。\n\n**ステップ3**: 公開設定を選択してください。`Public` (または `o`)、`Private` (または `p`) と入力してください。")
	}
}

func (w *WatchCommands) handleTemplateInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	attachment := firstImageAttachment(mc.Message.Attachments)
	if attachment == nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "テンプレート画像(PNG)を添付してください。")
		return
	}
	filename, width, height, err := w.saveTemplateFromAttachment(watch.GuildID, watch.ID, attachment)
	if err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("テンプレート画像の保存に失敗しました: %v", err))
		return
	}

	// 座標が既にある場合はタイル数をチェック
	if watch.Origin != "" {
		coord, _ := utils.ParseOrigin(watch.Origin)
		tiles := utils.CountRequiredTiles(coord, width, height)
		if tiles > models.MaxWatchTiles {
			// 失敗したらテンプレート保存をロールバック（削除）
			_ = w.storage.DeleteTemplateImage(watch.GuildID, filename)
			_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("❌ テンプレートの範囲が広すぎます (%dタイル)。最大 %d タイルまで許可されています。\n別のテンプレートを使うか、座標をタイルの境界に寄せる、あるいは画像を小さくして再アップロードしてください。", tiles, models.MaxWatchTiles))
			return
		}
	}

	watch.Template = filename

	if err := w.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to update watch template: %v", err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, "テンプレートの保存中にエラーが発生しました。")
		return
	}

	_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ テンプレートを登録しました。\n\n**ステップ3**: 公開設定を選択してください。\nこのチャンネルをサーバー全員に閲覧可能(Public)にする場合は `Public` (または `o`)、自分のみのまま(Private)にする場合は `Private` (または `p`) と入力してください。")
}

func (w *WatchCommands) handleVisibilityInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	text := strings.ToLower(strings.TrimSpace(mc.Content))
	var visibility models.WatchVisibility

	if text == "public" || text == "o" || text == "open" {
		visibility = models.WatchVisibilityPublic
	} else if text == "private" || text == "p" {
		visibility = models.WatchVisibilityPrivate
	} else {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "⚠️ `Public` または `Private` (または `o`/`p`) で入力してください。")
		return
	}

	watch.Visibility = visibility

	// Publicならチャンネル閲覧権限と履歴閲覧をサーバー全体(everyone)に付与、ただし発言は禁止
	if visibility == models.WatchVisibilityPublic {
		err := s.ChannelPermissionSet(watch.ChannelID, watch.GuildID, discordgo.PermissionOverwriteTypeRole,
			discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory,
			discordgo.PermissionSendMessages)
		if err != nil {
			log.Printf("failed to update channel permissions: %v", err)
			_, _ = s.ChannelMessageSend(mc.ChannelID, "⚠️ チャンネルの公開設定の変更に失敗しました。権限を確認してください。")
			return
		}
	}

	watch.Status = models.WatchStatusActive
	watch.NextScheduledCheck = time.Now().Add(5 * time.Minute)

	if err := w.storage.UpdateWatch(watch); err != nil {
		log.Printf("failed to finalise watch setup: %v", err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, "⚠️ 設定の最終保存中にエラーが発生しました。")
		return
	}

	visLabel := "🔒 Private (非公開)"
	if visibility == models.WatchVisibilityPublic {
		visLabel = "🌍 Public (公開)"
	}

	_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("✅ すべてのセットアップが完了しました！\n\n設定: %s\n監視を開始します。最初の結果は数分以内に投稿されます。", visLabel))
	w.manager.ScheduleWatch(watch)
	w.manager.TriggerImmediateRun(watch)
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

	newOrigin := getOptionString(options, "origin")
	newVisibility := getOptionString(options, "visibility")
	newTemplateID := getOptionAttachmentID(options, "template")
	var newTemplate *discordgo.MessageAttachment
	if newTemplateID != "" {
		data := ic.ApplicationCommandData()
		if data.Resolved != nil && data.Resolved.Attachments != nil {
			newTemplate = data.Resolved.Attachments[newTemplateID]
		}
	}

	if newOrigin == "" && newTemplate == nil && newVisibility == "" {
		respondEphemeral(s, ic, "変更する設定（座標、テンプレート、または公開設定）を指定してください。")
		return
	}

	// 1. 座標のパース
	var coord *utils.Coordinate
	if newOrigin != "" {
		coord, err = utils.ParseOrigin(newOrigin)
		if err != nil {
			respondEphemeral(s, ic, fmt.Sprintf("座標の形式が正しくありません: %v", err))
			return
		}
	} else if watch.Origin != "" {
		coord, _ = utils.ParseOrigin(watch.Origin)
	}

	// 2. テンプレートの保存（もしあれば）
	var savedFilename string
	var width, height int
	if newTemplate != nil {
		filename, w, h, err := w.saveTemplateFromAttachment(watch.GuildID, watch.ID, newTemplate)
		if err != nil {
			respondEphemeral(s, ic, fmt.Sprintf("テンプレートの保存に失敗しました: %v", err))
			return
		}
		savedFilename = filename
		width, height = w, h
	} else if watch.Template != "" {
		templatePath := w.storage.GetTemplateImagePath(watch.GuildID, watch.Template)
		width, height, err = getImageDimensions(templatePath)
		if err != nil {
			// ファイルがないなどの異常事態
			log.Printf("failed to get existing template size: %v", err)
		}
	}

	// 3. タイル枚数のバリデーション（座標とテンプレの両方がある場合）
	if coord != nil && width > 0 && height > 0 {
		tiles := utils.CountRequiredTiles(coord, width, height)
		if tiles > models.MaxWatchTiles {
			if savedFilename != "" {
				_ = w.storage.DeleteTemplateImage(watch.GuildID, savedFilename)
			}
			respondEphemeral(s, ic, fmt.Sprintf("❌ 監視範囲が広すぎます (%dタイル)。最大 %d タイルまで許可されています。\n範囲を狭めるか、座標を調整してください。", tiles, models.MaxWatchTiles))
			return
		}
	}

	// 4. 更新処理
	updatedFields := []string{}
	if newOrigin != "" {
		watch.Origin = newOrigin
		updatedFields = append(updatedFields, "座標")
	}
	if newVisibility != "" {
		vis := models.WatchVisibility(newVisibility)
		if vis == models.WatchVisibilityPublic {
			// 公開設定: 全員に閲覧と履歴表示を許可、ただし発言は禁止
			_ = s.ChannelPermissionSet(watch.ChannelID, watch.GuildID, discordgo.PermissionOverwriteTypeRole,
				discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory,
				discordgo.PermissionSendMessages)
			updatedFields = append(updatedFields, "公開設定 (Public)")
		} else {
			// 非公開設定: 全員に対して閲覧権限を剥奪
			_ = s.ChannelPermissionSet(watch.ChannelID, watch.GuildID, discordgo.PermissionOverwriteTypeRole,
				0, discordgo.PermissionViewChannel)
			updatedFields = append(updatedFields, "公開設定 (Private)")
		}
		watch.Visibility = vis
	}
	if savedFilename != "" {
		// 古い画像を削除
		if watch.Template != "" && watch.Template != savedFilename {
			_ = w.storage.DeleteTemplateImage(watch.GuildID, watch.Template)
		}
		watch.Template = savedFilename
		updatedFields = append(updatedFields, "テンプレート画像")
	}

	if watch.Origin != "" && watch.Template != "" && watch.Status == models.WatchStatusPending {
		watch.Status = models.WatchStatusActive
		watch.NextScheduledCheck = time.Now().Add(5 * time.Minute)
		updatedFields = append(updatedFields, "監視ステータス(Active)")
	}

	if err := w.storage.UpdateWatch(watch); err != nil {
		respondEphemeral(s, ic, "設定の更新に失敗しました。")
		return
	}

	// アクティブならマネージャーを更新
	if watch.Status == models.WatchStatusActive {
		w.manager.ScheduleWatch(watch)
		w.manager.TriggerImmediateRun(watch)
	}

	respondEphemeral(s, ic, fmt.Sprintf("✅ 監視設定（%s）を更新しました。", strings.Join(updatedFields, "、")))
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
	return w.cleanupWatchResources(s, watch, true)
}

func (w *WatchCommands) cleanupWatchResources(s *discordgo.Session, watch *models.Watch, deleteChannel bool) error {
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

	if err := w.storage.RemoveWatchRecord(watch.GuildID, watch.ID); err != nil {
		log.Printf("failed to purge watch %s: %v", watch.ID, err)
	}

	if deleteChannel && channelID != "" {
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

func getOptionAttachmentID(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, opt := range opts {
		if opt.Name == name {
			// In slash commands, attachment option value is the ID string
			return opt.StringValue()
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

func (w *WatchCommands) tryHandleQuickTextCommand(s *discordgo.Session, mc *discordgo.MessageCreate) bool {
	content := strings.TrimSpace(mc.Content)
	if len(content) < len(quickCommandPrefix) {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(content), quickCommandPrefix) {
		return false
	}

	payload := strings.TrimSpace(content[len(quickCommandPrefix):])
	if payload == "" {
		return true
	}

	if strings.EqualFold(payload, "now") {
		watch, err := w.storage.GetWatchByChannel(mc.GuildID, mc.ChannelID)
		if err != nil {
			log.Printf("failed to load watch for quick now: %v", err)
			_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ 監視状況の取得に失敗しました。")
			return true
		}
		if watch == nil {
			_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ このチャンネルに稼働中の監視がありません。")
			return true
		}
		w.sendWatchNowMessage(s, mc.ChannelID, watch, mc.Author.ID, false)
		return true
	}

	target, err := w.storage.GetWatchByLabel(mc.GuildID, payload)
	if err != nil {
		log.Printf("failed to find watch by label %q: %v", payload, err)
		_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ 監視状況の取得に失敗しました。")
		return true
	}
	if target == nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("❌ `%s` という監視は見つかりません。", payload))
		return true
	}

	w.sendWatchNowMessage(s, mc.ChannelID, target, mc.Author.ID, target.ChannelID != mc.ChannelID)
	return true
}

func (w *WatchCommands) handleNowSlash(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	if ic.GuildID == "" {
		respondEphemeral(s, ic, "ギルド内でのみ利用できます。")
		return
	}

	watch, err := w.storage.GetWatchByChannel(ic.GuildID, ic.ChannelID)
	if err != nil {
		log.Printf("failed to load watch for /watch now: %v", err)
		respondEphemeral(s, ic, "監視状況の取得に失敗しました。")
		return
	}
	if watch == nil {
		respondEphemeral(s, ic, "このチャンネルに稼働中の監視がありません。")
		return
	}

	if err := s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource}); err != nil {
		log.Printf("failed to defer now command: %v", err)
		return
	}

	result, err := w.runWatchSnapshot(watch)
	if err != nil {
		log.Printf("failed to run immediate snapshot: %v", err)
		msg := fmt.Sprintf("❌ 監視の取得に失敗しました: %v", err)
		_, _ = s.InteractionResponseEdit(ic.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}

	embed := notifications.BuildWatchEmbed("🔎 現在の監視状況", 0xFFD700, watch, result)
	if requester := interactionUser(ic); requester != nil {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Requested by %s", requester.Username)}
	}
	files := prepareWatchResultAssets(result, embed)

	edit := &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}}
	if len(files) > 0 {
		edit.Files = files
	}

	if _, err := s.InteractionResponseEdit(ic.Interaction, edit); err != nil {
		log.Printf("failed to send now response: %v", err)
	}
}

func (w *WatchCommands) sendWatchNowMessage(s *discordgo.Session, channelID string, watch *models.Watch, requesterID string, includeChannelField bool) {
	if watch == nil {
		_, _ = s.ChannelMessageSend(channelID, "❌ 監視が見つかりません。")
		return
	}
	_ = s.ChannelTyping(channelID)

	result, err := w.runWatchSnapshot(watch)
	if err != nil {
		log.Printf("failed to run watch snapshot: %v", err)
		_, _ = s.ChannelMessageSend(channelID, fmt.Sprintf("❌ 監視状況を取得できませんでした: %v", err))
		return
	}

	embed := notifications.BuildWatchEmbed("🔎 現在の監視状況", 0xFFD700, watch, result)
	if includeChannelField {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{Name: "監視チャンネル", Value: fmt.Sprintf("<#%s>", watch.ChannelID), Inline: true})
	}
	if requesterID != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Requested by <@%s>", requesterID)}
	}
	files := prepareWatchResultAssets(result, embed)

	msg := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}, Files: files}
	if _, err := s.ChannelMessageSendComplex(channelID, msg); err != nil {
		log.Printf("failed to send watch now message: %v", err)
	}
}

func prepareWatchResultAssets(result *wplace.Result, embed *discordgo.MessageEmbed) []*discordgo.File {
	if result == nil || embed == nil {
		return nil
	}
	if len(result.PreviewPNG) > 0 {
		embed.Image = &discordgo.MessageEmbedImage{URL: "attachment://watch_preview.png"}
		return []*discordgo.File{
			{Name: "watch_preview.png", ContentType: "image/png", Reader: bytes.NewReader(result.PreviewPNG)},
		}
	}
	if result.SnapshotURL != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: result.SnapshotURL}
	}
	return nil
}

func (w *WatchCommands) runWatchSnapshot(watch *models.Watch) (*wplace.Result, error) {
	if w.manager == nil {
		return nil, fmt.Errorf("監視エンジンが初期化されていません")
	}
	if watch == nil {
		return nil, fmt.Errorf("監視が見つかりません")
	}
	if watch.Status != models.WatchStatusActive {
		return nil, fmt.Errorf("監視が停止中です")
	}
	if watch.Origin == "" || watch.Template == "" {
		return nil, fmt.Errorf("監視のセットアップが完了していません")
	}
	return w.manager.RunWatchNow(watch)
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

func (w *WatchCommands) saveTemplateFromAttachment(guildID, watchID string, attachment *discordgo.MessageAttachment) (string, int, int, error) {
	if attachment == nil {
		return "", 0, 0, fmt.Errorf("添付ファイルが見つかりません")
	}
	if attachment.ContentType != "" && !strings.HasPrefix(attachment.ContentType, "image/") {
		return "", 0, 0, fmt.Errorf("画像ファイルを添付してください")
	}
	if attachment.Size > maxTemplateBytes {
		return "", 0, 0, fmt.Errorf("画像サイズが大きすぎます (最大%.1fMB)", float64(maxTemplateBytes)/(1<<20))
	}
	url := attachment.ProxyURL
	if url == "" {
		url = attachment.URL
	}
	if url == "" {
		return "", 0, 0, fmt.Errorf("添付ファイルのURLを取得できません")
	}
	resp, err := templateHTTPClient.Get(url)
	if err != nil {
		return "", 0, 0, fmt.Errorf("テンプレート画像の取得に失敗しました: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, 0, fmt.Errorf("テンプレート画像の取得に失敗しました (status %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTemplateBytes))
	if err != nil {
		return "", 0, 0, fmt.Errorf("テンプレート画像の読み込みに失敗しました: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, fmt.Errorf("画像のデコードに失敗しました")
	}

	bounds := img.Bounds()
	width, height := bounds.Dx(), bounds.Dy()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", 0, 0, fmt.Errorf("PNGエンコードに失敗しました")
	}
	filename := fmt.Sprintf("%s.png", watchID)
	if err := w.storage.SaveTemplateImage(guildID, filename, buf.Bytes()); err != nil {
		return "", 0, 0, err
	}
	return filename, width, height, nil
}

func getImageDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	config, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}
