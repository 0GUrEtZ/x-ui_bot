package bot

import (
	"context"
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
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован администратором.\n\nДля получения информации свяжитесь с администратором.")
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
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован администратором.\n\nДля получения информации свяжитесь с администратором.")
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
		if len(args) > 1 {
			email := args[1]
			b.handleUsage(chatID, email)
		} else {
			b.sendMessage(chatID, "❌ Использование: /usage <email>")
		}
	case constants.CmdClients:
		b.handleClients(chatID, isAdmin)
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

	// Check if client is blocked — block all non-admin actions (including chat)
	if !isAdmin {
		if b.isClientBlocked(userID) {
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован администратором.\n\nДля получения информации свяжитесь с администратором.")
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

	// Handle terms acceptance/decline (before block check)
	if data == constants.CbTermsAccept {
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
					email := client["email"]
					clientID := client["id"] // UUID for VMESS/VLESS

					// Delete the client using UUID
					err := b.apiClient.DeleteClient(context.Background(), inboundID, clientID)

					if err != nil {
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Ошибка удаления: %v", err),
							ShowAlert:       true,
						}); err != nil {
							b.logger.Errorf("Failed to answer delete error callback: %v", err)
						}
					} else {
						// Answer callback
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("🗑️ Клиент %s удалён", email),
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

	// Handle toggle_X_Y buttons
	if strings.HasPrefix(data, "toggle_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			inboundID, err1 := strconv.Atoi(parts[1])
			clientIndex, err2 := strconv.Atoi(parts[2])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if client, ok := b.getClientFromCacheCopy(cacheKey); ok {
					email := client["email"]
					enable := client["enable"]

					// Toggle the enable state
					var err error
					var resultMsg string
					if enable == "false" {
						err = b.clientService.EnableClient(inboundID, email, client)
						resultMsg = "✅ Клиент разблокирован"
					} else {
						err = b.clientService.DisableClient(inboundID, email, client)
						resultMsg = "🔒 Клиент заблокирован"
					}

					if err != nil {
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Ошибка: %v", err),
							ShowAlert:       true,
						}); err != nil {
							b.logger.Errorf("Failed to answer toggle error callback: %v", err)
						}
					} else {
						// Update enable status in cache immediately (safe update)
						var newEnable string
						if enable == "false" {
							newEnable = "true"
						} else {
							newEnable = "false"
						}
						b.updateClientField(cacheKey, "enable", newEnable)

						// Answer callback with text
						if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            resultMsg,
						}); err != nil {
							b.logger.Errorf("Failed to answer toggle success callback: %v", err)
						}
						// Refresh client menu with updated data
						b.handleClientMenu(chatID, messageID, inboundID, clientIndex, query.ID)
					}
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

	// Handle forecast_inbound_X callbacks
	if strings.HasPrefix(data, "forecast_inbound_") {
		b.handleForecastInboundCallback(chatID, query.ID, data)
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

	// 'client' already assigned via getClientFromCacheCopy or reloaded above
	email := client["email"]
	enable := client["enable"]
	tgId := client["tgId"]
	totalGB := client["totalGB"]
	expiryTime := client["expiryTime"]

	// Get client traffic stats
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
			percentage = int(math.Ceil((float64(total) / limitBytes) * 100))
		}

		trafficLimitStr = fmt.Sprintf(" / %.0f ГБ (%d%%)", limitGB, percentage)
	} else {
		trafficLimitStr = " (∞)"
	}

	// Status
	statusText := "🟢 Активен"
	if isExpired {
		statusText = "⛔ Истекла подписка"
	} else if enable == "false" {
		statusText = "🔴 Заблокирован"
	} else if isUnlimited {
		statusText = "💎 Безлимитная подписка"
	}

	// Build message
	msg := fmt.Sprintf(
		"👤 <b>%s</b>\n\n"+
			"📊 Статус: %s%s\n"+
			"📅 Подписка: %s\n\n"+
			"⬆️ Отдано: %s\n"+
			"⬇️ Получено: %s\n"+
			"📊 Всего: %s%s",
		html.EscapeString(email),
		statusText,
		tgUsernameStr,
		subscriptionStr,
		b.clientService.FormatBytes(up),
		b.clientService.FormatBytes(down),
		b.clientService.FormatBytes(total),
		trafficLimitStr,
	) // Build keyboard with actions
	var buttons [][]telego.InlineKeyboardButton

	// Toggle block/unblock button
	if enable == "false" {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("✅ Разблокировать").WithCallbackData(fmt.Sprintf("toggle_%d_%d", inboundID, clientIndex)),
		})
	} else {
		buttons = append(buttons, []telego.InlineKeyboardButton{
			tu.InlineKeyboardButton("🔒 Заблокировать").WithCallbackData(fmt.Sprintf("toggle_%d_%d", inboundID, clientIndex)),
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

// handleForecastInboundCallback handles forecast_inbound_X callback
func (b *Bot) handleForecastInboundCallback(chatID int64, callbackID string, data string) {
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

	// Answer callback to remove loading spinner
	if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: callbackID,
		Text:            fmt.Sprintf("📊 Прогноз для инбаунда #%d", inboundID),
	}); err != nil {
		b.logger.Errorf("Failed to answer forecast callback: %v", err)
	}

	// Show forecast for this inbound
	b.handleTrafficForecastInbound(chatID, inboundID)
}
