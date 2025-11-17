package bot

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

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

		keyboard := tu.Keyboard(
			tu.KeyboardRow(
				tu.KeyboardButton("📊 Статус сервера"),
				tu.KeyboardButton("👥 Список клиентов"),
			),
			tu.KeyboardRow(
				tu.KeyboardButton("📢 Сделать объявление"),
				tu.KeyboardButton("💾 Бэкап БД"),
			),
			tu.KeyboardRow(
				tu.KeyboardButton("📈 Прогноз трафика"),
			),
		).WithResizeKeyboard().WithIsPersistent()

		b.sendMessageWithKeyboard(chatID, msg, keyboard)
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
						tu.KeyboardButton("📱 Моя подписка"),
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
						tu.KeyboardButton("📱 Моя подписка"),
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

	panels := b.panelManager.GetPanels()
	if len(panels) == 0 {
		b.sendMessage(chatID, "❌ No panels configured")
		return
	}

	msg := "📊 Server Status:\n\n"

	for _, panel := range panels {
		msg += fmt.Sprintf("🏠 <b>%s</b> (%s)\n", panel.Name, panel.URL)

		// Show connection status
		if panel.IsHealthy {
			msg += "🟢 Connected\n"
		} else {
			msg += "🔴 Disconnected\n"
			if panel.Error != "" {
				msg += fmt.Sprintf("   Error: %s\n", panel.Error)
			}
		}

		// Get detailed status if healthy
		if panel.IsHealthy {
			client, err := b.panelManager.GetClient(panel.Name)
			if err == nil {
				status, err := client.GetStatus()
				if err == nil {
					if obj, ok := status["obj"].(map[string]interface{}); ok {
						if cpu, ok := obj["cpu"].(float64); ok {
							msg += fmt.Sprintf("   💻 CPU: %.1f%%\n", cpu)
						}
						if mem, ok := obj["mem"].(map[string]interface{}); ok {
							if current, ok := mem["current"].(float64); ok {
								if total, ok := mem["total"].(float64); ok {
									msg += fmt.Sprintf("   🧠 Memory: %.1f / %.1f GB\n",
										current/1024/1024/1024, total/1024/1024/1024)
								}
							}
						}
						if uptime, ok := obj["uptime"].(float64); ok {
							hours := int(uptime / 3600)
							minutes := int((uptime - float64(hours*3600)) / 60)
							msg += fmt.Sprintf("   ⏱️ Uptime: %dh %dm\n", hours, minutes)
						}
					}
				} else {
					msg += fmt.Sprintf("   ⚠️ Status error: %v\n", err)
				}
			}
		}

		// Show last health check time
		if !panel.LastHealthCheck.IsZero() {
			msg += fmt.Sprintf("   🔍 Last check: %s\n", panel.LastHealthCheck.Format("15:04:05"))
		}

		msg += "\n"
	}

	b.sendMessage(chatID, msg)
}

// handleID handles the /id command
func (b *Bot) handleID(chatID, userID int64) {
	b.logger.Infof("ID request from user ID: %d", userID)
	msg := fmt.Sprintf("🆔 Ваш Telegram ID: <code>%d</code>", userID)
	b.sendMessage(chatID, msg)
}

// handleClients handles the /clients command - shows list of inbounds
func (b *Bot) handleClients(chatID int64, isAdmin bool, messageID ...int) {
	if !isAdmin {
		b.sendMessage(chatID, "⛔ У вас нет прав для использования этой команды")
		return
	}

	b.logger.Infof("Clients list requested by user ID: %d", chatID)

	if len(messageID) == 0 {
		b.sendMessage(chatID, "⏳ Загружаю список панелей и инбаундов...")
	}

	panels := b.panelManager.GetHealthyPanels()
	if len(panels) == 0 {
		b.sendMessage(chatID, "❌ Нет доступных панелей")
		return
	}

	// Build inline keyboard with panels and their inbounds
	var buttons [][]telego.InlineKeyboardButton

	for _, panel := range panels {
		// Add panel header
		panelHeader := fmt.Sprintf("🏠 <b>%s</b> (%s)", panel.Name, panel.URL)
		if !panel.IsHealthy {
			panelHeader += " 🔴"
		} else {
			panelHeader += " 🟢"
		}
		headerButton := tu.InlineKeyboardButton(panelHeader).WithCallbackData("panel_header")
		buttons = append(buttons, []telego.InlineKeyboardButton{headerButton})

		// Get client for this panel
		client, err := b.panelManager.GetClient(panel.Name)
		if err != nil {
			b.logger.Errorf("Failed to get client for panel %s: %v", panel.Name, err)
			continue
		}

		// Get inbounds for this panel
		inbounds, err := client.GetInbounds()
		if err != nil {
			b.logger.Errorf("Failed to get inbounds for panel %s: %v", panel.Name, err)
			continue
		}

		if len(inbounds) == 0 {
			noInboundButton := tu.InlineKeyboardButton("   📭 Нет инбаундов").WithCallbackData("no_inbounds")
			buttons = append(buttons, []telego.InlineKeyboardButton{noInboundButton})
			continue
		}

		// Add inbound buttons for this panel
		for _, inbound := range inbounds {
			// Get inbound ID
			inboundID := 0
			if id, ok := inbound["id"].(float64); ok {
				inboundID = int(id)
			}

			// Get inbound remark (name)
			remark := "Unnamed"
			if r, ok := inbound["remark"].(string); ok && r != "" {
				remark = r
			}

			// Get protocol
			protocol := ""
			if p, ok := inbound["protocol"].(string); ok {
				protocol = strings.ToUpper(p)
			}

			// Get port
			port := ""
			if p, ok := inbound["port"].(float64); ok {
				port = fmt.Sprintf(":%d", int(p))
			}

			// Count clients
			settingsStr := ""
			if s, ok := inbound["settings"].(string); ok {
				settingsStr = s
			}

			clientCount := 0
			clients, err := b.clientService.ParseClients(settingsStr)
			if err == nil {
				clientCount = len(clients)
			}

			// Inbound status
			enable := true
			if e, ok := inbound["enable"].(bool); ok {
				enable = e
			}

			statusEmoji := "🟢"
			if !enable {
				statusEmoji = "🔴"
			}

			// Button text: status + protocol + remark + port + client count
			buttonText := fmt.Sprintf("   %s %s %s%s (%d клиентов)", statusEmoji, protocol, remark, port, clientCount)
			inboundButton := tu.InlineKeyboardButton(buttonText).
				WithCallbackData(fmt.Sprintf("inbound_%s_%d", panel.Name, inboundID))

			buttons = append(buttons, []telego.InlineKeyboardButton{inboundButton})
		}
	}

	if len(buttons) == 0 {
		b.sendMessage(chatID, "📭 Нет панелей и инбаундов для отображения")
		return
	}

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: buttons}
	msg := "📋 <b>Список панелей и инбаундов</b>\n\nВыберите инбаунд для просмотра клиентов:"

	if len(messageID) > 0 {
		b.editMessage(chatID, messageID[0], msg, keyboard)
	} else {
		b.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
	}

	b.logger.Infof("Sent %d panels with inbounds to user ID: %d", len(panels), chatID)
}

// handleInboundClients shows clients list for specific inbound
func (b *Bot) handleInboundClients(chatID int64, panelName string, inboundID int, messageID int) {
	b.logger.Infof("Inbound %d clients requested for panel %s by user ID: %d", inboundID, panelName, chatID)

	// Get client for the panel
	client, err := b.panelManager.GetClient(panelName)
	if err != nil {
		b.logger.Errorf("Failed to get client for panel %s: %v", panelName, err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка получения клиента панели: %v", err))
		return
	}

	inbound, err := client.GetInbound(inboundID)
	if err != nil {
		b.logger.Errorf("Failed to get inbound: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка получения инбаунда: %v", err))
		return
	}

	// Get inbound name
	remark := "Unnamed"
	if r, ok := inbound["remark"].(string); ok && r != "" {
		remark = r
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
			"panel":      panelName,
		}).Error("Failed to parse clients")
		b.sendMessage(chatID, "❌ Ошибка парсинга клиентов")
		return
	}

	if len(clients) == 0 {
		// Back button
		backButton := tu.InlineKeyboardButton("🔙 Назад").
			WithCallbackData("clients_back")
		keyboard := &telego.InlineKeyboardMarkup{
			InlineKeyboard: [][]telego.InlineKeyboardButton{{backButton}},
		}
		msg := fmt.Sprintf("📭 В инбаунде <b>%s</b> нет клиентов", html.EscapeString(remark))
		b.editMessage(chatID, messageID, msg, keyboard)
		return
	}

	// Build inline keyboard with clients
	var buttons [][]telego.InlineKeyboardButton

	for i, client := range clients {
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
		cacheKey := fmt.Sprintf("%s_%d_%d", panelName, inboundID, i)
		b.clientCache.Store(cacheKey, client)

		// Button text: status + email + username + traffic
		buttonText := fmt.Sprintf("%s %s%s%s", statusEmoji, email, tgUsernameStr, trafficStr)
		clientButton := tu.InlineKeyboardButton(buttonText).
			WithCallbackData(fmt.Sprintf("client_%s_%d_%d", panelName, inboundID, i))

		buttons = append(buttons, []telego.InlineKeyboardButton{clientButton})
	}

	// Add back button
	backButton := tu.InlineKeyboardButton("� Назад к инбаундам").
		WithCallbackData("clients_back")
	buttons = append(buttons, []telego.InlineKeyboardButton{backButton})

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: buttons}
	msg := fmt.Sprintf("📋 <b>Клиенты в инбаунде: %s</b>\n\nВыберите клиента для управления:", html.EscapeString(remark))

	b.editMessage(chatID, messageID, msg, keyboard)
	b.logger.Infof("Sent %d clients from inbound %d to user ID: %d", len(clients), inboundID, chatID)
}
