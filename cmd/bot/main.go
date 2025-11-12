package main

import (
	"context"
	"log"
	"time"

	"x-ui-bot/internal/bot"
	"x-ui-bot/internal/config"
	"x-ui-bot/internal/logger"
	"x-ui-bot/internal/shutdown"
	"x-ui-bot/internal/storage"
	"x-ui-bot/pkg/client"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Create API client
	apiClient := client.NewAPIClient(cfg.Panel.URL, cfg.Panel.Username, cfg.Panel.Password)

	// Create storage
	store, err := storage.NewSQLiteStorage("/root/data/bot.db")
	if err != nil {
		log.Fatalf("Failed to create storage: %v", err)
	}

	// Create and start bot
	tgBot, err := bot.NewBot(cfg, apiClient, store)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := tgBot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	log.Println("Bot started successfully")

	// Initialize logger and shutdown manager with 30s graceful timeout
	appLogger := logger.GetLogger()
	shutdownMgr := shutdown.NewManager(appLogger, 30*time.Second)

	// Register cleanup functions
	shutdownMgr.Register(func(ctx context.Context) error {
		log.Println("Stopping bot...")
		tgBot.Stop()
		return nil
	})

	shutdownMgr.Register(func(ctx context.Context) error {
		log.Println("Closing storage...")
		return store.Close()
	})

	// Wait for shutdown signal (blocks until SIGINT/SIGTERM)
	shutdownMgr.Wait()

	log.Println("Bot stopped gracefully")
}
