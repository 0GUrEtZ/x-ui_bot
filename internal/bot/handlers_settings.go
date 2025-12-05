package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"time"
	"unicode/utf8"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Settings handlers for user settings: subscription info, settings menu, username update

// sendSubscriptionInfo sends subscription details with QR code to user
func (b *Bot) sendSubscriptionInfo(chatID int64, userID int64, email string, title string) error {
	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(context.Background(), email)
	if err != nil {
		b.logger.Errorf("Failed to get subscription link: %v", err)
		return fmt.Errorf("не удалось получить ссылку: %w", err)
	}

	// Get client info for detailed stats
	clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), userID)
	if err != nil {
		return fmt.Errorf("не удалось получить информацию о клиенте: %w", err)
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
	traffic, err := b.apiClient.GetClientTraffics(context.Background(), email)
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

	msg := fmt.Sprintf(
		"%s\n\n"+
			"👤 Аккаунт: %s\n"+
			"%s Статус: %s\n"+
			"%s%s\n\n"+
			"%s\n\n"+
			"🔗 <b>Ваша VPN конфигурация:</b>\n"+
			"<blockquote expandable>%s</blockquote>\n\n"+
			"📲 Отсканируйте QR-код выше в приложении VPN или используйте ссылку",
		title,
		html.EscapeString(email),
		statusIcon,
		statusText,
		expiryText,
		limitDevicesText,
		trafficInfo,
		html.EscapeString(subLink),
	)

	// Create keyboard with Instructions button
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📖 Инструкции").WithCallbackData("instructions_menu"),
		),
	)

	// Generate and send QR code with caption
	qrCode, err := b.apiClient.GetClientQRCode(context.Background(), email)
	if err != nil {
		b.logger.Errorf("Failed to generate QR code for user %d: %v", userID, err)
		// Fallback to text-only message
		b.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
	} else {
		// Send QR code as photo with full caption
		photo := &telego.SendPhotoParams{
			ChatID:      tu.ID(chatID),
			Photo:       telego.InputFile{File: tu.NameReader(bytes.NewReader(qrCode), "qr_code.png")},
			Caption:     msg,
			ParseMode:   "HTML",
			ReplyMarkup: keyboard,
		}

		if _, err := b.bot.SendPhoto(context.Background(), photo); err != nil {
			b.logger.Errorf("Failed to send QR code to user %d: %v", userID, err)
			// Fallback to text-only message
			b.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
		}
	}

	return nil
}

// handleMySubscription shows detailed subscription information for the user
func (b *Bot) handleMySubscription(chatID int64, userID int64) {
	b.logger.Infof("User %d requested subscription info", userID)

	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), userID)
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

	// Send subscription info with QR code
	if err := b.sendSubscriptionInfo(chatID, userID, email, "📱 <b>Моя подписка</b>"); err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ %s", err.Error()))
		return
	}

	b.logger.Infof("Sent subscription info to user %d", userID)
}

// handleExtensionMenu shows the extension request menu
func (b *Bot) handleExtensionMenu(chatID int64, userID int64, messageID int) {
	b.logger.Infof("User %d opened extension menu", userID)

	// Get client info to show current subscription
	clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Вы не зарегистрированы в системе")
		return
	}

	email := ""
	if e, ok := clientInfo["email"].(string); ok {
		email = e
	}

	// Get expiry time
	expiryTime := time.Unix(0, 0)
	if exp, ok := clientInfo["expiryTime"].(float64); ok {
		expiryTime = time.UnixMilli(int64(exp))
	}

	msg := fmt.Sprintf(
		"⏰ <b>Продление подписки</b>\n\n"+
			"👤 Аккаунт: %s\n"+
			"📅 Истекает: %s\n\n"+
			"Выберите период продления:",
		email,
		expiryTime.Format("02.01.2006 15:04"),
	)

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📅 30 дней").WithCallbackData(fmt.Sprintf("extend_%d_30", userID)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📅 60 дней").WithCallbackData(fmt.Sprintf("extend_%d_60", userID)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("📅 90 дней").WithCallbackData(fmt.Sprintf("extend_%d_90", userID)),
		),
	)

	if _, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        msg,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}); err != nil {
		b.logger.Errorf("Failed to edit extension menu message: %v", err)
	}
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
	clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), userID)
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

	// Get all inbounds to update username across all of them
	inbounds, err := b.apiClient.GetInbounds(context.Background())
	if err != nil {
		b.sendMessage(chatID, "❌ Ошибка получения inbounds")
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	// Find all clients with this tgId and update them
	updatedCount := 0
	oldEmailClean := ""

	for idx, inbound := range inbounds {
		inboundID := int(inbound["id"].(float64))
		settingsStr := ""
		if settings, ok := inbound["settings"].(string); ok {
			settingsStr = settings
		}

		clients, err := b.clientService.ParseClients(settingsStr)
		if err != nil {
			b.logger.Errorf("Failed to parse clients for inbound %d: %v", inboundID, err)
			continue
		}

		// Find client with matching tgId
		for _, client := range clients {
			if client["tgId"] == fmt.Sprintf("%d", userID) {
				// Parse raw JSON
				rawJSON := client["_raw_json"]
				var clientData map[string]interface{}
				if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
					b.logger.Errorf("Failed to parse client JSON: %v", err)
					continue
				}

				// Get old email (with suffix if present)
				oldEmailWithSuffix := client["email"]
				if oldEmailClean == "" {
					oldEmailClean = stripInboundSuffix(oldEmailWithSuffix)
				}

				// Build new email with appropriate suffix for this inbound
				newEmailForInbound := newEmail
				if idx > 0 {
					newEmailForInbound = fmt.Sprintf("%s##ib%d", newEmail, inboundID)
				}

				// Update email field
				clientData["email"] = newEmailForInbound

				// Fix numeric fields
				b.clientService.FixNumericFields(clientData)

				// Update client in this inbound
				err = b.apiClient.UpdateClient(context.Background(), inboundID, oldEmailWithSuffix, clientData)
				if err != nil {
					b.logger.Errorf("Failed to update username in inbound %d: %v", inboundID, err)
				} else {
					b.logger.Infof("Updated username in inbound %d from %s to %s", inboundID, oldEmailWithSuffix, newEmailForInbound)
					updatedCount++
				}
			}
		}
	}

	if updatedCount == 0 {
		b.sendMessage(chatID, "❌ Не удалось обновить username ни в одном inbound")
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	b.sendMessage(chatID, fmt.Sprintf("✅ Username успешно обновлен во всех inbounds!\n\n👤 Старый: %s\n👤 Новый: %s\n📊 Обновлено: %d/%d", oldEmailClean, newEmail, updatedCount, len(inbounds)))
	b.logger.Infof("Username updated for user %d from %s to %s in %d inbounds", userID, oldEmailClean, newEmail, updatedCount)

	// Clear state
	if err := b.deleteUserState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}
}

// handleInstructionsMenu shows the platform selection menu
func (b *Bot) handleInstructionsMenu(chatID int64, messageID int) {
	keyboard := b.createInstructionsKeyboard()

	// Edit the message to show the instructions menu
	_, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      telego.ChatID{ID: chatID},
		MessageID:   messageID,
		Text:        "📖 <b>Инструкции по настройке</b>\n\nВыберите ваше устройство:",
		ParseMode:   telego.ModeHTML,
		ReplyMarkup: keyboard,
	})
	if err != nil {
		b.logger.Errorf("Failed to edit message to instructions menu: %v", err)
		// If edit fails (e.g. message too old), send a new one
		b.sendMessageWithInlineKeyboard(chatID, "📖 <b>Инструкции по настройке</b>\n\nВыберите ваше устройство:", keyboard)
	}
}

// handleInstructionPlatform sends the link for the selected platform
func (b *Bot) handleInstructionPlatform(chatID int64, userID int64, messageID int, platform string) {
	if platform == "back" {
		// Delete the instructions message
		if err := b.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
			ChatID:    telego.ChatID{ID: chatID},
			MessageID: messageID,
		}); err != nil {
			b.logger.Errorf("Failed to delete instructions message: %v", err)
		}

		// Show subscription info again
		b.handleMySubscription(chatID, userID)
		return
	}

	var url string
	var platformName string

	switch platform {
	case "ios":
		url = b.config.Instructions.IOS
		platformName = "iOS"
	case "macos":
		url = b.config.Instructions.MacOS
		platformName = "macOS"
	case "android":
		url = b.config.Instructions.Android
		platformName = "Android"
	case "windows":
		url = b.config.Instructions.Windows
		platformName = "Windows"
	}

	if url == "" {
		// Answer with alert
		b.sendMessage(chatID, fmt.Sprintf("❌ Инструкция для %s не найдена.", platformName))
		return
	}

	msg := fmt.Sprintf("📄 <b>Инструкция для %s</b>\n\n<a href=\"%s\">Нажмите здесь, чтобы открыть инструкцию</a>", platformName, url)

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔗 Открыть инструкцию").WithURL(url),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("◀️ Назад").WithCallbackData("instructions_menu"),
		),
	)

	// Edit the message to show the link
	_, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      telego.ChatID{ID: chatID},
		MessageID:   messageID,
		Text:        msg,
		ParseMode:   telego.ModeHTML,
		ReplyMarkup: keyboard,
	})
	if err != nil {
		b.logger.Errorf("Failed to edit message to instruction link: %v", err)
		b.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
	}
}
