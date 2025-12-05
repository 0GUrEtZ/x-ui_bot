package bot

import (
	"context"
	"fmt"
	"sync"
	"time"

	"x-ui-bot/internal/bot/middleware"
	"x-ui-bot/internal/bot/services"
	"x-ui-bot/internal/config"
	"x-ui-bot/internal/logger"
	"x-ui-bot/internal/storage"
	"x-ui-bot/pkg/client"

	"math/rand"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
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

// Use types from storage package
type RegistrationRequest = storage.RegistrationRequest
type AdminMessageState = storage.AdminMessageState
type UserMessageState = storage.UserMessageState
type BroadcastState = storage.BroadcastState

// Bot represents the Telegram bot
type Bot struct {
	config    *config.Config
	apiClient *client.APIClient
	bot       *telego.Bot
	handler   *th.BotHandler
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	isRunning bool
	storage   Storage // Storage interface for persistence
	logger    *logger.Logger

	// Services
	clientService       *services.ClientService
	subscriptionService *services.SubscriptionService
	backupService       *services.BackupService
	broadcastService    *services.BroadcastService
	forecastService     *services.ForecastService

	// Middleware
	authMiddleware *middleware.AuthMiddleware
	rateLimiter    *middleware.RateLimiter

	clientCache     sync.Map           // Cache for client data: "inboundID_index" -> client map
	cacheMutex      sync.RWMutex       // Protects concurrent access to clientCache
	stopBackup      chan struct{}      // Signal to stop backup scheduler
	broadcastCancel context.CancelFunc // Cancel function for active broadcast
	broadcastMutex  sync.Mutex
}

// Storage interface for bot data persistence
type Storage = storage.Storage

// NewBot creates a new Bot instance
func NewBot(cfg *config.Config, apiClient *client.APIClient, store Storage) (*Bot, error) {
	bot, err := createTelegoBot(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.APIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	// Initialize logger
	logger.Init("info", false) // false = JSON format
	log := logger.GetLogger()

	// Initialize services
	clientService := services.NewClientService(apiClient, log)
	subscriptionService := services.NewSubscriptionService(log)
	backupService := services.NewBackupService(apiClient, bot, cfg, log)
	broadcastService := services.NewBroadcastService(apiClient, bot, log)
	forecastService := services.NewForecastService(apiClient, store, bot, cfg, log)

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware(cfg)
	rateLimiter := middleware.NewRateLimiter(cfg.RateLimit.MaxRequestsPerMinute, cfg.RateLimit.WindowSeconds)

	return &Bot{
		config:              cfg,
		apiClient:           apiClient,
		bot:                 bot,
		storage:             store,
		logger:              log,
		clientService:       clientService,
		subscriptionService: subscriptionService,
		backupService:       backupService,
		broadcastService:    broadcastService,
		forecastService:     forecastService,
		authMiddleware:      authMiddleware,
		rateLimiter:         rateLimiter,
		stopBackup:          make(chan struct{}),
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
	if err := b.apiClient.Login(context.Background()); err != nil {
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
		b.logger.Warnf("Failed to set bot commands: %v", err)
	}

	// Start message handling
	if !b.isRunning {
		go b.receiveMessages()
		b.isRunning = true
	}

	// Start backup scheduler if enabled
	if b.config.Panel.BackupDays > 0 {
		go b.backupScheduler()
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
		if err := b.handler.Stop(); err != nil {
			b.logger.Errorf("Failed to stop handler: %v", err)
		}
	}
	if b.stopBackup != nil {
		close(b.stopBackup)
	}

	if b.forecastService != nil {
		b.forecastService.Stop()
	}

	// Close storage
	if b.storage != nil {
		if err := b.storage.Close(); err != nil {
			b.logger.Errorf("Failed to close storage: %v", err)
		}
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

		handler.Start() //nolint:errcheck // handler.Start() doesn't return error
	}()

	// Start traffic forecast scheduler (uses same ctx so it stops when ctx cancelled)
	if b.forecastService != nil {
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			b.forecastService.StartScheduler(ctx)
		}()
	}

	// Start cleanup goroutine for expired states (24h TTL)
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		b.cleanupExpiredStates(ctx)
	}()
}

// isAdmin checks if a user is an admin
// cleanupExpiredStates removes expired user states (TTL: 24 hours)
func (b *Bot) cleanupExpiredStates(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Use storage's cleanup method
			if err := b.storage.CleanupExpiredStates(24 * time.Hour); err != nil {
				b.logger.Errorf("Failed to cleanup expired states: %v", err)
			}

			// Cleanup rate limits from middleware
			b.rateLimiter.Cleanup()

			b.logger.Info("Completed periodic state cleanup")
		}
	}
}

// createClientForRequest creates a new client based on registration request
func (b *Bot) createClientForRequest(req *RegistrationRequest) error {
	// Get first inbound to add client to
	inbounds, err := b.apiClient.GetInbounds(context.Background())
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

	// Calculate traffic limit in bytes
	trafficLimitBytes := int64(0)
	if b.config.Panel.TrafficLimitGB > 0 {
		trafficLimitBytes = int64(b.config.Panel.TrafficLimitGB) * 1024 * 1024 * 1024
	}

	// Create client data based on protocol
	clientData := map[string]interface{}{
		"email":      req.Email,
		"enable":     true,
		"expiryTime": expiryTime,
		"totalGB":    trafficLimitBytes,
		"tgId":       req.UserID,
		"subId":      subID,
		"limitIp":    b.config.Panel.LimitIP,
		"comment":    "",
		"reset":      0,
	}

	// Add protocol-specific fields
	b.addProtocolFields(clientData, protocol, firstInbound)

	// Add client via API
	return b.apiClient.AddClient(context.Background(), inboundID, clientData)
}

// backupScheduler periodically sends database backups to admins
func (b *Bot) backupScheduler() {
	b.logger.Infof("Backup scheduler started (interval: %d days)", b.config.Panel.BackupDays)

	ticker := time.NewTicker(time.Duration(b.config.Panel.BackupDays) * 24 * time.Hour)
	defer ticker.Stop()

	// Send initial backup on start
	time.Sleep(1 * time.Minute) // Wait 1 minute after bot start
	b.sendBackupToAdmins()

	for {
		select {
		case <-ticker.C:
			b.sendBackupToAdmins()
		case <-b.stopBackup:
			b.logger.Info("Backup scheduler stopped")
			return
		}
	}
}
