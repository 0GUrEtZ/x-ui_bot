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
	"x-ui-bot/internal/panel"
	"x-ui-bot/pkg/client"
	"x-ui-bot/sqlite"

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

// Use types from sqlite package
type RegistrationRequest = sqlite.RegistrationRequest
type AdminMessageState = sqlite.AdminMessageState
type UserMessageState = sqlite.UserMessageState
type BroadcastState = sqlite.BroadcastState

// Bot represents the Telegram bot
type Bot struct {
	config    *config.Config
	apiClient *client.APIClient // Keep for backward compatibility, but will be removed
	bot       *telego.Bot
	handler   *th.BotHandler
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	isRunning bool
	storage   Storage // Storage interface for persistence
	logger    *logger.Logger

	// Panel manager for multi-server support
	panelManager *panel.PanelManager

	// Services
	clientService       *services.ClientService
	subscriptionService *services.SubscriptionService
	backupService       *services.BackupService
	broadcastService    *services.BroadcastService
	forecastService     *services.ForecastService

	// Middleware
	authMiddleware *middleware.AuthMiddleware
	rateLimiter    *middleware.RateLimiter

	clientCache sync.Map      // Cache for client data: "inboundID_index" -> client map
	stopBackup  chan struct{} // Signal to stop backup scheduler
}

// Storage interface for bot data persistence
type Storage = sqlite.Storage

// NewBot creates a new Bot instance
func NewBot(cfg *config.Config, panelManager *panel.PanelManager, store Storage) (*Bot, error) {
	bot, err := createTelegoBot(cfg.Telegram.Token, cfg.Telegram.Proxy, cfg.Telegram.APIServer)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	// Initialize logger
	logger.Init("info", false) // false = JSON format
	log := logger.GetLogger()

	// Get first panel client for backward compatibility
	firstPanel := panelManager.GetPanels()[0]
	firstClient, err := panelManager.GetClient(firstPanel.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get first panel client: %w", err)
	}

	// Initialize services
	clientService := services.NewClientService(firstClient, log)
	subscriptionService := services.NewSubscriptionService(log)
	backupService := services.NewBackupServiceWithPanelManager(panelManager, bot, cfg, log)
	broadcastService := services.NewBroadcastServiceWithPanelManager(panelManager, bot, log)
	forecastService := services.NewForecastServiceWithPanelManager(panelManager, store, log)

	// Initialize middleware
	authMiddleware := middleware.NewAuthMiddleware(cfg)
	rateLimiter := middleware.NewRateLimiter()

	return &Bot{
		config:              cfg,
		apiClient:           firstClient, // Keep for backward compatibility
		bot:                 bot,
		storage:             store,
		logger:              log,
		panelManager:        panelManager,
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

	// Start periodic traffic collection (every 4 hours)
	b.forecastService.StartPeriodicCollection()

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

	// Stop periodic traffic collection
	if b.forecastService != nil {
		b.forecastService.StopPeriodicCollection()
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
	// Use selected panel and inbound
	client, err := b.panelManager.GetClient(req.PanelName)
	if err != nil {
		return fmt.Errorf("failed to get client for panel %s: %w", req.PanelName, err)
	}

	// Get the selected inbound
	inbounds, err := client.GetInbounds()
	if err != nil {
		return fmt.Errorf("failed to get inbounds: %w", err)
	}

	var selectedInbound map[string]interface{}
	for _, inbound := range inbounds {
		if inboundID, ok := inbound["id"].(float64); ok && int(inboundID) == req.InboundID {
			selectedInbound = inbound
			break
		}
	}

	if selectedInbound == nil {
		return fmt.Errorf("selected inbound %d not found on panel %s", req.InboundID, req.PanelName)
	}
	inboundID := int(selectedInbound["id"].(float64))

	// Get protocol
	protocol := ""
	if p, ok := selectedInbound["protocol"].(string); ok {
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
	b.addProtocolFields(clientData, protocol, selectedInbound)

	// Add client via API
	return client.AddClient(inboundID, clientData)
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
