package services

import (
	"context"
	"time"

	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"
)

// TrafficSyncService handles synchronization of traffic between inbounds
type TrafficSyncService struct {
	apiClient     *client.APIClient
	clientService *ClientService
	logger        *logger.Logger
	enabled       bool
	syncHours     int
}

// NewTrafficSyncService creates a new traffic sync service
func NewTrafficSyncService(apiClient *client.APIClient, clientService *ClientService, logger *logger.Logger, syncHours int) *TrafficSyncService {
	return &TrafficSyncService{
		apiClient:     apiClient,
		clientService: clientService,
		logger:        logger,
		enabled:       syncHours > 0,
		syncHours:     syncHours,
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
		inboundID := int(inbound["id"].(float64))
		settingsStr := ""
		if settings, ok := inbound["settings"].(string); ok {
			settingsStr = settings
		}

		ts.logger.Debugf("Parsing clients for inbound %d", inboundID)

		// Parse clients from settings to get tgId
		clients, err := ts.clientService.ParseClients(settingsStr)
		if err != nil {
			ts.logger.Errorf("Failed to parse clients for inbound %d: %v", inboundID, err)
			continue
		}

		ts.logger.Debugf("Found %d clients in inbound %d", len(clients), inboundID)

		for _, client := range clients {
			email := client["email"]
			tgID := client["tgId"]

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
		// Skip users without valid tgId
		if tgID == "" || tgID == "0" {
			ts.logger.Debugf("Skipping user with empty/zero tgId")
			continue
		}

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
			email, _ := statMap["email"].(string)

			if err := ts.updateClientTraffic(ctx, email, totalUp, totalDown); err != nil {
				ts.logger.Errorf("Failed to update traffic for %s (tgId=%s) in inbound %d: %v", email, tgID, inboundID, err)
			} else {
				synced++
				ts.logger.Infof("Updated traffic for %s (tgId=%s): up=%d, down=%d", email, tgID, totalUp, totalDown)
			}
		}
	}

	ts.logger.Infof("Traffic sync completed: updated %d clients", synced)
}

// updateClientTraffic updates traffic for a specific client using the x-ui API
func (ts *TrafficSyncService) updateClientTraffic(ctx context.Context, email string, up int64, down int64) error {
	return ts.apiClient.UpdateClientTraffic(ctx, email, up, down)
}
