package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Utility helper methods for bot operations

// isClientBlocked checks if client is blocked (disabled) in panel
func (b *Bot) isClientBlocked(userID int64) bool {
	// Admins are never blocked
	if b.authMiddleware.IsAdmin(userID) {
		return false
	}

	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		// If client not found, consider as not blocked (allows registration)
		return false
	}

	// Check enable status
	if enable, ok := clientInfo["enable"].(bool); ok {
		return !enable
	}

	// Default to not blocked if status unclear
	return false
}

// getUserInfo gets user's name and Telegram username from Telegram API
func (b *Bot) getUserInfo(userID int64) (name string, username string) {
	chatInfo, err := b.bot.GetChat(context.Background(), &telego.GetChatParams{ChatID: tu.ID(userID)})
	if err == nil {
		if chatInfo.FirstName != "" {
			name = chatInfo.FirstName
			if chatInfo.LastName != "" {
				name += " " + chatInfo.LastName
			}
		}
		if chatInfo.Username != "" {
			username = "@" + chatInfo.Username
		}
	}
	if name == "" {
		name = fmt.Sprintf("User_%d", userID)
	}
	return name, username
}

// calculateTimeRemaining calculates days and hours remaining from expiryTime
func (b *Bot) calculateTimeRemaining(expiryTime int64) (days int, hours int) {
	if expiryTime <= 0 {
		return 0, 0
	}
	remainingMs := expiryTime - time.Now().UnixMilli()
	if remainingMs <= 0 {
		return 0, 0
	}
	days = int(remainingMs / (1000 * 60 * 60 * 24))
	hours = int((remainingMs % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60))
	return days, hours
}

// addProtocolFields adds protocol-specific fields to client data
func (b *Bot) addProtocolFields(clientData map[string]interface{}, protocol string, inbound map[string]interface{}) {
	switch protocol {
	case "vmess":
		clientData["id"] = uuid.New().String()
		clientData["security"] = "auto"
	case "vless":
		clientData["id"] = uuid.New().String()
		clientData["flow"] = ""
	case "trojan":
		clientData["password"] = generateRandomString(10)
	case "shadowsocks":
		// Get method from inbound settings
		settingsStr, _ := inbound["settings"].(string)
		var settings map[string]interface{}
		method := "aes-256-gcm" // default
		if json.Unmarshal([]byte(settingsStr), &settings) == nil {
			if m, ok := settings["method"].(string); ok {
				method = m
			}
		}
		clientData["method"] = method
		clientData["password"] = generateRandomString(16)
	default:
		// Fallback to VLESS-like
		clientData["id"] = uuid.New().String()
		clientData["flow"] = ""
	}
}

// findClientByTgID finds client and inbound by telegram user ID
func (b *Bot) findClientByTgID(userID int64) (client map[string]string, inboundID int, email string, err error) {
	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to get inbounds: %w", err)
	}

	for _, inbound := range inbounds {
		id := int(inbound["id"].(float64))
		settingsStr, ok := inbound["settings"].(string)
		if !ok {
			continue
		}

		clients, err := b.clientService.ParseClients(settingsStr)
		if err != nil {
			b.logger.Errorf("Error parsing clients in findClientByTgID (inboundID=%d): %v", id, err)
			continue
		}

		for _, c := range clients {
			if c["tgId"] == fmt.Sprintf("%d", userID) {
				return c, id, c["email"], nil
			}
		}
	}

	return nil, 0, "", fmt.Errorf("client not found for user ID %d", userID)
}

// (instructions URL is removed - instructions are included in the welcome text)

// createDurationKeyboard creates inline keyboard with duration options and prices
// callbackPrefix should be "reg_duration" for registration or "extend_<userID>" for extension
// isFirstPurchase indicates if trial option should be shown
func (b *Bot) createDurationKeyboard(callbackPrefix string, isFirstPurchase bool) *telego.InlineKeyboardMarkup {
	rows := [][]telego.InlineKeyboardButton{}

	// Add trial option only for first purchase if enabled
	if isFirstPurchase && b.config.Payment.TrialDays > 0 {
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("Пробный период %d дня - Бесплатно", b.config.Payment.TrialDays)).WithCallbackData(fmt.Sprintf("%s_%d", callbackPrefix, b.config.Payment.TrialDays)),
		))
	}

	// Add regular plans
	rows = append(rows,
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("30 дней - %d₽", b.config.Payment.Prices.OneMonth)).WithCallbackData(fmt.Sprintf("%s_30", callbackPrefix)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("90 дней - %d₽", b.config.Payment.Prices.ThreeMonth)).WithCallbackData(fmt.Sprintf("%s_90", callbackPrefix)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("180 дней - %d₽", b.config.Payment.Prices.SixMonth)).WithCallbackData(fmt.Sprintf("%s_180", callbackPrefix)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("365 дней - %d₽", b.config.Payment.Prices.OneYear)).WithCallbackData(fmt.Sprintf("%s_365", callbackPrefix)),
		),
	)

	return tu.InlineKeyboard(rows...)
}
