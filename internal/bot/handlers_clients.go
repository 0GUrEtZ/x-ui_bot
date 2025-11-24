package bot

import (
	"context"
	"fmt"
	"html"
	"strconv"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// handleClientMenu shows client details and actions
// handleClientMenu shows client details and actions
func (b *Bot) handleClientMenu(chatID int64, messageID int, panelName string, inboundID int, clientIndex int, queryID string) {
	b.handleClientMenuImpl(chatID, messageID, panelName, inboundID, clientIndex, queryID)
}

func (b *Bot) handleClientMenuImpl(chatID int64, messageID int, panelName string, inboundID int, clientIndex int, queryID string) {
	cacheKey := fmt.Sprintf("%s_%d_%d", panelName, inboundID, clientIndex)
	clientData, ok := b.clientCache.Load(cacheKey)
	if !ok {
		b.sendMessage(chatID, "❌ Клиент не найден в кэше")
		return
	}

	client := clientData.(map[string]string)
	email := client["email"]
	enable := client["enable"]
	tgId := client["tgId"]
	_ = client["expiryTime"]

	// Get client traffic stats
	var up, down, total int64
	clientAPI, err := b.panelManager.GetClient(panelName)
	if err == nil {
		traffic, err := clientAPI.GetClientTraffics(email)
		if err == nil && traffic != nil {
			if u, ok := traffic["up"].(float64); ok {
				up = int64(u)
			}
			if d, ok := traffic["down"].(float64); ok {
				down = int64(d)
			}
			total = up + down
		}
	}

	// Get Telegram username if exists
	tgUsernameStr := ""
	if tgId != "" && tgId != "0" {
		tgIDInt, err := strconv.ParseInt(tgId, 10, 64)
		if err == nil && tgIDInt > 0 {
			_, username := b.getUserInfo(tgIDInt)
			if username != "" {
				tgUsernameStr = fmt.Sprintf("\n👤 Telegram: %s", username)
			}
		}
	}

	// Subscription expiry status is omitted here (not used in message)

	// Build message
	msg := fmt.Sprintf(
		"👤 Email: %s\n%s\n\n📊 Трафик: %s\n\nВыберите действие:",
		html.EscapeString(email),
		tgUsernameStr,
		b.clientService.FormatBytes(total),
	)

	// Build keyboard
	var buttons [][]telego.InlineKeyboardButton

	// Toggle enable/disable
	if enable == "false" {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("✅ Разблокировать").WithCallbackData(fmt.Sprintf("toggle_%s_%d_%d", panelName, inboundID, clientIndex)),
		})
	} else {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("🔒 Заблокировать").WithCallbackData(fmt.Sprintf("toggle_%s_%d_%d", panelName, inboundID, clientIndex)),
		})
	}

	// Message button if tgId exists
	if tgId != "" && tgId != "0" {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("💬 Написать").WithCallbackData(fmt.Sprintf("msg_%s_%d_%d", panelName, inboundID, clientIndex)),
		})
	}

	// Move client button (new for multi-server)
	buttons = append(buttons, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("🔄 Переместить").WithCallbackData(fmt.Sprintf("move_%s_%d_%d", panelName, inboundID, clientIndex)),
	})

	// Delete button
	buttons = append(buttons, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("🗑️ Удалить").WithCallbackData(fmt.Sprintf("delete_%s_%d_%d", panelName, inboundID, clientIndex)),
	})

	// Back button - return to clients list in current inbound
	buttons = append(buttons, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("◀️ Назад").WithCallbackData(fmt.Sprintf("back_inbound_%s_%d", panelName, inboundID)),
	})

	keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: buttons}

	if _, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        msg,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}); err != nil {
		b.logger.Errorf("Failed to edit client menu message: %v", err)
	}
}

// handleMoveClient handles showing move options for a client
func (b *Bot) handleMoveClientImpl(chatID int64, messageID int, fromPanelName string, fromInboundID int, clientIndex int) {
	// Get client data from cache
	cacheKey := fmt.Sprintf("%s_%d_%d", fromPanelName, fromInboundID, clientIndex)
	clientData, ok := b.clientCache.Load(cacheKey)
	if !ok {
		b.sendMessage(chatID, "❌ Данные клиента не найдены в кэше")
		return
	}

	client := clientData.(map[string]string)
	email := client["email"]

	// Get healthy panels
	panels := b.panelManager.GetHealthyPanels()
	if len(panels) == 0 {
		b.sendMessage(chatID, "❌ Нет доступных панелей")
		return
	}

	// Create keyboard with available inbounds from all panels
	var buttons [][]telego.InlineKeyboardButton

	for _, panel := range panels {
		clientAPI, err := b.panelManager.GetClient(panel.Name)
		if err != nil {
			b.logger.Warnf("Failed to get client for panel %s: %v", panel.Name, err)
			continue
		}

		inbounds, err := clientAPI.GetInbounds()
		if err != nil {
			b.logger.Warnf("Failed to get inbounds for panel %s: %v", panel.Name, err)
			continue
		}

		// Add panel header
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton(fmt.Sprintf("🏠 %s", panel.Name)).WithCallbackData("noop"),
		})

		for _, inbound := range inbounds {
			inboundID := int(inbound["id"].(float64))

			// Skip current inbound
			if panel.Name == fromPanelName && inboundID == fromInboundID {
				continue
			}

			protocol := ""
			if p, ok := inbound["protocol"].(string); ok {
				protocol = p
			}
			port := ""
			if p, ok := inbound["port"].(float64); ok {
				port = fmt.Sprintf("%.0f", p)
			}
			remark := ""
			if r, ok := inbound["remark"].(string); ok {
				remark = r
			}

			buttonText := fmt.Sprintf("  %s:%s (%s)", protocol, port, remark)
			callbackData := fmt.Sprintf("move_confirm_%s_%d_%d_%s_%d", fromPanelName, fromInboundID, clientIndex, panel.Name, inboundID)

			buttons = append(buttons, []telego.InlineKeyboardButton{
				tu.InlineKeyboardButton(buttonText).WithCallbackData(callbackData),
			})
		}
	}

	if len(buttons) == 0 {
		b.sendMessage(chatID, "❌ Нет доступных inbound'ов для перемещения")
		return
	}

	// Add cancel button
	buttons = append(buttons, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("❌ Отмена").WithCallbackData(fmt.Sprintf("back_client_%s_%d_%d", fromPanelName, fromInboundID, clientIndex)),
	})

	keyboard := tu.InlineKeyboard(buttons...)

	msg := fmt.Sprintf(
		"🔄 <b>Перемещение клиента</b>\n\n"+
			"👤 Email: %s\n"+
			"📍 Текущий сервер: %s (Inbound ID: %d)\n\n"+
			"Выберите новый сервер и inbound для перемещения:",
		html.EscapeString(email),
		html.EscapeString(fromPanelName),
		fromInboundID,
	)

	// Update the message with move options
	editParams := &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        msg,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	}

	if _, err := b.bot.EditMessageText(context.Background(), editParams); err != nil {
		b.logger.Errorf("Failed to edit message with move options: %v", err)
		b.sendMessage(chatID, "❌ Ошибка при обновлении сообщения")
	}
}

// handleMoveClientConfirm handles the confirmation of client movement between inbounds
func (b *Bot) handleMoveClientConfirmImpl(chatID int64, messageID int, fromPanelName string, fromInboundID int, clientIndex int, toPanelName string, toInboundID int) {
	// Get client data from cache
	cacheKey := fmt.Sprintf("%s_%d_%d", fromPanelName, fromInboundID, clientIndex)
	clientData, ok := b.clientCache.Load(cacheKey)
	if !ok {
		b.sendMessage(chatID, "❌ Данные клиента не найдены в кэше")
		return
	}

	client := clientData.(map[string]string)
	email := client["email"]

	// Get source panel client
	fromClient, err := b.panelManager.GetClient(fromPanelName)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка доступа к исходной панели: %v", err))
		return
	}

	// Get destination panel client
	toClient, err := b.panelManager.GetClient(toPanelName)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка доступа к целевой панели: %v", err))
		return
	}

	// Get client details from source panel
	clients, err := fromClient.GetClientTrafficsById(fromInboundID)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка получения данных клиентов: %v", err))
		return
	}

	// Find the client by email
	var clientDetails map[string]interface{}
	for _, client := range clients {
		if clientEmail, ok := client["email"].(string); ok && clientEmail == email {
			clientDetails = client
			break
		}
	}

	if clientDetails == nil {
		b.sendMessage(chatID, "❌ Клиент не найден на исходной панели")
		return
	}

	// Check if client already exists on destination panel
	destClients, err := toClient.GetClientTrafficsById(toInboundID)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка проверки целевой панели: %v", err))
		return
	}

	for _, client := range destClients {
		if clientEmail, ok := client["email"].(string); ok && clientEmail == email {
			b.sendMessage(chatID, "❌ Клиент с таким email уже существует на целевой панели")
			return
		}
	}

	// Prepare client data for destination panel (copy all except traffic fields)
	clientConfig := make(map[string]interface{})
	for k, v := range clientDetails {
		if k != "up" && k != "down" && k != "total" {
			clientConfig[k] = v
		}
	}

	// Add client to destination panel
	err = toClient.AddClient(toInboundID, clientConfig)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка добавления клиента на целевую панель: %v", err))
		return
	}

	// Delete client from source panel
	err = fromClient.DeleteClient(fromInboundID, email)
	if err != nil {
		// Rollback: try to delete from destination if source deletion failed
		if rollbackErr := toClient.DeleteClient(toInboundID, email); rollbackErr != nil {
			b.logger.Errorf("Rollback failed: unable to delete client %s from destination panel %s inbound %d: %v", email, toPanelName, toInboundID, rollbackErr)
		}
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка удаления клиента с исходной панели: %v. Перемещение отменено.", err))
		return
	}

	// Clear cache for this client
	b.clientCache.Delete(cacheKey)

	// Update admin message
	msg := fmt.Sprintf(
		"✅ <b>Клиент успешно перемещен!</b>\n\n"+
			"👤 Email: %s\n"+
			"🏠 Из: %s (Inbound ID: %d)\n"+
			"🏠 В: %s (Inbound ID: %d)",
		html.EscapeString(email),
		html.EscapeString(fromPanelName),
		fromInboundID,
		html.EscapeString(toPanelName),
		toInboundID,
	)

	editParams := &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      msg,
		ParseMode: "HTML",
	}

	if _, err := b.bot.EditMessageText(context.Background(), editParams); err != nil {
		b.logger.Errorf("Failed to edit message with move result: %v", err)
		b.sendMessage(chatID, "❌ Ошибка при обновлении сообщения")
	}

	// Notify client if they have TG ID
	if tgId, ok := client["tgId"]; ok && tgId != "" && tgId != "0" {
		if tgIDInt, err := strconv.ParseInt(tgId, 10, 64); err == nil {
			userMsg := fmt.Sprintf(
				"🔄 <b>Ваш аккаунт был перемещен</b>\n\n" +
					"Ваш VPN аккаунт был перемещен на новый сервер.\n" +
					"Используйте ту же ссылку для подключения - она автоматически обновится.",
			)
			b.sendMessage(tgIDInt, userMsg)
		}
	}

	b.logger.Infof("Client %s moved from %s:%d to %s:%d", email, fromPanelName, fromInboundID, toPanelName, toInboundID)
}
