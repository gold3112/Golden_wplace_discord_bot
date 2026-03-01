package notifications

import (
	"bytes"
	"fmt"
	"time"

	"golden_wplace_discord_bot/internal/models"
	"golden_wplace_discord_bot/internal/wplace"

	"github.com/bwmarrin/discordgo"
)

// Notifier 通知ハンドラ
type Notifier struct {
	session *discordgo.Session
}

// NewNotifier 新しいNotifier
func NewNotifier(session *discordgo.Session) *Notifier {
	return &Notifier{session: session}
}

// SetSession セッションを更新
func (n *Notifier) SetSession(session *discordgo.Session) {
	n.session = session
}

// NotifyIncrease 差分増加通知
func (n *Notifier) NotifyIncrease(watch *models.Watch, result *wplace.Result, tier Tier) error {
	if err := n.ensureSession(); err != nil {
		return err
	}
	desc := tierIncreaseDescription(tier)
	content := fmt.Sprintf("【Wplace速報】 🚨 差分率が%sしました！[現在%.2f%%]\n対象: `%s`", desc, result.DiffPercentage, watch.Label)
	embed := n.buildWatchEmbed("🏯 Wplace 荒らし検知", GetTierColor(tier), watch, result)
	return n.sendWatchMessage(watch.ChannelID, content, embed, result)
}

// NotifyDecrease 差分減少通知
func (n *Notifier) NotifyDecrease(watch *models.Watch, result *wplace.Result, tier Tier, threshold float64) error {
	if err := n.ensureSession(); err != nil {
		return err
	}
	desc := TierRangeLabel(tier, threshold)
	content := fmt.Sprintf("【Wplace速報】 差分率が%sまで減少しました。[現在%.2f%%]\n対象: `%s`", desc, result.DiffPercentage, watch.Label)
	embed := n.buildWatchEmbed("🏯 Wplace 差分減少", GetTierColor(tier), watch, result)
	return n.sendWatchMessage(watch.ChannelID, content, embed, result)
}

// NotifyRecovery 0→上昇通知
func (n *Notifier) NotifyRecovery(watch *models.Watch, result *wplace.Result) error {
	if err := n.ensureSession(); err != nil {
		return err
	}
	content := fmt.Sprintf("🔔 【Wplace速報】変化検知 差分率: **%.2f%%**\n対象: `%s`", result.DiffPercentage, watch.Label)
	embed := n.buildWatchEmbed("🟢 Wplace 変化検知", 0x00FF00, watch, result)
	return n.sendWatchMessage(watch.ChannelID, content, embed, result)
}

// NotifyCompletion 0%通知
func (n *Notifier) NotifyCompletion(watch *models.Watch, result *wplace.Result) error {
	if err := n.ensureSession(); err != nil {
		return err
	}
	content := fmt.Sprintf("✅ 【Wplace速報】修復完了！ 差分率: **0.00%%**\n対象: `%s`", watch.Label)
	embed := n.buildWatchEmbed("🎉 Wplace 修復完了", 0x00FF00, watch, result)
	return n.sendWatchMessage(watch.ChannelID, content, embed, result)
}

func (n *Notifier) ensureSession() error {
	if n == nil || n.session == nil {
		return fmt.Errorf("notifier session not ready")
	}
	return nil
}

func (n *Notifier) buildWatchEmbed(title string, color int, watch *models.Watch, result *wplace.Result) *discordgo.MessageEmbed {
	fields := []*discordgo.MessageEmbedField{
		{Name: "差分率", Value: fmt.Sprintf("%.2f%%", result.DiffPercentage), Inline: true},
		{Name: "差分ピクセル", Value: fmt.Sprintf("%d / %d", result.DiffPixels, result.TemplateOpaque), Inline: true},
		{Name: "監視サイズ", Value: fmt.Sprintf("%dx%d", result.TemplateWidth, result.TemplateHeight), Inline: true},
	}
	if watch.Origin != "" {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Origin", Value: fmt.Sprintf("`%s`", watch.Origin), Inline: true})
	}
	fields = append(fields, &discordgo.MessageEmbedField{Name: "タイプ", Value: string(watch.Type), Inline: true})
	if result.SnapshotURL != "" {
		value := fmt.Sprintf("[地図で見る](%s)", result.SnapshotURL)
		if result.FullsizeKey != "" {
			value += fmt.Sprintf("\n`/get fullsize:%s`", result.FullsizeKey)
		}
		fields = append(fields, &discordgo.MessageEmbedField{Name: "Wplace.live", Value: value})
	}

	return &discordgo.MessageEmbed{
		Title:       title,
		Description: fmt.Sprintf("対象 `%s` の監視結果", watch.Label),
		Color:       color,
		Fields:      fields,
		Timestamp:   time.Now().Format(time.RFC3339),
	}
}

func (n *Notifier) sendWatchMessage(channelID, content string, embed *discordgo.MessageEmbed, result *wplace.Result) error {
	msg := &discordgo.MessageSend{Content: content, Embeds: []*discordgo.MessageEmbed{embed}}
	if len(result.PreviewPNG) > 0 {
		msg.Files = []*discordgo.File{
			{
				Name:        "watch_preview.png",
				ContentType: "image/png",
				Reader:      bytes.NewReader(result.PreviewPNG),
			},
		}
		embed.Image = &discordgo.MessageEmbedImage{URL: "attachment://watch_preview.png"}
	} else if result.SnapshotURL != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: result.SnapshotURL}
	}

	_, err := n.session.ChannelMessageSendComplex(channelID, msg)
	return err
}

func tierIncreaseDescription(tier Tier) string {
	switch tier {
	case Tier100:
		return "100%に急増"
	case Tier90:
		return "90%台に増加"
	case Tier80:
		return "80%台に増加"
	case Tier70:
		return "70%台に増加"
	case Tier60:
		return "60%台に増加"
	case Tier50:
		return "50%以上に急増"
	case Tier40:
		return "40%台に増加"
	case Tier30:
		return "30%台に増加"
	case Tier20:
		return "20%台に増加"
	case Tier10:
		return "10%台に増加"
	default:
		return "変化"
	}
}
