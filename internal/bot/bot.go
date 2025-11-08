package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"x-ui-bot/internal/config"
	"x-ui-bot/pkg/client"

	"math/rand"

	"github.com/google/uuid"
	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"
)

// generateRandomString generates a random string of lowercase letters and numbers
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// RegistrationRequest represents a user registration request
type RegistrationRequest struct {
	UserID    int64
	Username  string
	Email     string
	Duration  int // days
	Status    string
	Timestamp time.Time
}

// Bot represents the Telegram bot
type Bot struct {
	config            *config.Config
	apiClient         *client.APIClient
	bot               *telego.Bot
	handler           *th.BotHandler
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	isRunning         bool
	userStates        map[int64]string
	clientCache       sync.Map // Cache for client data: "inboundID_index" -> client map
	registrationReqs  map[int64]*RegistrationRequest
	registrationMutex sync.Mutex
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.Config, apiClient *client.APIClient) (*Bot, error) {
	bot, err := createTelegoBot(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.APIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	return &Bot{
		config:           cfg,
		apiClient:        apiClient,
		bot:              bot,
		userStates:       make(map[int64]string),
		registrationReqs: make(map[int64]*RegistrationRequest),
	}, nil
}

// createTelegoBot creates a telego bot with optional proxy settings
func createTelegoBot(token, proxy, apiServer string) (*telego.Bot, error) {
	if proxy != "" || apiServer != "" {
		// Handle proxy or custom API server
		return telego.NewBot(token)
	}
	return telego.NewBot(token)
}

// Start starts the bot
func (b *Bot) Start() error {
	// Login to API
	if err := b.apiClient.Login(); err != nil {
		return fmt.Errorf("failed to login to panel: %w", err)
	}

	// Set bot commands
	err := b.bot.SetMyCommands(context.Background(), &telego.SetMyCommandsParams{
		Commands: []telego.BotCommand{
			{Command: "start", Description: "Start the bot"},
			{Command: "help", Description: "Show help message"},
			{Command: "status", Description: "Show server status"},
			{Command: "id", Description: "Get your Telegram ID"},
			{Command: "usage", Description: "Get client usage statistics"},
		},
	})
	if err != nil {
		log.Printf("Failed to set bot commands: %v", err)
	}

	// Start message handling
	if !b.isRunning {
		go b.receiveMessages()
		b.isRunning = true
	}

	return nil
}

// Stop stops the bot
func (b *Bot) Stop() {
	if b.cancel != nil {
		b.cancel()
		b.wg.Wait()
	}
	if b.handler != nil {
		b.handler.Stop()
	}
	b.isRunning = false
}

// receiveMessages starts receiving and handling messages
func (b *Bot) receiveMessages() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	updates, _ := b.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()

		handler, _ := th.NewBotHandler(b.bot, updates)
		b.handler = handler

		// Handle commands
		handler.HandleMessage(b.handleCommand, th.AnyCommand())

		// Handle text messages (keyboard buttons)
		handler.HandleMessage(b.handleTextMessage, th.AnyMessage())

		// Handle callback queries
		handler.HandleCallbackQuery(b.handleCallback, th.AnyCallbackQueryWithMessage())

		handler.Start()
	}()
}

// handleCommand handles incoming commands
func (b *Bot) handleCommand(ctx *th.Context, message telego.Message) error {
	chatID := message.Chat.ID
	isAdmin := b.isAdmin(message.From.ID)

	command, _, args := tu.ParseCommand(message.Text)

	log.Printf("[INFO] Command /%s from user ID: %d", command, message.From.ID)

	switch command {
	case "start":
		b.handleStart(chatID, message.From.FirstName, isAdmin)
	case "help":
		b.handleHelp(chatID)
	case "status":
		b.handleStatus(chatID, isAdmin)
	case "id":
		b.handleID(chatID, message.From.ID)
	case "usage":
		if len(args) > 1 {
			email := args[1]
			b.handleUsage(chatID, email, isAdmin)
		} else {
			b.sendMessage(chatID, "❌ Использование: /usage &lt;email&gt;")
		}
	case "clients":
		b.handleClients(chatID, isAdmin)
	default:
		// Check if it's a client action command: /client_enable_1_0 or /client_disable_1_0
		if strings.HasPrefix(command, "client_") && isAdmin {
			parts := strings.Split(command, "_")
			if len(parts) == 4 {
				action := parts[1] // enable or disable
				inboundID, err1 := strconv.Atoi(parts[2])
				clientIndex, err2 := strconv.Atoi(parts[3])

				if err1 == nil && err2 == nil {
					cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
					if clientData, ok := b.clientCache.Load(cacheKey); ok {
						client := clientData.(map[string]string)
						email := client["email"]

						if action == "enable" {
							err := b.handleEnableClient(inboundID, email, client)
							if err != nil {
								b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка: %v", err))
							} else {
								b.sendMessage(chatID, fmt.Sprintf("✅ Клиент %s разблокирован", email))
								b.handleClients(chatID, isAdmin)
							}
						} else if action == "disable" {
							err := b.handleDisableClient(inboundID, email, client)
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
	// Skip if it's a command
	if strings.HasPrefix(message.Text, "/") {
		return nil
	}

	chatID := message.Chat.ID
	userID := message.From.ID
	isAdmin := b.isAdmin(userID)

	log.Printf("[INFO] Text message: '%s' by user ID: %d", message.Text, userID)

	// Check if user is in registration process
	if state, exists := b.userStates[chatID]; exists {
		switch state {
		case "awaiting_email":
			b.handleRegistrationEmail(chatID, userID, message.Text)
			return nil
		}
	}

	switch message.Text {
	case "📊 Статус сервера":
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleStatus(chatID, isAdmin)
	case "👥 Список клиентов":
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.handleClients(chatID, isAdmin)
	case "🔍 Найти клиента":
		if !isAdmin {
			b.sendMessage(chatID, "⛔ У вас нет прав")
			return nil
		}
		b.sendMessage(chatID, "🔍 Введите email клиента или используйте команду:\n/usage &lt;email&gt;")
	default:
		// Handle buttons with emoji (encoding issues)
		if strings.Contains(message.Text, "Зарегистрироваться") {
			// Use username if available, otherwise use firstName
			userName := message.From.Username
			if userName == "" {
				userName = message.From.FirstName
			}
			b.handleRegistrationStart(chatID, userID, userName)
		} else if strings.Contains(message.Text, "Получить VPN") {
			b.handleGetSubscriptionLink(chatID, userID)
		} else if strings.Contains(message.Text, "Статус подписки") {
			b.handleSubscriptionStatus(chatID, userID)
		} else if strings.Contains(message.Text, "Продлить подписку") {
			b.handleExtendSubscription(chatID, userID)
		} else if strings.Contains(message.Text, "Помощь") {
			b.handleHelp(chatID)
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

	log.Printf("[INFO] Callback from user %d: %s", userID, data)

	// Handle registration duration selection (non-admin can use)
	if strings.HasPrefix(data, "reg_duration_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			duration, err := strconv.Atoi(parts[2])
			if err == nil {
				b.handleRegistrationDuration(userID, chatID, duration)
				b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            fmt.Sprintf("✅ Выбрано: %d дней", duration),
				})
				return nil
			}
		}
	}

	// Handle subscription extension (non-admin can use)
	if strings.HasPrefix(data, "extend_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			requestUserID, err1 := strconv.ParseInt(parts[1], 10, 64)
			duration, err2 := strconv.Atoi(parts[2])
			if err1 == nil && err2 == nil && requestUserID == userID {
				b.handleExtensionRequest(userID, chatID, messageID, duration)
				b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            fmt.Sprintf("✅ Запрос на %d дней отправлен", duration),
				})
				return nil
			}
		}
	}

	// Check if user is admin for other callbacks
	if !b.isAdmin(userID) {
		b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "⛔ У вас нет прав",
			ShowAlert:       true,
		})
		return nil
	}

	// Handle registration approval/rejection
	if strings.HasPrefix(data, "approve_reg_") || strings.HasPrefix(data, "reject_reg_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			requestUserID, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				isApprove := strings.HasPrefix(data, "approve_reg_")
				b.handleRegistrationDecision(requestUserID, chatID, messageID, isApprove)
				return nil
			}
		}
	}

	// Handle extension approval/rejection
	if strings.HasPrefix(data, "approve_ext_") || strings.HasPrefix(data, "reject_ext_") {
		parts := strings.Split(data, "_")
		if strings.HasPrefix(data, "approve_ext_") && len(parts) == 4 {
			requestUserID, err1 := strconv.ParseInt(parts[2], 10, 64)
			duration, err2 := strconv.Atoi(parts[3])
			if err1 == nil && err2 == nil {
				b.handleExtensionApproval(requestUserID, chatID, messageID, duration)
				return nil
			}
		} else if strings.HasPrefix(data, "reject_ext_") && len(parts) == 3 {
			requestUserID, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil {
				b.handleExtensionRejection(requestUserID, chatID, messageID)
				return nil
			}
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
				if clientData, ok := b.clientCache.Load(cacheKey); ok {
					client := clientData.(map[string]string)
					email := client["email"]
					enable := client["enable"]

					// Toggle the enable state
					var err error
					var resultMsg string
					if enable == "false" {
						err = b.handleEnableClient(inboundID, email, client)
						resultMsg = "✅ Клиент разблокирован"
					} else {
						err = b.handleDisableClient(inboundID, email, client)
						resultMsg = "🔒 Клиент заблокирован"
					}

					if err != nil {
						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Ошибка: %v", err),
							ShowAlert:       true,
						})
					} else {
						// Answer callback
						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            resultMsg,
						})
						// Refresh client list
						b.handleClients(chatID, true, messageID)
					}
					return nil
				}
			}
		}
	}

	// Default callback response
	b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: query.ID,
		Text:            "Обработка...",
	})

	return nil
}

// handleStart handles the /start command
func (b *Bot) handleStart(chatID int64, firstName string, isAdmin bool) {
	log.Printf("[INFO] User %s (ID: %d) started bot", firstName, chatID)

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
				tu.KeyboardButton("🔍 Найти клиента"),
				tu.KeyboardButton("ℹ️ Помощь"),
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

			// Calculate days remaining (round up to include partial days)
			daysRemaining := 0
			if expiryTime > 0 {
				remainingMs := expiryTime - time.Now().UnixMilli()
				if remainingMs > 0 {
					// Round up: if there are any hours left, count as a full day
					daysRemaining = int((remainingMs + (1000 * 60 * 60 * 24) - 1) / (1000 * 60 * 60 * 24))
				}
			}

			statusIcon := "✅"
			statusText := fmt.Sprintf("%d дней", daysRemaining)
			if expiryTime == 0 {
				// Unlimited subscription
				statusIcon = "♾️"
				statusText = "Безлимитная"
			} else if daysRemaining <= 0 {
				statusIcon = "❌"
				statusText = "Истекла"
			} else if daysRemaining <= 7 {
				statusIcon = "⚠️"
			}

			msg += fmt.Sprintf("� Аккаунт: %s\n", html.EscapeString(email))
			msg += fmt.Sprintf("%s Подписка: %s\n\n", statusIcon, statusText)
			msg += "Выберите действие:"

			// Build keyboard based on subscription type
			var keyboard *telego.ReplyKeyboardMarkup
			if expiryTime == 0 {
				// Unlimited subscription - no extend button
				keyboard = tu.Keyboard(
					tu.KeyboardRow(
						tu.KeyboardButton("📱 Получить VPN"),
					),
					tu.KeyboardRow(
						tu.KeyboardButton("📊 Статус подписки"),
					),
				).WithResizeKeyboard().WithIsPersistent()
			} else {
				// Limited subscription - show extend button
				keyboard = tu.Keyboard(
					tu.KeyboardRow(
						tu.KeyboardButton("📱 Получить VPN"),
					),
					tu.KeyboardRow(
						tu.KeyboardButton("📊 Статус подписки"),
						tu.KeyboardButton("🔄 Продлить подписку"),
					),
				).WithResizeKeyboard().WithIsPersistent()
			}

			b.sendMessageWithKeyboard(chatID, msg, keyboard)
		} else {
			// User is not registered - show registration menu
			msg += "Выберите действие:"

			keyboard := tu.Keyboard(
				tu.KeyboardRow(
					tu.KeyboardButton("📝 Зарегистрироваться"),
				),
			).WithResizeKeyboard().WithIsPersistent()

			b.sendMessageWithKeyboard(chatID, msg, keyboard)
		}
	}
}

// handleHelp handles the /help command
func (b *Bot) handleHelp(chatID int64) {
	log.Printf("[INFO] Help requested by user ID: %d", chatID)

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
	log.Printf("[INFO] ID request from user ID: %d", userID)
	msg := fmt.Sprintf("🆔 Ваш Telegram ID: <code>%d</code>", userID)
	b.sendMessage(chatID, msg)
}

// handleClients handles the /clients command - shows all clients with traffic stats
func (b *Bot) handleClients(chatID int64, isAdmin bool, messageID ...int) {
	if !isAdmin {
		b.sendMessage(chatID, "⛔ У вас нет прав для использования этой команды")
		return
	}

	log.Printf("[INFO] Clients list requested by user ID: %d", chatID)

	if len(messageID) == 0 {
		b.sendMessage(chatID, "⏳ Загружаю список клиентов...")
	}

	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		log.Printf("[ERROR] Failed to get inbounds: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка получения списка: %v", err))
		return
	}

	if len(inbounds) == 0 {
		b.sendMessage(chatID, "📭 Нет доступных inbound'ов")
		return
	}

	totalClients := 0
	msg := "👥 <b>Список всех клиентов:</b>\n\n"

	for _, inbound := range inbounds {
		remark := "Unknown"
		if r, ok := inbound["remark"].(string); ok {
			remark = r
		}

		protocol := "unknown"
		if p, ok := inbound["protocol"].(string); ok {
			protocol = p
		}

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

		clients := b.parseClients(settingsStr)

		if len(clients) == 0 {
			continue
		}

		// Get real traffic data for all clients in this inbound
		trafficData := make(map[string]map[string]interface{})
		if inboundID > 0 {
			traffics, err := b.apiClient.GetClientTrafficsById(inboundID)
			if err == nil {
				for _, t := range traffics {
					if email, ok := t["email"].(string); ok {
						trafficData[email] = t
					}
				}
			} else {
				log.Printf("[WARN] Failed to get traffic for inbound %d: %v", inboundID, err)
			}
		}

		msg += fmt.Sprintf("📡 <b>%s</b> (%s)\n", remark, protocol)

		for i, client := range clients {
			totalClients++

			email := client["email"]
			totalGB := client["totalGB"]
			expiryTime := client["expiryTime"]
			enable := client["enable"]

			// Get real traffic stats
			var up, down, total int64
			if traffic, exists := trafficData[email]; exists {
				if u, ok := traffic["up"].(float64); ok {
					up = int64(u)
				}
				if d, ok := traffic["down"].(float64); ok {
					down = int64(d)
				}
				total = up + down
			}

			status := "🟢"
			if enable == "false" {
				status = "🔴"
			}

			// Format client info message
			msg += fmt.Sprintf("\n%d. %s <b>%s</b>\n", totalClients, status, html.EscapeString(email))
			msg += fmt.Sprintf("   ⬆️ %s | ⬇️ %s | 📊 %s",
				b.formatBytes(up),
				b.formatBytes(down),
				b.formatBytes(total))

			// Show limit and percentage if set
			if totalGB != "0" && totalGB != "" {
				limitBytes, _ := strconv.ParseFloat(totalGB, 64)
				limitBytes = limitBytes * 1024 * 1024 * 1024 // Convert GB to bytes
				percentage := 0.0
				if limitBytes > 0 {
					percentage = (float64(total) / limitBytes) * 100
				}

				emoji := "🟢"
				if percentage >= 90 {
					emoji = "🔴"
				} else if percentage >= 70 {
					emoji = "🟡"
				}

				msg += fmt.Sprintf(" / %s GB %s (%.1f%%)", totalGB, emoji, percentage)
			}

			if expiryTime != "0" && expiryTime != "" {
				expTime := b.formatTimestamp(expiryTime)

				// Calculate days remaining
				timestamp, _ := strconv.ParseInt(expiryTime, 10, 64)
				if timestamp > 0 {
					now := time.Now().Unix() * 1000
					daysLeft := (timestamp - now) / (1000 * 60 * 60 * 24)

					if daysLeft < 0 {
						msg += fmt.Sprintf("\n   📅 Истёк: %s ⛔", expTime)
					} else if daysLeft <= 3 {
						msg += fmt.Sprintf("\n   📅 До: %s 🔴 (%d дн.)", expTime, daysLeft)
					} else if daysLeft <= 7 {
						msg += fmt.Sprintf("\n   📅 До: %s 🟡 (%d дн.)", expTime, daysLeft)
					} else {
						msg += fmt.Sprintf("\n   📅 До: %s (%d дн.)", expTime, daysLeft)
					}
				} else {
					msg += fmt.Sprintf("\n   📅 До: %s", expTime)
				}
			}

			// Add block/unblock button command hint
			actionCmd := "enable"
			actionEmoji := "✅"
			if enable != "false" {
				actionCmd = "disable"
				actionEmoji = "🔒"
			}
			msg += fmt.Sprintf("\n   /client_%s_%d_%d %s\n", actionCmd, inboundID, i, actionEmoji)

			// Store client info for callback handling
			b.clientCache.Store(fmt.Sprintf("%d_%d", inboundID, i), client)
		}

		msg += "\n"
	}

	// Build inline keyboards with buttons for each inbound
	// Group by inbound for better organization
	for _, inbound := range inbounds {
		remark := "Unknown"
		if r, ok := inbound["remark"].(string); ok {
			remark = r
		}

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

		clients := b.parseClients(settingsStr)
		if len(clients) == 0 {
			continue
		}

		// Create inline keyboard buttons for this inbound's clients
		var buttons [][]telego.InlineKeyboardButton
		for i, client := range clients {
			email := client["email"]
			enable := client["enable"]
			totalGB := client["totalGB"]

			// Get real traffic stats for this client
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
				log.Printf("[DEBUG] Client %s traffic: up=%d, down=%d, total=%d", email, up, down, total)
			} else {
				log.Printf("[DEBUG] No traffic data for client %s: %v", email, err)
			}

			// Status icon
			statusIcon := "🟢"
			if enable == "false" {
				statusIcon = "🔴"
			}

			// Traffic percentage if limit is set
			trafficInfo := ""
			if totalGB != "0" && totalGB != "" {
				limitBytes, _ := strconv.ParseFloat(totalGB, 64)
				limitBytes = limitBytes * 1024 * 1024 * 1024 // Convert GB to bytes
				percentage := 0.0
				if limitBytes > 0 {
					percentage = (float64(total) / limitBytes) * 100
				}

				percentEmoji := "🟢"
				if percentage >= 90 {
					percentEmoji = "🔴"
				} else if percentage >= 70 {
					percentEmoji = "🟡"
				}

				trafficInfo = fmt.Sprintf(" %s %.0f%%", percentEmoji, percentage)
			} else {
				// Show total traffic in GB if no limit
				totalGBFloat := float64(total) / (1024 * 1024 * 1024)
				trafficInfo = fmt.Sprintf(" 📊 %.2f ГБ", totalGBFloat)
			}

			// Toggle button text
			actionText := "✅"
			if enable != "false" {
				actionText = "🔒"
			}

			buttonText := fmt.Sprintf("%s %s%s %s", statusIcon, email, trafficInfo, actionText)
			button := tu.InlineKeyboardButton(buttonText).
				WithCallbackData(fmt.Sprintf("toggle_%d_%d", inboundID, i))

			buttons = append(buttons, []telego.InlineKeyboardButton{button})
		}

		keyboard := &telego.InlineKeyboardMarkup{InlineKeyboard: buttons}
		inboundMsg := fmt.Sprintf("📡 <b>%s</b>\n\nВыберите клиента для управления:", remark)

		if len(messageID) > 0 {
			b.editMessage(chatID, messageID[0], inboundMsg, keyboard)
		} else {
			b.sendMessageWithInlineKeyboard(chatID, inboundMsg, keyboard)
		}
	}

	log.Printf("[INFO] Sent %d clients to user ID: %d", totalClients, chatID)
}

// handleUsage handles the /usage command
func (b *Bot) handleUsage(chatID int64, email string, isAdmin bool) {
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

// isAdmin checks if a user is an admin
func (b *Bot) isAdmin(userID int64) bool {
	for _, adminID := range b.config.Telegram.AdminIDs {
		if adminID == userID {
			return true
		}
	}
	return false
}

// sendMessage sends a text message
func (b *Bot) sendMessage(chatID int64, text string) {
	_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:    tu.ID(chatID),
		Text:      text,
		ParseMode: "HTML",
	})
	if err != nil {
		log.Printf("[ERROR] Failed to send message to %d: %v", chatID, err)
	}
}

// sendMessageWithKeyboard sends a message with keyboard
func (b *Bot) sendMessageWithKeyboard(chatID int64, text string, keyboard *telego.ReplyKeyboardMarkup) {
	_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatID),
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		log.Printf("[ERROR] Failed to send message with keyboard to %d: %v", chatID, err)
	}
}

// sendMessageWithInlineKeyboard sends a message with inline keyboard
func (b *Bot) sendMessageWithInlineKeyboard(chatID int64, text string, keyboard *telego.InlineKeyboardMarkup) {
	_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatID),
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		log.Printf("[ERROR] Failed to send message with inline keyboard to %d: %v", chatID, err)
	}
}

// editMessage edits an existing message
func (b *Bot) editMessage(chatID int64, messageID int, text string, keyboard *telego.InlineKeyboardMarkup) {
	_, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		log.Printf("[ERROR] Failed to edit message %d in chat %d: %v", messageID, chatID, err)
	}
}

// parseClients parses clients from inbound settings JSON
func (b *Bot) parseClients(settingsStr string) []map[string]string {
	var clients []map[string]string

	if settingsStr == "" {
		return clients
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
		log.Printf("[ERROR] Failed to parse settings JSON: %v", err)
		return clients
	}

	// Get clients array
	clientsArray, ok := settings["clients"].([]interface{})
	if !ok {
		return clients
	}

	for _, c := range clientsArray {
		clientMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		client := make(map[string]string)

		// Store raw JSON for API updates
		clientJSON, _ := json.Marshal(clientMap)
		client["_raw_json"] = string(clientJSON)

		// Email
		if email, ok := clientMap["email"].(string); ok {
			client["email"] = email
		}

		// ID (uuid for vless/vmess, password for trojan)
		if id, ok := clientMap["id"].(string); ok {
			client["id"] = id
		}

		// Total traffic limit (in GB)
		if totalGB, ok := clientMap["totalGB"].(float64); ok {
			client["totalGB"] = fmt.Sprintf("%.0f", totalGB)
		} else {
			client["totalGB"] = "0"
		}

		// Expiry time
		if expiryTime, ok := clientMap["expiryTime"].(float64); ok {
			client["expiryTime"] = fmt.Sprintf("%.0f", expiryTime)
		} else {
			client["expiryTime"] = "0"
		}

		// Enable status
		if enable, ok := clientMap["enable"].(bool); ok {
			client["enable"] = fmt.Sprintf("%t", enable)
		} else {
			client["enable"] = "true"
		}

		// Traffic stats - default to 0
		client["up"] = "0"
		client["down"] = "0"
		client["total"] = "0"

		clients = append(clients, client)
	}

	return clients
}

// formatBytes formats bytes to human readable string
func (b *Bot) formatBytes(value interface{}) string {
	var bytes float64

	switch v := value.(type) {
	case string:
		if v == "" {
			return "0 B"
		}
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "0 B"
		}
		bytes = parsed
	case float64:
		bytes = v
	case int:
		bytes = float64(v)
	case int64:
		bytes = float64(v)
	default:
		return "0 B"
	}

	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.0f B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.2f %s", bytes/float64(div), units[exp])
}

// formatTimestamp formats Unix timestamp to readable date
func (b *Bot) formatTimestamp(value interface{}) string {
	var timestamp int64

	switch v := value.(type) {
	case string:
		if v == "" || v == "0" {
			return "∞"
		}
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return "∞"
		}
		timestamp = parsed
	case float64:
		timestamp = int64(v)
	case int64:
		timestamp = v
	case int:
		timestamp = int64(v)
	default:
		return "∞"
	}

	if timestamp == 0 {
		return "∞"
	}

	t := time.Unix(timestamp/1000, 0)
	return t.Format("02.01.2006 15:04")
}

// handleEnableClient enables a client
func (b *Bot) handleEnableClient(inboundID int, email string, client map[string]string) error {
	log.Printf("[INFO] Enabling client: %s (inbound: %d)", email, inboundID)

	// Parse raw JSON and update enable field
	rawJSON := client["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		return fmt.Errorf("failed to parse client data: %w", err)
	}

	// Update enable field
	clientData["enable"] = true

	// Fix numeric fields - convert float64 to int64 for timestamps
	b.fixNumericFields(clientData)

	clientID := client["id"]
	if clientID == "" {
		clientID = email
	}

	return b.apiClient.UpdateClient(inboundID, clientID, clientData)
}

// handleDisableClient disables a client
func (b *Bot) handleDisableClient(inboundID int, email string, client map[string]string) error {
	log.Printf("[INFO] Disabling client: %s (inbound: %d)", email, inboundID)

	// Parse raw JSON and update enable field
	rawJSON := client["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		return fmt.Errorf("failed to parse client data: %w", err)
	}

	// Update enable field
	clientData["enable"] = false

	// Fix numeric fields - convert float64 to int64 for timestamps
	b.fixNumericFields(clientData)

	clientID := client["id"]
	if clientID == "" {
		clientID = email
	}

	return b.apiClient.UpdateClient(inboundID, clientID, clientData)
}

// fixNumericFields converts float64 to int64 for specific fields to avoid scientific notation
func (b *Bot) fixNumericFields(data map[string]interface{}) {
	numericFields := []string{"expiryTime", "totalGB", "reset", "limitIp", "tgId", "created_at", "updated_at"}
	for _, field := range numericFields {
		if val, ok := data[field].(float64); ok {
			data[field] = int64(val)
		}
	}
}

// handleRegistrationStart starts the registration process
func (b *Bot) handleRegistrationStart(chatID int64, userID int64, userName string) {
	log.Printf("[INFO] Registration started by user %d", userID)

	// Check if user already has pending request
	b.registrationMutex.Lock()
	if req, exists := b.registrationReqs[userID]; exists && req.Status == "pending" {
		b.registrationMutex.Unlock()
		b.sendMessage(chatID, "⏳ У вас уже есть активная заявка на регистрацию. Дождитесь ответа администратора.")
		return
	}
	b.registrationMutex.Unlock()

	// Create new registration request
	b.registrationMutex.Lock()
	b.registrationReqs[userID] = &RegistrationRequest{
		UserID:    userID,
		Username:  userName,
		Status:    "input_email",
		Timestamp: time.Now(),
	}
	b.registrationMutex.Unlock()

	b.userStates[chatID] = "awaiting_email"
	b.sendMessage(chatID, "📝 Регистрация нового клиента\n\n🔹 Шаг 1/2: Введите желаемый email (логин):")
}

// handleRegistrationEmail processes email input
func (b *Bot) handleRegistrationEmail(chatID int64, userID int64, email string) {
	b.registrationMutex.Lock()
	req, exists := b.registrationReqs[userID]
	b.registrationMutex.Unlock()

	if !exists {
		b.sendMessage(chatID, "❌ Ошибка: регистрация не найдена. Начните заново.")
		return
	}

	// Validate email - check if not empty and doesn't contain button text
	email = strings.TrimSpace(email)
	if email == "" || strings.Contains(strings.ToLower(email), "зарегистрироваться") {
		b.sendMessage(chatID, "❌ Username не может быть пустым.\n\n📧 Введите корректный email (логин):")
		return
	}

	req.Email = email
	req.Status = "input_duration"
	b.userStates[chatID] = "awaiting_duration"

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("30 дней").WithCallbackData("reg_duration_30"),
			tu.InlineKeyboardButton("90 дней").WithCallbackData("reg_duration_90"),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("180 дней").WithCallbackData("reg_duration_180"),
			tu.InlineKeyboardButton("365 дней").WithCallbackData("reg_duration_365"),
		),
	)

	msg := fmt.Sprintf("✅ Username: %s\n\n🔹 Шаг 2/2: Выберите срок действия:", email)
	b.bot.SendMessage(context.Background(), tu.Message(tu.ID(chatID), msg).WithReplyMarkup(keyboard))
}

// handleRegistrationDuration processes duration selection
func (b *Bot) handleRegistrationDuration(userID int64, chatID int64, duration int) {
	b.registrationMutex.Lock()
	req, exists := b.registrationReqs[userID]
	if exists {
		req.Duration = duration
		req.Status = "pending"
	}
	b.registrationMutex.Unlock()

	if !exists {
		b.sendMessage(chatID, "❌ Ошибка: регистрация не найдена")
		return
	}

	delete(b.userStates, chatID)

	// Send request to admins
	b.sendRegistrationRequestToAdmins(req)

	b.sendMessage(chatID, "✅ Заявка отправлена!\n\n⏳ Ожидайте подтверждения от администратора.")
}

// sendRegistrationRequestToAdmins sends registration request to all admins
func (b *Bot) sendRegistrationRequestToAdmins(req *RegistrationRequest) {
	msg := fmt.Sprintf(
		"📝 <b>Новая заявка на регистрацию</b>\n\n"+
			"👤 Пользователь: %s (ID: %d)\n"+
			"📧 Username: %s\n"+
			"📅 Срок: %d дней\n"+
			"🕐 Время: %s",
		html.EscapeString(req.Username),
		req.UserID,
		html.EscapeString(req.Email),
		req.Duration,
		req.Timestamp.Format("02.01.2006 15:04"),
	)

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Одобрить").WithCallbackData(fmt.Sprintf("approve_reg_%d", req.UserID)),
			tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData(fmt.Sprintf("reject_reg_%d", req.UserID)),
		),
	)

	for _, adminID := range b.config.Telegram.AdminIDs {
		b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), msg).WithReplyMarkup(keyboard))
		log.Printf("[INFO] Sent registration request to admin %d", adminID)
	}
}

// handleRegistrationDecision handles admin's approval or rejection
func (b *Bot) handleRegistrationDecision(requestUserID int64, adminChatID int64, messageID int, isApprove bool) {
	b.registrationMutex.Lock()
	req, exists := b.registrationReqs[requestUserID]
	b.registrationMutex.Unlock()

	if !exists {
		b.sendMessage(adminChatID, "❌ Заявка не найдена")
		return
	}

	if isApprove {
		// Create client via API
		err := b.createClientForRequest(req)
		if err != nil {
			b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при создании клиента: %v", err))
			log.Printf("[ERROR] Failed to create client for request: %v", err)
			return
		}

		req.Status = "approved"

		// Get subscription link
		subLink, err := b.apiClient.GetClientLink(req.Email)
		if err != nil {
			log.Printf("[WARNING] Failed to get subscription link: %v", err)
			subLink = "Не удалось получить ссылку. Обратитесь к администратору."
		}

		// Notify user with subscription link
		userMsg := fmt.Sprintf(
			"✅ <b>Ваша заявка одобрена!</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"📅 Срок: %d дней\n\n"+
				"🔗 <b>Ваша VPN конфигурация:</b>\n"+
				"<code>%s</code>\n\n"+
				"Скопируйте эту ссылку и добавьте её в ваше VPN приложение.",
			html.EscapeString(req.Email),
			req.Duration,
			html.EscapeString(subLink),
		)
		b.sendMessage(req.UserID, userMsg)

		// Show main menu to the user after successful registration
		time.Sleep(1 * time.Second) // Small delay for better UX
		b.handleStart(req.UserID, req.Username, false)

		// Update admin message
		adminMsg := fmt.Sprintf(
			"✅ <b>Заявка ОДОБРЕНА</b>\n\n"+
				"👤 Пользователь: %s (ID: %d)\n"+
				"📧 Username: %s\n"+
				"📅 Срок: %d дней",
			html.EscapeString(req.Username),
			req.UserID,
			html.EscapeString(req.Email),
			req.Duration,
		)
		b.editMessageText(adminChatID, messageID, adminMsg)

		log.Printf("[INFO] Registration approved for user %d, email: %s", requestUserID, req.Email)
	} else {
		req.Status = "rejected"

		// Notify user
		userMsg := "❌ К сожалению, ваша заявка была отклонена администратором."
		b.sendMessage(req.UserID, userMsg)

		// Update admin message
		adminMsg := fmt.Sprintf(
			"❌ <b>Заявка ОТКЛОНЕНА</b>\n\n"+
				"👤 Пользователь: %s (ID: %d)\n"+
				"📧 Username: %s\n"+
				"📅 Срок: %d дней",
			html.EscapeString(req.Username),
			req.UserID,
			html.EscapeString(req.Email),
			req.Duration,
		)
		b.editMessageText(adminChatID, messageID, adminMsg)

		log.Printf("[INFO] Registration rejected for user %d, email: %s", requestUserID, req.Email)
	}

	// Clean up old requests and states
	b.registrationMutex.Lock()
	delete(b.registrationReqs, requestUserID)
	b.registrationMutex.Unlock()

	// Clear FSM state for user
	delete(b.userStates, requestUserID)
	log.Printf("[DEBUG] Cleared FSM state for user %d", requestUserID)
}

// createClientForRequest creates a new client based on registration request
func (b *Bot) createClientForRequest(req *RegistrationRequest) error {
	// Get first inbound to add client to
	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		return fmt.Errorf("failed to get inbounds: %w", err)
	}

	if len(inbounds) == 0 {
		return fmt.Errorf("no inbounds available")
	}

	// Use first inbound
	firstInbound := inbounds[0]
	inboundID := int(firstInbound["id"].(float64))

	// Get protocol
	protocol := ""
	if p, ok := firstInbound["protocol"].(string); ok {
		protocol = p
	}

	// Calculate expiry time
	expiryTime := time.Now().Add(time.Duration(req.Duration) * 24 * time.Hour).UnixMilli()

	// Generate subscription ID (16 lowercase alphanumeric characters)
	subID := generateRandomString(16)

	// Create client data based on protocol
	clientData := map[string]interface{}{
		"email":      req.Email,
		"enable":     true,
		"expiryTime": expiryTime,
		"totalGB":    0, // Unlimited
		"tgId":       req.UserID,
		"subId":      subID,
		"limitIp":    1,
		"comment":    "",
		"reset":      0,
	}

	// Add protocol-specific fields
	switch protocol {
	case "vmess":
		clientData["id"] = uuid.New().String()
		clientData["security"] = "auto"
	case "vless":
		clientData["id"] = uuid.New().String()
		clientData["flow"] = ""
	case "trojan":
		clientData["password"] = generateRandomString(10)
	case "shadowsocks":
		// Get method from inbound settings
		settingsStr, _ := firstInbound["settings"].(string)
		var settings map[string]interface{}
		method := "aes-256-gcm" // default
		if json.Unmarshal([]byte(settingsStr), &settings) == nil {
			if m, ok := settings["method"].(string); ok {
				method = m
			}
		}
		clientData["method"] = method
		clientData["password"] = generateRandomString(16)
	default:
		// Fallback to VLESS-like
		clientData["id"] = uuid.New().String()
		clientData["flow"] = ""
	}

	// Add client via API
	return b.apiClient.AddClient(inboundID, clientData)
}

// editMessageText edits a message text
func (b *Bot) editMessageText(chatID int64, messageID int, text string) {
	b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      text,
		ParseMode: "HTML",
	})
}

// handleGetSubscriptionLink sends subscription link to user
func (b *Bot) handleGetSubscriptionLink(chatID int64, userID int64) {
	log.Printf("[INFO] User %d requested subscription link", userID)

	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Вы не зарегистрированы.\n\nДля получения VPN необходимо зарегистрироваться.")
		// Start registration process
		b.handleRegistrationStart(chatID, userID, "")
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
		log.Printf("[ERROR] Failed to get subscription link: %v", err)
		b.sendMessage(chatID, "❌ Не удалось получить ссылку. Попробуйте позже или обратитесь к администратору.")
		return
	}

	msg := fmt.Sprintf(
		"� <b>Ваш VPN конфигурация:</b>\n\n"+
			"<code>%s</code>\n\n"+
			"� <b>Как подключиться:</b>\n"+
			"1. Скопируйте ссылку выше\n"+
			"2. Откройте VPN приложение:\n"+
			"   • V2rayNG (Android)\n"+
			"   • V2rayN (Windows)\n"+
			"   • Streisand (iOS)\n"+
			"   • Nekoray (Windows/Linux)\n"+
			"3. Используйте 'Импорт по ссылке' или 'Subscription'\n\n"+
			"✅ Подключение готово к использованию!",
		html.EscapeString(subLink),
	)

	b.sendMessage(chatID, msg)
	log.Printf("[INFO] Sent VPN config to user %d", userID)
}

// handleSubscriptionStatus shows detailed subscription status to user
func (b *Bot) handleSubscriptionStatus(chatID int64, userID int64) {
	log.Printf("[INFO] User %d requested subscription status", userID)

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

	expiryTime := int64(0)
	if et, ok := clientInfo["expiryTime"].(float64); ok {
		expiryTime = int64(et)
	}

	// Calculate days remaining (round up to include partial days)
	daysRemaining := 0
	hoursRemaining := 0
	if expiryTime > 0 {
		remainingMs := expiryTime - time.Now().UnixMilli()
		if remainingMs > 0 {
			totalHours := remainingMs / (1000 * 60 * 60)
			daysRemaining = int((remainingMs + (1000 * 60 * 60 * 24) - 1) / (1000 * 60 * 60 * 24))
			hoursRemaining = int(totalHours % 24)
		}
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
	var msg string

	if expiryTime == 0 {
		// Unlimited subscription
		statusIcon = "♾️"
		statusText = "Безлимитная"
		msg = fmt.Sprintf(
			"📊 <b>Статус подписки</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"%s Статус: %s\n"+
				"⏰ Истекает: ∞ (бессрочно)\n\n"+
				"📈 <b>Трафик:</b>\n"+
				"⬆️ Отправлено: %s\n"+
				"⬇️ Получено: %s\n"+
				"📊 Всего: %s",
			html.EscapeString(email),
			statusIcon,
			statusText,
			b.formatBytes(up),
			b.formatBytes(down),
			b.formatBytes(total),
		)
	} else {
		// Limited subscription
		if daysRemaining <= 0 {
			statusIcon = "❌"
			statusText = "Истекла"
		} else if daysRemaining <= 3 {
			statusIcon = "🔴"
			statusText = "Заканчивается"
		} else if daysRemaining <= 7 {
			statusIcon = "⚠️"
			statusText = "Скоро истечёт"
		}

		// Format expiry date
		expiryDate := time.UnixMilli(expiryTime).Format("02.01.2006 15:04")

		msg = fmt.Sprintf(
			"📊 <b>Статус подписки</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"%s Статус: %s\n"+
				"⏰ Истекает: %s\n"+
				"📅 Осталось: %d дней %d часов\n\n"+
				"📈 <b>Трафик:</b>\n"+
				"⬆️ Отправлено: %s\n"+
				"⬇️ Получено: %s\n"+
				"📊 Всего: %s",
			html.EscapeString(email),
			statusIcon,
			statusText,
			expiryDate,
			daysRemaining,
			hoursRemaining,
			b.formatBytes(up),
			b.formatBytes(down),
			b.formatBytes(total),
		)
	}

	b.sendMessage(chatID, msg)
	log.Printf("[INFO] Sent subscription status to user %d", userID)
}

// handleExtendSubscription handles subscription extension request
func (b *Bot) handleExtendSubscription(chatID int64, userID int64) {
	log.Printf("[INFO] User %d requested subscription extension", userID)

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
		log.Printf("[INFO] User %d has unlimited subscription, extension denied", userID)
		return
	}

	// Show duration selection keyboard
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("30 дней").WithCallbackData(fmt.Sprintf("extend_%d_30", userID)),
			tu.InlineKeyboardButton("90 дней").WithCallbackData(fmt.Sprintf("extend_%d_90", userID)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("180 дней").WithCallbackData(fmt.Sprintf("extend_%d_180", userID)),
			tu.InlineKeyboardButton("365 дней").WithCallbackData(fmt.Sprintf("extend_%d_365", userID)),
		),
	)

	msg := fmt.Sprintf(
		"🔄 <b>Продление подписки</b>\n\n"+
			"� Аккаунт: %s\n\n"+
			"Выберите срок продления:",
		html.EscapeString(email),
	)

	b.bot.SendMessage(context.Background(), tu.Message(tu.ID(chatID), msg).
		WithReplyMarkup(keyboard).
		WithParseMode("HTML"))
}

// handleExtensionRequest processes subscription extension request
func (b *Bot) handleExtensionRequest(userID int64, chatID int64, messageID int, duration int) {
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

	// Try to get username from Telegram
	// (In real scenario, we might want to cache this from previous interactions)
	userName = fmt.Sprintf("User_%d", userID)

	// Send request to all admins
	for _, adminID := range b.config.Telegram.AdminIDs {
		keyboard := tu.InlineKeyboard(
			tu.InlineKeyboardRow(
				tu.InlineKeyboardButton("✅ Одобрить").WithCallbackData(fmt.Sprintf("approve_ext_%d_%d", userID, duration)),
				tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData(fmt.Sprintf("reject_ext_%d", userID)),
			),
		)

		adminMsg := fmt.Sprintf(
			"🔄 <b>Запрос на продление подписки</b>\n\n"+
				"👤 Пользователь: %s (ID: %d)\n"+
				"📧 Email: %s\n"+
				"📅 Продлить на: %d дней",
			html.EscapeString(userName),
			userID,
			html.EscapeString(email),
			duration,
		)

		b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), adminMsg).
			WithReplyMarkup(keyboard).
			WithParseMode("HTML"))
		log.Printf("[INFO] Sent extension request to admin %d", adminID)
	}

	// Update user's message
	b.editMessageText(chatID, messageID, fmt.Sprintf(
		"✅ Запрос на продление отправлен администраторам!\n\n"+
			"� Аккаунт: %s\n"+
			"📅 Срок: %d дней\n\n"+
			"⏳ Ожидайте одобрения...",
		html.EscapeString(email),
		duration,
	))

	log.Printf("[INFO] Extension request sent for user %d, email: %s, duration: %d days", userID, email, duration)
}

// handleExtensionApproval processes admin approval for subscription extension
func (b *Bot) handleExtensionApproval(userID int64, adminChatID int64, messageID int, duration int) {
	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка: клиент не найден")
		log.Printf("[ERROR] Client not found for extension: %v", err)
		return
	}

	email := ""
	if e, ok := clientInfo["email"].(string); ok {
		email = e
	}

	// Get current expiry time
	currentExpiry := int64(0)
	if et, ok := clientInfo["expiryTime"].(float64); ok {
		currentExpiry = int64(et)
	}

	// Get inbound ID
	inboundID := int(clientInfo["_inboundID"].(float64))

	// Get client UUID and subId (must preserve them)
	clientUUID := ""
	if id, ok := clientInfo["id"].(string); ok {
		clientUUID = id
	}

	clientSubID := ""
	if subId, ok := clientInfo["subId"].(string); ok {
		clientSubID = subId
	}
	if clientSubID == "" {
		// Generate new subId if not exists
		clientSubID = generateRandomString(16)
		log.Printf("[INFO] Generated new subId for client: %s", clientSubID)
	}

	// Delete old client using UUID
	log.Printf("[DEBUG] Attempting to delete client UUID: %s, email: %s", clientUUID, email)
	err = b.apiClient.DeleteClient(inboundID, clientUUID)
	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при удалении старого клиента: %v", err))
		log.Printf("[ERROR] Failed to delete client: %v", err)
		return
	}

	// Calculate new expiry time: add extension to CURRENT expiry (or to now if expired)
	now := time.Now().UnixMilli()
	baseTime := currentExpiry
	if currentExpiry < now {
		// If subscription already expired, start from now
		baseTime = now
	}
	newExpiry := baseTime + (int64(duration) * 24 * 60 * 60 * 1000) // Add days in milliseconds

	log.Printf("[INFO] Deleted client %s (UUID: %s), extending from %s by %d days to %s",
		email, clientUUID,
		time.UnixMilli(currentExpiry).Format("2006-01-02 15:04:05"),
		duration,
		time.UnixMilli(newExpiry).Format("2006-01-02 15:04:05"))

	// Wait for the deletion to be fully processed
	time.Sleep(5 * time.Second)

	// Verify deletion
	checkClient, _ := b.apiClient.GetClientByTgID(userID)
	if checkClient != nil {
		log.Printf("[WARNING] Client still exists after deletion, waiting additional 5 seconds")
		time.Sleep(5 * time.Second)
	}

	// Get inbound to determine protocol
	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при получении inbounds: %v", err))
		return
	}

	var currentInbound map[string]interface{}
	for _, inb := range inbounds {
		if int(inb["id"].(float64)) == inboundID {
			currentInbound = inb
			break
		}
	}

	if currentInbound == nil {
		b.sendMessage(adminChatID, "❌ Ошибка: inbound не найден")
		return
	}

	protocol := ""
	if p, ok := currentInbound["protocol"].(string); ok {
		protocol = p
	}

	// Create new client with same UUID/password, subId and extended time
	newClientData := map[string]interface{}{
		"email":      email,
		"enable":     true,
		"expiryTime": newExpiry,
		"totalGB":    0, // Unlimited
		"tgId":       userID,
		"subId":      clientSubID, // Keep same subId
		"limitIp":    1,
		"comment":    "",
		"reset":      0,
	}

	// Add protocol-specific fields, preserving existing IDs/passwords
	switch protocol {
	case "vmess":
		if clientUUID != "" {
			newClientData["id"] = clientUUID // Keep same UUID
		} else {
			newClientData["id"] = uuid.New().String()
		}
		// Get security from old client or use default
		if sec, ok := clientInfo["security"].(string); ok {
			newClientData["security"] = sec
		} else {
			newClientData["security"] = "auto"
		}
	case "vless":
		if clientUUID != "" {
			newClientData["id"] = clientUUID // Keep same UUID
		} else {
			newClientData["id"] = uuid.New().String()
		}
		// Get flow from old client or use default
		if flow, ok := clientInfo["flow"].(string); ok {
			newClientData["flow"] = flow
		} else {
			newClientData["flow"] = ""
		}
	case "trojan":
		// Get password from old client or generate new
		if pass, ok := clientInfo["password"].(string); ok && pass != "" {
			newClientData["password"] = pass // Keep same password
		} else {
			newClientData["password"] = generateRandomString(10)
		}
	case "shadowsocks":
		// Get method from old client
		if method, ok := clientInfo["method"].(string); ok {
			newClientData["method"] = method
		} else {
			newClientData["method"] = "aes-256-gcm"
		}
		// Get password from old client or generate new
		if pass, ok := clientInfo["password"].(string); ok && pass != "" {
			newClientData["password"] = pass // Keep same password
		} else {
			newClientData["password"] = generateRandomString(16)
		}
	default:
		// Fallback to VLESS-like
		if clientUUID != "" {
			newClientData["id"] = clientUUID
		} else {
			newClientData["id"] = uuid.New().String()
		}
		newClientData["flow"] = ""
	}

	err = b.apiClient.AddClient(inboundID, newClientData)
	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при создании нового клиента: %v", err))
		log.Printf("[ERROR] Failed to recreate client: %v", err)
		return
	}

	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(email)
	if err != nil {
		log.Printf("[WARNING] Failed to get subscription link: %v", err)
		subLink = "Не удалось получить ссылку"
	}

	// Calculate days for display (from now until new expiry)
	daysUntilExpiry := int((newExpiry - now) / (1000 * 60 * 60 * 24))
	oldExpiry := time.UnixMilli(currentExpiry).Format("2006-01-02 15:04:05")
	newExpiryFormatted := time.UnixMilli(newExpiry).Format("2006-01-02 15:04:05")

	// Notify user
	userMsg := fmt.Sprintf(
		"✅ <b>Ваша подписка продлена!</b>\n\n"+
			"👤 Аккаунт: %s\n"+
			"📅 Продлено на: %d дней\n"+
			"⏰ Истекает: %s\n"+
			"📅 Осталось дней: %d\n\n"+
			"🔗 <b>Ваша VPN конфигурация:</b>\n"+
			"<code>%s</code>\n\n"+
			"ℹ️ Ссылка осталась прежней, переподключение не требуется.",
		html.EscapeString(email),
		duration,
		newExpiryFormatted,
		daysUntilExpiry,
		html.EscapeString(subLink),
	)
	b.sendMessage(userID, userMsg)

	// Update admin message
	adminMsg := fmt.Sprintf(
		"✅ <b>Продление ОДОБРЕНО</b>\n\n"+
			"👤 Пользователь ID: %d\n"+
			"📧 Username: %s\n"+
			"⏰ Было до: %s\n"+
			"📅 Продлено: +%d дней\n"+
			"⏰ Теперь до: %s",
		userID,
		html.EscapeString(email),
		oldExpiry,
		duration,
		newExpiryFormatted,
	)
	b.editMessageText(adminChatID, messageID, adminMsg)

	log.Printf("[INFO] Subscription extended for user %d, email: %s, added: %d days, expires: %s",
		userID, email, duration, newExpiryFormatted)
}

// handleExtensionRejection processes admin rejection for subscription extension
func (b *Bot) handleExtensionRejection(userID int64, adminChatID int64, messageID int) {
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
	adminMsg := fmt.Sprintf(
		"❌ <b>Продление ОТКЛОНЕНО</b>\n\n"+
			"👤 Пользователь ID: %d\n"+
			"📧 Username: %s",
		userID,
		html.EscapeString(email),
	)
	b.editMessageText(adminChatID, messageID, adminMsg)

	log.Printf("[INFO] Extension rejected for user %d, email: %s", userID, email)
}
