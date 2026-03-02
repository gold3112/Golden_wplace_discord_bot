package commands

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	_ "image/gif"
	_ "image/jpeg"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

	btnTypeProgress = "setup_type_progress"
	btnTypeVandal   = "setup_type_vandal"
	btnVisPublic    = "setup_vis_public"
	btnVisPrivate   = "setup_vis_private"
	btnFixApply     = "setup_fix_apply"
	btnFixCancel    = "setup_fix_cancel"
)

const defaultThresholdPercent = models.DefaultThresholdPercent
const quickCommandPrefix = "w!"

type watchCreateInput struct {
	Label string
}

type WatchCommands struct {
	storage *storage.Storage
	manager *watchmanager.Manager
	config  *config.Config
}

func NewWatchCommands(storage *storage.Storage, manager *watchmanager.Manager, cfg *config.Config) *WatchCommands {
	return &WatchCommands{storage: storage, manager: manager, config: cfg}
}

func (w *WatchCommands) Register(session *discordgo.Session, appID string) error {
	watchCmd := &discordgo.ApplicationCommand{
		Name:        "watch",
		Description: "Golden watch utilities",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "init",
				Description: "[管理者専用] このチャンネルを監視チャンネルとして初期化",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "label", Description: "監視の表示名", Type: discordgo.ApplicationCommandOptionString, Required: true},
				},
			},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "status", Description: "自分の監視ステータスを表示"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "pause", Description: "監視の一時停止"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "resume", Description: "監視の再開"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "delete", Description: "監視の削除"},
			{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "now", Description: "このチャンネルの監視を即時取得"},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "settings",
				Description: "現在の設定確認と変更",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "origin", Description: "座標を変更 (例: 1818-806-989-358)", Type: discordgo.ApplicationCommandOptionString, Required: false},
					{Name: "template", Description: "テンプレート画像を変更", Type: discordgo.ApplicationCommandOptionAttachment, Required: false},
					{Name: "threshold", Description: "通知閾値 (10%〜100%、10%刻み)", Type: discordgo.ApplicationCommandOptionInteger, Required: false},
					{Name: "type", Description: "タイプを変更", Type: discordgo.ApplicationCommandOptionString, Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "Progress (進捗追跡)", Value: "progress"},
						{Name: "Vandal (荒らし検知)", Value: "vandal"},
					}},
					{Name: "visibility", Description: "公開設定を変更", Type: discordgo.ApplicationCommandOptionString, Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "Public (全体公開)", Value: "public"},
						{Name: "Private (自分のみ)", Value: "private"},
					}},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "mod_delete",
				Description: "[管理者専用] 指定ユーザーの監視を強制削除",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "user", Description: "対象ユーザー", Type: discordgo.ApplicationCommandOptionUser, Required: true},
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
		sub := data.Options[0]
		switch sub.Name {
		case "init":
			w.handleInitCommand(s, ic, sub.Options)
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
	case "createmonitor":
		w.handleCreateMonitorCommand(s, ic)
	case "w":
		sub := data.Options[0]
		if sub.Name == "now" {
			w.handleNowSlash(s, ic)
		}
	}
}

func (w *WatchCommands) HandleMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	if mc.Author == nil || mc.Author.Bot || mc.GuildID == "" {
		return
	}
	if w.tryHandleQuickTextCommand(s, mc) {
		return
	}
	watch, err := w.storage.GetWatchByChannel(mc.GuildID, mc.ChannelID)
	if err != nil || watch == nil || watch.Status == models.WatchStatusDeleted {
		return
	}

	// セットアップ中の入力処理（オーナーのみ）
	if watch.OwnerID == mc.Author.ID {
		if watch.Type == "" {
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
	}

	// 閾値変更コマンド
	if looksLikeThresholdCommand(mc.Content) {
		w.handleThresholdMessage(s, mc, watch)
	}
}

func (w *WatchCommands) handleComponentInteraction(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	data := ic.MessageComponentData()
	if data.CustomID == watchPanelButtonID {
		w.presentCreateModal(s, ic)
		return
	}
	btnID, watchID := w.parseButtonID(data.CustomID)
	switch btnID {
	case btnTypeProgress, btnTypeVandal:
		w.handleTypeButton(s, ic, watchID, btnID)
	case btnFixApply, btnFixCancel:
		w.handleFixButton(s, ic, watchID, btnID)
	case btnVisPublic, btnVisPrivate:
		w.handleVisButton(s, ic, watchID, btnID)
	case "edit_origin":
		w.presentEditOriginModal(s, ic, watchID)
	case "edit_threshold":
		w.presentEditThresholdModal(s, ic, watchID)
	case "edit_template":
		respondEphemeral(s, ic, "🖼️ 新しいテンプレート画像をこのチャンネルにアップロードしてください。")
	case "edit_type":
		w.handleToggleType(s, ic, watchID)
	case "edit_vis":
		w.handleToggleVis(s, ic, watchID)
	}
}

func (w *WatchCommands) makeButtonID(btnID, watchID string) string { return btnID + ":" + watchID }
func (w *WatchCommands) parseButtonID(customID string) (string, string) {
	parts := strings.Split(customID, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return customID, ""
}

func (w *WatchCommands) handleModalSubmit(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	data := ic.ModalSubmitData()
	if data.CustomID == watchModalID {
		w.processCreateRequest(s, ic, watchCreateInput{Label: getModalValue(ic, "label")}, false)
		return
	}

	btnID, watchID := w.parseButtonID(data.CustomID)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID {
		respondEphemeral(s, ic, "❌ セッションが切れました。再度コマンドを実行してください。")
		return
	}

	switch btnID {
	case "modal_edit_origin":
		val := getModalValue(ic, "origin")
		if _, err := utils.ParseOrigin(val); err != nil {
			respondEphemeral(s, ic, "❌ 座標形式が不正です。")
			return
		}
		wt.Origin = val
		_ = w.storage.UpdateWatch(wt)
		if wt.Status == models.WatchStatusActive {
			w.manager.ScheduleWatch(wt)
		}
		respondEphemeral(s, ic, "✅ 座標を更新しました。")
	case "modal_edit_threshold":
		val, _ := strconv.Atoi(getModalValue(ic, "threshold"))
		if val < 10 || val > 100 || val%10 != 0 {
			respondEphemeral(s, ic, "❌ 閾値は10〜100の間で10刻みで指定してください。")
			return
		}
		wt.ThresholdPercent = float64(val)
		_ = w.storage.UpdateWatch(wt)
		respondEphemeral(s, ic, fmt.Sprintf("✅ 閾値を %d%% に更新しました。", val))
	}
}

func (w *WatchCommands) presentEditOriginModal(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID string) {
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: w.makeButtonID("modal_edit_origin", watchID),
			Title:    "座標の変更",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "origin", Label: "新しい座標", Style: discordgo.TextInputShort, Required: true, Placeholder: "例: 1818-806-989-358"},
				}},
			},
		},
	})
}

func (w *WatchCommands) presentEditThresholdModal(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID string) {
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: w.makeButtonID("modal_edit_threshold", watchID),
			Title:    "閾値の変更",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "threshold", Label: "新しい閾値 (10〜100)", Style: discordgo.TextInputShort, Required: true, Placeholder: "例: 30"},
				}},
			},
		},
	})
}

func (w *WatchCommands) handleToggleType(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID string) {
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID {
		return
	}
	if wt.Type == models.WatchTypeProgress {
		wt.Type = models.WatchTypeVandal
	} else {
		wt.Type = models.WatchTypeProgress
	}
	_ = w.storage.UpdateWatch(wt)
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("✅ 監視タイプを **%s** に変更しました。", wt.Type), Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (w *WatchCommands) handleToggleVis(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID string) {
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID {
		return
	}
	if wt.Visibility == models.WatchVisibilityPublic {
		wt.Visibility = models.WatchVisibilityPrivate
		_ = s.ChannelPermissionDelete(wt.ChannelID, wt.GuildID)
	} else {
		wt.Visibility = models.WatchVisibilityPublic
		_ = s.ChannelPermissionSet(wt.ChannelID, wt.GuildID, discordgo.PermissionOverwriteTypeRole, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory, discordgo.PermissionSendMessages)
	}
	_ = w.storage.UpdateWatch(wt)
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: fmt.Sprintf("✅ 公開設定を **%s** に変更しました。", wt.Visibility), Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (w *WatchCommands) handleInitCommand(s *discordgo.Session, ic *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	if !hasPermission(ic.Member, discordgo.PermissionManageChannels) {
		respondEphemeral(s, ic, "このコマンドを実行するにはチャンネル管理権限が必要です。")
		return
	}
	w.processCreateRequest(s, ic, watchCreateInput{Label: getOptionString(opts, "label")}, true)
}

func (w *WatchCommands) processCreateRequest(s *discordgo.Session, ic *discordgo.InteractionCreate, input watchCreateInput, isExternal bool) {
	user := interactionUser(ic)
	label := strings.TrimSpace(input.Label)
	if label == "" {
		respondEphemeral(s, ic, "名前を入力してください。")
		return
	}

	existing, _ := w.storage.GetUserWatch(ic.GuildID, user.ID)
	if existing != nil && existing.Status != models.WatchStatusDeleted {
		respondEphemeral(s, ic, "既に監視チャンネルが存在します。`/watch status` で確認してください。")
		return
	}

	watchID := utils.GenerateWatchID(user.ID)
	var channelID string

	if isExternal {
		channelID = ic.ChannelID
	} else {
		categoryID, _ := w.getOrCreateCategory(s, ic.GuildID)
		channel, err := s.GuildChannelCreateComplex(ic.GuildID, discordgo.GuildChannelCreateData{
			Name:     utils.SlugifyChannelName(label),
			Type:     discordgo.ChannelTypeGuildText,
			ParentID: categoryID,
			PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: ic.GuildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
				{ID: user.ID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles},
			},
		})
		if err != nil {
			respondEphemeral(s, ic, "❌ チャンネル作成に失敗しました。")
			return
		}
		channelID = channel.ID
	}

	watch := &models.Watch{
		ID:                watchID,
		Label:             label,
		OwnerID:           user.ID,
		GuildID:           ic.GuildID,
		ChannelID:         channelID,
		Status:            models.WatchStatusPending,
		IsExternalChannel: isExternal,
		CreatedAt:         time.Now().UTC(),
		ThresholdPercent:  defaultThresholdPercent,
	}
	_ = w.storage.AddWatch(watch)

	msg := &discordgo.MessageSend{
		Content: fmt.Sprintf("👋 %s さんの監視チャンネルとして初期化しました。\n\n**ステップ1**: 監視タイプを選択してください。", user.Mention()),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "Progress (進捗追跡)", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID(btnTypeProgress, watchID)},
					discordgo.Button{Label: "Vandal (荒らし検知)", Style: discordgo.DangerButton, CustomID: w.makeButtonID(btnTypeVandal, watchID)},
				},
			},
		},
	}
	_, _ = s.ChannelMessageSendComplex(channelID, msg)
	if isExternal {
		respondEphemeral(s, ic, "このチャンネルを監視用に初期化しました。")
	} else {
		respondEphemeral(s, ic, fmt.Sprintf("作成しました <#%s>", channelID))
	}
}

func (w *WatchCommands) getOrCreateCategory(s *discordgo.Session, guildID string) (string, error) {
	if w.config == nil || w.config.WatchCategoryName == "" {
		return "", nil
	}
	chs, _ := s.GuildChannels(guildID)
	for _, ch := range chs {
		if ch.Type == discordgo.ChannelTypeGuildCategory && strings.EqualFold(ch.Name, w.config.WatchCategoryName) {
			return ch.ID, nil
		}
	}
	cat, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{Name: w.config.WatchCategoryName, Type: discordgo.ChannelTypeGuildCategory})
	if err != nil {
		return "", err
	}
	return cat.ID, nil
}

func (w *WatchCommands) ReorganizeChannels(s *discordgo.Session) error {
	guildIDs, _ := w.storage.ListGuildIDs()
	for _, gID := range guildIDs {
		cID, _ := w.getOrCreateCategory(s, gID)
		if cID == "" {
			continue
		}
		data, _ := w.storage.LoadGuildWatches(gID)
		for _, wt := range data.Watches {
			if wt.ChannelID != "" && wt.Status != models.WatchStatusDeleted {
				ch, err := s.Channel(wt.ChannelID)
				if err == nil && ch.ParentID != cID {
					_, _ = s.ChannelEditComplex(wt.ChannelID, &discordgo.ChannelEdit{ParentID: cID})
				}
			}
		}
	}
	return nil
}

func (w *WatchCommands) handleTypeButton(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID, btnID string) {
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID {
		return
	}
	if btnID == btnTypeProgress {
		wt.Type = models.WatchTypeProgress
	} else {
		wt.Type = models.WatchTypeVandal
	}
	_ = w.storage.UpdateWatch(wt)
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("✅ 監視タイプを **%s** に設定しました。\n\n**ステップ2**: 座標を入力してください（例: `1818-806-989-358`）。", wt.Type),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (w *WatchCommands) handleOriginInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	text := strings.TrimSpace(mc.Content)
	coord, err := utils.ParseOrigin(text)
	if err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ 座標の形式が正しくありません。`1818-806-989-358` のような形式で送信してください。")
		return
	}

	// テンプレートが既にある場合はタイル数をチェック
	if watch.Template != "" {
		templatePath := w.storage.GetTemplateImagePath(watch.GuildID, watch.Template)
		width, height, err := getImageDimensions(templatePath)
		if err == nil {
			tiles := utils.CountRequiredTiles(coord, width, height)
			if tiles > models.MaxWatchTiles {
				_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("❌ 監視範囲が広すぎます (%dタイル)。最大 %d タイルまで許可されています。\n範囲を狭めるか、座標を調整してください。", tiles, models.MaxWatchTiles))
				return
			}
		}
	}

	watch.Origin = text
	_ = w.storage.UpdateWatch(watch)

	if watch.Template == "" {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ 座標を登録しました。次にテンプレート画像をアップロードしてください。")
	} else if watch.IsExternalChannel {
		w.finalizeSetup(s, mc.ChannelID, watch)
	} else {
		w.promptVisibility(s, mc.ChannelID, watch.ID)
	}
}

var setupCache = struct {
	sync.RWMutex
	fixedImages map[string][]byte
}{fixedImages: make(map[string][]byte)}

func (w *WatchCommands) handleTemplateInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	att := firstImageAttachment(mc.Message.Attachments)
	if att == nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ テンプレート画像を添付してください。")
		return
	}
	data, err := w.downloadAttachment(att)
	if err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ 画像のダウンロードに失敗しました。")
		return
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ 画像の解析に失敗しました。対応形式: PNG, WebP, JPEG")
		return
	}

	nrgba := toNRGBA(img)
	width, height := nrgba.Bounds().Dx(), nrgba.Bounds().Dy()

	// 総ピクセル数のチェック
	if int64(width)*int64(height) > models.MaxWatchPixels {
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("❌ 画像が巨大すぎます (%dx%d)。最大 %d ピクセルまでです。", width, height, models.MaxWatchPixels))
		return
	}

	// 座標が既にある場合はタイル数をチェック
	if watch.Origin != "" {
		coord, _ := utils.ParseOrigin(watch.Origin)
		tiles := utils.CountRequiredTiles(coord, width, height)
		if tiles > models.MaxWatchTiles {
			_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("❌ テンプレートの範囲が広すぎます (%dタイル)。最大 %d タイルまで許可されています。\n別の画像を使うか、画像を小さくしてください。", tiles, models.MaxWatchTiles))
			return
		}
	}

	hasInv, fixedImg := wplace.HasNonPaletteColors(nrgba)
	if hasInv {
		fixedData, _ := encodePNG(fixedImg)
		setupCache.Lock()
		setupCache.fixedImages[watch.ID] = fixedData
		setupCache.Unlock()
		_, _ = s.ChannelMessageSendComplex(mc.ChannelID, &discordgo.MessageSend{
			Content: "⚠️ 公式外の色が含まれています。Wplaceパレットの近似色に補正しますか？",
			Files:   []*discordgo.File{{Name: "fixed.png", Reader: bytes.NewReader(fixedData)}},
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "補正する", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID(btnFixApply, watch.ID)},
				discordgo.Button{Label: "やり直す", Style: discordgo.SecondaryButton, CustomID: w.makeButtonID(btnFixCancel, watch.ID)},
			}}},
		})
		return
	}

	filename := watch.ID + ".png"
	_ = w.storage.SaveTemplateImage(watch.GuildID, filename, data)
	watch.Template = filename
	watch.PaletteFixSet = true
	_ = w.storage.UpdateWatch(watch)

	if watch.IsExternalChannel {
		w.finalizeSetup(s, mc.ChannelID, watch)
	} else {
		w.promptVisibility(s, mc.ChannelID, watch.ID)
	}
}

func (w *WatchCommands) handleFixButton(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID, btnID string) {
	if btnID == btnFixCancel {
		_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "やり直してください。", Components: []discordgo.MessageComponent{}}})
		return
	}
	setupCache.RLock()
	data, ok := setupCache.fixedImages[watchID]
	setupCache.RUnlock()
	if !ok {
		return
	}
	_ = w.storage.SaveTemplateImage(ic.GuildID, watchID+".png", data)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt != nil && wt.ID == watchID {
		wt.Template = watchID + ".png"
		wt.PaletteFix = true
		wt.PaletteFixSet = true
		_ = w.storage.UpdateWatch(wt)
	}
	setupCache.Lock()
	delete(setupCache.fixedImages, watchID)
	setupCache.Unlock()
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "✅ 補正を適用しました。", Components: []discordgo.MessageComponent{}}})

	if wt != nil {
		if wt.IsExternalChannel {
			w.finalizeSetup(s, ic.ChannelID, wt)
		} else {
			w.promptVisibility(s, ic.ChannelID, watchID)
		}
	}
}

func (w *WatchCommands) finalizeSetup(s *discordgo.Session, channelID string, wt *models.Watch) {
	wt.Status = models.WatchStatusActive
	wt.NextScheduledCheck = time.Now().Add(1 * time.Minute)
	if wt.Visibility == "" {
		wt.Visibility = models.WatchVisibilityPrivate // 内部的なデフォルト値
	}
	_ = w.storage.UpdateWatch(wt)
	_, _ = s.ChannelMessageSend(channelID, "✅ セットアップが完了しました！現在の権限設定を維持したまま監視を開始します。")
	w.manager.ScheduleWatch(wt)
	go w.sendWatchNowMessage(s, wt.ChannelID, wt, "", false)
}

func (w *WatchCommands) handleVisButton(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID, btnID string) {
	vis := models.WatchVisibilityPrivate
	if btnID == btnVisPublic {
		vis = models.WatchVisibilityPublic
	}
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID {
		return
	}
	wt.Visibility = vis
	if vis == models.WatchVisibilityPublic {
		_ = s.ChannelPermissionSet(wt.ChannelID, wt.GuildID, discordgo.PermissionOverwriteTypeRole, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory, discordgo.PermissionSendMessages)
	}
	wt.Status = models.WatchStatusActive
	wt.NextScheduledCheck = time.Now().Add(1 * time.Minute)
	_ = w.storage.UpdateWatch(wt)
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "✅ 監視を開始しました！", Components: []discordgo.MessageComponent{}}})
	w.manager.ScheduleWatch(wt)
	go w.sendWatchNowMessage(s, wt.ChannelID, wt, "", false)
}

func (w *WatchCommands) promptVisibility(s *discordgo.Session, cID, wID string) {
	_, _ = s.ChannelMessageSendComplex(cID, &discordgo.MessageSend{
		Content: "✅ 公開設定を選択してください。\n\n**Public**: サーバーの全員が閲覧可能（発言不可）\n**Private**: 自分のみ閲覧可能",
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Public (公開)", Style: discordgo.SuccessButton, CustomID: w.makeButtonID(btnVisPublic, wID)},
			discordgo.Button{Label: "Private (非公開)", Style: discordgo.SecondaryButton, CustomID: w.makeButtonID(btnVisPrivate, wID)},
		}}},
	})
}

func (w *WatchCommands) downloadAttachment(a *discordgo.MessageAttachment) ([]byte, error) {
	u := a.ProxyURL
	if u == "" {
		u = a.URL
	}
	r, err := templateHTTPClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxTemplateBytes))
}

func (w *WatchCommands) handleThresholdMessage(s *discordgo.Session, mc *discordgo.MessageCreate, wt *models.Watch) {
	match := thresholdNumberPattern.FindString(mc.Content)
	if match == "" {
		return
	}
	val, _ := strconv.Atoi(match)
	if val >= 10 && val <= 100 && val%10 == 0 {
		wt.ThresholdPercent = float64(val)
		_ = w.storage.UpdateWatch(wt)
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("🔧 通知閾値を %d%% に変更しました。", val))
	} else {
		_, _ = s.ChannelMessageSend(mc.ChannelID, "❌ 閾値は 10〜100 の間で 10刻みで指定してください（例: `30%`）。")
	}
}

func (w *WatchCommands) handleStatus(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt == nil {
		respondEphemeral(s, ic, "稼働中の監視はありません。`/watch create` で作成してください。")
		return
	}
	status := fmt.Sprintf("🎨 **%s**\n状態: `%s`\nタイプ: `%s`\n公開設定: `%s`\n閾値: `%.0f%%`\n座標: `%s`\n最終チェック: `%s`",
		wt.Label, wt.Status, wt.Type, wt.Visibility, wt.ThresholdPercent, wt.Origin, formatTime(wt.LastCheckedAt))
	respondEphemeral(s, ic, status)
}

func (w *WatchCommands) handlePause(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt != nil && wt.Status == models.WatchStatusActive {
		wt.Status = models.WatchStatusPaused
		_ = w.storage.UpdateWatch(wt)
		w.manager.PauseWatch(wt)
		respondEphemeral(s, ic, "監視を一時停止しました。")
	} else {
		respondEphemeral(s, ic, "一時停止可能な監視が見つかりません。")
	}
}

func (w *WatchCommands) handleResume(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt != nil && wt.Status == models.WatchStatusPaused {
		wt.Status = models.WatchStatusActive
		_ = w.storage.UpdateWatch(wt)
		w.manager.ScheduleWatch(wt)
		respondEphemeral(s, ic, "監視を再開しました。")
	} else {
		respondEphemeral(s, ic, "再開可能な監視が見つかりません。")
	}
}

func (w *WatchCommands) handleSettings(s *discordgo.Session, ic *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	u := interactionUser(ic)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt == nil {
		respondEphemeral(s, ic, "設定可能な監視が見つかりません。")
		return
	}

	if len(opts) > 0 {
		w.performDirectSettingsUpdate(s, ic, wt, opts)
		return
	}

	emb := &discordgo.MessageEmbed{
		Title:       "⚙️ 監視設定",
		Description: fmt.Sprintf("対象: **%s**", wt.Label),
		Color:       0x5865F2,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "監視タイプ", Value: string(wt.Type), Inline: true},
			{Name: "通知閾値", Value: fmt.Sprintf("%.0f%%", wt.ThresholdPercent), Inline: true},
			{Name: "公開設定", Value: string(wt.Visibility), Inline: true},
			{Name: "座標 (Origin)", Value: fmt.Sprintf("`%s`", wt.Origin), Inline: false},
		},
		Footer: &discordgo.MessageEmbedFooter{Text: "下のボタンから設定を個別に変更できます。"},
	}

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "座標変更", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID("edit_origin", wt.ID)},
				discordgo.Button{Label: "閾値変更", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID("edit_threshold", wt.ID)},
				discordgo.Button{Label: "画像変更", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID("edit_template", wt.ID)},
			},
		},
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "タイプ切替", Style: discordgo.SecondaryButton, CustomID: w.makeButtonID("edit_type", wt.ID)},
				discordgo.Button{Label: "公開設定切替", Style: discordgo.SecondaryButton, CustomID: w.makeButtonID("edit_vis", wt.ID)},
			},
		},
	}

	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{emb},
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral,
		},
	})
}

func (w *WatchCommands) performDirectSettingsUpdate(s *discordgo.Session, ic *discordgo.InteractionCreate, wt *models.Watch, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	updated := false
	if origin := getOptionString(opts, "origin"); origin != "" {
		if _, err := utils.ParseOrigin(origin); err == nil {
			wt.Origin = origin
			updated = true
		}
	}
	if t := getOptionString(opts, "type"); t != "" {
		wt.Type = models.WatchType(t)
		updated = true
	}
	if v := getOptionString(opts, "visibility"); v != "" {
		wt.Visibility = models.WatchVisibility(v)
		if wt.Visibility == models.WatchVisibilityPublic {
			_ = s.ChannelPermissionSet(wt.ChannelID, wt.GuildID, discordgo.PermissionOverwriteTypeRole, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory, discordgo.PermissionSendMessages)
		} else {
			_ = s.ChannelPermissionDelete(wt.ChannelID, wt.GuildID)
		}
		updated = true
	}
	if th := getOptionInt(opts, "threshold"); th > 0 {
		if th >= 10 && th <= 100 && th%10 == 0 {
			wt.ThresholdPercent = float64(th)
			updated = true
		}
	}
	if attID := getOptionAttachmentID(opts, "template"); attID != "" {
		data := ic.ApplicationCommandData()
		if data.Resolved != nil && data.Resolved.Attachments != nil {
			if att, ok := data.Resolved.Attachments[attID]; ok {
				d, err := w.downloadAttachment(att)
				if err == nil {
					_ = w.storage.SaveTemplateImage(wt.GuildID, wt.ID+".png", d)
					wt.Template = wt.ID + ".png"
					updated = true
				}
			}
		}
	}

	if updated {
		_ = w.storage.UpdateWatch(wt)
		if wt.Status == models.WatchStatusActive {
			w.manager.ScheduleWatch(wt)
		}
		respondEphemeral(s, ic, "✅ 監視設定を更新しました。")
	} else {
		respondEphemeral(s, ic, "❌ 有効な変更内容がありませんでした。")
	}
}

func (w *WatchCommands) handleDelete(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt != nil {
		_ = w.deleteWatchAndCleanup(s, wt)
		respondEphemeral(s, ic, "監視を削除しました。チャンネルとテンプレート画像も削除されました。")
	} else {
		respondEphemeral(s, ic, "削除可能な監視が見つかりません。")
	}
}

func (w *WatchCommands) handleNowSlash(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	wt, _ := w.storage.GetWatchByChannel(ic.GuildID, ic.ChannelID)
	if wt == nil {
		respondEphemeral(s, ic, "このチャンネルに関連付けられた監視が見つかりません。")
		return
	}
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	res, err := w.manager.RunWatchNow(wt)
	if err != nil {
		msg := "❌ 監視データの取得に失敗しました。"
		_, _ = s.InteractionResponseEdit(ic.Interaction, &discordgo.WebhookEdit{Content: &msg})
		return
	}
	emb := notifications.BuildWatchEmbed("🔎 現在の監視状況", 0xFFD700, wt, res)
	files := prepareWatchResultAssets(res, emb)
	_, _ = s.InteractionResponseEdit(ic.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}, Files: files})
}

func (w *WatchCommands) sendWatchNowMessage(s *discordgo.Session, cID string, wt *models.Watch, rID string, inc bool) {
	res, err := w.manager.RunWatchNow(wt)
	if err != nil {
		_, _ = s.ChannelMessageSend(cID, "❌ 監視データの取得に失敗しました。")
		return
	}
	emb := notifications.BuildWatchEmbed("🔎 現在の監視状況", 0xFFD700, wt, res)
	if inc {
		emb.Fields = append(emb.Fields, &discordgo.MessageEmbedField{Name: "監視チャンネル", Value: fmt.Sprintf("<#%s>", wt.ChannelID), Inline: true})
	}
	if rID != "" {
		emb.Footer = &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Requested by %s", rID)}
	}
	files := prepareWatchResultAssets(res, emb)
	_, _ = s.ChannelMessageSendComplex(cID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}, Files: files})
}

func prepareWatchResultAssets(res *wplace.Result, emb *discordgo.MessageEmbed) []*discordgo.File {
	if res == nil || len(res.PreviewPNG) == 0 {
		return nil
	}
	emb.Image = &discordgo.MessageEmbedImage{URL: "attachment://p.png"}
	return []*discordgo.File{{Name: "p.png", Reader: bytes.NewReader(res.PreviewPNG)}}
}

func (w *WatchCommands) deleteWatchAndCleanup(s *discordgo.Session, watch *models.Watch) error {
	w.manager.RemoveWatch(watch.ID)
	_ = w.storage.DeleteTemplateImage(watch.GuildID, watch.Template)
	_ = w.storage.RemoveWatchRecord(watch.GuildID, watch.ID)
	if !watch.IsExternalChannel {
		_, _ = s.ChannelDelete(watch.ChannelID)
	}
	return nil
}

func (w *WatchCommands) HandleChannelDelete(s *discordgo.Session, cd *discordgo.ChannelDelete) {
	if cd == nil || cd.Channel == nil || cd.Channel.GuildID == "" {
		return
	}
	watch, err := w.storage.GetWatchByChannel(cd.Channel.GuildID, cd.Channel.ID)
	if err == nil && watch != nil {
		w.manager.RemoveWatch(watch.ID)
		_ = w.storage.DeleteTemplateImage(watch.GuildID, watch.Template)
		_ = w.storage.RemoveWatchRecord(watch.GuildID, watch.ID)
	}
}

func (w *WatchCommands) handleModeratorDelete(s *discordgo.Session, ic *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if !hasPermission(ic.Member, discordgo.PermissionManageChannels) {
		respondEphemeral(s, ic, "モデレーター権限が必要です。")
		return
	}
	target := getOptionUserID(options, "user")
	watch, _ := w.storage.GetUserWatch(ic.GuildID, target)
	if watch != nil {
		_ = w.deleteWatchAndCleanup(s, watch)
		respondEphemeral(s, ic, fmt.Sprintf("ユーザー <@%s> の監視を削除しました。", target))
	} else {
		respondEphemeral(s, ic, "対象ユーザーの監視が見つかりません。")
	}
}

func getOptionString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts {
		if o.Name == name {
			return o.StringValue()
		}
	}
	return ""
}
func getOptionInt(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) int64 {
	for _, o := range opts {
		if o.Name == name {
			return o.IntValue()
		}
	}
	return 0
}
func getOptionUserID(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts {
		if o.Name == name {
			if u := o.UserValue(nil); u != nil {
				return u.ID
			}
		}
	}
	return ""
}
func getOptionAttachmentID(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts {
		if o.Name == name {
			return o.StringValue()
		}
	}
	return ""
}
func getModalValue(ic *discordgo.InteractionCreate, id string) string {
	for _, c := range ic.ModalSubmitData().Components {
		if r, ok := c.(*discordgo.ActionsRow); ok {
			for _, i := range r.Components {
				if input, ok := i.(*discordgo.TextInput); ok && input.CustomID == id {
					return input.Value
				}
			}
		}
	}
	return ""
}
func respondEphemeral(s *discordgo.Session, ic *discordgo.InteractionCreate, c string) {
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: c, Flags: discordgo.MessageFlagsEphemeral}})
}
func hasPermission(m *discordgo.Member, p int64) bool { return m != nil && m.Permissions&p != 0 }
func interactionUser(ic *discordgo.InteractionCreate) *discordgo.User {
	if ic.Member != nil {
		return ic.Member.User
	}
	return ic.User
}

func formatTime(t *time.Time) string {
	if t == nil {
		return "未チェック"
	}
	return t.Local().Format("2006/01/02 15:04:05")
}

func getImageDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	return cfg.Width, cfg.Height, err
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
		respondEphemeral(s, ic, "パネルの設置に失敗しました。")
		return
	}
	respondEphemeral(s, ic, "監視リクエストパネルを設置しました。")
}

func (w *WatchCommands) presentCreateModal(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	existing, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if existing != nil && existing.Status != models.WatchStatusDeleted {
		respondEphemeral(s, ic, "既に監視チャンネルが存在します。")
		return
	}

	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: watchModalID,
			Title:    "監視リクエスト",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.TextInput{CustomID: "label", Label: "監視の名前", Style: discordgo.TextInputShort, Required: true, Placeholder: "例: Golden Logo"},
				}},
			},
		},
	})
}

func (w *WatchCommands) tryHandleQuickTextCommand(s *discordgo.Session, mc *discordgo.MessageCreate) bool {
	content := strings.TrimSpace(mc.Content)
	if !strings.HasPrefix(strings.ToLower(content), quickCommandPrefix) {
		return false
	}
	payload := strings.TrimSpace(content[len(quickCommandPrefix):])
	if payload == "" {
		return true
	}

	if strings.EqualFold(payload, "now") {
		wt, _ := w.storage.GetWatchByChannel(mc.GuildID, mc.ChannelID)
		if wt != nil {
			w.sendWatchNowMessage(s, mc.ChannelID, wt, mc.Author.ID, false)
		}
		return true
	}

	target, _ := w.storage.GetWatchByLabel(mc.GuildID, payload)
	if target != nil {
		w.sendWatchNowMessage(s, mc.ChannelID, target, mc.Author.ID, target.ChannelID != mc.ChannelID)
	}
	return true
}

func looksLikeThresholdCommand(content string) bool {
	clean := strings.ToLower(strings.TrimSpace(content))
	return strings.HasPrefix(clean, "threshold") || strings.HasPrefix(clean, "閾値") || strings.HasSuffix(clean, "%")
}

const maxTemplateBytes = 8 << 20

var templateHTTPClient = &http.Client{Timeout: 30 * time.Second}
var thresholdNumberPattern = regexp.MustCompile(`\d+`)

func firstImageAttachment(atts []*discordgo.MessageAttachment) *discordgo.MessageAttachment {
	for _, a := range atts {
		if a != nil && (a.ContentType == "" || strings.HasPrefix(a.ContentType, "image/")) {
			return a
		}
	}
	return nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Over)
	return out
}

func encodePNG(img image.Image) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := png.Encode(buf, img)
	return buf.Bytes(), err
}
