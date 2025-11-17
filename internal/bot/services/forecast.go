package services

import (
	"fmt"
	"sync"
	"time"

	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"
	"x-ui-bot/sqlite"
)

// ForecastService handles traffic forecasting calculations
type ForecastService struct {
	apiClient *client.APIClient
	storage   sqlite.Storage
	logger    *logger.Logger
	cache     *forecastCache
	stopChan  chan struct{}
}

// ForecastData contains calculated forecast information
type ForecastData struct {
	AnalysisPeriodDays int
	ActiveClientsCount int
	DailyAvgBytes      uint64
	WeeklyAvgBytes     uint64
	MonthlyAvgBytes    uint64
	Forecast7Days      uint64
	Forecast14Days     uint64
	Forecast30Days     uint64
	LastUpdate         time.Time
}

// forecastCache holds cached forecast data
type forecastCache struct {
	data      *ForecastData
	timestamp time.Time
	mu        sync.RWMutex
}

// NewForecastService creates a new forecast service
func NewForecastService(apiClient *client.APIClient, storage sqlite.Storage, logger *logger.Logger) *ForecastService {
	return &ForecastService{
		apiClient: apiClient,
		storage:   storage,
		logger:    logger,
		cache: &forecastCache{
			timestamp: time.Now().Add(-time.Hour), // Expired cache initially
		},
		stopChan: make(chan struct{}),
	}
}

// CalculateForecast calculates traffic forecast based on historical data
func (fs *ForecastService) CalculateForecast() (*ForecastData, error) {
	// Check cache first
	fs.cache.mu.RLock()
	if time.Since(fs.cache.timestamp) < time.Hour && fs.cache.data != nil {
		data := *fs.cache.data // Copy data
		fs.cache.mu.RUnlock()
		fs.logger.Info("Returning forecast from cache")
		return &data, nil
	}
	fs.cache.mu.RUnlock()

	fs.logger.Info("Calculating new forecast")

	// Check if we need to collect today's snapshot
	today := time.Now().Format("2006-01-02")
	if err := fs.ensureTodaySnapshot(today); err != nil {
		fs.logger.Errorf("Failed to collect traffic snapshot: %v", err)
		// Continue with existing data if snapshot collection fails
	}

	// Get traffic history for the last 30 days
	records, err := fs.storage.GetTrafficHistory(30)
	if err != nil {
		return nil, fmt.Errorf("failed to get traffic history: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("no traffic data available")
	}

	// Calculate forecast
	data, err := fs.calculateFromRecords(records)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate forecast: %w", err)
	}

	// Update cache
	fs.cache.mu.Lock()
	fs.cache.data = data
	fs.cache.timestamp = time.Now()
	fs.cache.mu.Unlock()

	fs.logger.Infof("Forecast calculated successfully - analysis_days: %d, clients: %d, daily_avg: %d",
		data.AnalysisPeriodDays, data.ActiveClientsCount, data.DailyAvgBytes)

	return data, nil
}

// ensureTodaySnapshot collects traffic snapshot for today if not exists
func (fs *ForecastService) ensureTodaySnapshot(today string) error {
	// Check if today's snapshot already exists
	records, err := fs.storage.GetTrafficHistory(1)
	if err != nil {
		return fmt.Errorf("failed to check existing snapshots: %w", err)
	}

	// Check if we have today's data
	hasTodayData := false
	for _, record := range records {
		if record.Date == today {
			hasTodayData = true
			break
		}
	}

	if hasTodayData {
		fs.logger.Debug("Today's traffic snapshot already exists")
		return nil
	}

	fs.logger.Info("Collecting today's traffic snapshot")

	// Collect new snapshot from API
	if err := fs.CollectTrafficSnapshot(); err != nil {
		return fmt.Errorf("failed to collect traffic snapshot: %w", err)
	}

	// Cleanup old data
	if err := fs.CleanupOldData(); err != nil {
		fs.logger.Warnf("Failed to cleanup old traffic data: %v", err)
		// Don't fail the whole operation for cleanup errors
	}

	return nil
}

// CollectTrafficSnapshot collects current traffic data from 3X-UI API
func (fs *ForecastService) CollectTrafficSnapshot() error {
	inbounds, err := fs.apiClient.GetInbounds()
	if err != nil {
		return fmt.Errorf("failed to get inbounds from API: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	collectedCount := 0

	for _, inbound := range inbounds {
		if inbound == nil {
			continue
		}

		// Get inbound ID
		inboundID, ok := inbound["id"].(float64)
		if !ok {
			fs.logger.Warn("Inbound missing ID field")
			continue
		}

		// Get client traffic statistics for this inbound
		clientStats, err := fs.apiClient.GetClientTrafficsById(int(inboundID))
		if err != nil {
			fs.logger.Errorf("Failed to get client traffics for inbound %d: %v", int(inboundID), err)
			continue
		}

		// Process each client's traffic data
		for _, clientData := range clientStats {
			email, ok := clientData["email"].(string)
			if !ok || email == "" {
				continue
			}

			// Get traffic stats (up and down are in bytes)
			var totalBytes uint64
			if up, ok := clientData["up"].(float64); ok {
				totalBytes += uint64(up)
			}
			if down, ok := clientData["down"].(float64); ok {
				totalBytes += uint64(down)
			}

			// Skip if no traffic data
			if totalBytes == 0 {
				continue
			}

			// Save snapshot
			if err := fs.storage.SaveTrafficSnapshot(today, email, totalBytes); err != nil {
				fs.logger.Errorf("Failed to save traffic snapshot for %s: %v", email, err)
				continue
			}

			collectedCount++
		}
	}

	fs.logger.Infof("Traffic snapshot collection completed - snapshots: %d", collectedCount)
	return nil
}

// CleanupOldData removes traffic data older than 30 days
func (fs *ForecastService) CleanupOldData() error {
	err := fs.storage.CleanupOldTraffic(30)
	if err != nil {
		return fmt.Errorf("failed to cleanup old traffic data: %w", err)
	}

	fs.logger.Info("Old traffic data cleanup completed")
	return nil
}

// calculateFromRecords calculates forecast from traffic records
func (fs *ForecastService) calculateFromRecords(records []sqlite.TrafficRecord) (*ForecastData, error) {
	if len(records) == 0 {
		return nil, fmt.Errorf("no records to analyze")
	}

	// Group by date to get daily totals
	dailyTotals := make(map[string]uint64)
	clientEmails := make(map[string]bool)

	for _, record := range records {
		dailyTotals[record.Date] += record.TotalBytes
		clientEmails[record.Email] = true
	}

	// Calculate metrics
	totalTraffic := uint64(0)
	for _, daily := range dailyTotals {
		totalTraffic += daily
	}

	daysCount := len(dailyTotals)
	if daysCount == 0 {
		return nil, fmt.Errorf("no daily data available")
	}

	dailyAvg := totalTraffic / uint64(daysCount)
	weeklyAvg := dailyAvg * 7
	monthlyAvg := dailyAvg * 30

	data := &ForecastData{
		AnalysisPeriodDays: daysCount,
		ActiveClientsCount: len(clientEmails),
		DailyAvgBytes:      dailyAvg,
		WeeklyAvgBytes:     weeklyAvg,
		MonthlyAvgBytes:    monthlyAvg,
		Forecast7Days:      dailyAvg * 7,
		Forecast14Days:     dailyAvg * 14,
		Forecast30Days:     dailyAvg * 30,
		LastUpdate:         time.Now(),
	}

	return data, nil
}

// FormatForecastMessage formats forecast data into a readable message
func (fs *ForecastService) FormatForecastMessage(data *ForecastData) string {
	formatBytes := func(bytes uint64) string {
		gb := float64(bytes) / (1024 * 1024 * 1024)
		return fmt.Sprintf("%.2f", gb)
	}

	message := fmt.Sprintf(`📊 Прогноз потребления трафика

📅 Период анализа: %d дней
👥 Активных клиентов: %d

📈 Средний расход:
├─ В день: %s GB
├─ В неделю: %s GB
└─ В месяц: %s GB

🔮 Прогноз на следующие:
├─ 7 дней: %s GB
├─ 14 дней: %s GB
└─ 30 дней: %s GB

⚙️ Последнее обновление: %s`,
		data.AnalysisPeriodDays,
		data.ActiveClientsCount,
		formatBytes(data.DailyAvgBytes),
		formatBytes(data.WeeklyAvgBytes),
		formatBytes(data.MonthlyAvgBytes),
		formatBytes(data.Forecast7Days),
		formatBytes(data.Forecast14Days),
		formatBytes(data.Forecast30Days),
		data.LastUpdate.Format("02.01.2006 15:04"),
	)

	return message
}

// StartPeriodicCollection starts automatic traffic data collection every 4 hours
func (fs *ForecastService) StartPeriodicCollection() {
	fs.logger.Info("Starting periodic traffic collection (every 4 hours)")

	// Collect initial snapshot immediately
	go func() {
		if err := fs.collectAndCleanup(); err != nil {
			fs.logger.Errorf("Failed initial traffic collection: %v", err)
		}
	}()

	// Start periodic collection
	go func() {
		ticker := time.NewTicker(4 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := fs.collectAndCleanup(); err != nil {
					fs.logger.Errorf("Failed periodic traffic collection: %v", err)
				}
			case <-fs.stopChan:
				fs.logger.Info("Stopping periodic traffic collection")
				return
			}
		}
	}()
}

// StopPeriodicCollection stops the periodic traffic collection
func (fs *ForecastService) StopPeriodicCollection() {
	close(fs.stopChan)
}

// collectAndCleanup collects traffic snapshot and cleans up old data
func (fs *ForecastService) collectAndCleanup() error {
	fs.logger.Info("Running scheduled traffic collection")

	// Collect snapshot
	if err := fs.CollectTrafficSnapshot(); err != nil {
		return fmt.Errorf("failed to collect snapshot: %w", err)
	}

	// Cleanup old data (older than 30 days)
	if err := fs.CleanupOldData(); err != nil {
		fs.logger.Warnf("Failed to cleanup old traffic data: %v", err)
		// Don't fail the whole operation for cleanup errors
	}

	fs.logger.Info("Scheduled traffic collection completed successfully")
	return nil
}
