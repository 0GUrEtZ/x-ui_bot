package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"x-ui-bot/internal/bot"
	"x-ui-bot/internal/config"
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

	// Create and start bot
	tgBot, err := bot.NewBot(cfg, apiClient)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	if err := tgBot.Start(); err != nil {
		log.Fatalf("Failed to start bot: %v", err)
	}

	log.Println("Bot started successfully")

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down bot...")
	tgBot.Stop()
	log.Println("Bot stopped")
}
