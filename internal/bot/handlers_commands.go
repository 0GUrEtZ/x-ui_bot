package bot

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"time"
	"x-ui-bot/internal/bot/keyboard"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Command handlers for bot commands: /start, /help, /status, /id, /clients

// handleStart handles the /start command - shows main menu based on user role
func (b *Bot) handleStart(chatID int64, firstName string, isAdmin bool) {
	b.logger.Infof("User %s (ID: %d) started bot", firstName, chatID)

	msg := fmt.Sprintf("👋 Привет, %s!\n\n", firstName)
	if isAdmin {
		msg += "✅ Вы авторизованы как администратор\n\n"
		msg += "Используйте кнопки ниже для управления:"

		kb := keyboard.BuildAdminKeyboard()

		b.sendMessageWithKeyboard(chatID, msg, kb)
	} else {
		// Check if user is registered
		clientInfo, err := b.apiClient.GetClientByTgID(chatID)
		if err == nil && clientInfo != nil {
			// User is registered - show client menu with subscription info
			email := ""
			if e, ok := clientInfo["email"].(string); ok {
				email = e
			}

			expiryTime := int64(0)
			if et, ok := clientInfo["expiryTime"].(float64); ok {
				expiryTime = int64(et)
			}

			// Calculate days remaining
			daysRemaining, hoursRemaining := b.calculateTimeRemaining(expiryTime)

			// Get traffic limit
			totalGB := int64(0)
			if tgb, ok := clientInfo["totalGB"].(float64); ok {
				totalGB = int64(tgb)
			}

			// Get traffic stats
			var total int64
			traffic, err := b.apiClient.GetClientTraffics(email)
			if err == nil && traffic != nil {
				if u, ok := traffic["up"].(float64); ok {
					total += int64(u)
				}
				if d, ok := traffic["down"].(float64); ok {
					total += int64(d)
				}
			}

			statusIcon := "✅"
			statusText := fmt.Sprintf("%d дн. %d ч.", daysRemaining, hoursRemaining)
			if expiryTime == 0 {
				// Unlimited subscription
				statusIcon = "♾️"
				statusText = "Безлимитная"
			} else if daysRemaining <= 0 {
				statusIcon = "⛔"
				statusText = "Истекла"
			} else if daysRemaining <= 3 {
				statusIcon = "🔴"
				statusText = fmt.Sprintf("%d дн. %d ч. (критично!)", daysRemaining, hoursRemaining)
			} else if daysRemaining <= 7 {
				statusIcon = "⚠️"
				statusText = fmt.Sprintf("%d дн. %d ч.", daysRemaining, hoursRemaining)
			}

			msg += fmt.Sprintf("👤 Аккаунт: %s\n", html.EscapeString(email))
			msg += fmt.Sprintf("%s Подписка: %s\n", statusIcon, statusText)

			// Add traffic info
			if totalGB > 0 {
				limitBytes := totalGB
				percentage := float64(total) / float64(limitBytes) * 100
				trafficEmoji := "🟢"
				if percentage >= 90 {
					trafficEmoji = "🔴"
				} else if percentage >= 70 {
					trafficEmoji = "🟡"
				}
				msg += fmt.Sprintf("📊 Трафик: %s / %s %s (%.1f%%)\n",
					b.clientService.FormatBytes(total),
					b.clientService.FormatBytes(limitBytes),
					trafficEmoji,
					percentage,
				)
			} else {
				msg += fmt.Sprintf("📊 Трафик: %s (безлимит)\n", b.clientService.FormatBytes(total))
			}
			msg += "\nВыберите действие:"

			// Build keyboard based on subscription type
			var keyboard *telego.ReplyKeyboardMarkup
			if expiryTime == 0 {
				// Unlimited subscription - no extend button
				keyboard = tu.Keyboard(
					tu.KeyboardRow(
						tu.KeyboardButton("📱 Моя подписка и инструкции"),
						tu.KeyboardButton("⚙️ Настройки"),
					),
					tu.KeyboardRow(
						tu.KeyboardButton("💬 Связь с админом"),
					),
				).WithResizeKeyboard().WithIsPersistent()
			} else {
				// Limited subscription - show extend button
				keyboard = tu.Keyboard(
					tu.KeyboardRow(
						tu.KeyboardButton("📱 Моя подписка и инструкции"),
						tu.KeyboardButton("⏰ Продлить подписку"),
					),
					tu.KeyboardRow(
						tu.KeyboardButton("⚙️ Настройки"),
						tu.KeyboardButton("💬 Связь с админом"),
					),
				).WithResizeKeyboard().WithIsPersistent()
			}

			b.sendMessageWithKeyboard(chatID, msg, keyboard)
		} else {
			// User is not registered - send welcome message
			welcomeMsg := fmt.Sprintf("👋 Привет, %s!\n\nДля использования VPN сервиса необходимо ознакомиться с условиями.", firstName)

			keyboard := tu.Keyboard(
				tu.KeyboardRow(
					tu.KeyboardButton("📜 Ознакомиться с условиями"),
				),
			).WithResizeKeyboard().WithIsPersistent()

			b.sendMessageWithKeyboard(chatID, welcomeMsg, keyboard)
		}
	}
}

// handleHelp handles the /help command
func (b *Bot) handleHelp(chatID int64) {
	b.logger.Infof("Help requested by user ID: %d", chatID)

	msg := `📋 Доступные команды:

🏠 /start - Главное меню
ℹ️ /help - Эта справка
📊 /status - Статус сервера
🆔 /id - Получить ваш Telegram ID
👤 /usage &lt;email&gt; - Статистика клиента
👥 /clients - Список всех клиентов

Или используйте кнопки ниже для быстрого доступа.`
	b.sendMessage(chatID, msg)
}

// handleStatus handles the /status command
func (b *Bot) handleStatus(chatID int64, isAdmin bool) {
	if !isAdmin {
		b.sendMessage(chatID, "⛔ You don't have permission to use this command.")
		return
	}

	status, err := b.apiClient.GetStatus()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Failed to get status: %v", err))
		return
	}

	// Format status message
	msg := "📊 Server Status:\n\n"
	if obj, ok := status["obj"].(map[string]interface{}); ok {
		if cpu, ok := obj["cpu"].(float64); ok {
			msg += fmt.Sprintf("💻 CPU: %.2f%%\n", cpu)
		}
		if mem, ok := obj["mem"].(map[string]interface{}); ok {
			if current, ok := mem["current"].(float64); ok {
				if total, ok := mem["total"].(float64); ok {
					msg += fmt.Sprintf("🧠 Memory: %.2f / %.2f GB\n", current/1024/1024/1024, total/1024/1024/1024)
				}
			}
		}
		if uptime, ok := obj["uptime"].(float64); ok {
			hours := int(uptime / 3600)
			minutes := int((uptime - float64(hours*3600)) / 60)
			msg += fmt.Sprintf("⏱️ Uptime: %dh %dm\n", hours, minutes)
		}
	}

	b.sendMessage(chatID, msg)
}

// handleID handles the /id command
func (b *Bot) handleID(chatID, userID int64) {
	b.logger.Infof("ID request from user ID: %d", userID)
	msg := fmt.Sprintf("🆔 Ваш Telegram ID: <code>%d</code>", userID)
	b.sendMessage(chatID, msg)
}

// handleClients handles the /clients command - shows all clients with traffic stats
func (b *Bot) handleClients(chatID int64, isAdmin bool, messageID ...int) {
	if !isAdmin {
		b.sendMessage(chatID, "⛔ У вас нет прав для использования этой команды")
		return
	}

	b.logger.Infof("Clients list requested by user ID: %d", chatID)

	if len(messageID) == 0 {
		b.sendMessage(chatID, "⏳ Загружаю список клиентов...")
	}

	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		b.logger.Errorf("Failed to get inbounds: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка получения списка: %v", err))
		return
	}

	if len(inbounds) == 0 {
		b.sendMessage(chatID, "📭 Нет доступных inbound'ов")
		return
	}

	// Build inline keyboard with all clients
	var buttons [][]telego.InlineKeyboardButton
	totalClients := 0

	for _, inbound := range inbounds {
		// Get inbound ID
		inboundID := 0
		if id, ok := inbound["id"].(float64); ok {
			inboundID = int(id)
		}

		// Parse settings to get client configurations
		settingsStr := ""
		if s, ok := inbound["settings"].(string); ok {
			settingsStr = s
		}

		clients, err := b.clientService.ParseClients(settingsStr)
		if err != nil {
			b.logger.WithFields(map[string]interface{}{
				"error":      err,
				"inbound_id": inboundID,
			}).Error("Failed to parse clients")
			continue
		}
		if len(clients) == 0 {
			continue
		}

		// Create button for each client
		for i, client := range clients {
			totalClients++
			email := client["email"]
			enable := client["enable"]
			totalGB := client["totalGB"]
			expiryTime := client["expiryTime"]

			// Check if subscription expired
			isExpired := false
			isUnlimited := false
			if expiryTime != "" && expiryTime != "0" {
				timestamp, err := strconv.ParseInt(expiryTime, 10, 64)
				if err == nil && timestamp > 0 {
					now := time.Now().UnixMilli()
					if timestamp < now {
						isExpired = true
					}
				}
			} else {
				isUnlimited = true
			}

			// Status emoji with subscription status
			var statusEmoji string
			if isExpired {
				statusEmoji = "⛔" // Expired subscription
			} else if enable == "false" {
				statusEmoji = "🔴" // Blocked
			} else if isUnlimited {
				statusEmoji = "💎" // Unlimited subscription
			} else {
				statusEmoji = "🟢" // Active
			}

			// Get traffic info
			trafficStr := ""
			traffic, err := b.apiClient.GetClientTraffics(email)
			if err == nil && traffic != nil {
				var up, down, total int64
				if u, ok := traffic["up"].(float64); ok {
					up = int64(u)
				}
				if d, ok := traffic["down"].(float64); ok {
					down = int64(d)
				}
				total = up + down

				// Show traffic with limit or unlimited
				if totalGB != "" && totalGB != "0" {
					// totalGB is already in bytes
					limitBytes, _ := strconv.ParseFloat(totalGB, 64)
					limitGB := limitBytes / (1024 * 1024 * 1024)

					usedGB := float64(total) / (1024 * 1024 * 1024)

					// Calculate percentage and round up
					percentage := 0
					if limitBytes > 0 {
						percentage = int(math.Ceil((float64(total) / limitBytes) * 100))
					}

					trafficStr = fmt.Sprintf(" %.1fGB/%.0fGB (%d%%)", usedGB, limitGB, percentage)
				} else {
					// Unlimited traffic
					trafficStr = " ∞"
				}
			}

			// Get Telegram username if exists
			tgUsernameStr := ""
			if tgId, ok := client["tgId"]; ok && tgId != "" && tgId != "0" {
				tgIDInt, err := strconv.ParseInt(tgId, 10, 64)
				if err == nil && tgIDInt > 0 {
					_, username := b.getUserInfo(tgIDInt)
					if username != "" {
						tgUsernameStr = fmt.Sprintf(" %s", username)
					}
				}
			}

			// Store client info for callback handling
			b.clientCache.Store(fmt.Sprintf("%d_%d", inboundID, i), client)

			// Button text: status + email + username + traffic
			buttonText := fmt.Sprintf("%s %s%s%s", statusEmoji, email, tgUsernameStr, trafficStr)
			clientButton := tu.InlineKeyboardButton(buttonText).
				WithCallbackData(fmt.Sprintf("client_%d_%d", inboundID, i))

			buttons = append(buttons, []telego.InlineKeyboardButton{clientButton})
		}
	}

	if len(buttons) == 0 {
		b.sendMessage(chatID, "📭 Нет клиентов для отображения")
		return
	}

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: buttons}
	msg := "📋 <b>Список клиентов</b>\n\nВыберите клиента для управления:"

	if len(messageID) > 0 {
		b.editMessage(chatID, messageID[0], msg, keyboard)
	} else {
		b.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
	}

	b.logger.Infof("Sent %d clients to user ID: %d", totalClients, chatID)
}
