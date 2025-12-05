package services

import (
	"context"
	"fmt"

	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BroadcastService handles broadcast messaging
type BroadcastService struct {
	apiClient *client.APIClient
	bot       *telego.Bot
	logger    *logger.Logger
}

// NewBroadcastService creates a new broadcast service
func NewBroadcastService(apiClient *client.APIClient, bot *telego.Bot, log *logger.Logger) *BroadcastService {
	return &BroadcastService{
		apiClient: apiClient,
		bot:       bot,
		logger:    log,
	}
}

// SendBroadcast sends a message to all registered clients
func (s *BroadcastService) SendBroadcast(message string) (sent int, failed int, err error) {
	s.logger.Info("Starting broadcast")

	// Get all inbounds
	inbounds, err := s.apiClient.GetInbounds(context.Background())
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get inbounds: %w", err)
	}

	// Collect all unique telegram IDs
	tgIDs := make(map[int64]bool)

	for _, inbound := range inbounds {
		settingsStr, ok := inbound["settings"].(string)
		if !ok {
			continue
		}

		// Parse clients from settings
		clientService := NewClientService(s.apiClient, s.logger)
		clients, err := clientService.ParseClients(settingsStr)
		if err != nil {
			s.logger.ErrorErr(err, "Failed to parse clients")
			continue
		}

		for _, client := range clients {
			tgIDStr := client["tgId"]
			if tgIDStr == "" || tgIDStr == "0" {
				continue
			}

			var tgID int64
			if _, err := fmt.Sscanf(tgIDStr, "%d", &tgID); err == nil && tgID > 0 {
				tgIDs[tgID] = true
			}
		}
	}

	// Send message to each user
	for tgID := range tgIDs {
		err := s.SendMessage(tgID, message)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"tg_id": tgID,
				"error": err,
			}).Error("Failed to send broadcast message")
			failed++
		} else {
			sent++
		}
	}

	s.logger.WithFields(map[string]interface{}{
		"sent":   sent,
		"failed": failed,
	}).Info("Broadcast completed")

	return sent, failed, nil
}

// SendMessage sends a message to a specific user
func (s *BroadcastService) SendMessage(chatID int64, message string) error {
	_, err := s.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:    tu.ID(chatID),
		Text:      message,
		ParseMode: "HTML",
	})
	return err
}
