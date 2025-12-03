package bot

import (
	"encoding/json"
	"fmt"
	"html"
	"time"
	"unicode/utf8"

	tu "github.com/mymmrac/telego/telegoutil"
)

// Settings handlers for user settings: subscription info, settings menu, username update

// handleMySubscription shows detailed subscription information for the user
func (b *Bot) handleMySubscription(chatID int64, userID int64) {
	b.logger.Infof("User %d requested subscription info", userID)

	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Вы не зарегистрированы.\n\nДля получения VPN необходимо зарегистрироваться.")
		// Start registration process - get user info from Telegram
		userName, tgUsername := b.getUserInfo(userID)
		// Remove @ prefix for storage
		if tgUsername != "" && tgUsername[0] == '@' {
			tgUsername = tgUsername[1:]
		}
		b.handleRegistrationStart(chatID, userID, userName, tgUsername)
		return
	}

	email := ""
	if e, ok := clientInfo["email"].(string); ok {
		email = e
	}

	if email == "" {
		b.sendMessage(chatID, "❌ Ошибка: не удалось получить информацию о клиенте")
		return
	}

	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(email)
	if err != nil {
		b.logger.Errorf("Failed to get subscription link: %v", err)
		b.sendMessage(chatID, "❌ Не удалось получить ссылку. Попробуйте позже или обратитесь к администратору.")
		return
	}

	// Get expiry time
	expiryTime := int64(0)
	if et, ok := clientInfo["expiryTime"].(float64); ok {
		expiryTime = int64(et)
	}

	// Get traffic limit
	totalGB := int64(0)
	if tgb, ok := clientInfo["totalGB"].(float64); ok {
		totalGB = int64(tgb)
	}

	// Get traffic stats
	var up, down, total int64
	traffic, err := b.apiClient.GetClientTraffics(email)
	if err == nil && traffic != nil {
		if u, ok := traffic["up"].(float64); ok {
			up = int64(u)
		}
		if d, ok := traffic["down"].(float64); ok {
			down = int64(d)
		}
		total = up + down
	}

	// Status icon and text
	statusIcon := "✅"
	statusText := "Активна"
	expiryText := ""

	if expiryTime == 0 {
		// Unlimited subscription
		statusIcon = "♾️"
		statusText = "Безлимитная"
		expiryText = "⏰ Истекает: ∞ (бессрочно)"
	} else {
		// Calculate days remaining
		daysRemaining, hoursRemaining := b.calculateTimeRemaining(expiryTime)

		if daysRemaining <= 0 {
			statusIcon = "⛔"
			statusText = "Истекла"
		} else if daysRemaining <= 3 {
			statusIcon = "🔴"
			statusText = "Заканчивается"
		} else if daysRemaining <= 7 {
			statusIcon = "⚠️"
			statusText = "Скоро истечёт"
		}

		expiryDate := time.UnixMilli(expiryTime).Format("02.01.2006 15:04")
		expiryText = fmt.Sprintf("⏰ Истекает: %s\n📅 Осталось: %d дней %d часов", expiryDate, daysRemaining, hoursRemaining)
	}

	// Build traffic info
	trafficInfo := fmt.Sprintf("📊 <b>Трафик:</b> %s", b.clientService.FormatBytes(total))
	if totalGB > 0 {
		limitBytes := totalGB
		percentage := float64(total) / float64(limitBytes) * 100
		trafficEmoji := "🟢"
		if percentage >= 90 {
			trafficEmoji = "🔴"
		} else if percentage >= 70 {
			trafficEmoji = "🟡"
		}
		trafficInfo += fmt.Sprintf(" / %s %s (%.1f%%)",
			b.clientService.FormatBytes(limitBytes),
			trafficEmoji,
			percentage,
		)
	} else {
		trafficInfo += " (безлимит)"
	}

	// Get device limit
	limitDevicesText := ""
	if limitIP, ok := clientInfo["limitIp"].(float64); ok && int(limitIP) > 0 {
		limitDevicesText = fmt.Sprintf("\n📱 Лимит устройств: %d", int(limitIP))
	}

	// Instructions URL removed - confirmation text included in welcome message

	msg := fmt.Sprintf(
		"📱 <b>Моя подписка</b>\n\n"+
			"👤 Аккаунт: %s\n"+
			"%s Статус: %s\n"+
			"%s%s\n\n"+
			"%s\n\n"+
			"🔗 <b>Ваша VPN конфигурация:</b>\n"+
			"<blockquote expandable>%s</blockquote>",
		html.EscapeString(email),
		statusIcon,
		statusText,
		expiryText,
		limitDevicesText,
		trafficInfo,
		html.EscapeString(subLink),
	)

	b.sendMessage(chatID, msg)
	b.logger.Infof("Sent subscription info to user %d", userID)
}

// handleSettings shows the settings menu for the user
func (b *Bot) handleSettings(chatID int64, userID int64) {
	b.logger.Infof("User %d opened settings", userID)

	msg := "⚙️ <b>Настройки</b>\n\nВыберите действие:"

	keyboard := tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton("🔄 Обновить username"),
		),
		tu.KeyboardRow(
			tu.KeyboardButton("◀️ Назад"),
		),
	).WithResizeKeyboard().WithIsPersistent()

	b.sendMessageWithKeyboard(chatID, msg, keyboard)
}

// handleUpdateUsername initiates the username update process
func (b *Bot) handleUpdateUsername(chatID int64, userID int64) {
	b.logger.Infof("User %d requested username update", userID)

	// Get client info to verify registration
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Вы не зарегистрированы в системе")
		return
	}

	currentEmail := ""
	if e, ok := clientInfo["email"].(string); ok {
		currentEmail = e
	}

	// Set state and ask for new username
	if err := b.setUserState(chatID, "awaiting_new_email"); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}
	b.sendMessage(chatID, fmt.Sprintf("👤 Текущий username: %s\n\nВведите новый username:", currentEmail))
	b.logger.Infof("User %d entering username update mode", userID)
}

// handleNewEmailInput processes new username input and updates client
func (b *Bot) handleNewEmailInput(chatID int64, userID int64, newEmail string) {
	b.logger.Infof("User %d updating username to: %s", userID, newEmail)

	// Validate username length (3-32 characters, count actual characters not bytes)
	usernameLength := utf8.RuneCountInString(newEmail)
	if usernameLength < 3 {
		b.sendMessage(chatID, "❌ Username слишком короткий. Минимум 3 символа.\n\nВведите новый username:")
		return
	}
	if usernameLength > 32 {
		b.sendMessage(chatID, "❌ Username слишком длинный. Максимум 32 символа.\n\nВведите новый username:")
		return
	}

	// Find client by tgId
	foundClient, inboundID, oldEmail, err := b.findClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Ошибка: клиент не найден")
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	// Parse raw JSON and update email field
	rawJSON := foundClient["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		b.sendMessage(chatID, "❌ Ошибка при обработке данных клиента")
		b.logger.Errorf("Failed to parse client JSON: %v", err)
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	// Update email field
	clientData["email"] = newEmail

	// Fix numeric fields
	b.clientService.FixNumericFields(clientData)

	// Call UpdateClient with old email as identifier
	err = b.apiClient.UpdateClient(inboundID, oldEmail, clientData)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка обновления: %v", err))
		b.logger.Errorf("Failed to update username for user %d: %v", userID, err)
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	b.sendMessage(chatID, fmt.Sprintf("✅ Username успешно обновлен!\n\n👤 Старый: %s\n👤 Новый: %s", oldEmail, newEmail))
	b.logger.Infof("Username updated for user %d from %s to %s", userID, oldEmail, newEmail)

	// Clear state
	if err := b.deleteUserState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}
}
