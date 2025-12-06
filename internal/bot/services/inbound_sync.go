package services

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"
)

// stripInboundSuffix removes the ::remarkName suffix from email if present
func stripInboundSuffix(email string) string {
	for i := 0; i < len(email)-2; i++ {
		if email[i] == ':' && email[i+1] == ':' {
			return email[:i]
		}
	}
	return email
}

// UserClientInfo holds user client information
type UserClientInfo struct {
	TgID       int64
	Email      string
	SubID      string
	ExpiryTime int64
	TotalGB    int64
	LimitIP    int
	Enable     bool
	RawData    map[string]interface{} // Store original client data for protocol-specific fields
}

// InboundSyncService handles synchronization of users across all inbounds
type InboundSyncService struct {
	apiClient *client.APIClient
	logger    *logger.Logger
	enabled   bool
}

// NewInboundSyncService creates a new inbound sync service
func NewInboundSyncService(apiClient *client.APIClient, logger *logger.Logger, enabled bool) *InboundSyncService {
	return &InboundSyncService{
		apiClient: apiClient,
		logger:    logger,
		enabled:   enabled,
	}
}

// Start begins the periodic sync check
func (s *InboundSyncService) Start(ctx context.Context, intervalHours int) {
	if !s.enabled {
		s.logger.Info("Multi-inbound sync is disabled")
		return
	}

	s.logger.Infof("Starting inbound sync service (interval: %d hours)", intervalHours)

	// Run immediately on start
	if err := s.SyncUserInbounds(); err != nil {
		s.logger.Errorf("Initial inbound sync failed: %v", err)
	}

	ticker := time.NewTicker(time.Duration(intervalHours) * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping inbound sync service")
			return
		case <-ticker.C:
			if err := s.SyncUserInbounds(); err != nil {
				s.logger.Errorf("Inbound sync failed: %v", err)
			}
		}
	}
}

// SyncUserInbounds syncs all users to all inbounds
func (s *InboundSyncService) SyncUserInbounds() error {
	s.logger.Info("Starting inbound synchronization")

	// Get all inbounds
	inbounds, err := s.apiClient.GetInbounds(context.Background())
	if err != nil {
		return fmt.Errorf("failed to get inbounds: %w", err)
	}

	if len(inbounds) == 0 {
		s.logger.Warn("No inbounds found, skipping sync")
		return nil
	}

	s.logger.Infof("Found %d inbounds", len(inbounds))

	// Collect all unique users from all inbounds
	users := s.collectAllUsers(inbounds)
	s.logger.Infof("Found %d unique users", len(users))

	if len(users) == 0 {
		s.logger.Info("No users found, nothing to sync")
		return nil
	}

	// Sync each user to all inbounds
	syncCount := 0
	errorCount := 0

	for _, userInfo := range users {
		// Track which inbounds already have this user
		inboundsWithUser := make(map[int]bool)

		// First pass: find all inbounds where user exists
		for _, inbound := range inbounds {
			inboundID := int(inbound["id"].(float64))
			if s.hasClientInInbound(userInfo.Email, inbound) {
				inboundsWithUser[inboundID] = true
			}
		}

		// Second pass: create in missing inbounds with appropriate email suffix
		for idx, inbound := range inbounds {
			inboundID := int(inbound["id"].(float64))

			// Skip if already exists
			if inboundsWithUser[inboundID] {
				continue
			}

			// Create client in this inbound
			s.logger.Infof("Creating client %s (tgID: %d) in inbound %d", userInfo.Email, userInfo.TgID, inboundID)

			if err := s.createClientInInbound(userInfo, inbound, idx); err != nil {
				s.logger.Errorf("Failed to create client %s in inbound %d: %v", userInfo.Email, inboundID, err)
				errorCount++
			} else {
				syncCount++
			}
		}
	}

	s.logger.Infof("Inbound sync completed: %d clients created, %d errors", syncCount, errorCount)
	return nil
}

// collectAllUsers collects all unique users from all inbounds
func (s *InboundSyncService) collectAllUsers(inbounds []map[string]interface{}) map[int64]*UserClientInfo {
	users := make(map[int64]*UserClientInfo)

	for _, inbound := range inbounds {
		settingsStr := ""
		if settings, ok := inbound["settings"].(string); ok {
			settingsStr = settings
		}

		clients := s.parseClients(settingsStr)

		for _, clientData := range clients {
			tgID := s.extractTgID(clientData)
			if tgID == 0 {
				continue // Skip clients without Telegram ID
			}

			// Only add if not already present (use first occurrence)
			if _, exists := users[tgID]; !exists {
				// Strip ::ibN suffix from email
				email := s.extractString(clientData, "email")
				cleanEmail := stripInboundSuffix(email)

				users[tgID] = &UserClientInfo{
					TgID:       tgID,
					Email:      cleanEmail, // Use clean email without suffix
					SubID:      s.extractString(clientData, "subId"),
					ExpiryTime: s.extractInt64(clientData, "expiryTime"),
					TotalGB:    s.extractInt64(clientData, "totalGB"),
					LimitIP:    int(s.extractInt64(clientData, "limitIp")),
					Enable:     s.extractBool(clientData, "enable"),
					RawData:    clientData,
				}
			}
		}
	}

	return users
}

// parseClients parses clients from inbound settings JSON
func (s *InboundSyncService) parseClients(settingsStr string) []map[string]interface{} {
	var clients []map[string]interface{}

	if settingsStr == "" {
		return clients
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
		s.logger.Errorf("Failed to parse settings JSON: %v", err)
		return clients
	}

	clientsArray, ok := settings["clients"].([]interface{})
	if !ok {
		return clients
	}

	for _, c := range clientsArray {
		if clientMap, ok := c.(map[string]interface{}); ok {
			clients = append(clients, clientMap)
		}
	}

	return clients
}

// hasClientInInbound checks if client exists in inbound
func (s *InboundSyncService) hasClientInInbound(email string, inbound map[string]interface{}) bool {
	settingsStr := ""
	if settings, ok := inbound["settings"].(string); ok {
		settingsStr = settings
	}

	clients := s.parseClients(settingsStr)
	for _, client := range clients {
		if s.extractString(client, "email") == email {
			return true
		}
	}

	return false
}

// createClientInInbound creates a client in the specified inbound
func (s *InboundSyncService) createClientInInbound(userInfo *UserClientInfo, inbound map[string]interface{}, _ int) error {
	inboundID := int(inbound["id"].(float64))
	protocol := ""
	if p, ok := inbound["protocol"].(string); ok {
		protocol = p
	}

	// Extract inbound remark (name) for suffix
	inboundRemark := ""
	if remark, ok := inbound["remark"].(string); ok && remark != "" {
		inboundRemark = remark
	} else {
		inboundRemark = fmt.Sprintf("inbound%d", inboundID)
	}

	// Add unique suffix to email to avoid duplicate errors across inbounds
	// Format: email::remarkName
	emailForInbound := fmt.Sprintf("%s::%s", userInfo.Email, inboundRemark)

	// Build client data with same parameters
	clientData := map[string]interface{}{
		"email":      emailForInbound,
		"enable":     userInfo.Enable,
		"expiryTime": userInfo.ExpiryTime,
		"totalGB":    userInfo.TotalGB,
		"tgId":       userInfo.TgID,
		"subId":      userInfo.SubID, // CRITICAL: same subId for unified subscription
		"limitIp":    userInfo.LimitIP,
		"reset":      0,
	}

	// Add protocol-specific fields
	s.addProtocolFields(clientData, protocol, inbound)

	return s.apiClient.AddClient(context.Background(), inboundID, clientData)
}

// addProtocolFields adds protocol-specific fields to client data
func (s *InboundSyncService) addProtocolFields(clientData map[string]interface{}, protocol string, inbound map[string]interface{}) {
	switch protocol {
	case "vless":
		clientData["id"] = s.generateUUID()
		clientData["flow"] = ""
	case "vmess":
		clientData["id"] = s.generateUUID()
		clientData["alterId"] = 0
	case "trojan":
		clientData["password"] = s.generatePassword()
	case "shadowsocks":
		// Shadowsocks uses method from inbound settings
		if settingsStr, ok := inbound["settings"].(string); ok {
			var settings map[string]interface{}
			if err := json.Unmarshal([]byte(settingsStr), &settings); err == nil {
				if method, ok := settings["method"].(string); ok {
					clientData["method"] = method
				}
			}
		}
		clientData["password"] = s.generatePassword()
	}
}

// Helper functions for extracting data
func (s *InboundSyncService) extractTgID(data map[string]interface{}) int64 {
	if tgID, ok := data["tgId"].(float64); ok {
		return int64(tgID)
	}
	if tgID, ok := data["tgId"].(int64); ok {
		return tgID
	}
	return 0
}

func (s *InboundSyncService) extractString(data map[string]interface{}, key string) string {
	if val, ok := data[key].(string); ok {
		return val
	}
	return ""
}

func (s *InboundSyncService) extractInt64(data map[string]interface{}, key string) int64 {
	if val, ok := data[key].(float64); ok {
		return int64(val)
	}
	if val, ok := data[key].(int64); ok {
		return val
	}
	return 0
}

func (s *InboundSyncService) extractBool(data map[string]interface{}, key string) bool {
	if val, ok := data[key].(bool); ok {
		return val
	}
	return true // Default to enabled
}

// generateUUID generates a random UUID for vless/vmess
func (s *InboundSyncService) generateUUID() string {
	// Simple UUID v4 generation
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		s.randomBytes(4),
		s.randomBytes(2),
		0x4000|s.randomBytes(2)&0x0FFF,
		0x8000|s.randomBytes(2)&0x3FFF,
		s.randomBytes(6))
}

// generatePassword generates a random password for trojan/shadowsocks
func (s *InboundSyncService) generatePassword() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 16)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// randomBytes generates random bytes for UUID
func (s *InboundSyncService) randomBytes(n int) uint64 {
	return uint64(time.Now().UnixNano()) % (1 << (n * 8))
}
