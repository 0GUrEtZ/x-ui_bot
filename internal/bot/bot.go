package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math"
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
	UserID     int64
	Username   string
	TgUsername string // Telegram @username
	Email      string
	Duration   int // days
	Status     string
	Timestamp  time.Time
}

// AdminMessageState represents state for admin sending message to client
type AdminMessageState struct {
	ClientEmail string
	ClientTgID  string
	InboundID   int
	ClientIndex int
	Timestamp   time.Time
}

// UserMessageState represents state for user sending message to admin
type UserMessageState struct {
	UserID     int64
	Username   string
	TgUsername string
	Timestamp  time.Time
}

// RateLimitEntry represents rate limit tracking for a user
type RateLimitEntry struct {
	count     int
	resetTime time.Time
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
	adminMessageState map[int64]*AdminMessageState // State for admin messaging clients
	userMessageState  map[int64]*UserMessageState  // State for user messaging admins
	rateLimits        map[int64]*RateLimitEntry    // Rate limiting per user
	rateLimitMutex    sync.Mutex
}

// NewBot creates a new Bot instance
func NewBot(cfg *config.Config, apiClient *client.APIClient) (*Bot, error) {
	bot, err := createTelegoBot(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.APIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	return &Bot{
		config:            cfg,
		apiClient:         apiClient,
		bot:               bot,
		userStates:        make(map[int64]string),
		registrationReqs:  make(map[int64]*RegistrationRequest),
		adminMessageState: make(map[int64]*AdminMessageState),
		userMessageState:  make(map[int64]*UserMessageState),
		rateLimits:        make(map[int64]*RateLimitEntry),
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

	// Start cleanup goroutine for expired states (24h TTL)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.cleanupExpiredStates(ctx)
	}()
}

// handleCommand handles incoming commands
func (b *Bot) handleCommand(ctx *th.Context, message telego.Message) error {
	chatID := message.Chat.ID
	userID := message.From.ID
	isAdmin := b.isAdmin(userID)

	command, _, args := tu.ParseCommand(message.Text)

	log.Printf("[INFO] Command /%s from user ID: %d", command, userID)

	// Check rate limit
	if !b.checkRateLimit(userID) {
		log.Printf("[WARN] Rate limit exceeded for user ID: %d", userID)
		return nil // Silently ignore
	}

	// Check if client is blocked (except for start, help, id commands and admins)
	if !isAdmin && command != "start" && command != "help" && command != "id" {
		if b.isClientBlocked(userID) {
			b.sendMessage(chatID, "🔒 Ваш доступ заблокирован администратором.\n\nДля получения информации свяжитесь с администратором.")
			return nil
		}
	}

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

	// Check rate limit
	if !b.checkRateLimit(userID) {
		log.Printf("[WARN] Rate limit exceeded for user ID: %d", userID)
		return nil
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
	if state, exists := b.userStates[chatID]; exists {
		switch state {
		case "awaiting_email":
			b.handleRegistrationEmail(chatID, userID, message.Text)
			return nil
		case "awaiting_new_email":
			b.handleNewEmailInput(chatID, userID, message.Text)
			return nil
		case "awaiting_admin_message":
			b.handleAdminMessageSend(chatID, message.Text)
			return nil
		case "awaiting_user_message":
			b.handleUserMessageSend(chatID, userID, message.Text, message.From)
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
	default:
		// Handle buttons with emoji (encoding issues)
		if strings.Contains(message.Text, "Зарегистрироваться") {
			// Get user info
			userName := message.From.FirstName
			if message.From.LastName != "" {
				userName += " " + message.From.LastName
			}
			if userName == "" {
				userName = fmt.Sprintf("User_%d", userID)
			}
			tgUsername := message.From.Username
			b.handleRegistrationStart(chatID, userID, userName, tgUsername)
		} else if strings.Contains(message.Text, "Получить VPN") {
			b.handleGetSubscriptionLink(chatID, userID)
		} else if strings.Contains(message.Text, "Статус подписки") {
			b.handleSubscriptionStatus(chatID, userID)
		} else if strings.Contains(message.Text, "Продлить подписку") {
			b.handleExtendSubscription(chatID, userID)
		} else if strings.Contains(message.Text, "Настройки") {
			b.handleSettings(chatID, userID)
		} else if strings.Contains(message.Text, "Обновить username") {
			b.handleUpdateUsername(chatID, userID)
		} else if strings.Contains(message.Text, "Назад") {
			// Return to main menu
			b.handleStart(chatID, message.From.FirstName, false)
		} else if strings.Contains(message.Text, "Связь с админом") {
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
	isAdmin := b.isAdmin(userID)

	log.Printf("[INFO] Callback from user %d: %s", userID, data)

	// Check if client is blocked — block all non-admin callbacks
	if !isAdmin {
		if b.isClientBlocked(userID) {
			b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
				Text:            "🔒 Ваш доступ заблокирован",
				ShowAlert:       true,
			})
			return nil
		}
	}

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
				// Get Telegram username from callback query
				tgUsername := query.From.Username
				b.handleExtensionRequest(userID, chatID, messageID, duration, tgUsername)
				b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            fmt.Sprintf("✅ Запрос на %d дней отправлен", duration),
				})
				return nil
			}
		}
	}

	// Handle contact admin (non-admin can use)
	if data == "contact_admin" {
		b.handleContactAdmin(chatID, userID)
		b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
			Text:            "✅ Введите ваше сообщение",
		})
		return nil
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

	// Handle client_X_Y buttons (show client actions menu)
	if strings.HasPrefix(data, "client_") {
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
	if data == "back_to_clients" {
		b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: query.ID,
		})
		b.handleClients(chatID, true, messageID)
		return nil
	}

	// Handle delete_X_Y buttons
	if strings.HasPrefix(data, "delete_") {
		parts := strings.Split(data, "_")
		if len(parts) == 3 {
			inboundID, err1 := strconv.Atoi(parts[1])
			clientIndex, err2 := strconv.Atoi(parts[2])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if clientData, ok := b.clientCache.Load(cacheKey); ok {
					client := clientData.(map[string]string)
					email := client["email"]

					// Show confirmation dialog
					confirmMsg := fmt.Sprintf("❗ Вы уверены, что хотите удалить клиента?\n\n👤 Email: %s", email)
					keyboard := tu.InlineKeyboard(
						tu.InlineKeyboardRow(
							tu.InlineKeyboardButton("✅ Да, удалить").WithCallbackData(fmt.Sprintf("confirm_delete_%d_%d", inboundID, clientIndex)),
							tu.InlineKeyboardButton("❌ Отмена").WithCallbackData(fmt.Sprintf("cancel_delete_%d_%d", inboundID, clientIndex)),
						),
					)

					b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
						ChatID:      tu.ID(chatID),
						MessageID:   messageID,
						Text:        confirmMsg,
						ReplyMarkup: keyboard,
					})

					b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
						CallbackQueryID: query.ID,
					})
					return nil
				}
			}
		}
	}

	if strings.HasPrefix(data, "confirm_delete_") {
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			inboundID, err1 := strconv.Atoi(parts[2])
			clientIndex, err2 := strconv.Atoi(parts[3])

			if err1 == nil && err2 == nil {
				cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
				if clientData, ok := b.clientCache.Load(cacheKey); ok {
					client := clientData.(map[string]string)
					email := client["email"]
					clientID := client["id"] // UUID for VMESS/VLESS

					// Delete the client using UUID
					err := b.apiClient.DeleteClient(inboundID, clientID)

					if err != nil {
						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("❌ Ошибка удаления: %v", err),
							ShowAlert:       true,
						})
					} else {
						// Answer callback
						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            fmt.Sprintf("🗑️ Клиент %s удалён", email),
						})
						// Refresh client list
						b.handleClients(chatID, true, messageID)
					}
					return nil
				}
			}
		}
	}

	if strings.HasPrefix(data, "cancel_delete_") {
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			inboundID, err1 := strconv.Atoi(parts[2])
			clientIndex, err2 := strconv.Atoi(parts[3])

			if err1 == nil && err2 == nil {
				b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
					CallbackQueryID: query.ID,
					Text:            "❌ Удаление отменено",
				})
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
				if clientData, ok := b.clientCache.Load(cacheKey); ok {
					client := clientData.(map[string]string)
					email := client["email"]
					tgId := client["tgId"]

					if tgId != "" && tgId != "0" {
						// Store admin chat ID and client info for message sending
						b.adminMessageState[chatID] = &AdminMessageState{
							ClientEmail: email,
							ClientTgID:  tgId,
							InboundID:   inboundID,
							ClientIndex: clientIndex,
							Timestamp:   time.Now(),
						}
						b.userStates[chatID] = "awaiting_admin_message"

						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
						}) // Ask admin to type message
						msg := fmt.Sprintf("💬 Отправка сообщения клиенту %s\n\nВведите текст сообщения:", email)
						b.sendMessage(chatID, msg)
					} else {
						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            "❌ У клиента нет привязанного Telegram ID",
							ShowAlert:       true,
						})
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
			b.adminMessageState[chatID] = &AdminMessageState{
				ClientTgID: userIDStr,
				Timestamp:  time.Now(),
			}
			b.userStates[chatID] = "awaiting_admin_message"

			b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: query.ID,
			})

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
						// Update enable status in cache immediately
						if enable == "false" {
							client["enable"] = "true"
						} else {
							client["enable"] = "false"
						}
						b.clientCache.Store(cacheKey, client)

						// Answer callback with text
						b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
							CallbackQueryID: query.ID,
							Text:            resultMsg,
						})
						// Refresh client menu with updated data
						b.handleClientMenu(chatID, messageID, inboundID, clientIndex, query.ID)
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
					b.formatBytes(total),
					b.formatBytes(limitBytes),
					trafficEmoji,
					percentage,
				)
			} else {
				msg += fmt.Sprintf("📊 Трафик: %s (безлимит)\n", b.formatBytes(total))
			}

			msg += "\nВыберите действие:"

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
						tu.KeyboardButton("📱 Получить VPN"),
					),
					tu.KeyboardRow(
						tu.KeyboardButton("📊 Статус подписки"),
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

		clients := b.parseClients(settingsStr)
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

	log.Printf("[INFO] Sent %d clients to user ID: %d", totalClients, chatID)
}

// handleClientMenu shows actions menu for a specific client
func (b *Bot) handleClientMenu(chatID int64, messageID int, inboundID int, clientIndex int, queryID string) {
	cacheKey := fmt.Sprintf("%d_%d", inboundID, clientIndex)
	clientData, ok := b.clientCache.Load(cacheKey)

	// If not in cache, reload from API
	if !ok {
		inbounds, err := b.apiClient.GetInbounds()
		if err != nil {
			b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: queryID,
				Text:            "❌ Ошибка загрузки данных",
				ShowAlert:       true,
			})
			return
		}

		// Find the specific inbound and client
		for _, inbound := range inbounds {
			if id, ok := inbound["id"].(float64); ok && int(id) == inboundID {
				if settingsStr, ok := inbound["settings"].(string); ok {
					var settings map[string]interface{}
					if err := json.Unmarshal([]byte(settingsStr), &settings); err == nil {
						if clients, ok := settings["clients"].([]interface{}); ok && clientIndex < len(clients) {
							if clientMap, ok := clients[clientIndex].(map[string]interface{}); ok {
								// Convert to map[string]string for compatibility
								client := make(map[string]string)
								for k, v := range clientMap {
									client[k] = fmt.Sprintf("%v", v)
								}
								// Cache it for future use
								b.clientCache.Store(cacheKey, client)
								clientData = client
								ok = true
								break
							}
						}
					}
				}
			}
		}

		if !ok {
			b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
				CallbackQueryID: queryID,
				Text:            "❌ Клиент не найден",
				ShowAlert:       true,
			})
			return
		}
	}

	client := clientData.(map[string]string)
	email := client["email"]
	enable := client["enable"]
	tgId := client["tgId"]
	totalGB := client["totalGB"]
	expiryTime := client["expiryTime"]

	// Get client traffic stats
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
		b.formatBytes(up),
		b.formatBytes(down),
		b.formatBytes(total),
		trafficLimitStr,
	)

	// Build keyboard with actions
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

	b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        msg,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})

	b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
		CallbackQueryID: queryID,
	})
}

// handleAdminMessageSend handles sending message from admin to client
func (b *Bot) handleAdminMessageSend(adminChatID int64, messageText string) {
	state, exists := b.adminMessageState[adminChatID]
	if !exists {
		b.sendMessage(adminChatID, "❌ Ошибка: состояние не найдено")
		delete(b.userStates, adminChatID)
		return
	}

	// Parse client Telegram ID
	clientTgID, err := strconv.ParseInt(state.ClientTgID, 10, 64)
	if err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка: неверный Telegram ID клиента")
		delete(b.userStates, adminChatID)
		delete(b.adminMessageState, adminChatID)
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
	delete(b.userStates, adminChatID)
	delete(b.adminMessageState, adminChatID)
}

// handleContactAdmin initiates user messaging admin
func (b *Bot) handleContactAdmin(chatID int64, userID int64) {
	log.Printf("[INFO] User %d wants to contact admin", userID)

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
	b.userMessageState[chatID] = &UserMessageState{
		UserID:     userID,
		Username:   userName,
		TgUsername: tgUsername,
		Timestamp:  time.Now(),
	}
	b.userStates[chatID] = "awaiting_user_message"

	b.sendMessage(chatID, "💬 Напишите ваше сообщение администратору:")
}

// handleUserMessageSend handles sending message from user to admins
func (b *Bot) handleUserMessageSend(chatID int64, userID int64, messageText string, from *telego.User) {
	state, exists := b.userMessageState[chatID]
	if !exists {
		b.sendMessage(chatID, "❌ Ошибка: состояние не найдено")
		delete(b.userStates, chatID)
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

		b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), msg).
			WithReplyMarkup(keyboard).
			WithParseMode("HTML"))
	}

	b.sendMessage(chatID, "✅ Ваше сообщение отправлено администратору")

	// Clear state
	delete(b.userStates, chatID)
	delete(b.userMessageState, chatID)
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

// checkRateLimit checks if user exceeded rate limit (10 requests per minute)
func (b *Bot) checkRateLimit(userID int64) bool {
	// Admins bypass rate limiting
	if b.isAdmin(userID) {
		return true
	}

	b.rateLimitMutex.Lock()
	defer b.rateLimitMutex.Unlock()

	now := time.Now()
	entry, exists := b.rateLimits[userID]

	if !exists || now.After(entry.resetTime) {
		// Create new entry or reset
		b.rateLimits[userID] = &RateLimitEntry{
			count:     1,
			resetTime: now.Add(time.Minute),
		}
		return true
	}

	// Check if limit exceeded
	if entry.count >= 10 {
		return false
	}

	entry.count++
	return true
}

// cleanupExpiredStates removes expired user states (TTL: 24 hours)
func (b *Bot) cleanupExpiredStates(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			ttl := 24 * time.Hour

			// Cleanup registration requests
			b.registrationMutex.Lock()
			for userID, req := range b.registrationReqs {
				if now.Sub(req.Timestamp) > ttl {
					delete(b.registrationReqs, userID)
					log.Printf("[INFO] Cleaned up expired registration for user %d", userID)
				}
			}
			b.registrationMutex.Unlock()

			// Cleanup admin message states
			for userID, state := range b.adminMessageState {
				if now.Sub(state.Timestamp) > ttl {
					delete(b.adminMessageState, userID)
					delete(b.userStates, userID)
					log.Printf("[INFO] Cleaned up expired admin message state for user %d", userID)
				}
			}

			// Cleanup user message states
			for userID, state := range b.userMessageState {
				if now.Sub(state.Timestamp) > ttl {
					delete(b.userMessageState, userID)
					delete(b.userStates, userID)
					log.Printf("[INFO] Cleaned up expired user message state for user %d", userID)
				}
			}

			// Cleanup rate limits older than 2 minutes (no longer needed)
			b.rateLimitMutex.Lock()
			for userID, entry := range b.rateLimits {
				if now.After(entry.resetTime.Add(1 * time.Minute)) {
					delete(b.rateLimits, userID)
				}
			}
			b.rateLimitMutex.Unlock()

			log.Printf("[INFO] Completed periodic state cleanup")
		}
	}
}

// isClientBlocked checks if client is blocked (disabled) in panel
func (b *Bot) isClientBlocked(userID int64) bool {
	// Admins are never blocked
	if b.isAdmin(userID) {
		return false
	}

	// Get client info
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		// If client not found, consider as not blocked (allows registration)
		return false
	}

	// Check enable status
	if enable, ok := clientInfo["enable"].(bool); ok {
		return !enable
	}

	// Default to not blocked if status unclear
	return false
}

// getUserInfo gets user's name and Telegram username from Telegram API
func (b *Bot) getUserInfo(userID int64) (name string, username string) {
	chatInfo, err := b.bot.GetChat(context.Background(), &telego.GetChatParams{ChatID: tu.ID(userID)})
	if err == nil {
		if chatInfo.FirstName != "" {
			name = chatInfo.FirstName
			if chatInfo.LastName != "" {
				name += " " + chatInfo.LastName
			}
		}
		if chatInfo.Username != "" {
			username = "@" + chatInfo.Username
		}
	}
	if name == "" {
		name = fmt.Sprintf("User_%d", userID)
	}
	return name, username
}

// calculateTimeRemaining calculates days and hours remaining from expiryTime
func (b *Bot) calculateTimeRemaining(expiryTime int64) (days int, hours int) {
	if expiryTime <= 0 {
		return 0, 0
	}
	remainingMs := expiryTime - time.Now().UnixMilli()
	if remainingMs <= 0 {
		return 0, 0
	}
	days = int(remainingMs / (1000 * 60 * 60 * 24))
	hours = int((remainingMs % (1000 * 60 * 60 * 24)) / (1000 * 60 * 60))
	return days, hours
}

// addProtocolFields adds protocol-specific fields to client data
func (b *Bot) addProtocolFields(clientData map[string]interface{}, protocol string, inbound map[string]interface{}) {
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
		settingsStr, _ := inbound["settings"].(string)
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
}

// findClientByTgID finds client and inbound by telegram user ID
func (b *Bot) findClientByTgID(userID int64) (client map[string]string, inboundID int, email string, err error) {
	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to get inbounds: %w", err)
	}

	for _, inbound := range inbounds {
		id := int(inbound["id"].(float64))
		settingsStr, ok := inbound["settings"].(string)
		if !ok {
			continue
		}

		clients := b.parseClients(settingsStr)
		for _, c := range clients {
			if c["tgId"] == fmt.Sprintf("%d", userID) {
				return c, id, c["email"], nil
			}
		}
	}

	return nil, 0, "", fmt.Errorf("client not found for user ID %d", userID)
}

// getInstructionsText returns formatted instructions text if URL is configured
func (b *Bot) getInstructionsText() string {
	if b.config.Payment.InstructionsURL != "" {
		return fmt.Sprintf("\n\n📖 <b>Инструкции по подключению:</b>\n%s", b.config.Payment.InstructionsURL)
	}
	return ""
}

// createDurationKeyboard creates inline keyboard with duration options and prices
// callbackPrefix should be "reg_duration" for registration or "extend_<userID>" for extension
func (b *Bot) createDurationKeyboard(callbackPrefix string) *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("30 дней - %d₽", b.config.Payment.Prices.OneMonth)).WithCallbackData(fmt.Sprintf("%s_30", callbackPrefix)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("90 дней - %d₽", b.config.Payment.Prices.ThreeMonth)).WithCallbackData(fmt.Sprintf("%s_90", callbackPrefix)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("180 дней - %d₽", b.config.Payment.Prices.SixMonth)).WithCallbackData(fmt.Sprintf("%s_180", callbackPrefix)),
		),
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton(fmt.Sprintf("365 дней - %d₽", b.config.Payment.Prices.OneYear)).WithCallbackData(fmt.Sprintf("%s_365", callbackPrefix)),
		),
	)
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

		// Telegram ID
		if tgId, ok := clientMap["tgId"].(string); ok {
			client["tgId"] = tgId
		} else if tgId, ok := clientMap["tgId"].(float64); ok {
			client["tgId"] = fmt.Sprintf("%.0f", tgId)
		} else {
			client["tgId"] = ""
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

	// Use email as clientID for UpdateClient (it searches by email field)
	return b.apiClient.UpdateClient(inboundID, email, clientData)
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

	// Use email as clientID for UpdateClient (it searches by email field)
	return b.apiClient.UpdateClient(inboundID, email, clientData)
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
func (b *Bot) handleRegistrationStart(chatID int64, userID int64, userName string, tgUsername string) {
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
		UserID:     userID,
		Username:   userName,
		TgUsername: tgUsername,
		Status:     "input_email",
		Timestamp:  time.Now(),
	}
	b.registrationMutex.Unlock()

	b.userStates[chatID] = "awaiting_email"
	b.sendMessage(chatID, "📝 Регистрация нового клиента\n\n🔹 Шаг 1/2: Введите желаемый username:")
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
		b.sendMessage(chatID, "❌ Username не может быть пустым.\n\nВведите корректный username:")
		return
	}

	// Validate username length (3-32 characters)
	if len(email) < 3 {
		b.sendMessage(chatID, "❌ Username слишком короткий. Минимум 3 символа.\n\nВведите другой username:")
		return
	}
	if len(email) > 32 {
		b.sendMessage(chatID, "❌ Username слишком длинный. Максимум 32 символа.\n\nВведите другой username:")
		return
	}

	req.Email = email
	req.Status = "input_duration"
	b.userStates[chatID] = "awaiting_duration"

	keyboard := b.createDurationKeyboard("reg_duration")

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

	paymentMsg := fmt.Sprintf(
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

	b.sendMessage(chatID, paymentMsg)
}

// sendRegistrationRequestToAdmins sends registration request to all admins
func (b *Bot) sendRegistrationRequestToAdmins(req *RegistrationRequest) {
	log.Printf("[DEBUG] Sending registration to admins - UserID: %d, TgUsername: '%s'", req.UserID, req.TgUsername)

	// Format Telegram username
	tgUsernameStr := ""
	if req.TgUsername != "" {
		tgUsernameStr = fmt.Sprintf("\n💬 Telegram: @%s", req.TgUsername)
	}

	msg := fmt.Sprintf(
		"📝 Новая заявка на регистрацию\n\n"+
			"👤 Пользователь: %s (ID: %d)%s\n"+
			"👤 Username: %s\n"+
			"📅 Срок: %d дней\n"+
			"🕐 Время: %s",
		req.Username,
		req.UserID,
		tgUsernameStr,
		req.Email,
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
		instructionsText := b.getInstructionsText()

		userMsg := fmt.Sprintf(
			"✅ <b>Ваша заявка одобрена!</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"📅 Срок: %d дней\n\n"+
				"🔗 <b>Ваша VPN конфигурация:</b>\n"+
				"<blockquote expandable>%s</blockquote>\n\n"+
				"Скопируйте эту ссылку и добавьте её в ваше VPN приложение.%s",
			html.EscapeString(req.Email),
			req.Duration,
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

		log.Printf("[INFO] Registration approved for user %d, email: %s", requestUserID, req.Email)
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

		log.Printf("[INFO] Registration rejected for user %d, email: %s", requestUserID, req.Email)
	}

	// Clean up old requests and states
	b.registrationMutex.Lock()
	delete(b.registrationReqs, requestUserID)
	b.registrationMutex.Unlock()

	// Clear FSM state for user
	delete(b.userStates, requestUserID)

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
		"limitIp":    b.config.Panel.LimitIP,
		"comment":    "",
		"reset":      0,
	}

	// Add protocol-specific fields
	b.addProtocolFields(clientData, protocol, firstInbound)

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

	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(email)
	if err != nil {
		log.Printf("[ERROR] Failed to get subscription link: %v", err)
		b.sendMessage(chatID, "❌ Не удалось получить ссылку. Попробуйте позже или обратитесь к администратору.")
		return
	}

	// Get traffic limit
	totalGB := int64(0)
	if tgb, ok := clientInfo["totalGB"].(float64); ok {
		totalGB = int64(tgb)
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

	// Build traffic info
	trafficText := fmt.Sprintf("\n\n📊 <b>Трафик:</b> %s", b.formatBytes(total))
	if totalGB > 0 {
		limitBytes := totalGB
		percentage := float64(total) / float64(limitBytes) * 100
		trafficEmoji := "🟢"
		if percentage >= 90 {
			trafficEmoji = "🔴"
		} else if percentage >= 70 {
			trafficEmoji = "🟡"
		}
		trafficText += fmt.Sprintf(" / %s %s (%.1f%%)",
			b.formatBytes(limitBytes),
			trafficEmoji,
			percentage,
		)
	} else {
		trafficText += " (безлимит)"
	}

	instructionsText := b.getInstructionsText()

	msg := fmt.Sprintf(
		"✅ <b>Ваша VPN конфигурация:</b>\n\n"+
			"<blockquote expandable>%s</blockquote>%s%s",
		html.EscapeString(subLink),
		trafficText,
		instructionsText,
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

	// Get traffic limit
	totalGB := int64(0)
	if tgb, ok := clientInfo["totalGB"].(float64); ok {
		totalGB = int64(tgb)
	}

	// Calculate days and hours remaining
	daysRemaining, hoursRemaining := b.calculateTimeRemaining(expiryTime)

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

	// Build traffic info string with limit if applicable
	trafficInfo := fmt.Sprintf(
		"📈 <b>Трафик:</b>\n"+
			"⬆️ Отправлено: %s\n"+
			"⬇️ Получено: %s\n"+
			"📊 Всего: %s",
		b.formatBytes(up),
		b.formatBytes(down),
		b.formatBytes(total),
	)

	// Add traffic limit if set
	if totalGB > 0 {
		limitBytes := totalGB // totalGB is already in bytes
		percentage := float64(total) / float64(limitBytes) * 100
		trafficEmoji := "🟢"
		if percentage >= 90 {
			trafficEmoji = "🔴"
		} else if percentage >= 70 {
			trafficEmoji = "🟡"
		}
		trafficInfo += fmt.Sprintf("\n🎯 Лимит: %s %s (%.1f%%)",
			b.formatBytes(limitBytes),
			trafficEmoji,
			percentage,
		)
	} else {
		trafficInfo += "\n🎯 Лимит: ∞ (безлимит)"
	}

	if expiryTime == 0 {
		// Unlimited subscription
		statusIcon = "♾️"
		statusText = "Безлимитная"
		msg = fmt.Sprintf(
			"📊 <b>Статус подписки</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"%s Статус: %s\n"+
				"⏰ Истекает: ∞ (бессрочно)\n\n"+
				"%s",
			html.EscapeString(email),
			statusIcon,
			statusText,
			trafficInfo,
		)
	} else {
		// Limited subscription
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

		// Format expiry date
		expiryDate := time.UnixMilli(expiryTime).Format("02.01.2006 15:04")

		msg = fmt.Sprintf(
			"📊 <b>Статус подписки</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"%s Статус: %s\n"+
				"⏰ Истекает: %s\n"+
				"📅 Осталось: %d дней %d часов\n\n"+
				"%s",
			html.EscapeString(email),
			statusIcon,
			statusText,
			expiryDate,
			daysRemaining,
			hoursRemaining,
			trafficInfo,
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

	// Show duration selection keyboard with prices
	keyboard := b.createDurationKeyboard(fmt.Sprintf("extend_%d", userID))

	msg := fmt.Sprintf(
		"🔄 <b>Продление подписки</b>\n\n"+
			"👤 Аккаунт: %s\n\n"+
			"Выберите срок продления:",
		html.EscapeString(email),
	)

	b.bot.SendMessage(context.Background(), tu.Message(tu.ID(chatID), msg).
		WithReplyMarkup(keyboard).
		WithParseMode("HTML"))
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

		b.bot.SendMessage(context.Background(), tu.Message(tu.ID(adminID), adminMsg).
			WithReplyMarkup(keyboard))
		log.Printf("[INFO] Sent extension request to admin %d", adminID)
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

	log.Printf("[INFO] Extension request sent for user %d, email: %s, duration: %d days", userID, email, duration)
}

// handleSettings shows settings menu to user
func (b *Bot) handleSettings(chatID int64, userID int64) {
	log.Printf("[INFO] User %d opened settings", userID)

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
	log.Printf("[INFO] User %d requested username update", userID)

	// Get client info to verify registration
	clientInfo, err := b.apiClient.GetClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Вы не зарегистрированы в системе")
		return
	}

	currentEmail := ""
	if e, ok := clientInfo["email"].(string); ok {
		currentEmail = e
	}

	// Set state and ask for new username
	b.userStates[chatID] = "awaiting_new_email"
	b.sendMessage(chatID, fmt.Sprintf("👤 Текущий username: %s\n\nВведите новый username:", currentEmail))
	log.Printf("[INFO] User %d entering username update mode", userID)
}

// handleNewEmailInput processes new username input and updates client
func (b *Bot) handleNewEmailInput(chatID int64, userID int64, newEmail string) {
	log.Printf("[INFO] User %d updating username to: %s", userID, newEmail)

	// Find client by tgId
	foundClient, inboundID, oldEmail, err := b.findClientByTgID(userID)
	if err != nil {
		b.sendMessage(chatID, "❌ Ошибка: клиент не найден")
		delete(b.userStates, chatID)
		return
	}

	// Parse raw JSON and update email field
	rawJSON := foundClient["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		b.sendMessage(chatID, "❌ Ошибка при обработке данных клиента")
		log.Printf("[ERROR] Failed to parse client JSON: %v", err)
		delete(b.userStates, chatID)
		return
	}

	// Update email field
	clientData["email"] = newEmail

	// Fix numeric fields
	b.fixNumericFields(clientData)

	// Call UpdateClient with old email as identifier
	err = b.apiClient.UpdateClient(inboundID, oldEmail, clientData)
	if err != nil {
		b.sendMessage(chatID, fmt.Sprintf("❌ Ошибка обновления: %v", err))
		log.Printf("[ERROR] Failed to update username for user %d: %v", userID, err)
		delete(b.userStates, chatID)
		return
	}

	b.sendMessage(chatID, fmt.Sprintf("✅ Username успешно обновлен!\n\n👤 Старый: %s\n👤 Новый: %s", oldEmail, newEmail))
	log.Printf("[INFO] Username updated for user %d from %s to %s", userID, oldEmail, newEmail)

	// Clear state
	delete(b.userStates, chatID)
}

// handleExtensionApproval processes admin approval for subscription extension
func (b *Bot) handleExtensionApproval(userID int64, adminChatID int64, messageID int, duration int) {
	// Get user info from Telegram
	userName, tgUsername := b.getUserInfo(userID)

	// Find client by tgId
	foundClient, inboundID, email, err := b.findClientByTgID(userID)
	if err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка: клиент не найден")
		log.Printf("[ERROR] %v", err)
		return
	}

	// Parse raw JSON to preserve all fields
	rawJSON := foundClient["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		b.sendMessage(adminChatID, "❌ Ошибка при обработке данных клиента")
		log.Printf("[ERROR] Failed to parse client JSON: %v", err)
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

	log.Printf("[INFO] Extending subscription for %s from %s by %d days to %s",
		email,
		time.UnixMilli(currentExpiry).Format("2006-01-02 15:04:05"),
		duration,
		time.UnixMilli(newExpiry).Format("2006-01-02 15:04:05"))

	// Update only expiryTime field
	clientData["expiryTime"] = newExpiry

	// Fix numeric fields for proper type conversion
	b.fixNumericFields(clientData)

	// Update client via API
	err = b.apiClient.UpdateClient(inboundID, email, clientData)
	if err != nil {
		b.sendMessage(adminChatID, fmt.Sprintf("❌ Ошибка при обновлении подписки: %v", err))
		log.Printf("[ERROR] Failed to update client subscription: %v", err)
		return
	}

	// Get subscription link
	subLink, err := b.apiClient.GetClientLink(email)
	if err != nil {
		log.Printf("[WARNING] Failed to get subscription link: %v", err)
		subLink = "Не удалось получить ссылку"
	}

	// Calculate time remaining (days and hours)
	daysUntilExpiry, hoursUntilExpiry := b.calculateTimeRemaining(newExpiry)

	oldExpiry := time.UnixMilli(currentExpiry).Format("02.01.2006 15:04")
	newExpiryFormatted := time.UnixMilli(newExpiry).Format("02.01.2006 15:04")

	// Notify user
	instructionsText := b.getInstructionsText()

	userMsg := fmt.Sprintf(
		"✅ <b>Ваша подписка продлена!</b>\n\n"+
			"👤 Аккаунт: %s\n"+
			"📅 Продлено на: %d дней\n"+
			"⏰ Истекает: %s\n"+
			"📅 Осталось: %d дней %d часов\n\n"+
			"🔗 <b>Ваша VPN конфигурация:</b>\n"+
			"<blockquote expandable>%s</blockquote>%s",
		html.EscapeString(email),
		duration,
		newExpiryFormatted,
		daysUntilExpiry,
		hoursUntilExpiry,
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

	log.Printf("[INFO] Subscription extended for user %d, email: %s, added: %d days, expires: %s",
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

	log.Printf("[INFO] Extension rejected for user %d, email: %s", userID, email)
}
