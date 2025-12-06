package bot

import (
	"context"
	"fmt"
	"html"
	"math"
	"strconv"
	"time"
	"x-ui-bot/internal/bot/constants"
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
		clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), chatID)
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
			traffic, err := b.apiClient.GetClientTraffics(context.Background(), email)
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

	status, err := b.apiClient.GetStatus(context.Background())
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

	inbounds, err := b.apiClient.GetInbounds(context.Background())
	if err != nil {
		b.logger.Errorf("Failed to get inbounds: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка получения списка: %v", err))
		return
	}

	if len(inbounds) == 0 {
		b.sendMessage(chatID, "📭 Нет доступных inbound'ов")
		return
	}

	// Group clients by tgId to show unified view
	type GroupedClient struct {
		TgID          string
		Email         string // Clean email without suffix
		Username      string
		Enable        bool // true if enabled in ANY inbound
		IsExpired     bool
		IsUnlimited   bool
		TotalTraffic  int64
		LimitBytes    float64
		InboundIDs    []int // List of inbound IDs
		ClientIndexes []int // Corresponding client indexes
		InboundCount  int   // Number of inbounds
	}

	groupedClients := make(map[string]*GroupedClient) // key: tgId or clean email
	clientCache := make(map[string]map[string]string) // key: "inboundID_clientIndex"

	for _, inbound := range inbounds {
		inboundID := int(inbound["id"].(float64))
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

		for i, client := range clients {
			tgId := client["tgId"]
			email := client["email"]
			cleanEmail := stripInboundSuffix(email)

			// Use tgId as key, or clean email if no tgId
			groupKey := tgId
			if groupKey == "" || groupKey == "0" {
				groupKey = "email_" + cleanEmail
			}

			// Store to cache for callback handling
			cacheKey := fmt.Sprintf("%d_%d", inboundID, i)
			clientCache[cacheKey] = client
			b.storeClientToCache(cacheKey, client)

			// Get or create grouped client
			gc, exists := groupedClients[groupKey]
			if !exists {
				// Parse expiry and limits
				isExpired := false
				isUnlimited := false
				expiryTime := client["expiryTime"]
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

				totalGB := client["totalGB"]
				limitBytes := 0.0
				if totalGB != "" && totalGB != "0" {
					limitBytes, _ = strconv.ParseFloat(totalGB, 64)
				}

				// Get Telegram username
				username := ""
				if tgId != "" && tgId != "0" {
					tgIDInt, err := strconv.ParseInt(tgId, 10, 64)
					if err == nil && tgIDInt > 0 {
						_, username = b.getUserInfo(tgIDInt)
					}
				}

				gc = &GroupedClient{
					TgID:          tgId,
					Email:         cleanEmail,
					Username:      username,
					Enable:        client["enable"] == "true",
					IsExpired:     isExpired,
					IsUnlimited:   isUnlimited,
					LimitBytes:    limitBytes,
					InboundIDs:    []int{inboundID},
					ClientIndexes: []int{i},
					InboundCount:  1,
				}
				groupedClients[groupKey] = gc
			} else {
				// Add to existing group
				gc.InboundIDs = append(gc.InboundIDs, inboundID)
				gc.ClientIndexes = append(gc.ClientIndexes, i)
				gc.InboundCount++
				// If enabled in ANY inbound, show as enabled
				if client["enable"] == "true" {
					gc.Enable = true
				}
			}

			// Accumulate traffic from all inbounds
			traffic, err := b.apiClient.GetClientTraffics(context.Background(), email)
			if err == nil && traffic != nil {
				var up, down int64
				if u, ok := traffic["up"].(float64); ok {
					up = int64(u)
				}
				if d, ok := traffic["down"].(float64); ok {
					down = int64(d)
				}

				// Use max traffic instead of sum, as traffic is synced across inbounds
				currentTotal := up + down
				if currentTotal > gc.TotalTraffic {
					gc.TotalTraffic = currentTotal
				}
			}
		}
	}

	// Build buttons from grouped clients
	var buttons [][]telego.InlineKeyboardButton
	totalClients := 0

	for _, gc := range groupedClients {
		totalClients++

		// Status emoji
		var statusEmoji string
		if gc.IsExpired {
			statusEmoji = "⛔"
		} else if !gc.Enable {
			statusEmoji = "🔴"
		} else if gc.IsUnlimited {
			statusEmoji = "💎"
		} else {
			statusEmoji = "🟢"
		}

		// Traffic info
		trafficStr := ""
		if gc.LimitBytes > 0 {
			limitGB := gc.LimitBytes / (1024 * 1024 * 1024)
			usedGB := float64(gc.TotalTraffic) / (1024 * 1024 * 1024)
			percentage := 0
			if gc.LimitBytes > 0 {
				percentage = int(math.Ceil((float64(gc.TotalTraffic) / gc.LimitBytes) * 100))
			}
			trafficStr = fmt.Sprintf(" %.1fGB/%.0fGB (%d%%)", usedGB, limitGB, percentage)
		} else {
			trafficStr = " ∞"
		}

		// Username
		tgUsernameStr := ""
		if gc.Username != "" {
			tgUsernameStr = fmt.Sprintf(" %s", gc.Username)
		}

		// Inbound count indicator
		inboundIndicator := ""
		if gc.InboundCount > 1 {
			inboundIndicator = fmt.Sprintf(" [%d🌐]", gc.InboundCount)
		}

		// Button text: status + email + username + inbound count + traffic
		buttonText := fmt.Sprintf("%s %s%s%s%s", statusEmoji, gc.Email, tgUsernameStr, inboundIndicator, trafficStr)

		// Use first inbound for callback (we'll handle all inbounds in the menu)
		clientButton := tu.InlineKeyboardButton(buttonText).
			WithCallbackData(fmt.Sprintf("client_%d_%d", gc.InboundIDs[0], gc.ClientIndexes[0]))

		buttons = append(buttons, []telego.InlineKeyboardButton{clientButton})
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

// handleForecast handles the /forecast command - shows total traffic forecast
func (b *Bot) handleForecast(chatID int64, isAdmin bool) {
	if !isAdmin {
		b.sendMessage(chatID, "❌ Эта команда доступна только администраторам")
		return
	}

	if b.forecastService == nil {
		b.sendMessage(chatID, "❌ Сервис прогноза не инициализирован")
		return
	}

	forecast, err := b.forecastService.CalculateTotalForecast()
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка расчета прогноза: %v", err))
		return
	}

	message := "🌐 <b>ОБЩИЙ ПРОГНОЗ ТРАФИКА</b>\n\n" + b.forecastService.FormatForecastMessage(forecast)

	// Build keyboard with inbounds
	inbounds, err := b.apiClient.GetInbounds(context.Background())
	var keyboard *telego.InlineKeyboardMarkup
	if err == nil {
		var rows [][]telego.InlineKeyboardButton
		for _, inbound := range inbounds {
			id := 0
			if v, ok := inbound["id"].(float64); ok {
				id = int(v)
			}
			remark := fmt.Sprintf("Inbound %d", id)
			if r, ok := inbound["remark"].(string); ok && r != "" {
				remark = r
			}

			btn := tu.InlineKeyboardButton(fmt.Sprintf("📊 %s", remark)).
				WithCallbackData(fmt.Sprintf("%s%d", constants.CbForecastInboundPrefix, id))
			rows = append(rows, []telego.InlineKeyboardButton{btn})
		}
		// Add refresh button
		rows = append(rows, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("🔄 Обновить").WithCallbackData(constants.CbForecastTotal),
		})
		keyboard = &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
	}

	b.sendMessageWithInlineKeyboard(chatID, message, keyboard)
}
