package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"golden_wplace_discord_bot/internal/commands"
	"golden_wplace_discord_bot/internal/config"
	"golden_wplace_discord_bot/internal/notifications"
	"golden_wplace_discord_bot/internal/storage"
	"golden_wplace_discord_bot/internal/utils"
	"golden_wplace_discord_bot/internal/watchmanager"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()
	cfg := config.Load()

	dataDir := filepath.Join(".", "data")
	storage := storage.NewStorage(dataDir)
	watchInterval := time.Duration(cfg.MonitorInterval) * time.Minute

	dg, err := discordgo.New("Bot " + cfg.Token)
	if err != nil {
		log.Fatalf("failed to create Discord session: %v", err)
	}
	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildMessageReactions

	notifier := notifications.NewNotifier(dg)
	watchLimiter := utils.NewRateLimiter(4)
	defer watchLimiter.Close()
	manager := watchmanager.NewManager(storage, notifier, watchLimiter, watchInterval)

	watchCommands := commands.NewWatchCommands(storage, manager, cfg)
	dg.AddHandler(watchCommands.HandleInteraction)
	dg.AddHandler(watchCommands.HandleMessageCreate)
	dg.AddHandler(watchCommands.HandleChannelDelete)

	if err := dg.Open(); err != nil {
		log.Fatalf("failed to open Discord session: %v", err)
	}
	defer dg.Close()

	// Register commands after session is ready
	appID := dg.State.User.ID
	if err := watchCommands.Register(dg, appID); err != nil {
		log.Printf("failed to register commands: %v", err)
	}
// 既存の監視を開始
if err := manager.StartExisting(dg); err != nil {
	log.Printf("failed to start existing watches: %v", err)
}

	// 既存チャンネルのカテゴリ整理
	if err := watchCommands.ReorganizeChannels(dg); err != nil {
		log.Printf("failed to reorganize channels: %v", err)
	}

	log.Println("Golden Wplace Bot is running. Press Ctrl+C to exit.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
	manager.Stop()
}
