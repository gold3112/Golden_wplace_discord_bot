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
				Name:        "create",
				Description: "監視チャンネルを作成",
				Options: []*discordgo.ApplicationCommandOption{
					{Name: "label", Description: "監視の表示名", Type: discordgo.ApplicationCommandOptionString, Required: true},
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
					{Name: "origin", Description: "新しい座標", Type: discordgo.ApplicationCommandOptionString, Required: false},
					{Name: "template", Description: "新しい画像", Type: discordgo.ApplicationCommandOptionAttachment, Required: false},
					{Name: "type", Description: "タイプ", Type: discordgo.ApplicationCommandOptionString, Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "progress", Value: "progress"},
						{Name: "vandal", Value: "vandal"},
					}},
					{Name: "visibility", Description: "公開設定", Type: discordgo.ApplicationCommandOptionString, Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
						{Name: "public", Value: "public"},
						{Name: "private", Value: "private"},
					}},
				},
			},
		},
	}
	_, err := session.ApplicationCommandCreate(appID, "", watchCmd)
	return err
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
	if data.Name == "watch" {
		sub := data.Options[0]
		switch sub.Name {
		case "create":
			w.processCreateRequest(s, ic, watchCreateInput{Label: getOptionString(sub.Options, "label")})
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
		}
	}
}

func (w *WatchCommands) HandleMessageCreate(s *discordgo.Session, mc *discordgo.MessageCreate) {
	if mc.Author == nil || mc.Author.Bot || mc.GuildID == "" { return }
	watch, err := w.storage.GetWatchByChannel(mc.GuildID, mc.ChannelID)
	if err != nil || watch == nil || watch.OwnerID != mc.Author.ID || watch.Status == models.WatchStatusDeleted { return }

	if watch.Type == "" { return }
	if watch.Origin == "" { w.handleOriginInput(s, mc, watch); return }
	if watch.Template == "" { w.handleTemplateInput(s, mc, watch); return }
}

func (w *WatchCommands) handleComponentInteraction(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	data := ic.MessageComponentData()
	btnID, watchID := w.parseButtonID(data.CustomID)
	switch btnID {
	case btnTypeProgress, btnTypeVandal: w.handleTypeButton(s, ic, watchID, btnID)
	case btnFixApply, btnFixCancel: w.handleFixButton(s, ic, watchID, btnID)
	case btnVisPublic, btnVisPrivate: w.handleVisButton(s, ic, watchID, btnID)
	}
}

func (w *WatchCommands) makeButtonID(btnID, watchID string) string { return btnID + ":" + watchID }
func (w *WatchCommands) parseButtonID(customID string) (string, string) {
	parts := strings.Split(customID, ":")
	if len(parts) == 2 { return parts[0], parts[1] }
	return customID, ""
}

func (w *WatchCommands) handleModalSubmit(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	if ic.ModalSubmitData().CustomID == watchModalID {
		w.processCreateRequest(s, ic, watchCreateInput{Label: getModalValue(ic, "label")})
	}
}

func (w *WatchCommands) processCreateRequest(s *discordgo.Session, ic *discordgo.InteractionCreate, input watchCreateInput) {
	user := interactionUser(ic)
	label := strings.TrimSpace(input.Label)
	if label == "" { respondEphemeral(s, ic, "名前を入力してください。"); return }

	watchID := utils.GenerateWatchID(user.ID)
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
	if err != nil { respondEphemeral(s, ic, "作成失敗。"); return }

	watch := &models.Watch{
		ID: watchID, Label: label, OwnerID: user.ID, GuildID: ic.GuildID, ChannelID: channel.ID,
		Status: models.WatchStatusPending, CreatedAt: time.Now().UTC(), ThresholdPercent: defaultThresholdPercent,
	}
	_ = w.storage.AddWatch(watch)

	msg := &discordgo.MessageSend{
		Content: fmt.Sprintf("👋 %s さんの監視チャンネルを作成しました。\n\n**ステップ1**: 監視タイプを選択してください。", user.Mention()),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "Progress", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID(btnTypeProgress, watchID)},
					discordgo.Button{Label: "Vandal", Style: discordgo.DangerButton, CustomID: w.makeButtonID(btnTypeVandal, watchID)},
				},
			},
		},
	}
	_, _ = s.ChannelMessageSendComplex(channel.ID, msg)
	respondEphemeral(s, ic, fmt.Sprintf("作成しました <#%s>", channel.ID))
}

func (w *WatchCommands) getOrCreateCategory(s *discordgo.Session, guildID string) (string, error) {
	if w.config == nil || w.config.WatchCategoryName == "" { return "", nil }
	chs, _ := s.GuildChannels(guildID)
	for _, ch := range chs {
		if ch.Type == discordgo.ChannelTypeGuildCategory && strings.EqualFold(ch.Name, w.config.WatchCategoryName) { return ch.ID, nil }
	}
	cat, err := s.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{Name: w.config.WatchCategoryName, Type: discordgo.ChannelTypeGuildCategory})
	if err != nil { return "", err }
	return cat.ID, nil
}

func (w *WatchCommands) ReorganizeChannels(s *discordgo.Session) error {
	guildIDs, _ := w.storage.ListGuildIDs()
	for _, gID := range guildIDs {
		cID, _ := w.getOrCreateCategory(s, gID)
		if cID == "" { continue }
		data, _ := w.storage.LoadGuildWatches(gID)
		for _, wt := range data.Watches {
			if wt.ChannelID != "" && wt.Status != models.WatchStatusDeleted {
				ch, err := s.Channel(wt.ChannelID)
				if err == nil && ch.ParentID != cID { _, _ = s.ChannelEditComplex(wt.ChannelID, &discordgo.ChannelEdit{ParentID: cID}) }
			}
		}
	}
	return nil
}

func (w *WatchCommands) handleTypeButton(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID, btnID string) {
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID { return }
	if btnID == btnTypeProgress { wt.Type = models.WatchTypeProgress } else { wt.Type = models.WatchTypeVandal }
	_ = w.storage.UpdateWatch(wt)
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("✅ 監視タイプを **%s** に設定しました。\n\n**ステップ2**: 座標を入力してください。", wt.Type),
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (w *WatchCommands) handleOriginInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	watch.Origin = strings.TrimSpace(mc.Content)
	_ = w.storage.UpdateWatch(watch)
	_, _ = s.ChannelMessageSend(mc.ChannelID, "✅ 座標を登録しました。次に画像をアップロードしてください。")
}

var setupCache = struct { sync.RWMutex; fixedImages map[string][]byte }{ fixedImages: make(map[string][]byte) }

func (w *WatchCommands) handleTemplateInput(s *discordgo.Session, mc *discordgo.MessageCreate, watch *models.Watch) {
	att := firstImageAttachment(mc.Message.Attachments)
	if att == nil { return }
	data, _ := w.downloadAttachment(att)
	img, _, _ := image.Decode(bytes.NewReader(data))
	nrgba := toNRGBA(img)
	hasInv, fixedImg := wplace.HasNonPaletteColors(nrgba)
	if hasInv {
		fixedData, _ := encodePNG(fixedImg)
		setupCache.Lock(); setupCache.fixedImages[watch.ID] = fixedData; setupCache.Unlock()
		_, _ = s.ChannelMessageSendComplex(mc.ChannelID, &discordgo.MessageSend{
			Content: "⚠️ 公式外の色があります。補正しますか？",
			Files: []*discordgo.File{{Name: "fixed.png", Reader: bytes.NewReader(fixedData)}},
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "補正する", Style: discordgo.PrimaryButton, CustomID: w.makeButtonID(btnFixApply, watch.ID)},
				discordgo.Button{Label: "やり直す", Style: discordgo.SecondaryButton, CustomID: w.makeButtonID(btnFixCancel, watch.ID)},
			}}},
		})
		return
	}
	filename, _, _, _ := w.saveAndCheckTemplate(watch, nrgba)
	watch.Template = filename; watch.PaletteFixSet = true; _ = w.storage.UpdateWatch(watch)
	w.promptVisibility(s, mc.ChannelID, watch.ID)
}

func (w *WatchCommands) handleFixButton(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID, btnID string) {
	if btnID == btnFixCancel {
		_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "やり直してください。", Components: []discordgo.MessageComponent{}}})
		return
	}
	setupCache.RLock(); data, ok := setupCache.fixedImages[watchID]; setupCache.RUnlock()
	if !ok { return }
	_ = w.storage.SaveTemplateImage(ic.GuildID, watchID+".png", data)
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt != nil && wt.ID == watchID { wt.Template = watchID+".png"; wt.PaletteFix = true; wt.PaletteFixSet = true; _ = w.storage.UpdateWatch(wt) }
	setupCache.Lock(); delete(setupCache.fixedImages, watchID); setupCache.Unlock()
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "✅ 補正しました。", Components: []discordgo.MessageComponent{}}})
	w.promptVisibility(s, ic.ChannelID, watchID)
}

func (w *WatchCommands) handleVisButton(s *discordgo.Session, ic *discordgo.InteractionCreate, watchID, btnID string) {
	vis := models.WatchVisibilityPrivate
	if btnID == btnVisPublic { vis = models.WatchVisibilityPublic }
	wt, _ := w.storage.GetUserWatch(ic.GuildID, interactionUser(ic).ID)
	if wt == nil || wt.ID != watchID { return }
	wt.Visibility = vis
	if vis == models.WatchVisibilityPublic {
		_ = s.ChannelPermissionSet(wt.ChannelID, wt.GuildID, discordgo.PermissionOverwriteTypeRole, discordgo.PermissionViewChannel|discordgo.PermissionReadMessageHistory, discordgo.PermissionSendMessages)
	}
	wt.Status = models.WatchStatusActive; wt.NextScheduledCheck = time.Now().Add(1 * time.Minute); _ = w.storage.UpdateWatch(wt)
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseUpdateMessage, Data: &discordgo.InteractionResponseData{Content: "✅ 監視開始！", Components: []discordgo.MessageComponent{}}})
	w.manager.ScheduleWatch(wt); go w.sendWatchNowMessage(s, wt.ChannelID, wt, "", false)
}

func (w *WatchCommands) promptVisibility(s *discordgo.Session, cID, wID string) {
	_, _ = s.ChannelMessageSendComplex(cID, &discordgo.MessageSend{
		Content: "✅ 公開設定を選択してください。",
		Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Public", Style: discordgo.SuccessButton, CustomID: w.makeButtonID(btnVisPublic, wID)},
			discordgo.Button{Label: "Private", Style: discordgo.SecondaryButton, CustomID: w.makeButtonID(btnVisPrivate, wID)},
		}}},
	})
}

func (w *WatchCommands) downloadAttachment(a *discordgo.MessageAttachment) ([]byte, error) {
	u := a.ProxyURL
	if u == "" { u = a.URL }
	r, _ := templateHTTPClient.Get(u)
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxTemplateBytes))
}

func (w *WatchCommands) saveAndCheckTemplate(wt *models.Watch, img *image.NRGBA) (string, int, int, error) {
	buf := new(bytes.Buffer); _ = png.Encode(buf, img)
	_ = w.storage.SaveTemplateImage(wt.GuildID, wt.ID+".png", buf.Bytes())
	return wt.ID + ".png", img.Bounds().Dx(), img.Bounds().Dy(), nil
}

func (w *WatchCommands) handleThresholdMessage(s *discordgo.Session, mc *discordgo.MessageCreate, wt *models.Watch) {
	match := thresholdNumberPattern.FindString(mc.Content)
	val, _ := strconv.Atoi(match)
	if val >= 10 && val <= 100 && val%10 == 0 {
		wt.ThresholdPercent = float64(val); _ = w.storage.UpdateWatch(wt)
		_, _ = s.ChannelMessageSend(mc.ChannelID, fmt.Sprintf("🔧 閾値を %d%% にしました。", val))
	}
}

func (w *WatchCommands) handleStatus(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic); wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt == nil { respondEphemeral(s, ic, "なし"); return }
	respondEphemeral(s, ic, fmt.Sprintf("状態: %s", wt.Status))
}

func (w *WatchCommands) handlePause(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic); wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt != nil { wt.Status = models.WatchStatusPaused; _ = w.storage.UpdateWatch(wt); w.manager.PauseWatch(wt) }
	respondEphemeral(s, ic, "停止")
}

func (w *WatchCommands) handleResume(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic); wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt != nil { wt.Status = models.WatchStatusActive; _ = w.storage.UpdateWatch(wt); w.manager.ScheduleWatch(wt) }
	respondEphemeral(s, ic, "再開")
}

func (w *WatchCommands) handleSettings(s *discordgo.Session, ic *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	u := interactionUser(ic); wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt == nil { respondEphemeral(s, ic, "なし"); return }
	orig := getOptionString(opts, "origin")
	if orig != "" { wt.Origin = orig }
	_ = w.storage.UpdateWatch(wt)
	respondEphemeral(s, ic, "更新完了")
}

func (w *WatchCommands) handleDelete(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	u := interactionUser(ic); wt, _ := w.storage.GetUserWatch(ic.GuildID, u.ID)
	if wt != nil { _ = w.deleteWatchAndCleanup(s, wt) }
	respondEphemeral(s, ic, "削除完了")
}

func (w *WatchCommands) handleNowSlash(s *discordgo.Session, ic *discordgo.InteractionCreate) {
	wt, _ := w.storage.GetWatchByChannel(ic.GuildID, ic.ChannelID)
	if wt == nil { respondEphemeral(s, ic, "なし"); return }
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource})
	res, _ := w.manager.RunWatchNow(wt)
	emb := notifications.BuildWatchEmbed("🔎 現在", 0xFFD700, wt, res)
	files := prepareWatchResultAssets(res, emb)
	_, _ = s.InteractionResponseEdit(ic.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{emb}, Files: files})
}

func (w *WatchCommands) sendWatchNowMessage(s *discordgo.Session, cID string, wt *models.Watch, rID string, inc bool) {
	res, _ := w.manager.RunWatchNow(wt)
	emb := notifications.BuildWatchEmbed("🔎 現在", 0xFFD700, wt, res)
	files := prepareWatchResultAssets(res, emb)
	_, _ = s.ChannelMessageSendComplex(cID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{emb}, Files: files})
}

func prepareWatchResultAssets(res *wplace.Result, emb *discordgo.MessageEmbed) []*discordgo.File {
	if res == nil || len(res.PreviewPNG) == 0 { return nil }
	emb.Image = &discordgo.MessageEmbedImage{URL: "attachment://p.png"}
	return []*discordgo.File{{Name: "p.png", Reader: bytes.NewReader(res.PreviewPNG)}}
}

func (w *WatchCommands) deleteWatchAndCleanup(s *discordgo.Session, watch *models.Watch) error {
	w.manager.RemoveWatch(watch.ID); _ = w.storage.RemoveWatchRecord(watch.GuildID, watch.ID)
	_, _ = s.ChannelDelete(watch.ChannelID)
	return nil
}

func (w *WatchCommands) HandleChannelDelete(s *discordgo.Session, cd *discordgo.ChannelDelete) {
	if cd == nil || cd.Channel == nil || cd.Channel.GuildID == "" { return }
	watch, err := w.storage.GetWatchByChannel(cd.Channel.GuildID, cd.Channel.ID)
	if err == nil && watch != nil { _ = w.cleanupWatchResources(s, watch, false) }
}

func (w *WatchCommands) cleanupWatchResources(s *discordgo.Session, wt *models.Watch, del bool) error {
	w.manager.RemoveWatch(wt.ID); _ = w.storage.RemoveWatchRecord(wt.GuildID, wt.ID)
	if del { _, _ = s.ChannelDelete(wt.ChannelID) }
	return nil
}

func (w *WatchCommands) handleModeratorDelete(s *discordgo.Session, ic *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	target := getOptionUserID(options, "user")
	watch, _ := w.storage.GetUserWatch(ic.GuildID, target)
	if watch != nil { _ = w.deleteWatchAndCleanup(s, watch) }
	respondEphemeral(s, ic, "削除完了")
}

func getOptionString(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts { if o.Name == name { return o.StringValue() } }; return ""
}
func getOptionUserID(opts []*discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range opts { if o.Name == name { if u := o.UserValue(nil); u != nil { return u.ID } } }; return ""
}
func getModalValue(ic *discordgo.InteractionCreate, id string) string {
	for _, c := range ic.ModalSubmitData().Components {
		if r, ok := c.(*discordgo.ActionsRow); ok {
			for _, i := range r.Components { if input, ok := i.(*discordgo.TextInput); ok && input.CustomID == id { return input.Value } }
		}
	}
	return ""
}
func respondEphemeral(s *discordgo.Session, ic *discordgo.InteractionCreate, c string) {
	_ = s.InteractionRespond(ic.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: &discordgo.InteractionResponseData{Content: c, Flags: discordgo.MessageFlagsEphemeral}})
}
func hasPermission(m *discordgo.Member, p int64) bool { return m != nil && m.Permissions&p != 0 }
func interactionUser(ic *discordgo.InteractionCreate) *discordgo.User {
	if ic.Member != nil { return ic.Member.User }; return ic.User
}

const maxTemplateBytes = 8 << 20
var templateHTTPClient = &http.Client{Timeout: 30 * time.Second}
var thresholdNumberPattern = regexp.MustCompile(`\d+`)

func firstImageAttachment(atts []*discordgo.MessageAttachment) *discordgo.MessageAttachment {
	for _, a := range atts { if a != nil && (a.ContentType == "" || strings.HasPrefix(a.ContentType, "image/")) { return a } }; return nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	b := src.Bounds(); out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Over); return out
}

func encodePNG(img image.Image) ([]byte, error) {
	buf := new(bytes.Buffer); err := png.Encode(buf, img); return buf.Bytes(), err
}
