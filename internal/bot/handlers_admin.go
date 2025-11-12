package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Admin handlers for administrative functions: messaging, terms, extensions, backups

// handleAdminMessageSend handles sending message from admin to client
func (b *Bot) handleAdminMessageSend(adminChatID int64, messageText string) {
	state, exists := b.getAdminMessageState(adminChatID)
	if !exists {
		b.sendMessage(adminChatID, "❌ Ошибка: состояние не найдено")
		if err := b.deleteUserState(adminChatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	// Parse client Telegram ID
	clientTgID, err := strconv.ParseInt(state.ClientTgID, 10, 64)
	if err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка: неверный Telegram ID клиента")
		if err := b.deleteUserState(adminChatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		if err := b.deleteAdminMessageState(adminChatID); err != nil {
			b.logger.Errorf("Failed to delete admin message state: %v", err)
		}
		return
	}

	// Create reply button for user
	replyButton := tu.InlineKeyboardButton("💬 Ответить").
		WithCallbackData("contact_admin")

	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{
			{replyButton},
		},
	}

	// Send message to client with reply button
	_, err = b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(clientTgID),
		Text:        fmt.Sprintf("📨 <b>Сообщение от администратора:</b>\n\n%s", messageText),
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})

	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Не удалось отправить сообщение клиенту %s: %v", state.ClientEmail, err))
	} else {
		b.sendMessage(adminChatID, fmt.Sprintf("✅ Сообщение отправлено клиенту %s", state.ClientEmail))
	}

	// Clear state
	if err := b.deleteUserState(adminChatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}
	if err := b.deleteAdminMessageState(adminChatID); err != nil {
		b.logger.Errorf("Failed to delete admin message state: %v", err)
	}
}

// handleContactAdmin initiates user messaging admin
func (b *Bot) handleContactAdmin(chatID int64, userID int64) {
	b.logger.Infof("User %d wants to contact admin", userID)

	// Get user info from Telegram
	tgUsername := ""
	userName := ""

	// Try to get from API (if registered)
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err == nil && clientInfo != nil {
		if email, ok := clientInfo["email"].(string); ok {
			userName = email
		}
	}

	// Store state
	if err := b.setUserMessageState(chatID, &UserMessageState{
		UserID:     userID,
		Username:   userName,
		TgUsername: tgUsername,
		Timestamp:  time.Now(),
	}); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}
	if err := b.setUserState(chatID, "awaiting_user_message"); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}

	b.sendMessage(chatID, "💬 Напишите ваше сообщение администратору:")
}

// handleUserMessageSend handles sending message from user to admins
func (b *Bot) handleUserMessageSend(chatID int64, userID int64, messageText string, from *telego.User) {
	state, exists := b.getUserMessageState(chatID)
	if !exists {
		b.sendMessage(chatID, "❌ Ошибка: состояние не найдено")
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	// Get username from message if not in state
	tgUsername := ""
	if from.Username != "" {
		tgUsername = "@" + from.Username
	}
	userName := state.Username
	if userName == "" {
		userName = from.FirstName
	}

	// Send message to all admins with reply button
	for _, adminID := range b.config.Telegram.AdminIDs {
		msg := fmt.Sprintf(
			"📨 <b>Сообщение от пользователя:</b>\n\n"+
				"👤 %s %s\n"+
				"🆔 ID: %d\n\n"+
				"💬 <i>%s</i>",
			userName,
			tgUsername,
			userID,
			html.EscapeString(messageText),
		)

		keyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("💬 Ответить").WithCallbackData(fmt.Sprintf("reply_%d", userID)),
			),
		)

		if _, err := b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), msg).
			WithReplyMarkup(keyboard).
			WithParseMode("HTML")); err != nil {
			b.logger.Errorf("Failed to send message to admin %d: %v", adminID, err)
		}
	}

	b.sendMessage(chatID, "✅ Ваше сообщение отправлено администратору")

	// Clear state
	if err := b.deleteUserState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}
	if err := b.deleteUserMessageState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user message state: %v", err)
	}
}

// handleUsage handles the /usage command
func (b *Bot) handleUsage(chatID int64, email string) {
	traffic, err := b.apiClient.GetClientTraffics(email)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Failed to get client traffic: %v", err))
		return
	}

	// Format usage message
	msg := fmt.Sprintf("📈 Usage for %s:\n\n", email)

	if up, ok := traffic["up"].(float64); ok {
		msg += fmt.Sprintf("⬆️ Upload: %.2f GB\n", up/1024/1024/1024)
	}
	if down, ok := traffic["down"].(float64); ok {
		msg += fmt.Sprintf("⬇️ Download: %.2f GB\n", down/1024/1024/1024)
	}
	if total, ok := traffic["total"].(float64); ok {
		msg += fmt.Sprintf("📊 Total: %.2f GB\n", total/1024/1024/1024)
	}

	b.sendMessage(chatID, msg)
}

// handleShowTerms shows terms and conditions
func (b *Bot) handleShowTerms(chatID int64, userID int64) {
	b.logger.Infof("Showing terms to user %d", userID)

	// Read terms from file
	terms, err := os.ReadFile("terms.txt")
	if err != nil {
		b.logger.Errorf("Failed to read terms.txt: %v", err)
		b.sendMessage(chatID, "❌ Ошибка загрузки условий. Обратитесь к администратору.")
		return
	}

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Принять").WithCallbackData("terms_accept"),
			tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData("terms_decline"),
		),
	)

	if _, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatID),
		Text:        string(terms),
		ParseMode:   "Markdown",
		ReplyMarkup: keyboard,
	}); err != nil {
		b.logger.Errorf("Failed to send terms to user %d: %v", chatID, err)
	}
}

// handleTermsAccept handles terms acceptance
func (b *Bot) handleTermsAccept(chatID int64, userID int64, messageID int, from *telego.User) {
	b.logger.Infof("User %d accepted terms", userID)

	// Update message
	if _, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      "✅ Вы приняли условия использования.\n\nТеперь можете приступить к регистрации.",
	}); err != nil {
		b.logger.Errorf("Failed to edit terms message for user %d: %v", chatID, err)
	}

	// Get user info
	userName := from.FirstName
	if from.LastName != "" {
		userName += " " + from.LastName
	}
	if userName == "" {
		userName = fmt.Sprintf("User_%d", userID)
	}
	tgUsername := from.Username

	// Start registration process
	b.handleRegistrationStart(chatID, userID, userName, tgUsername)
}

// handleTermsDecline handles terms decline
func (b *Bot) handleTermsDecline(chatID int64, messageID int) {
	b.logger.Infof("User %d declined terms", chatID)

	// Update message
	if _, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      "❌ Вы отклонили условия использования.\n\nБез принятия условий регистрация невозможна.\n\nВы можете ознакомиться с условиями заново в любое время.",
	}); err != nil {
		b.logger.Errorf("Failed to edit terms decline message for user %d: %v", chatID, err)
	}
}

// handleExtendSubscription handles subscription extension request
func (b *Bot) handleExtendSubscription(chatID int64, userID int64) {
	b.logger.Infof("User %d requested subscription extension", userID)

	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ У вас нет активной подписки.\n\nДля получения VPN используйте кнопку '📱 Получить VPN'")
		return
	}

	email := ""
	if e, ok := clientInfo["email"].(string); ok {
		email = e
	}

	// Check if user has unlimited subscription (expiryTime = 0)
	expiryTime := int64(0)
	if et, ok := clientInfo["expiryTime"].(float64); ok {
		expiryTime = int64(et)
	}

	if expiryTime == 0 {
		b.sendMessage(chatID, "✅ У вас безлимитная подписка!\n\n∞ Срок действия: бессрочно\n\nПродление не требуется.")
		b.logger.Infof("User %d has unlimited subscription, extension denied", userID)
		return
	}

	// Show duration selection keyboard with prices (no trial for renewals)
	keyboard := b.createDurationKeyboard(fmt.Sprintf("extend_%d", userID), false)

	msg := fmt.Sprintf(
		"🔄 <b>Продление подписки</b>\n\n"+
			"👤 Аккаунт: %s\n\n"+
			"Выберите срок продления:",
		html.EscapeString(email),
	)

	if _, err := b.bot.SendMessage(context.Background(), tu.Message(tu.ID(chatID), msg).
		WithReplyMarkup(keyboard).
		WithParseMode("HTML")); err != nil {
		b.logger.Errorf("Failed to send extension message to admin %d: %v", chatID, err)
	}
}

// handleExtensionRequest processes subscription extension request
func (b *Bot) handleExtensionRequest(userID int64, chatID int64, messageID int, duration int, tgUsername string) {
	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Ошибка: клиент не найден")
		return
	}

	email := ""
	userName := ""
	if e, ok := clientInfo["email"].(string); ok {
		email = e
	}

	// Use Telegram username if available, otherwise use email or fallback
	if tgUsername != "" {
		userName = tgUsername
	} else if email != "" {
		userName = email
	} else {
		userName = fmt.Sprintf("User_%d", userID)
	}

	// Format Telegram username for display
	tgUsernameStr := ""
	if tgUsername != "" {
		tgUsernameStr = fmt.Sprintf("\n💬 Telegram: @%s", tgUsername)
	}

	// Send request to all admins
	for _, adminID := range b.config.Telegram.AdminIDs {
		keyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("✅ Одобрить").WithCallbackData(fmt.Sprintf("approve_ext_%d_%d", userID, duration)),
				tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData(fmt.Sprintf("reject_ext_%d", userID)),
			),
		)

		adminMsg := fmt.Sprintf(
			"🔄 Запрос на продление подписки\n\n"+
				"👤 Пользователь: %s (ID: %d)%s\n"+
				"👤 Username: %s\n"+
				"📅 Продлить на: %d дней",
			userName,
			userID,
			tgUsernameStr,
			email,
			duration,
		)

		if _, err := b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), adminMsg).
			WithReplyMarkup(keyboard)); err != nil {
			b.logger.Errorf("Failed to send extension request to admin %d: %v", adminID, err)
		} else {
			b.logger.Infof("Sent extension request to admin %d", adminID)
		}
	}

	// Determine price based on duration
	var price int
	switch duration {
	case 30:
		price = b.config.Payment.Prices.OneMonth
	case 90:
		price = b.config.Payment.Prices.ThreeMonth
	case 180:
		price = b.config.Payment.Prices.SixMonth
	case 365:
		price = b.config.Payment.Prices.OneYear
	}

	// Update user's message with payment info
	b.editMessageText(chatID, messageID, fmt.Sprintf(
		"✅ Запрос на продление отправлен администраторам!\n\n"+
			"👤 Аккаунт: %s\n"+
			"📅 Срок: %d дней\n\n"+
			"💳 <b>Реквизиты для оплаты:</b>\n"+
			"🏦 Банк: %s\n"+
			"📱 Номер: %s\n"+
			"💰 Сумма: %d₽\n\n"+
			"✍️ В комментарии укажите свой username.\n\n"+
			"⏳ После оплаты дождитесь одобрения администратора...",
		html.EscapeString(email),
		duration,
		html.EscapeString(b.config.Payment.Bank),
		b.config.Payment.PhoneNumber,
		price,
	))

	b.logger.Infof("Extension request sent for user %d, email: %s, duration: %d days", userID, email, duration)
}

// handleExtensionApproval processes admin approval for subscription extension
func (b *Bot) handleExtensionApproval(userID int64, adminChatID int64, messageID int, duration int) {
	// Get user info from Telegram
	userName, tgUsername := b.getUserInfo(userID)

	// Find client by tgId
	foundClient, inboundID, email, err := b.findClientByTgID(userID)
	if err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка: клиент не найден")
		b.logger.Errorf("%v", err)
		return
	}

	// Parse raw JSON to preserve all fields
	rawJSON := foundClient["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка при обработке данных клиента")
		b.logger.Errorf("Failed to parse client JSON: %v", err)
		return
	}

	// Get current expiry time
	currentExpiry := int64(0)
	if et, ok := clientData["expiryTime"].(float64); ok {
		currentExpiry = int64(et)
	}

	// Calculate new expiry time: add extension to CURRENT expiry (or to now if expired)
	now := time.Now().UnixMilli()
	baseTime := currentExpiry
	if currentExpiry < now {
		// If subscription already expired, start from now
		baseTime = now
	}
	newExpiry := baseTime + (int64(duration) * 24 * 60 * 60 * 1000) // Add days in milliseconds

	b.logger.Infof("Extending subscription for %s from %s by %d days to %s",
		email,
		time.UnixMilli(currentExpiry).Format("2006-01-02 15:04:05"),
		duration,
		time.UnixMilli(newExpiry).Format("2006-01-02 15:04:05"))

	// Update only expiryTime field
	clientData["expiryTime"] = newExpiry

	// Fix numeric fields for proper type conversion
	b.clientService.FixNumericFields(clientData)

	// Update client via API
	err = b.apiClient.UpdateClient(inboundID, email, clientData)
	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при обновлении подписки: %v", err))
		b.logger.Errorf("Failed to update client subscription: %v", err)
		return
	}

	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(email)
	if err != nil {
		b.logger.Warnf("Failed to get subscription link: %v", err)
		subLink = "Не удалось получить ссылку"
	}

	// Calculate time remaining (days and hours)
	daysUntilExpiry, hoursUntilExpiry := b.calculateTimeRemaining(newExpiry)

	oldExpiry := time.UnixMilli(currentExpiry).Format("02.01.2006 15:04")
	newExpiryFormatted := time.UnixMilli(newExpiry).Format("02.01.2006 15:04")

	// Notify user
	instructionsText := b.getInstructionsText()

	// Get client info for device limit
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	limitDevicesText := ""
	if err == nil {
		if limitIP, ok := clientInfo["limitIp"].(float64); ok && int(limitIP) > 0 {
			limitDevicesText = fmt.Sprintf("\n📱 Лимит устройств: %d", int(limitIP))
		}
	}

	userMsg := fmt.Sprintf(
		"✅ <b>Ваша подписка продлена!</b>\n\n"+
			"👤 Аккаунт: %s\n"+
			"📅 Продлено на: %d дней\n"+
			"⏰ Истекает: %s\n"+
			"📅 Осталось: %d дней %d часов%s\n\n"+
			"🔗 <b>Ваша VPN конфигурация:</b>\n"+
			"<blockquote expandable>%s</blockquote>%s",
		html.EscapeString(email),
		duration,
		newExpiryFormatted,
		daysUntilExpiry,
		hoursUntilExpiry,
		limitDevicesText,
		html.EscapeString(subLink),
		instructionsText,
	)
	b.sendMessage(userID, userMsg)

	// Update admin message
	tgUsernameStr := ""
	if tgUsername != "" {
		tgUsernameStr = fmt.Sprintf(" (%s)", tgUsername)
	}

	adminMsg := fmt.Sprintf(
		"✅ <b>Продление ОДОБРЕНО</b>\n\n"+
			"👤 Пользователь: %s%s\n"+
			"👤 Username: %s\n"+
			"⏰ Было до: %s\n"+
			"📅 Продлено: +%d дней\n"+
			"⏰ Теперь до: %s",
		html.EscapeString(userName),
		tgUsernameStr,
		html.EscapeString(email),
		oldExpiry,
		duration,
		newExpiryFormatted,
	)
	b.editMessageText(adminChatID, messageID, adminMsg)

	b.logger.Infof("Subscription extended for user %d, email: %s, added: %d days, expires: %s",
		userID, email, duration, newExpiryFormatted)
}

// handleExtensionRejection processes admin rejection for subscription extension
func (b *Bot) handleExtensionRejection(userID int64, adminChatID int64, messageID int) {
	// Get user info from Telegram
	userName, tgUsername := b.getUserInfo(userID)

	// Get client info for logging
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	email := ""
	if err == nil {
		if e, ok := clientInfo["email"].(string); ok {
			email = e
		}
	}

	// Notify user
	userMsg := "❌ К сожалению, ваш запрос на продление подписки был отклонен администратором.\n\n" +
		"Пожалуйста, обратитесь к администратору для уточнения деталей."
	b.sendMessage(userID, userMsg)

	// Update admin message
	tgUsernameStr := ""
	if tgUsername != "" {
		tgUsernameStr = fmt.Sprintf(" (%s)", tgUsername)
	}

	adminMsg := fmt.Sprintf(
		"❌ <b>Продление ОТКЛОНЕНО</b>\n\n"+
			"👤 Пользователь: %s%s\n"+
			"👤 Username: %s",
		html.EscapeString(userName),
		tgUsernameStr,
		html.EscapeString(email),
	)
	b.editMessageText(adminChatID, messageID, adminMsg)

	b.logger.Infof("Extension rejected for user %d, email: %s", userID, email)
}

// handleBackupRequest handles manual backup request from admin
func (b *Bot) handleBackupRequest(chatID int64) {
	b.logger.Infof("Manual backup requested by admin %d", chatID)

	b.sendMessage(chatID, "⏳ Создаю бэкап базы данных...")

	// Download backup from panel
	backup, err := b.apiClient.GetDatabaseBackup()
	if err != nil {
		b.logger.Errorf("Failed to download backup: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка создания бэкапа: %v", err))
		return
	}

	// Send backup to requesting admin
	filename := fmt.Sprintf("x-ui_%s.db", time.Now().Format("2006-01-02_15-04"))
	reader := &namedBytesReader{
		Reader: strings.NewReader(string(backup)),
		name:   filename,
	}

	_, err = b.bot.SendDocument(context.Background(), &telego.SendDocumentParams{
		ChatID: tu.ID(chatID),
		Document: telego.InputFile{
			File: reader,
		},
		Caption:   fmt.Sprintf("📦 <b>Database Backup</b>\n\n🕐 Time: %s\n💾 Size: %.2f MB", time.Now().Format("2006-01-02 15:04:05"), float64(len(backup))/1024/1024),
		ParseMode: "HTML",
	})

	if err != nil {
		b.logger.Errorf("Failed to send backup to admin %d: %v", chatID, err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка отправки бэкапа: %v", err))
	} else {
		b.logger.Infof("Manual backup sent to admin %d", chatID)
	}
}

// handleForecast handles the /forecast command for traffic forecasting
func (b *Bot) handleForecast(chatID int64, isAdmin bool) {
	if !isAdmin {
		b.sendMessage(chatID, "⛔ У вас нет прав для просмотра прогноза трафика")
		return
	}

	// Get forecast data
	forecast, err := b.forecastService.CalculateForecast()
	if err != nil {
		b.logger.Errorf("Failed to calculate forecast for admin %d: %v", chatID, err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка расчета прогноза: %v", err))
		return
	}

	// Format and send forecast message
	message := b.forecastService.FormatForecastMessage(forecast)

	// Create keyboard with refresh button
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("🔄 Обновить").WithCallbackData("forecast_refresh"),
		),
	)

	b.sendMessageWithInlineKeyboard(chatID, message, keyboard)
}
