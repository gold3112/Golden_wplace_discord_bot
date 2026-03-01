package notifications

import (
	"bytes"
	"fmt"

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

// NotifyDiff 差分を通知
func (n *Notifier) NotifyDiff(watch *models.Watch, result *wplace.Result) error {
	if n == nil || n.session == nil {
		return fmt.Errorf("notifier session not ready")
	}

	embed := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("%s - 差分検知", watch.Label),
		Description: fmt.Sprintf("Diff: %dpx (%.2f%%)", result.DiffPixels, result.DiffPercentage),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "タイプ", Value: string(watch.Type), Inline: true},
			{Name: "Origin", Value: watch.Origin, Inline: true},
		},
	}
	if result.SnapshotURL != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "Wplace",
			Value: fmt.Sprintf("[地図で確認](%s)", result.SnapshotURL),
		})
	}

	files := make([]*discordgo.File, 0, 1)
	if len(result.LivePNG) > 0 {
		files = append(files, &discordgo.File{
			Name:        "watch.png",
			ContentType: "image/png",
			Reader:      bytes.NewReader(result.LivePNG),
		})
		embed.Image = &discordgo.MessageEmbedImage{URL: "attachment://watch.png"}
	} else if result.SnapshotURL != "" {
		embed.Image = &discordgo.MessageEmbedImage{URL: result.SnapshotURL}
	}

	msg := &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}}
	if len(files) > 0 {
		msg.Files = files
	}

	_, err := n.session.ChannelMessageSendComplex(watch.ChannelID, msg)
	return err
}
