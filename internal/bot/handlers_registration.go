package bot

import (
	"context"
	"fmt"
	"html"
	"strings"
	"time"
	"unicode/utf8"

	tu "github.com/mymmrac/telego/telegoutil"
)

// Registration handlers for user registration process

// handleRegistrationStart initiates the registration process
func (b *Bot) handleRegistrationStart(chatID int64, userID int64, userName string, tgUsername string) {
	b.logger.Infof("Registration started by user %d", userID)

	// Check if user already has pending request
	if req, exists := b.getRegistrationRequest(userID); exists && req.Status == "pending" {
		b.sendMessage(chatID, "⏳ У вас уже есть активная заявка на регистрацию. Дождитесь ответа администратора.")
		return
	}

	// Create new registration request
	if err := b.setRegistrationRequest(userID, &RegistrationRequest{
		UserID:     userID,
		Username:   userName,
		TgUsername: tgUsername,
		Status:     "input_email",
		Timestamp:  time.Now(),
	}); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения заявки")
		return
	}

	if err := b.setUserState(chatID, "awaiting_email"); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}
	b.sendMessage(chatID, "📝 Регистрация нового клиента\n\n🔹 Шаг 1/2: Введите желаемый username:")
}

// handleRegistrationEmail processes email input
func (b *Bot) handleRegistrationEmail(chatID int64, userID int64, email string) {
	req, exists := b.getRegistrationRequest(userID)

	if !exists {
		b.sendMessage(chatID, "❌ Ошибка: регистрация не найдена. Начните заново.")
		return
	}

	// Validate username - check if not empty and length
	email = strings.TrimSpace(email)
	if email == "" || strings.Contains(strings.ToLower(email), "зарегистрироваться") {
		b.sendMessage(chatID, "❌ Username не может быть пустым.\n\nВведите корректный username:")
		return
	}

	// Validate username length (3-32 characters, count actual characters not bytes)
	usernameLength := utf8.RuneCountInString(email)
	if usernameLength < 3 {
		b.sendMessage(chatID, "❌ Username слишком короткий. Минимум 3 символа.\n\nВведите другой username:")
		return
	}
	if usernameLength > 32 {
		b.sendMessage(chatID, "❌ Username слишком длинный. Максимум 32 символа.\n\nВведите другой username:")
		return
	}

	req.Email = email
	req.Status = "input_duration"
	if err := b.setRegistrationRequest(userID, req); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения заявки")
		return
	}
	if err := b.setUserState(chatID, "awaiting_duration"); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}

	// Check if user has had previous subscriptions - trial only for first purchase
	isFirstPurchase := true
	_, err := b.apiClient.GetClientByTgID(userID)
	if err == nil {
		// User already exists - not first purchase
		isFirstPurchase = false
	}

	keyboard := b.createDurationKeyboard("reg_duration", isFirstPurchase)

	msg := fmt.Sprintf("✅ Username: %s\n\n🔹 Шаг 2/2: Выберите срок действия:", email)
	if _, err := b.bot.SendMessage(context.Background(), tu.Message(tu.ID(chatID), msg).WithReplyMarkup(keyboard)); err != nil {
		b.logger.Errorf("Failed to send duration selection to user %d: %v", chatID, err)
	}
}

// handleRegistrationDuration processes duration selection
func (b *Bot) handleRegistrationDuration(userID int64, chatID int64, duration int) {
	req, exists := b.getRegistrationRequest(userID)
	if !exists {
		b.sendMessage(chatID, "❌ Ошибка: регистрация не найдена")
		return
	}

	req.Duration = duration
	req.Status = "pending"
	if err := b.setRegistrationRequest(userID, req); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения заявки")
		return
	}

	if err := b.deleteUserState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}

	// Send request to admins
	b.sendRegistrationRequestToAdmins(req)

	// Determine price based on duration
	var price int
	isTrial := (duration == b.config.Payment.TrialDays && b.config.Payment.TrialDays > 0)

	if isTrial {
		price = 0
	} else {
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
	}

	var paymentMsg string
	if isTrial {
		paymentMsg = fmt.Sprintf(
			"✅ Заявка на пробный период отправлена!\n\n"+
				"🎁 <b>Пробный период: %d дня БЕСПЛАТНО</b>\n\n"+
				"⏳ Ожидайте подтверждения от администратора.\n\n"+
				"<i>Оплата не требуется. После активации вы получите доступ к VPN на %d дня.</i>",
			duration,
			duration,
		)
	} else {
		paymentMsg = fmt.Sprintf(
			"✅ Заявка отправлена!\n\n"+
				"⏳ Ожидайте подтверждения от администратора.\n\n"+
				"💳 <b>Реквизиты для оплаты:</b>\n"+
				"🏦 Банк: %s\n"+
				"📱 Номер: %s\n"+
				"💰 Сумма: %d₽\n\n"+
				"✍️ В комментарии укажите свой username.\n\n"+
				"<i>После оплаты дождитесь подтверждения от администратора.</i>",
			html.EscapeString(b.config.Payment.Bank),
			b.config.Payment.PhoneNumber,
			price,
		)
	}

	b.sendMessage(chatID, paymentMsg)
}

// sendRegistrationRequestToAdmins sends registration request to all admins
func (b *Bot) sendRegistrationRequestToAdmins(req *RegistrationRequest) {
	b.logger.Debugf("Sending registration to admins - UserID: %d, TgUsername: '%s'", req.UserID, req.TgUsername)

	// Format Telegram username
	tgUsernameStr := ""
	if req.TgUsername != "" {
		tgUsernameStr = fmt.Sprintf("\n💬 Telegram: @%s", req.TgUsername)
	}

	// Check if this is a trial subscription
	isTrial := (req.Duration == b.config.Payment.TrialDays && b.config.Payment.TrialDays > 0)
	trialTag := ""
	if isTrial {
		trialTag = " 🎁 ПРОБНЫЙ ПЕРИОД"
	}

	// Determine correct plural form
	durationText := fmt.Sprintf("%d дней", req.Duration)
	if req.Duration <= 4 {
		durationText = fmt.Sprintf("%d дня", req.Duration)
	}

	msg := fmt.Sprintf(
		"📝 Новая заявка на регистрацию%s\n\n"+
			"👤 Пользователь: %s (ID: %d)%s\n"+
			"👤 Username: %s\n"+
			"📅 Срок: %s\n"+
			"🕐 Время: %s",
		trialTag,
		req.Username,
		req.UserID,
		tgUsernameStr,
		req.Email,
		durationText,
		req.Timestamp.Format("02.01.2006 15:04"),
	)

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Одобрить").WithCallbackData(fmt.Sprintf("approve_reg_%d", req.UserID)),
			tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData(fmt.Sprintf("reject_reg_%d", req.UserID)),
		),
	)

	for _, adminID := range b.config.Telegram.AdminIDs {
		if _, err := b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), msg).WithReplyMarkup(keyboard)); err != nil {
			b.logger.Errorf("Failed to send registration request to admin %d: %v", adminID, err)
		} else {
			b.logger.Infof("Sent registration request to admin %d", adminID)
		}
	}
}

// handleRegistrationDecision handles admin's approval or rejection
func (b *Bot) handleRegistrationDecision(requestUserID int64, adminChatID int64, messageID int, isApprove bool) {
	req, exists := b.getRegistrationRequest(requestUserID)

	if !exists {
		b.sendMessage(adminChatID, "❌ Заявка не найдена")
		return
	}

	if isApprove {
		// Create client via API
		err := b.createClientForRequest(req)
		if err != nil {
			b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при создании клиента: %v", err))
			b.logger.Errorf("Failed to create client for request: %v", err)
			return
		}

		req.Status = "approved"

		// Get subscription link
		subLink, err := b.apiClient.GetClientLink(req.Email)
		if err != nil {
			b.logger.Warnf("Failed to get subscription link: %v", err)
			subLink = "Не удалось получить ссылку. Обратитесь к администратору."
		}

		// Notify user with subscription link
		instructionsText := b.getInstructionsText()

		limitDevicesText := ""
		if b.config.Panel.LimitIP > 0 {
			limitDevicesText = fmt.Sprintf("\n📱 Лимит устройств: %d", b.config.Panel.LimitIP)
		}

		userMsg := fmt.Sprintf(
			"✅ <b>Ваша заявка одобрена!</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"📅 Срок: %d дней%s\n\n"+
				"🔗 <b>Ваша VPN конфигурация:</b>\n"+
				"<blockquote expandable>%s</blockquote>\n\n"+
				"Скопируйте эту ссылку и добавьте её в ваше VPN приложение.%s",
			html.EscapeString(req.Email),
			req.Duration,
			limitDevicesText,
			html.EscapeString(subLink),
			instructionsText,
		)
		b.sendMessage(req.UserID, userMsg)

		// Show main menu to the user after successful registration
		time.Sleep(1 * time.Second) // Small delay for better UX
		b.handleStart(req.UserID, req.Username, false)

		// Update admin message
		tgUsernameStr := ""
		if req.TgUsername != "" {
			tgUsernameStr = fmt.Sprintf(" (@%s)", req.TgUsername)
		}

		adminMsg := fmt.Sprintf(
			"✅ <b>Заявка ОДОБРЕНА</b>\n\n"+
				"👤 Пользователь: %s%s\n"+
				"👤 Username: %s\n"+
				"📅 Срок: %d дней",
			html.EscapeString(req.Username),
			tgUsernameStr,
			html.EscapeString(req.Email),
			req.Duration,
		)
		b.editMessageText(adminChatID, messageID, adminMsg)

		b.logger.Infof("Registration approved for user %d, email: %s", requestUserID, req.Email)
	} else {
		req.Status = "rejected"

		// Notify user
		userMsg := "❌ К сожалению, ваша заявка была отклонена администратором."
		b.sendMessage(req.UserID, userMsg)

		// Update admin message
		tgUsernameStr := ""
		if req.TgUsername != "" {
			tgUsernameStr = fmt.Sprintf(" (@%s)", req.TgUsername)
		}

		adminMsg := fmt.Sprintf(
			"❌ <b>Заявка ОТКЛОНЕНА</b>\n\n"+
				"👤 Пользователь: %s%s\n"+
				"👤 Username: %s\n"+
				"📅 Срок: %d дней",
			html.EscapeString(req.Username),
			tgUsernameStr,
			html.EscapeString(req.Email),
			req.Duration,
		)
		b.editMessageText(adminChatID, messageID, adminMsg)

		b.logger.Infof("Registration rejected for user %d, email: %s", requestUserID, req.Email)
	}

	// Clean up old requests and states
	if err := b.deleteRegistrationRequest(requestUserID); err != nil {
		b.logger.Errorf("Failed to delete registration request: %v", err)
	}

	// Clear FSM state for user
	if err := b.deleteUserState(req.UserID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}
}

// handleRegistrationPanelSelection handles the final step of registration - creating client on selected panel/inbound
func (b *Bot) handleRegistrationPanelSelection(requestUserID int64, adminChatID int64, messageID int, panelName string, inboundID int) {
	req, exists := b.getRegistrationRequest(requestUserID)
	if !exists {
		b.sendMessage(adminChatID, "❌ Заявка не найдена")
		return
	}

	// Update request with selected panel and inbound
	req.PanelName = panelName
	req.InboundID = inboundID
	req.Status = "approved"

	if err := b.setRegistrationRequest(requestUserID, req); err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка сохранения заявки: %v", err))
		b.logger.Errorf("Failed to update registration request: %v", err)
		return
	}

	// Create client via API
	err := b.createClientForRequest(req)
	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при создании клиента: %v", err))
		b.logger.Errorf("Failed to create client for request: %v", err)
		return
	}

	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(req.Email)
	if err != nil {
		b.logger.Warnf("Failed to get subscription link: %v", err)
		subLink = "Не удалось получить ссылку. Обратитесь к администратору."
	}

	// Notify user with subscription link
	instructionsText := b.getInstructionsText()

	limitDevicesText := ""
	if b.config.Panel.LimitIP > 0 {
		limitDevicesText = fmt.Sprintf("\n📱 Лимит устройств: %d", b.config.Panel.LimitIP)
	}

	userMsg := fmt.Sprintf(
		"✅ <b>Ваша заявка одобрена!</b>\n\n"+
			"👤 Аккаунт: %s\n"+
			"📅 Срок: %d дней%s\n\n"+
			"🔗 <b>Ваша VPN конфигурация:</b>\n"+
			"<blockquote expandable>%s</blockquote>\n\n"+
			"Скопируйте эту ссылку и добавьте её в ваше VPN приложение.%s",
		html.EscapeString(req.Email),
		req.Duration,
		limitDevicesText,
		html.EscapeString(subLink),
		instructionsText,
	)
	b.sendMessage(req.UserID, userMsg)

	// Show main menu to the user after successful registration
	time.Sleep(1 * time.Second) // Small delay for better UX
	b.handleStart(req.UserID, req.Username, false)

	// Update admin message
	tgUsernameStr := ""
	if req.TgUsername != "" {
		tgUsernameStr = fmt.Sprintf(" (@%s)", req.TgUsername)
	}

	adminMsg := fmt.Sprintf(
		"✅ <b>Заявка ОДОБРЕНА</b>\n\n"+
			"👤 Пользователь: %s%s\n"+
			"👤 Username: %s\n"+
			"🏠 Панель: %s\n"+
			"📡 Inbound ID: %d\n"+
			"📅 Срок: %d дней",
		html.EscapeString(req.Username),
		tgUsernameStr,
		html.EscapeString(req.Email),
		html.EscapeString(panelName),
		inboundID,
		req.Duration,
	)
	b.editMessageText(adminChatID, messageID, adminMsg)

	b.logger.Infof("Registration approved for user %d, email: %s, panel: %s, inbound: %d", requestUserID, req.Email, panelName, inboundID)

	// Clean up old requests and states
	if err := b.deleteRegistrationRequest(requestUserID); err != nil {
		b.logger.Errorf("Failed to delete registration request: %v", err)
	}

	// Clear FSM state for user
	if err := b.deleteUserState(req.UserID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}
}
