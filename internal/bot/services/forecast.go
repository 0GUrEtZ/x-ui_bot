package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"x-ui-bot/internal/config"
	"x-ui-bot/internal/logger"
	"x-ui-bot/internal/storage"
	"x-ui-bot/pkg/client"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// ForecastService collects traffic snapshots and calculates forecast
type ForecastService struct {
	apiClient             *client.APIClient
	storage               storage.Storage
	cfg                   *config.Config
	bot                   *telego.Bot
	log                   *logger.Logger
	ticker                *time.Ticker
	stopChan              chan struct{}
	closeOnce             sync.Once
	alertedThreshold      map[int]bool // per-inbound threshold alert state
	alertedPercent        map[int]bool // per-inbound percent alert state
	alertedTotalThreshold bool         // total traffic threshold alert state
	alertedTotalPercent   bool         // total traffic percent alert state
}

// NewForecastService creates a new ForecastService
func NewForecastService(apiClient *client.APIClient, store storage.Storage, bot *telego.Bot, cfg *config.Config, log *logger.Logger) *ForecastService {
	return &ForecastService{
		apiClient:        apiClient,
		storage:          store,
		bot:              bot,
		cfg:              cfg,
		log:              log,
		stopChan:         make(chan struct{}),
		closeOnce:        sync.Once{},
		alertedThreshold: make(map[int]bool),
		alertedPercent:   make(map[int]bool),
	}
}

// CollectTrafficData pulls inbound traffic and saves snapshots per inbound
func (s *ForecastService) CollectTrafficData() error {
	s.log.Infof("Collecting traffic data")
	inbounds, err := s.apiClient.GetInbounds(context.Background())
	if err != nil {
		s.log.Errorf("Failed to get inbounds from API: %v", err)
		return err
	}

	now := time.Now().UTC()
	for _, inbound := range inbounds {
		up := int64(0)
		down := int64(0)

		// Parse up/down from inbound (they're typically at top level)
		if v, ok := inbound["up"].(float64); ok {
			up = int64(v)
		}
		if v, ok := inbound["down"].(float64); ok {
			down = int64(v)
		}

		inboundID := 0
		if v, ok := inbound["id"].(float64); ok {
			inboundID = int(v)
		}

		snapshot := &storage.TrafficSnapshot{
			InboundID:     inboundID,
			Timestamp:     now,
			UploadBytes:   up,
			DownloadBytes: down,
			TotalBytes:    up + down,
		}

		if err := s.storage.SaveTrafficSnapshot(snapshot); err != nil {
			s.log.Errorf("Failed to save traffic snapshot for inbound %d: %v", inboundID, err)
			return err
		}
	}
	s.log.Infof("Saved traffic snapshots for %d inbounds", len(inbounds))

	// Check alerts for each inbound
	if s.cfg != nil && s.bot != nil {
		for _, inbound := range inbounds {
			inboundID := 0
			if v, ok := inbound["id"].(float64); ok {
				inboundID = int(v)
			}
			forecast, err := s.CalculateForecast(inboundID)
			if err == nil {
				s.evaluateAlerts(inboundID, forecast)
			} else {
				s.log.Debugf("CalculateForecast for inbound %d: %v", inboundID, err)
			}
		}

		// Check total traffic alerts
		totalForecast, err := s.CalculateTotalForecast()
		if err == nil {
			s.evaluateTotalAlerts(totalForecast)
		} else {
			s.log.Debugf("CalculateTotalForecast: %v", err)
		}
	}
	return nil
}

// notifyAdmins sends message to all configured admin IDs
func (s *ForecastService) notifyAdmins(message string) {
	if s.cfg == nil || s.bot == nil {
		s.log.Warn("notifyAdmins: missing cfg or bot, skipping notifications")
		return
	}

	ctx := context.Background()
	for _, adminID := range s.cfg.Telegram.AdminIDs {
		_, err := s.bot.SendMessage(ctx, &telego.SendMessageParams{
			ChatID:    tu.ID(adminID),
			Text:      message,
			ParseMode: "HTML",
		})
		if err != nil {
			s.log.Errorf("Failed to send forecast alert to admin %d: %v", adminID, err)
		}
	}
}

// evaluateAlerts checks crossing thresholds and sends notifications only when crossing (per-inbound)
func (s *ForecastService) evaluateAlerts(inboundID int, forecast *TrafficForecast) {
	// Determine base threshold in bytes: prefer TrafficAlertThresholdGB; otherwise use TrafficLimitGB
	thresholdGB := int64(s.cfg.Panel.TrafficAlertThresholdGB)
	if thresholdGB <= 0 {
		thresholdGB = int64(s.cfg.Panel.TrafficLimitGB)
	}
	if thresholdGB <= 0 {
		// nothing to evaluate
		return
	}
	thresholdBytes := thresholdGB * 1024 * 1024 * 1024

	// Check percent threshold
	percent := s.cfg.Panel.TrafficAlertPercent
	if percent <= 0 {
		percent = 90
	}
	percentBytes := thresholdBytes * int64(percent) / 100

	// Crossing percent threshold for this inbound
	if !s.alertedPercent[inboundID] && forecast.PredictedTotal >= percentBytes {
		// send percent alert
		alert := fmt.Sprintf("⚠️ Инбаунд #%d: Прогноз трафика достиг %d%% от порога (%d GB)\n\n%s", inboundID, percent, thresholdGB, s.FormatForecastMessage(forecast))
		s.notifyAdmins(alert)
		s.alertedPercent[inboundID] = true
	}
	if s.alertedPercent[inboundID] && forecast.PredictedTotal < percentBytes {
		// reset flag when below
		s.alertedPercent[inboundID] = false
	}

	// Crossing absolute threshold for this inbound
	if !s.alertedThreshold[inboundID] && forecast.PredictedTotal >= thresholdBytes {
		alert := fmt.Sprintf("⚠️ Инбаунд #%d: Прогноз трафика превысил порог %d GB\n\n%s", inboundID, thresholdGB, s.FormatForecastMessage(forecast))
		s.notifyAdmins(alert)
		s.alertedThreshold[inboundID] = true
	}
	if s.alertedThreshold[inboundID] && forecast.PredictedTotal < thresholdBytes {
		s.alertedThreshold[inboundID] = false
	}
}

// evaluateTotalAlerts checks crossing thresholds for total traffic and sends notifications
func (s *ForecastService) evaluateTotalAlerts(forecast *TrafficForecast) {
	// Determine base threshold in bytes: prefer TrafficAlertThresholdGB; otherwise use TrafficLimitGB
	thresholdGB := int64(s.cfg.Panel.TrafficAlertThresholdGB)
	if thresholdGB <= 0 {
		thresholdGB = int64(s.cfg.Panel.TrafficLimitGB)
	}
	if thresholdGB <= 0 {
		// nothing to evaluate
		return
	}
	thresholdBytes := thresholdGB * 1024 * 1024 * 1024

	// Check percent threshold
	percent := s.cfg.Panel.TrafficAlertPercent
	if percent <= 0 {
		percent = 90
	}
	percentBytes := thresholdBytes * int64(percent) / 100

	// Crossing percent threshold for total traffic
	if !s.alertedTotalPercent && forecast.PredictedTotal >= percentBytes {
		alert := fmt.Sprintf("⚠️ ОБЩИЙ ТРАФИК: Прогноз достиг %d%% от порога (%d GB)\n\n%s", percent, thresholdGB, s.FormatForecastMessage(forecast))
		s.notifyAdmins(alert)
		s.alertedTotalPercent = true
	}
	if s.alertedTotalPercent && forecast.PredictedTotal < percentBytes {
		s.alertedTotalPercent = false
	}

	// Crossing absolute threshold for total traffic
	if !s.alertedTotalThreshold && forecast.PredictedTotal >= thresholdBytes {
		alert := fmt.Sprintf("⚠️ ОБЩИЙ ТРАФИК: Прогноз превысил порог %d GB\n\n%s", thresholdGB, s.FormatForecastMessage(forecast))
		s.notifyAdmins(alert)
		s.alertedTotalThreshold = true
	}
	if s.alertedTotalThreshold && forecast.PredictedTotal < thresholdBytes {
		s.alertedTotalThreshold = false
	}
}

// StartScheduler starts the periodic collection using a 4-hour ticker and daily cleanup
func (s *ForecastService) StartScheduler(ctx context.Context) {
	// Collect immediately
	_ = s.CollectTrafficData()

	// Start data collection ticker (every 4 hours)
	s.ticker = time.NewTicker(4 * time.Hour)
	// Start cleanup ticker (every 24 hours)
	cleanupTicker := time.NewTicker(24 * time.Hour)

	go func() {
		for {
			select {
			case <-ctx.Done():
				s.log.Infof("ForecastService scheduler stopped")
				if s.ticker != nil {
					s.ticker.Stop()
				}
				cleanupTicker.Stop()
				return
			case <-s.ticker.C:
				if err := s.CollectTrafficData(); err != nil {
					s.log.Errorf("CollectTrafficData failed: %v", err)
				}
			case <-cleanupTicker.C:
				// Delete traffic snapshots older than 30 days
				cutoff := time.Now().Add(-30 * 24 * time.Hour)
				if err := s.storage.DeleteOldTrafficSnapshots(cutoff); err != nil {
					s.log.Errorf("Failed to delete old traffic snapshots: %v", err)
				} else {
					s.log.Infof("Deleted traffic snapshots older than 30 days")
				}
			}
		}
	}()
}

// Stop stops the scheduler
func (s *ForecastService) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	if s.stopChan != nil {
		s.closeOnce.Do(func() { close(s.stopChan) })
	}
}

// TrafficForecast result structure
type TrafficForecast struct {
	CurrentTotal   int64
	PredictedTotal int64
	AveragePerDay  int64
	DaysInMonth    int
	DaysElapsed    int
	DaysRemaining  int
	LastUpdate     time.Time
}

// CalculateForecast builds a simple forecast to the end of the month for a specific inbound
func (s *ForecastService) CalculateForecast(inboundID int) (*TrafficForecast, error) {
	now := time.Now().UTC()
	year, month, _ := now.Date()
	loc := time.UTC
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, loc)

	snapshots, err := s.storage.GetTrafficSnapshots(inboundID, monthStart, now)
	if err != nil {
		return nil, err
	}
	if len(snapshots) < 2 {
		return nil, fmt.Errorf("not enough data to build forecast")
	}

	// Compute total bytes consumed between snapshots, handling potential counter resets
	totalConsumed := int64(0)
	for i := 1; i < len(snapshots); i++ {
		prev := snapshots[i-1]
		curr := snapshots[i]
		delta := curr.TotalBytes - prev.TotalBytes
		if delta < 0 {
			// Counter reset detected: treat delta as curr.TotalBytes
			delta = curr.TotalBytes
		}
		totalConsumed += delta
	}

	// Calculate forecast using hours for better precision
	hoursElapsed := now.Sub(monthStart).Hours()
	if hoursElapsed <= 0 {
		hoursElapsed = 1 // Avoid division by zero
	}

	// Calculate end of month
	nextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, loc)
	hoursInMonth := nextMonth.Sub(monthStart).Hours()
	hoursRemaining := hoursInMonth - hoursElapsed
	if hoursRemaining < 0 {
		hoursRemaining = 0
	}

	bytesPerHour := float64(totalConsumed) / hoursElapsed
	predictedTotal := totalConsumed + int64(bytesPerHour*hoursRemaining)
	averagePerDay := int64(bytesPerHour * 24)

	// For display purposes
	daysInMonth := int(hoursInMonth / 24)
	daysElapsed := int(hoursElapsed / 24)
	daysRemaining := int(hoursRemaining / 24)

	return &TrafficForecast{
		CurrentTotal:   totalConsumed,
		PredictedTotal: predictedTotal,
		AveragePerDay:  averagePerDay,
		DaysInMonth:    daysInMonth,
		DaysElapsed:    daysElapsed,
		DaysRemaining:  daysRemaining,
		LastUpdate:     time.Now().UTC(),
	}, nil
}

// CalculateTotalForecast builds a forecast for total traffic across all inbounds
func (s *ForecastService) CalculateTotalForecast() (*TrafficForecast, error) {
	now := time.Now().UTC()
	year, month, _ := now.Date()
	loc := time.UTC
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, loc)

	// Get all inbounds to sum their traffic
	inbounds, err := s.apiClient.GetInbounds(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get inbounds: %w", err)
	}

	totalConsumed := int64(0)
	totalSnapshots := 0

	for _, inbound := range inbounds {
		inboundID := 0
		if id, ok := inbound["id"].(float64); ok {
			inboundID = int(id)
		}

		snapshots, err := s.storage.GetTrafficSnapshots(inboundID, monthStart, now)
		if err != nil {
			s.log.Debugf("Failed to get snapshots for inbound %d: %v", inboundID, err)
			continue
		}
		if len(snapshots) < 2 {
			continue
		}

		// Compute total bytes consumed for this inbound
		for i := 1; i < len(snapshots); i++ {
			prev := snapshots[i-1]
			curr := snapshots[i]
			delta := curr.TotalBytes - prev.TotalBytes
			if delta < 0 {
				// Counter reset detected: treat delta as curr.TotalBytes
				delta = curr.TotalBytes
			}
			totalConsumed += delta
		}
		totalSnapshots += len(snapshots) - 1 // number of deltas
	}

	if totalSnapshots < 2 {
		return nil, fmt.Errorf("not enough data to build total forecast")
	}

	// Calculate forecast using hours for better precision
	hoursElapsed := now.Sub(monthStart).Hours()
	if hoursElapsed <= 0 {
		hoursElapsed = 1 // Avoid division by zero
	}

	// Calculate end of month
	nextMonth := time.Date(year, month+1, 1, 0, 0, 0, 0, loc)
	hoursInMonth := nextMonth.Sub(monthStart).Hours()
	hoursRemaining := hoursInMonth - hoursElapsed
	if hoursRemaining < 0 {
		hoursRemaining = 0
	}

	bytesPerHour := float64(totalConsumed) / hoursElapsed
	predictedTotal := totalConsumed + int64(bytesPerHour*hoursRemaining)
	averagePerDay := int64(bytesPerHour * 24)

	// For display purposes
	daysInMonth := int(hoursInMonth / 24)
	daysElapsed := int(hoursElapsed / 24)
	daysRemaining := int(hoursRemaining / 24)

	return &TrafficForecast{
		CurrentTotal:   totalConsumed,
		PredictedTotal: predictedTotal,
		AveragePerDay:  averagePerDay,
		DaysInMonth:    daysInMonth,
		DaysElapsed:    daysElapsed,
		DaysRemaining:  daysRemaining,
		LastUpdate:     time.Now().UTC(),
	}, nil
}

// FormatBytes human-friendly format
func (s *ForecastService) FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	if bytes >= TB {
		return fmt.Sprintf("%.2f TB", float64(bytes)/float64(TB))
	}
	if bytes >= GB {
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	}
	if bytes >= MB {
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	}
	if bytes >= KB {
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	}
	return fmt.Sprintf("%d B", bytes)
}

// FormatForecastMessage prepares a nice message for admin
func (s *ForecastService) FormatForecastMessage(forecast *TrafficForecast) string {
	return fmt.Sprintf(
		"📊 Прогноз трафика на текущий месяц\n\n📈 Текущий расход: %s\n🔮 Прогноз до конца месяца: %s\n📉 Средний расход в день: %s\n\n⏱ Дней прошло: %d / %d\n⏳ Дней осталось: %d\n🕐 Обновлено: %s",
		s.FormatBytes(forecast.CurrentTotal),
		s.FormatBytes(forecast.PredictedTotal),
		s.FormatBytes(forecast.AveragePerDay),
		forecast.DaysElapsed, forecast.DaysInMonth, forecast.DaysRemaining, forecast.LastUpdate.Format("02.01.2006 15:04"),
	)
}
