package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"tg-channel-repost-bot/internal/bot"
	"tg-channel-repost-bot/internal/database"
	"tg-channel-repost-bot/internal/scheduler"
	"tg-channel-repost-bot/internal/services"
	"tg-channel-repost-bot/pkg/config"
)

func main() {
	// Parse command line flags
	configPath := flag.String("config", "configs/config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Initialize database
	db, err := database.New(&cfg.Database)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Run database migrations
	if err := db.Migrate(); err != nil {
		log.Fatalf("Failed to run database migrations: %v", err)
	}

	// Create repository
	repo := database.NewRepository(db)

	// Initialize Telegram Bot API
	api, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		log.Fatalf("Failed to create Telegram Bot API: %v", err)
	}

	api.Debug = cfg.Server.Debug
	log.Printf("Authorized on account %s", api.Self.UserName)

	// Create services
	messageService := services.NewMessageService(api, repo, cfg)

	// Create scheduler
	sched := scheduler.New(repo, messageService, &cfg.Scheduler)

	// Create bot
	telegramBot, err := bot.New(cfg, repo, messageService)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// Start scheduler
	sched.Start()
	defer sched.Stop()

	// Start bot in a goroutine
	go func() {
		if err := telegramBot.Start(); err != nil {
			log.Fatalf("Failed to start bot: %v", err)
		}
	}()

	log.Println("Bot is running. Press Ctrl+C to stop.")

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down...")

	// Stop bot
	telegramBot.Stop()

	log.Println("Bot stopped successfully")
}
