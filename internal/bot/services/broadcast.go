package services

import (
	"context"
	"fmt"

	"x-ui-bot/internal/logger"
	"x-ui-bot/internal/panel"
	"x-ui-bot/pkg/client"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BroadcastService handles broadcast messaging
type BroadcastService struct {
	apiClient    *client.APIClient // Keep for backward compatibility
	panelManager *panel.PanelManager
	bot          *telego.Bot
	logger       *logger.Logger
}

// NewBroadcastService creates a new broadcast service
func NewBroadcastService(apiClient *client.APIClient, bot *telego.Bot, log *logger.Logger) *BroadcastService {
	return &BroadcastService{
		apiClient: apiClient,
		bot:       bot,
		logger:    log,
	}
}

// NewBroadcastServiceWithPanelManager creates a new broadcast service with panel manager
func NewBroadcastServiceWithPanelManager(panelManager *panel.PanelManager, bot *telego.Bot, log *logger.Logger) *BroadcastService {
	return &BroadcastService{
		panelManager: panelManager,
		bot:          bot,
		logger:       log,
	}
}

// SendBroadcast sends a message to all registered clients
func (s *BroadcastService) SendBroadcast(message string) (sent int, failed int, err error) {
	s.logger.Info("Starting broadcast")

	// Collect all unique telegram IDs
	tgIDs := make(map[int64]bool)

	// If we have panel manager, collect from all panels
	if s.panelManager != nil {
		panels := s.panelManager.GetHealthyPanels()
		for _, panel := range panels {
			client, err := s.panelManager.GetClient(panel.Name)
			if err != nil {
				s.logger.Errorf("Failed to get client for panel %s: %v", panel.Name, err)
				continue
			}

			inbounds, err := client.GetInbounds()
			if err != nil {
				s.logger.Errorf("Failed to get inbounds for panel %s: %v", panel.Name, err)
				continue
			}

			for _, inbound := range inbounds {
				settingsStr, ok := inbound["settings"].(string)
				if !ok {
					continue
				}

				// Parse clients from settings
				clientService := NewClientService(client, s.logger)
				clients, err := clientService.ParseClients(settingsStr)
				if err != nil {
					s.logger.ErrorErr(err, "Failed to parse clients")
					continue
				}

				for _, clientData := range clients {
					tgIDStr := clientData["tgId"]
					if tgIDStr == "" || tgIDStr == "0" {
						continue
					}

					var tgID int64
					if _, err := fmt.Sscanf(tgIDStr, "%d", &tgID); err == nil && tgID > 0 {
						tgIDs[tgID] = true
					}
				}
			}
		}
	} else if s.apiClient != nil {
		// Fallback to single panel mode
		inbounds, err := s.apiClient.GetInbounds()
		if err != nil {
			return 0, 0, fmt.Errorf("failed to get inbounds: %w", err)
		}

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
	} else {
		return 0, 0, fmt.Errorf("no API client or panel manager configured")
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
