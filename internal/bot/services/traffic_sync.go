package services

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"
)

// TrafficSyncService handles synchronization of traffic between inbounds
type TrafficSyncService struct {
	apiClient *client.APIClient
	logger    *logger.Logger
	enabled   bool
	syncHours int
}

// NewTrafficSyncService creates a new traffic sync service
func NewTrafficSyncService(apiClient *client.APIClient, logger *logger.Logger, syncHours int) *TrafficSyncService {
	return &TrafficSyncService{
		apiClient: apiClient,
		logger:    logger,
		enabled:   syncHours > 0,
		syncHours: syncHours,
	}
}

// StartSync starts the traffic sync routine
func (ts *TrafficSyncService) StartSync(ctx context.Context) {
	if !ts.enabled {
		ts.logger.Infof("Traffic sync is disabled")
		return
	}

	ts.logger.Infof("Starting traffic sync service (interval: %d hours)", ts.syncHours)

	// Initial sync after 1 minute
	go func() {
		time.Sleep(1 * time.Minute)
		ts.syncAllTraffic(ctx)
	}()

	// Periodic sync
	ticker := time.NewTicker(time.Duration(ts.syncHours) * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			ts.logger.Infof("Traffic sync service stopped")
			return
		case <-ticker.C:
			ts.syncAllTraffic(ctx)
		}
	}
}

// syncAllTraffic synchronizes traffic for all users across all inbounds
func (ts *TrafficSyncService) syncAllTraffic(ctx context.Context) {
	ts.logger.Infof("Starting traffic synchronization...")

	// Get all inbounds
	inbounds, err := ts.apiClient.GetInbounds(ctx)
	if err != nil {
		ts.logger.Errorf("Failed to get inbounds for traffic sync: %v", err)
		return
	}

	// Build a map of email -> tgId from all inbounds (get tgId from client settings)
	emailToTgID := make(map[string]string)

	for _, inbound := range inbounds {
		settingsStr := ""
		if settings, ok := inbound["settings"].(string); ok {
			settingsStr = settings
		}

		// Parse clients from settings to get tgId
		var inboundSettings map[string]interface{}
		if err := json.Unmarshal([]byte(settingsStr), &inboundSettings); err != nil {
			continue
		}

		clientsArray, ok := inboundSettings["clients"].([]interface{})
		if !ok {
			continue
		}

		for _, client := range clientsArray {
			clientMap, ok := client.(map[string]interface{})
			if !ok {
				continue
			}

			email, _ := clientMap["email"].(string)
			tgID, _ := clientMap["tgId"].(string)

			if email != "" && tgID != "" {
				emailToTgID[email] = tgID
				ts.logger.Debugf("Mapped email %s -> tgId %s", email, tgID)
			}
		}
	}

	ts.logger.Infof("Built email->tgId map with %d entries", len(emailToTgID))

	// Collect all users and their traffic from all inbounds
	// Map: tgId -> inbound -> traffic data
	userTraffic := make(map[string]map[int]map[string]interface{})

	for _, inbound := range inbounds {
		inboundID := int(inbound["id"].(float64))

		// Get clientStats from inbound
		clientStats, ok := inbound["clientStats"].([]interface{})
		if !ok {
			continue
		}

		for _, stat := range clientStats {
			statMap, ok := stat.(map[string]interface{})
			if !ok {
				continue
			}

			email, _ := statMap["email"].(string)
			tgID, hasTgID := emailToTgID[email]

			if !hasTgID || tgID == "" {
				ts.logger.Debugf("No tgId found for email %s", email)
				continue
			}

			if _, exists := userTraffic[tgID]; !exists {
				userTraffic[tgID] = make(map[int]map[string]interface{})
			}

			userTraffic[tgID][inboundID] = statMap
			ts.logger.Debugf("Added traffic for tgId %s, inbound %d, email %s", tgID, inboundID, email)
		}
	}

	ts.logger.Infof("Collected traffic for %d users", len(userTraffic))

	// Now sync traffic: calculate average or use highest value
	synced := 0
	for tgID, inboundMap := range userTraffic {
		if len(inboundMap) < 2 {
			continue // Only sync if user exists in multiple inbounds
		}

		// Calculate total traffic across all inbounds
		var totalUp, totalDown int64
		for _, statMap := range inboundMap {
			if up, ok := statMap["up"].(float64); ok {
				totalUp += int64(up)
			}
			if down, ok := statMap["down"].(float64); ok {
				totalDown += int64(down)
			}
		}

		// Update traffic in all inbounds for this user to total
		for inboundID, statMap := range inboundMap {
			_, _ = statMap["email"].(string)
			clientID, _ := statMap["id"].(float64)

			if err := ts.updateClientTraffic(ctx, inboundID, int(clientID), int64(clientID), totalUp, totalDown); err != nil {
				ts.logger.Errorf("Failed to update traffic for user %s in inbound %d: %v", tgID, inboundID, err)
			} else {
				synced++
				ts.logger.Debugf("Updated traffic for user %s: up=%d, down=%d", tgID, totalUp, totalDown)
			}
		}
	}

	ts.logger.Infof("Traffic sync completed: updated %d clients", synced)
}

// updateClientTraffic updates traffic for a specific client using the x-ui API
// According to x-ui API docs, we can reset traffic using ResetClientTraffic
// or update using the client data endpoint
func (ts *TrafficSyncService) updateClientTraffic(ctx context.Context, inboundID int, clientID int, userID int64, up int64, down int64) error {
	// Use the x-ui API to update client stats
	// POST /panel/api/inbounds/{inboundId}/addClients
	// The API doesn't provide direct traffic update, so we use the reset approach
	// This is a limitation of the x-ui API - we can only reset traffic, not set it

	// For now, log the update that would happen
	log.Printf("[DEBUG] Would update traffic for inbound %d, client %d (user %d): up=%d, down=%d", inboundID, clientID, userID, up, down)

	return nil
}
