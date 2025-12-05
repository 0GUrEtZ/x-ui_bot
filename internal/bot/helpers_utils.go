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
	clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), userID)
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
	inbounds, err := b.apiClient.GetInbounds(context.Background())
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

// createInstructionsKeyboard creates inline keyboard with platform options for instructions
func (b *Bot) createInstructionsKeyboard() *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📱 iOS").WithCallbackData("instr_ios"),
			tu.InlineKeyboardButton("💻 MacOS").WithCallbackData("instr_macos"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🤖 Android").WithCallbackData("instr_android"),
			tu.InlineKeyboardButton("🖥️ Windows").WithCallbackData("instr_windows"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("◀️ Назад").WithCallbackData("instr_back"),
		),
	)
}

// createDurationKeyboard creates inline keyboard with duration options and prices
// callbackPrefix should be "reg_duration" for registration or "extend_<userID>" for extension
// isFirstPurchase indicates if trial option should be shown
func (b *Bot) createDurationKeyboard(callbackPrefix string, isFirstPurchase bool) *telego.InlineKeyboardMarkup {
	rows := [][]telego.InlineKeyboardButton{}

	// Add trial option only for first purchase if enabled
	if isFirstPurchase && b.config.Payment.TrialDays > 0 {
		trialText := b.config.Payment.TrialText
		if trialText == "" {
			trialText = fmt.Sprintf("%d дня", b.config.Payment.TrialDays)
		}
		rows = append(rows, tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("Пробный период %s - Бесплатно", trialText)).WithCallbackData(fmt.Sprintf("%s_%d", callbackPrefix, b.config.Payment.TrialDays)),
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

// copyClientMap makes a deep copy of client map to avoid concurrent mutation
func copyClientMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// getClientFromCacheCopy returns a copy of client map stored under cacheKey
func (b *Bot) getClientFromCacheCopy(cacheKey string) (map[string]string, bool) {
	b.cacheMutex.RLock()
	defer b.cacheMutex.RUnlock()

	if v, ok := b.clientCache.Load(cacheKey); ok {
		if client, ok2 := v.(map[string]string); ok2 {
			return copyClientMap(client), true
		}
	}
	return nil, false
}

// storeClientToCache stores a copy of the client map in cache
func (b *Bot) storeClientToCache(cacheKey string, client map[string]string) {
	if client == nil {
		return
	}
	b.cacheMutex.Lock()
	defer b.cacheMutex.Unlock()
	b.clientCache.Store(cacheKey, copyClientMap(client))
}

// updateClientField safely updates a single field in the cached client map
// returns the new map copy or nil if not found
func (b *Bot) updateClientField(cacheKey, field, value string) map[string]string {
	b.cacheMutex.Lock()
	defer b.cacheMutex.Unlock()

	v, ok := b.clientCache.Load(cacheKey)
	if !ok {
		return nil
	}
	client, ok := v.(map[string]string)
	if !ok {
		return nil
	}
	newClient := copyClientMap(client)
	newClient[field] = value
	b.clientCache.Store(cacheKey, newClient)
	return newClient
}

// extractClientFromInbound safely extracts a client map from an inbound's settings
func (b *Bot) extractClientFromInbound(inbound map[string]interface{}, clientIndex int) (map[string]string, error) {
	settingsStr, ok := inbound["settings"].(string)
	if !ok {
		return nil, fmt.Errorf("settings not found or not a string")
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings json: %w", err)
	}

	clients, ok := settings["clients"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("clients array not found")
	}

	if clientIndex < 0 || clientIndex >= len(clients) {
		return nil, fmt.Errorf("client index out of range")
	}

	clientMap, ok := clients[clientIndex].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid client data format")
	}

	// Convert to map[string]string
	result := make(map[string]string)
	for k, v := range clientMap {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result, nil
}
