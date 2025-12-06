package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
	"time"

	"x-ui-bot/internal/bot/constants"
	kbd "x-ui-bot/internal/bot/keyboard"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
)

// handleMediaMessage handles media messages (photos, videos, documents, etc.)
func (b *Bot) handleMediaMessage(_ *th.Context, message telego.Message) error {
	chatID := message.Chat.ID
	userID := message.From.ID
	isAdmin := b.authMiddleware.IsAdmin(userID)

	b.logger.Infof("Media message from user ID: %d", userID)

	// Check rate limit (admins bypass automatically)
	if !isAdmin {
		if err := b.rateLimiter.Check(userID); err != nil {
			b.logger.Warnf("Rate limit exceeded for user ID: %d", userID)
			return nil
		}
	}

	// Check if client is blocked
	if !isAdmin {
		if b.isClientBlocked(userID) {
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован")
			return nil
		}
	}

	// Check if user is waiting for message to send to admin
	if state, exists := b.getUserState(chatID); exists {
		if state == "awaiting_user_message" {
			b.handleUserMediaSend(chatID, userID, &message, message.From)
			return nil
		}
		if state == "awaiting_admin_message" && isAdmin {
			b.handleAdminMediaSend(chatID, &message)
			return nil
		}
	}

	return nil
}

// handleCommand handles incoming commands
func (b *Bot) handleCommand(ctx *th.Context, message telego.Message) error {
	chatID := message.Chat.ID
	userID := message.From.ID
	isAdmin := b.authMiddleware.IsAdmin(userID)

	command, _, args := tu.ParseCommand(message.Text)

	b.logger.Infof("Command /%s from user ID: %d", command, userID)

	// Check rate limit (admins bypass automatically)
	if !isAdmin {
		if err := b.rateLimiter.Check(userID); err != nil {
			b.logger.Warnf("Rate limit exceeded for user ID: %d", userID)
			return nil // Silently ignore
		}
	}

	// Check if client is blocked (except for start, help, id commands and admins)
	if !isAdmin && command != constants.CmdStart && command != constants.CmdHelp && command != constants.CmdID {
		if b.isClientBlocked(userID) {
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован")
			return nil
		}
	}

	switch command {
	case constants.CmdStart:
		b.handleStart(chatID, message.From.FirstName, isAdmin)
	case constants.CmdHelp:
		b.handleHelp(chatID)
	case constants.CmdStatus:
		b.handleStatus(chatID, isAdmin)
	case constants.CmdID:
		b.handleID(chatID, message.From.ID)
	case constants.CmdUsage:
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		if len(args) > 1 {
			email := args[1]
			b.handleUsage(chatID, email)
		} else {
			b.sendMessage(chatID, "❌ Использование: /usage <email>")
		}
	case constants.CmdClients:
		b.handleClients(chatID, isAdmin)
	case constants.CmdForecast:
		b.handleForecast(chatID, isAdmin)
	default:
		// Check if it's a client action command: /client_enable_1_0 or /client_disable_1_0
		if strings.HasPrefix(command, constants.CbClientPrefix) && isAdmin {
			parts := strings.Split(command, "_")
			if len(parts) == 4 {
				action := parts[1] // enable or disable
				inboundID, err1 := strconv.Atoi(parts[2])
				clientIndex, err2 := strconv.Atoi(parts[3])

				if err1 == nil && err2 == nil {
					cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
					if client, ok := b.getClientFromCacheCopy(cacheKey); ok {
						email := client["email"]

						switch action {
						case "enable":
							err := b.clientService.EnableClient(inboundID, email, client)
							if err != nil {
								b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка: %v", err))
							} else {
								b.sendMessage(chatID, fmt.Sprintf("✅ Клиент %s разблокирован", email))
								b.handleClients(chatID, isAdmin)
							}
						case "disable":
							err := b.clientService.DisableClient(inboundID, email, client)
							if err != nil {
								b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка: %v", err))
							} else {
								b.sendMessage(chatID, fmt.Sprintf("🔒 Клиент %s заблокирован", email))
								b.handleClients(chatID, isAdmin)
							}
						}
					} else {
						b.sendMessage(chatID, "❌ Клиент не найден. Обновите список: /clients")
					}
					return nil
				}
			}
		}

		b.sendMessage(chatID, "❌ Неизвестная команда. Используйте /help для справки.")
	}

	return nil
}

// handleTextMessage handles text messages from keyboard buttons
func (b *Bot) handleTextMessage(ctx *th.Context, message telego.Message) error {
	// Handle media messages
	if message.Photo != nil || message.Video != nil || message.Document != nil || message.Audio != nil || message.Voice != nil {
		return b.handleMediaMessage(ctx, message)
	}

	// Skip if it's a command
	if strings.HasPrefix(message.Text, "/") {
		return nil
	}

	chatID := message.Chat.ID
	userID := message.From.ID
	isAdmin := b.authMiddleware.IsAdmin(userID)

	b.logger.Infof("Text message: '%s' by user ID: %d", message.Text, userID)

	// Check rate limit (admins bypass automatically)
	if !isAdmin {
		if err := b.rateLimiter.Check(userID); err != nil {
			b.logger.Warnf("Rate limit exceeded for user ID: %d", userID)
			return nil
		}
	}

	// Check message length (max 2000 chars for user messages)
	if len(message.Text) > 2000 {
		b.sendMessage(chatID, "❌ Сообщение слишком длинное. Максимум 2000 символов.")
		return nil
	}

	// Check if client is blocked — block all non-admin actions (including chat and registration)
	if !isAdmin {
		if b.isClientBlocked(userID) {
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован")
			// Clear any pending states for blocked user
			_ = b.deleteUserState(chatID)
			return nil
		}
	}

	// Check if user is in registration process
	if state, exists := b.getUserState(chatID); exists {
		switch state {
		case constants.StateAwaitingEmail:
			b.handleRegistrationEmail(chatID, userID, message.Text)
			return nil
		case constants.StateAwaitingNewEmail:
			b.handleNewEmailInput(chatID, userID, message.Text)
			return nil
		case constants.StateAwaitingAdminMessage:
			b.handleAdminMessageSend(chatID, message.Text)
			return nil
		case constants.StateAwaitingUserMessage:
			b.handleUserMessageSend(chatID, userID, message.Text, message.From)
			return nil
		case constants.StateAwaitingBroadcastMessage:
			b.handleBroadcastMessage(chatID, message.Text)
			return nil
		}
	}

	switch message.Text {
	case constants.BtnServerStatus:
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleStatus(chatID, isAdmin)
	case constants.BtnTrafficForecast:
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleTrafficForecast(chatID)
	case constants.BtnClientList:
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleClients(chatID, isAdmin)
	case constants.BtnBroadcast:
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleBroadcastStart(chatID)
	case constants.BtnBackupDB:
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleBackupRequest(chatID)
	default:
		// Handle buttons with emoji (encoding issues)
		if strings.Contains(message.Text, constants.BtnTerms) {
			b.handleShowTerms(chatID, userID)
		} else if strings.Contains(message.Text, constants.BtnMySubscription) || strings.Contains(message.Text, constants.BtnInstructions) {
			b.handleMySubscription(chatID, userID)
		} else if strings.Contains(message.Text, constants.BtnExtendSubscription) {
			b.handleExtendSubscription(chatID, userID)
		} else if strings.Contains(message.Text, constants.BtnSettings) {
			b.handleSettings(chatID, userID)
		} else if strings.Contains(message.Text, constants.BtnUpdateUsername) {
			b.handleUpdateUsername(chatID, userID)
		} else if strings.Contains(message.Text, constants.BtnBack) {
			// Return to main menu
			b.handleStart(chatID, message.From.FirstName, false)
		} else if strings.Contains(message.Text, constants.BtnContactAdmin) {
			b.handleContactAdmin(chatID, userID)
		}
	}

	return nil
}

// handleCallback handles callback queries
func (b *Bot) handleCallback(ctx *th.Context, query telego.CallbackQuery) error {
	data := query.Data
	userID := query.From.ID
	chatID := query.Message.GetChat().ID
	messageID := query.Message.GetMessageID()
	isAdmin := b.authMiddleware.IsAdmin(userID)

	b.logger.Infof("Callback from user %d: %s", userID, data)

	// Handle terms acceptance/decline
	if data == constants.CbTermsAccept {
		// Check if client is blocked before accepting terms
		if !isAdmin && b.isClientBlocked(userID) {
			if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "🔒 Ваш доступ заблокирован",
				ShowAlert:       true,
			}); err != nil {
				b.logger.Errorf("Failed to answer blocked user callback: %v", err)
			}
			return nil
		}

		b.handleTermsAccept(chatID, userID, messageID, &query.From)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "✅ Условия приняты",
		}); err != nil {
			b.logger.Errorf("Failed to answer terms accept callback: %v", err)
		}
		return nil
	}

	if data == constants.CbTermsDecline {
		b.handleTermsDecline(chatID, messageID)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ Регистрация отменена",
		}); err != nil {
			b.logger.Errorf("Failed to answer terms decline callback: %v", err)
		}
		return nil
	}

	// Handle instructions menu (before block check - available to all users)
	if data == constants.CbInstructionsMenu {
		b.handleInstructionsMenu(chatID, messageID)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		}); err != nil {
			b.logger.Errorf("Failed to answer instructions menu callback: %v", err)
		}
		return nil
	}

	// Handle instruction platform selection (before block check - available to all users)
	if strings.HasPrefix(data, constants.CbInstrPrefix) {
		platform := strings.TrimPrefix(data, constants.CbInstrPrefix)
		b.handleInstructionPlatform(chatID, userID, messageID, platform)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		}); err != nil {
			b.logger.Errorf("Failed to answer instruction platform callback: %v", err)
		}
		return nil
	}

	// Handle back to subscription (before block check - available to all users)
	if data == "back_to_subscription" {
		// Re-send subscription info
		clientInfo, err := b.apiClient.GetClientByTgID(context.Background(), userID)
		if err == nil {
			if email, ok := clientInfo["email"].(string); ok {
				// Delete old message and send new one with QR code
				if err := b.bot.DeleteMessage(context.Background(), &telego.DeleteMessageParams{
					ChatID:    tu.ID(chatID),
					MessageID: messageID,
				}); err != nil {
					b.logger.Errorf("Failed to delete message: %v", err)
				}
				if err := b.sendSubscriptionInfo(chatID, userID, email, "📱 Моя подписка"); err != nil {
					b.logger.Errorf("Failed to send subscription info: %v", err)
				}
			}
		}
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		}); err != nil {
			b.logger.Errorf("Failed to answer back to subscription callback: %v", err)
		}
		return nil
	}

	// Check if client is blocked — block all non-admin callbacks
	if !isAdmin {
		if b.isClientBlocked(userID) {
			if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "🔒 Ваш доступ заблокирован",
				ShowAlert:       true,
			}); err != nil {
				b.logger.Errorf("Failed to answer blocked user callback: %v", err)
			}
			return nil
		}
	}

	// Handle registration duration selection (non-admin can use)
	if strings.HasPrefix(data, constants.CbRegDurationPrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			duration, err := strconv.Atoi(parts[2])
			if err == nil {
				b.handleRegistrationDuration(userID, chatID, duration)
				if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            fmt.Sprintf("✅ Выбрано: %d дней", duration),
				}); err != nil {
					b.logger.Errorf("Failed to answer duration selection callback: %v", err)
				}
				return nil
			}
		}
	}

	// Handle subscription extension (non-admin can use)
	if strings.HasPrefix(data, constants.CbExtendPrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			requestUserID, err1 := strconv.ParseInt(parts[1], 10, 64)
			duration, err2 := strconv.Atoi(parts[2])
			if err1 == nil && err2 == nil && requestUserID == userID {
				// Get Telegram username from callback query
				tgUsername := query.From.Username
				b.handleExtensionRequest(userID, chatID, messageID, duration, tgUsername)
				if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            fmt.Sprintf("✅ Запрос на %d дней отправлен", duration),
				}); err != nil {
					b.logger.Errorf("Failed to answer extension request callback: %v", err)
				}
				return nil
			}
		}
	}

	// Handle extend subscription from notification (non-admin can use)
	if data == "extend_subscription" {
		b.handleExtensionMenu(chatID, userID, messageID)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		}); err != nil {
			b.logger.Errorf("Failed to answer extend subscription callback: %v", err)
		}
		return nil
	}

	// Handle contact admin (non-admin can use)
	if data == constants.CbContactAdmin {
		b.handleContactAdmin(chatID, userID)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "✅ Введите ваше сообщение",
		}); err != nil {
			b.logger.Errorf("Failed to answer contact admin callback: %v", err)
		}
		return nil
	}

	// Check if user is admin for other callbacks
	if !b.authMiddleware.IsAdmin(userID) {
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "⛔ У вас нет прав",
			ShowAlert:       true,
		}); err != nil {
			b.logger.Errorf("Failed to answer permission denied callback: %v", err)
		}
		return nil
	}

	// Handle registration approval/rejection
	if strings.HasPrefix(data, constants.CbApproveRegPrefix) || strings.HasPrefix(data, constants.CbRejectRegPrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			requestUserID, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				isApprove := strings.HasPrefix(data, constants.CbApproveRegPrefix)
				b.handleRegistrationDecision(requestUserID, chatID, messageID, isApprove)
				return nil
			}
		}
	}

	// Handle extension approval/rejection
	if strings.HasPrefix(data, constants.CbApproveExtPrefix) || strings.HasPrefix(data, constants.CbRejectExtPrefix) {
		parts := strings.Split(data, "_")
		if strings.HasPrefix(data, constants.CbApproveExtPrefix) && len(parts) == 4 {
			requestUserID, err1 := strconv.ParseInt(parts[2], 10, 64)
			duration, err2 := strconv.Atoi(parts[3])
			if err1 == nil && err2 == nil {
				b.handleExtensionApproval(requestUserID, chatID, messageID, duration)
				return nil
			}
		} else if strings.HasPrefix(data, constants.CbRejectExtPrefix) && len(parts) == 3 {
			requestUserID, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				b.handleExtensionRejection(requestUserID, chatID, messageID)
				return nil
			}
		}
	}

	// Handle client_X_Y buttons (show client actions menu)
	if strings.HasPrefix(data, constants.CbClientPrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			inboundID, err1 := strconv.Atoi(parts[1])
			clientIndex, err2 := strconv.Atoi(parts[2])

			if err1 == nil && err2 == nil {
				b.handleClientMenu(chatID, messageID, inboundID, clientIndex, query.ID)
				return nil
			}
		}
	}

	// Handle back_to_clients button
	if data == constants.CbBackToClients {
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		}); err != nil {
			b.logger.Errorf("Failed to answer back to clients callback: %v", err)
		}
		b.handleClients(chatID, true, messageID)
		return nil
	}

	// Handle delete_X_Y buttons
	if strings.HasPrefix(data, constants.CbDeletePrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			inboundID, err1 := strconv.Atoi(parts[1])
			clientIndex, err2 := strconv.Atoi(parts[2])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if client, ok := b.getClientFromCacheCopy(cacheKey); ok {
					email := client["email"]

					// Show confirmation dialog
					confirmMsg := fmt.Sprintf("❗ Вы уверены, что хотите удалить клиента?\n\n👤 Email: %s", email)
					keyboard := kbd.BuildConfirmDeleteKeyboard(inboundID, clientIndex)

					if _, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
						ChatID:      tu.ID(chatID),
						MessageID:   messageID,
						Text:        confirmMsg,
						ReplyMarkup: keyboard,
					}); err != nil {
						b.logger.Errorf("Failed to edit delete confirmation message: %v", err)
					}

					if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
						CallbackQueryID: query.ID,
					}); err != nil {
						b.logger.Errorf("Failed to answer delete confirmation callback: %v", err)
					}
					return nil
				}
			}
		}
	}

	if strings.HasPrefix(data, constants.CbConfirmDeletePrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			inboundID, err1 := strconv.Atoi(parts[2])
			clientIndex, err2 := strconv.Atoi(parts[3])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if client, ok := b.getClientFromCacheCopy(cacheKey); ok {
					tgIDStr := client["tgId"]
					email := client["email"]
					cleanEmail := stripInboundSuffix(email)

					// Delete from ALL inbounds where this user exists
					deletedCount := 0
					var deleteErrors []string

					// Get all inbounds
					inbounds, err := b.apiClient.GetInbounds(context.Background())
					if err != nil {
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Ошибка получения инбаундов: %v", err),
							ShowAlert:       true,
						}); err != nil {
							b.logger.Errorf("Failed to answer delete error callback: %v", err)
						}
						return nil
					}

					// Find and delete from all inbounds
					for _, inbound := range inbounds {
						ibID := int(inbound["id"].(float64))
						settingsStr := ""
						if settings, ok := inbound["settings"].(string); ok {
							settingsStr = settings
						}

						clients, err := b.clientService.ParseClients(settingsStr)
						if err != nil {
							continue
						}

						// Find client with matching tgId
						for _, c := range clients {
							if c["tgId"] == tgIDStr {
								clientID := c["id"] // UUID for VMESS/VLESS
								err := b.apiClient.DeleteClient(context.Background(), ibID, clientID)
								if err != nil {
									deleteErrors = append(deleteErrors, fmt.Sprintf("inbound %d: %v", ibID, err))
									b.logger.Errorf("Failed to delete client from inbound %d: %v", ibID, err)
								} else {
									deletedCount++
									b.logger.Infof("Deleted client %s from inbound %d", c["email"], ibID)
								}
								break
							}
						}
					}

					// Report result
					if deletedCount == 0 {
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Не удалось удалить клиента: %s", strings.Join(deleteErrors, "; ")),
							ShowAlert:       true,
						}); err != nil {
							b.logger.Errorf("Failed to answer delete error callback: %v", err)
						}
					} else {
						resultText := fmt.Sprintf("🗑️ Клиент %s удалён из %d инбаундов", cleanEmail, deletedCount)
						if len(deleteErrors) > 0 {
							resultText += fmt.Sprintf("\n\nОшибки: %s", strings.Join(deleteErrors, "; "))
						}

						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            resultText,
						}); err != nil {
							b.logger.Errorf("Failed to answer delete success callback: %v", err)
						}
						// Refresh client list
						b.handleClients(chatID, true, messageID)
					}
					return nil
				}
			}
		}
	}

	if strings.HasPrefix(data, constants.CbCancelDeletePrefix) {
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			inboundID, err1 := strconv.Atoi(parts[2])
			clientIndex, err2 := strconv.Atoi(parts[3])

			if err1 == nil && err2 == nil {
				if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            "❌ Удаление отменено",
				}); err != nil {
					b.logger.Errorf("Failed to answer cancel delete callback: %v", err)
				}
				// Return to client menu
				b.handleClientMenu(chatID, messageID, inboundID, clientIndex, query.ID)
				return nil
			}
		}
	}

	if strings.HasPrefix(data, "msg_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			inboundID, err1 := strconv.Atoi(parts[1])
			clientIndex, err2 := strconv.Atoi(parts[2])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if client, ok := b.getClientFromCacheCopy(cacheKey); ok {
					email := client["email"]
					tgId := client["tgId"]

					if tgId != "" && tgId != "0" {
						// Store admin chat ID and client info for message sending
						if err := b.setAdminMessageState(chatID, &AdminMessageState{
							ClientEmail: email,
							ClientTgID:  tgId,
							InboundID:   inboundID,
							ClientIndex: clientIndex,
							Timestamp:   time.Now(),
						}); err != nil {
							b.logger.Errorf("Failed to set admin message state: %v", err)
							return nil
						}
						if err := b.setUserState(chatID, "awaiting_admin_message"); err != nil {
							b.logger.Errorf("Failed to set user state: %v", err)
							return nil
						}

						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
						}); err != nil { // Ask admin to type message
							b.logger.Errorf("Failed to answer message client callback: %v", err)
						}
						msg := fmt.Sprintf("💬 Отправка сообщения клиенту %s\n\nВведите текст сообщения:", email)
						b.sendMessage(chatID, msg)
					} else {
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            "❌ У клиента нет привязанного Telegram ID",
							ShowAlert:       true,
						}); err != nil {
							b.logger.Errorf("Failed to answer no tg id callback: %v", err)
						}
					}
					return nil
				}
			}
		}
	}

	// Handle reply_X button (admin replying to user message)
	if strings.HasPrefix(data, "reply_") {
		userIDStr := strings.TrimPrefix(data, "reply_")
		replyToUserID, err := strconv.ParseInt(userIDStr, 10, 64)
		if err == nil {
			// Store state for admin reply
			if err := b.setAdminMessageState(chatID, &AdminMessageState{
				ClientTgID: userIDStr,
				Timestamp:  time.Now(),
			}); err != nil {
				b.logger.Errorf("Failed to set admin message state: %v", err)
				return nil
			}
			if err := b.setUserState(chatID, "awaiting_admin_message"); err != nil {
				b.logger.Errorf("Failed to set user state: %v", err)
				return nil
			}

			if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
			}); err != nil {
				b.logger.Errorf("Failed to answer reply callback: %v", err)
			}

			b.sendMessage(chatID, fmt.Sprintf("💬 Введите ответ пользователю (ID: %d):", replyToUserID))
			return nil
		}
	}

	// Handle toggle_X_Y buttons - toggle across ALL inbounds
	if strings.HasPrefix(data, "toggle_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			inboundID, err1 := strconv.Atoi(parts[1])
			clientIndex, err2 := strconv.Atoi(parts[2])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if client, ok := b.getClientFromCacheCopy(cacheKey); ok {
					tgIDStr := client["tgId"]

					// Determine target state: if ANY inbound is enabled, we'll disable all; otherwise enable all
					shouldEnable := true

					// Find all clients with same tgId
					inbounds, err := b.apiClient.GetInbounds(context.Background())
					if err != nil {
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Ошибка получения инбаундов: %v", err),
							ShowAlert:       true,
						}); err != nil {
							b.logger.Errorf("Failed to answer toggle error callback: %v", err)
						}
						return nil
					}

					// First pass: check if any is enabled
					for _, inbound := range inbounds {
						settingsStr := ""
						if settings, ok := inbound["settings"].(string); ok {
							settingsStr = settings
						}

						clients, err := b.clientService.ParseClients(settingsStr)
						if err != nil {
							continue
						}

						for _, c := range clients {
							if c["tgId"] == tgIDStr && tgIDStr != "" && tgIDStr != "0" {
								if c["enable"] == "true" {
									shouldEnable = false // Found enabled instance, so we'll disable all
									break
								}
							}
						}
						if !shouldEnable {
							break
						}
					}

					// Second pass: toggle all instances
					toggledCount := 0
					var toggleErrors []string

					for _, inbound := range inbounds {
						ibID := int(inbound["id"].(float64))
						settingsStr := ""
						if settings, ok := inbound["settings"].(string); ok {
							settingsStr = settings
						}

						clients, err := b.clientService.ParseClients(settingsStr)
						if err != nil {
							continue
						}

						for idx, c := range clients {
							if c["tgId"] == tgIDStr && tgIDStr != "" && tgIDStr != "0" {
								// Parse raw JSON
								rawJSON := c["_raw_json"]
								var clientData map[string]interface{}
								if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
									toggleErrors = append(toggleErrors, fmt.Sprintf("inbound %d: parse error", ibID))
									continue
								}

								var err error
								if shouldEnable {
									err = b.clientService.EnableClient(ibID, c["email"], c)
								} else {
									err = b.clientService.DisableClient(ibID, c["email"], c)
								}

								if err != nil {
									toggleErrors = append(toggleErrors, fmt.Sprintf("inbound %d: %v", ibID, err))
									b.logger.Errorf("Failed to toggle client in inbound %d: %v", ibID, err)
								} else {
									toggledCount++
									b.logger.Infof("Toggled client %s in inbound %d (enable: %v)", c["email"], ibID, shouldEnable)

									// Update cache
									ck := fmt.Sprintf("%d_%d", ibID, idx)
									if shouldEnable {
										b.updateClientField(ck, "enable", "true")
									} else {
										b.updateClientField(ck, "enable", "false")
									}
								}
								break
							}
						}
					}

					// Report result
					var resultMsg string
					if toggledCount == 0 {
						resultMsg = fmt.Sprintf("❌ Не удалось изменить статус: %s", strings.Join(toggleErrors, "; "))
					} else {
						if shouldEnable {
							resultMsg = fmt.Sprintf("✅ Разблокировано в %d инбаундах", toggledCount)
						} else {
							resultMsg = fmt.Sprintf("🔒 Заблокировано в %d инбаундах", toggledCount)
						}
						if len(toggleErrors) > 0 {
							resultMsg += fmt.Sprintf("\nОшибки: %s", strings.Join(toggleErrors, "; "))
						}
					}

					if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
						CallbackQueryID: query.ID,
						Text:            resultMsg,
					}); err != nil {
						b.logger.Errorf("Failed to answer toggle success callback: %v", err)
					}

					// Refresh client menu with updated data
					b.handleClientMenu(chatID, messageID, inboundID, clientIndex, query.ID)
					return nil
				}
			}
		}
	}

	// Handle broadcast confirmation/cancellation
	if data == constants.CbBroadcastConfirm {
		b.handleBroadcastConfirm(chatID, messageID)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "📢 Отправка рассылки...",
		}); err != nil {
			b.logger.Errorf("Failed to answer callback query for broadcast confirm: %v", err)
		}
		return nil
	}

	if data == constants.CbBroadcastCancel {
		b.handleBroadcastCancel(chatID, messageID)
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "❌ Отменено",
		}); err != nil {
			b.logger.Errorf("Failed to answer callback query for broadcast cancel: %v", err)
		}
		return nil
	}

	// Handle forecast callbacks
	if data == constants.CbForecastTotal {
		b.handleForecastTotalCallback(chatID, messageID, query.ID)
		return nil
	}
	if strings.HasPrefix(data, constants.CbForecastInboundPrefix) {
		b.handleForecastInboundCallback(chatID, messageID, query.ID, data)
		return nil
	}

	// Default callback response
	if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "Обработка...",
	}); err != nil {
		b.logger.Errorf("Failed to answer callback query: %v", err)
	}

	return nil
}

// handleClientMenu shows actions menu for a specific client
func (b *Bot) handleClientMenu(chatID int64, messageID int, inboundID int, clientIndex int, queryID string) {
	cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
	client, ok := b.getClientFromCacheCopy(cacheKey)

	// If not in cache, reload from API
	if !ok {
		inbounds, err := b.apiClient.GetInbounds(context.Background())
		if err != nil {
			if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: queryID,
				Text:            "❌ Ошибка загрузки данных",
				ShowAlert:       true,
			}); err != nil {
				b.logger.Errorf("Failed to answer callback query for data loading error: %v", err)
			}
			return
		}

		// Find the specific inbound and client
		for _, inbound := range inbounds {
			if id, ok := inbound["id"].(float64); ok && int(id) == inboundID {
				c, err := b.extractClientFromInbound(inbound, clientIndex)
				if err == nil {
					// Cache it for future use
					b.storeClientToCache(cacheKey, c)
					// Ensure the local 'client' variable has the loaded data
					client = c
					break
				}
			}
		}

		if !ok {
			if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: queryID,
				Text:            "❌ Клиент не найден",
				ShowAlert:       true,
			}); err != nil {
				b.logger.Errorf("Failed to answer callback query for client not found: %v", err)
			}
			return
		}
	}

	// Get info from clicked client
	email := client["email"]
	cleanEmail := stripInboundSuffix(email)
	tgId := client["tgId"]
	totalGB := client["totalGB"]
	expiryTime := client["expiryTime"]

	// Find ALL clients with same tgId across all inbounds
	type InboundClientInfo struct {
		InboundID   int
		InboundName string
		ClientIndex int
		Email       string
		Enable      bool
		Traffic     int64
	}

	var allClientInstances []InboundClientInfo

	inbounds, err := b.apiClient.GetInbounds(context.Background())
	if err == nil {
		for _, inbound := range inbounds {
			ibID := int(inbound["id"].(float64))
			ibName := ""
			if remark, ok := inbound["remark"].(string); ok && remark != "" {
				ibName = remark
			} else {
				ibName = fmt.Sprintf("Inbound %d", ibID)
			}

			settingsStr := ""
			if settings, ok := inbound["settings"].(string); ok {
				settingsStr = settings
			}

			clients, err := b.clientService.ParseClients(settingsStr)
			if err != nil {
				continue
			}

			for idx, c := range clients {
				if c["tgId"] == tgId && tgId != "" && tgId != "0" {
					// Found client in this inbound
					isEnabled := c["enable"] == "true"

					// Get traffic for this specific instance
					var traffic int64
					trafficData, err := b.apiClient.GetClientTraffics(context.Background(), c["email"])
					if err == nil && trafficData != nil {
						var up, down int64
						if u, ok := trafficData["up"].(float64); ok {
							up = int64(u)
						}
						if d, ok := trafficData["down"].(float64); ok {
							down = int64(d)
						}
						traffic = up + down
					}

					allClientInstances = append(allClientInstances, InboundClientInfo{
						InboundID:   ibID,
						InboundName: ibName,
						ClientIndex: idx,
						Email:       c["email"],
						Enable:      isEnabled,
						Traffic:     traffic,
					})
				}
			}
		}
	}

	// Calculate total traffic: use highest traffic among inbounds (synced value)
	var totalTraffic int64
	for _, instance := range allClientInstances {
		if instance.Traffic > totalTraffic {
			totalTraffic = instance.Traffic
		}
	}

	// Get Telegram username
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

	// Check subscription status
	isExpired := false
	isUnlimited := false
	subscriptionStr := ""

	if expiryTime != "" && expiryTime != "0" {
		timestamp, err := strconv.ParseInt(expiryTime, 10, 64)
		if err == nil && timestamp > 0 {
			now := time.Now().UnixMilli()
			if timestamp < now {
				isExpired = true
				expireDate := time.UnixMilli(timestamp).Format("02.01.2006 15:04")
				subscriptionStr = fmt.Sprintf("⛔ Истекла: %s", expireDate)
			} else {
				// Calculate remaining time
				days, hours := b.calculateTimeRemaining(timestamp)
				expireDate := time.UnixMilli(timestamp).Format("02.01.2006 15:04")
				subscriptionStr = fmt.Sprintf("✅ До: %s (%d дн. %d ч.)", expireDate, days, hours)
			}
		}
	} else {
		isUnlimited = true
		subscriptionStr = "💎 Безлимитная (∞)"
	}

	// Traffic limit info
	trafficLimitStr := ""
	if totalGB != "" && totalGB != "0" {
		// totalGB is already in bytes
		limitBytes, _ := strconv.ParseFloat(totalGB, 64)
		limitGB := limitBytes / (1024 * 1024 * 1024)

		percentage := 0
		if limitBytes > 0 {
			percentage = int(math.Ceil((float64(totalTraffic) / limitBytes) * 100))
		}

		trafficLimitStr = fmt.Sprintf(" / %.0f ГБ (%d%%)", limitGB, percentage)
	} else {
		trafficLimitStr = " (∞)"
	}

	// Status - based on whether enabled in ANY inbound
	statusText := "🟢 Активен"
	anyEnabled := false
	for _, instance := range allClientInstances {
		if instance.Enable {
			anyEnabled = true
			break
		}
	}

	if isExpired {
		statusText = "⛔ Истекла подписка"
	} else if !anyEnabled {
		statusText = "🔴 Заблокирован"
	} else if isUnlimited {
		statusText = "💎 Безлимитная подписка"
	}

	// Build inbounds list
	inboundsListStr := ""
	if len(allClientInstances) > 0 {
		inboundsListStr = "\n\n🌐 <b>Инбаунды:</b>"
		for _, instance := range allClientInstances {
			statusEmoji := "🟢"
			if !instance.Enable {
				statusEmoji = "🔴"
			}
			inboundsListStr += fmt.Sprintf("\n%s %s - %s",
				statusEmoji,
				instance.InboundName,
				b.clientService.FormatBytes(instance.Traffic))
		}
	}

	// Build message
	msg := fmt.Sprintf(
		"👤 <b>%s</b>\n\n"+
			"📊 Статус: %s%s\n"+
			"📅 Подписка: %s\n\n"+
			"📊 Трафик: %s%s%s",
		html.EscapeString(cleanEmail),
		statusText,
		tgUsernameStr,
		subscriptionStr,
		b.clientService.FormatBytes(totalTraffic),
		trafficLimitStr,
		inboundsListStr,
	)

	// Build keyboard with actions
	var buttons [][]telego.InlineKeyboardButton

	// Toggle block/unblock button - will affect ALL inbounds
	if anyEnabled {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("🔒 Заблокировать везде").WithCallbackData(fmt.Sprintf("toggle_%d_%d", inboundID, clientIndex)),
		})
	} else {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("✅ Разблокировать везде").WithCallbackData(fmt.Sprintf("toggle_%d_%d", inboundID, clientIndex)),
		})
	}

	// Message button if tgId exists
	if tgId != "" && tgId != "0" {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("💬 Написать").WithCallbackData(fmt.Sprintf("msg_%d_%d", inboundID, clientIndex)),
		})
	}

	// Delete button
	buttons = append(buttons, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("🗑️ Удалить").WithCallbackData(fmt.Sprintf("delete_%d_%d", inboundID, clientIndex)),
	})

	// Back button
	buttons = append(buttons, []telego.InlineKeyboardButton{
		tu.InlineKeyboardButton("◀️ Назад").WithCallbackData("back_to_clients"),
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

	if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: queryID,
	}); err != nil {
		b.logger.Errorf("Failed to answer client menu callback: %v", err)
	}
}

// handleForecastTotalCallback shows total forecast (back from inbound view)
func (b *Bot) handleForecastTotalCallback(chatID int64, messageID int, callbackID string) {
	forecast, err := b.forecastService.CalculateTotalForecast()
	if err != nil {
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: callbackID,
			Text:            "❌ Ошибка расчета прогноза",
			ShowAlert:       true,
		}); err != nil {
			b.logger.Errorf("Failed to answer callback query: %v", err)
		}
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

	b.editMessage(chatID, messageID, message, keyboard)
	if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: callbackID}); err != nil {
		b.logger.Errorf("Failed to answer callback query: %v", err)
	}
}

// handleForecastInboundCallback handles forecast_inbound_X callback
func (b *Bot) handleForecastInboundCallback(chatID int64, messageID int, callbackID string, data string) {
	// Parse inbound ID from callback data: forecast_inbound_X
	parts := strings.Split(data, "_")
	if len(parts) != 3 {
		b.logger.Errorf("Invalid forecast callback data: %s", data)
		return
	}

	inboundID, err := strconv.Atoi(parts[2])
	if err != nil {
		b.logger.Errorf("Failed to parse inbound ID from callback: %v", err)
		return
	}

	forecast, err := b.forecastService.CalculateForecast(inboundID)
	if err != nil {
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: callbackID,
			Text:            fmt.Sprintf("❌ Ошибка: %v", err),
			ShowAlert:       true,
		}); err != nil {
			b.logger.Errorf("Failed to answer callback query: %v", err)
		}
		return
	}

	message := fmt.Sprintf("📊 <b>ПРОГНОЗ ДЛЯ INBOUND #%d</b>\n\n%s", inboundID, b.forecastService.FormatForecastMessage(forecast))

	// Back button
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔙 Назад к общему").WithCallbackData(constants.CbForecastTotal),
		),
	)

	b.editMessage(chatID, messageID, message, keyboard)
	if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{CallbackQueryID: callbackID}); err != nil {
		b.logger.Errorf("Failed to answer callback query: %v", err)
	}
}
