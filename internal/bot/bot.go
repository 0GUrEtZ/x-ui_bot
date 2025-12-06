package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
	expiryNotifier      *services.ExpiryNotifierService
	inboundSyncService  *services.InboundSyncService
	trafficSyncService  *services.TrafficSyncService

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
	expiryNotifier := services.NewExpiryNotifierService(bot, store, log, cfg.Notifications.ExpiryWarningDays)
	inboundSyncService := services.NewInboundSyncService(apiClient, log, cfg.Panel.MultiInboundSync)
	trafficSyncService := services.NewTrafficSyncService(apiClient, clientService, store, log, cfg.Panel.TrafficSyncHours)

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
		expiryNotifier:      expiryNotifier,
		inboundSyncService:  inboundSyncService,
		trafficSyncService:  trafficSyncService,
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
			{Command: "forecast", Description: "Show total traffic forecast"},
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

	// Create shared context for all services
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel

	// Start expiry notifier
	if len(b.config.Notifications.ExpiryWarningDays) > 0 {
		go b.expiryNotifier.Start(ctx)
		go b.subscriptionSyncScheduler(ctx)
		b.logger.Info("Started expiry notifier and sync service")
	}

	// Start inbound sync scheduler if enabled
	if b.config.Panel.MultiInboundSync {
		syncHours := b.config.Panel.MultiInboundSyncHours
		if syncHours <= 0 {
			syncHours = 24 // Default to 24 hours
		}
		go b.inboundSyncService.Start(ctx, syncHours)
		b.logger.Infof("Started multi-inbound sync service (interval: %d hours)", syncHours)
	}

	// Start traffic sync scheduler if enabled
	if b.config.Panel.TrafficSyncHours > 0 {
		go b.trafficSyncService.StartSync(ctx)
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

	// If user already exists in any inbound, reuse their subId to avoid duplicates
	if b.config.Panel.MultiInboundNewUsers {
		for _, inbound := range inbounds {
			settingsStr := ""
			if settings, ok := inbound["settings"].(string); ok {
				settingsStr = settings
			}

			clients, err := b.clientService.ParseClients(settingsStr)
			if err != nil {
				b.logger.Errorf("Failed to parse clients for inbound %v: %v", inbound["id"], err)
				continue
			}

			for _, c := range clients {
				email := c["email"]
				if email == "" || email != req.Email {
					continue
				}

				// Parse raw JSON to extract subId
				rawJSON := c["_raw_json"]
				var clientData map[string]interface{}
				if err := json.Unmarshal([]byte(rawJSON), &clientData); err == nil {
					if existingSub, ok := clientData["subId"].(string); ok && existingSub != "" {
						subID = existingSub
						b.logger.Infof("Reusing existing subId for user %s from inbound %v", req.Email, inbound["id"])
						break
					}
				}
			}
		}
	}

	// Calculate traffic limit in bytes
	trafficLimitBytes := int64(0)
	if b.config.Panel.TrafficLimitGB > 0 {
		trafficLimitBytes = int64(b.config.Panel.TrafficLimitGB) * 1024 * 1024 * 1024
	}

	// Check if multi-inbound mode is enabled for new users
	if b.config.Panel.MultiInboundNewUsers {
		// Create client in ALL inbounds
		b.logger.Infof("Creating client %s in all inbounds (multi-inbound mode)", req.Email)

		createdCount := 0
		existedCount := 0

		for _, inbound := range inbounds {
			inboundID := int(inbound["id"].(float64))
			protocol := ""
			if p, ok := inbound["protocol"].(string); ok {
				protocol = p
			}

			// Get inbound remark (name)
			inboundRemark := ""
			if remark, ok := inbound["remark"].(string); ok {
				inboundRemark = remark
			}
			if inboundRemark == "" {
				inboundRemark = fmt.Sprintf("inbound%d", inboundID)
			}

			// Add inbound name suffix to email: email__remarkName
			// This allows multiple clients with same base email across inbounds
			emailForInbound := fmt.Sprintf("%s__%s", req.Email, inboundRemark)

			// Create client data with SAME subId for all inbounds
			clientData := map[string]interface{}{
				"email":      emailForInbound,
				"enable":     true,
				"expiryTime": expiryTime,
				"totalGB":    trafficLimitBytes,
				"tgId":       req.UserID,
				"subId":      subID, // Same subId for unified subscription
				"limitIp":    b.config.Panel.LimitIP,
				"comment":    "",
				"reset":      0,
			}

			// Add protocol-specific fields
			b.addProtocolFields(clientData, protocol, inbound)

			// Add client to this inbound
			if err := b.apiClient.AddClient(context.Background(), inboundID, clientData); err != nil {
				b.logger.Errorf("Failed to create client %s in inbound %d: %v", emailForInbound, inboundID, err)
			} else {
				b.logger.Infof("Created client %s in inbound %d", emailForInbound, inboundID)
				createdCount++
			}

			// Small delay between inbound creations to avoid race conditions
			time.Sleep(100 * time.Millisecond)
		}

		if createdCount == 0 && existedCount == 0 {
			return fmt.Errorf("failed to create client in any inbound")
		}

		b.logger.Infof("Multi-inbound result for %s: created %d, existed %d, total inbounds %d", req.Email, createdCount, existedCount, len(inbounds))
		return nil
	}

	// Single inbound mode (default) - create only in first inbound
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

// stripInboundSuffix removes the __remarkName suffix from email if present
func stripInboundSuffix(email string) string {
	// Find last "__" to handle remark names that might contain single "_"
	lastIndex := strings.LastIndex(email, "__")
	if lastIndex != -1 {
		return email[:lastIndex]
	}
	return email
}

// subscriptionSyncScheduler periodically syncs subscription expiry data
func (b *Bot) subscriptionSyncScheduler(ctx context.Context) {
	b.logger.Info("Subscription sync scheduler started")

	// Sync immediately on start
	if err := b.syncSubscriptionExpiry(); err != nil {
		b.logger.Errorf("Failed to sync subscriptions: %v", err)
	}

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Stopping subscription sync scheduler")
			return
		case <-ticker.C:
			if err := b.syncSubscriptionExpiry(); err != nil {
				b.logger.Errorf("Failed to sync subscriptions: %v", err)
			}
		}
	}
}

// syncSubscriptionExpiry syncs subscription expiry data to local database
func (b *Bot) syncSubscriptionExpiry() error {
	inbounds, err := b.apiClient.GetInbounds(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get inbounds: %w", err)
	}

	for _, inbound := range inbounds {
		settingsStr := ""
		if settings, ok := inbound["settings"].(string); ok {
			settingsStr = settings
		}

		clients, err := b.clientService.ParseClients(settingsStr)
		if err != nil {
			b.logger.Errorf("Failed to parse clients for inbound: %v", err)
			continue
		}

		for _, client := range clients {
			// Parse tgId
			var tgID int64
			if tgIDStr, ok := client["tgId"]; ok && tgIDStr != "" {
				if val, err := strconv.ParseInt(tgIDStr, 10, 64); err == nil {
					tgID = val
				}
			}

			// Skip clients without telegram ID
			if tgID == 0 {
				continue
			}

			// Parse expiry time
			var expiryTime int64
			if expiryStr, ok := client["expiryTime"]; ok && expiryStr != "" {
				if val, err := strconv.ParseInt(expiryStr, 10, 64); err == nil {
					expiryTime = val
				}
			}

			// Skip clients without expiry or expired
			if expiryTime == 0 || expiryTime < time.Now().UnixMilli() {
				continue
			}

			// Upsert to database
			email := client["email"]
			if err := b.storage.UpsertSubscriptionExpiry(email, tgID, expiryTime); err != nil {
				b.logger.Errorf("Failed to upsert subscription expiry for %s: %v", email, err)
			}
		}
	}

	return nil
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
