package services

import (
	"context"
	"fmt"
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
	apiClient        *client.APIClient
	storage          storage.Storage
	cfg              *config.Config
	bot              *telego.Bot
	log              *logger.Logger
	ticker           *time.Ticker
	stopChan         chan struct{}
	alertedThreshold bool
	alertedPercent   bool
}

// NewForecastService creates a new ForecastService
func NewForecastService(apiClient *client.APIClient, store storage.Storage, bot *telego.Bot, cfg *config.Config, log *logger.Logger) *ForecastService {
	return &ForecastService{
		apiClient: apiClient,
		storage:   store,
		bot:       bot,
		cfg:       cfg,
		log:       log,
		stopChan:  make(chan struct{}),
	}
}

// CollectTrafficData pulls server status and saves a snapshot
func (s *ForecastService) CollectTrafficData() error {
	s.log.Infof("Collecting traffic data")
	status, err := s.apiClient.GetStatus()
	if err != nil {
		s.log.Errorf("Failed to get status from API: %v", err)
		return err
	}

	// Parse netIO struct (obj.netIO.up/down)
	obj, ok := status["obj"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected API response: missing obj")
	}
	netIO, ok := obj["netIO"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected API response: missing netIO")
	}

	up := int64(0)
	down := int64(0)
	if v, ok := netIO["up"].(float64); ok {
		up = int64(v)
	}
	if v, ok := netIO["down"].(float64); ok {
		down = int64(v)
	}

	snapshot := &storage.TrafficSnapshot{
		Timestamp:     time.Now().UTC(),
		UploadBytes:   up,
		DownloadBytes: down,
		TotalBytes:    up + down,
	}

	if err := s.storage.SaveTrafficSnapshot(snapshot); err != nil {
		s.log.Errorf("Failed to save traffic snapshot: %v", err)
		return err
	}
	s.log.Infof("Saved traffic snapshot: up=%d down=%d total=%d", up, down, up+down)
	// After saving, optionally calculate forecast and notify admins if threshold exceeded or percent reached
	if s.cfg != nil && s.bot != nil {
		forecast, err := s.CalculateForecast()
		if err == nil {
			s.evaluateAlerts(forecast)
		} else {
			s.log.Debugf("CalculateForecast not enough data or failed: %v", err)
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

// evaluateAlerts checks crossing thresholds and sends notifications only when crossing
func (s *ForecastService) evaluateAlerts(forecast *TrafficForecast) {
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

	// Crossing percent threshold
	if !s.alertedPercent && forecast.PredictedTotal >= percentBytes {
		// send percent alert
		alert := fmt.Sprintf("⚠️ Прогноз трафика достиг %d%% от порога (%d GB)\n\n%s", percent, thresholdGB, s.FormatForecastMessage(forecast))
		s.notifyAdmins(alert)
		s.alertedPercent = true
	}
	if s.alertedPercent && forecast.PredictedTotal < percentBytes {
		// reset flag when below
		s.alertedPercent = false
	}

	// Crossing absolute threshold
	if !s.alertedThreshold && forecast.PredictedTotal >= thresholdBytes {
		alert := fmt.Sprintf("⚠️ Прогноз трафика превысил порог %d GB\n\n%s", thresholdGB, s.FormatForecastMessage(forecast))
		s.notifyAdmins(alert)
		s.alertedThreshold = true
	}
	if s.alertedThreshold && forecast.PredictedTotal < thresholdBytes {
		s.alertedThreshold = false
	}
}

// StartScheduler starts the periodic collection using a 4-hour ticker
func (s *ForecastService) StartScheduler(ctx context.Context) {
	// Collect immediately
	_ = s.CollectTrafficData()

	s.ticker = time.NewTicker(4 * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				s.log.Infof("ForecastService scheduler stopped")
				if s.ticker != nil {
					s.ticker.Stop()
				}
				return
			case <-s.ticker.C:
				if err := s.CollectTrafficData(); err != nil {
					s.log.Errorf("CollectTrafficData failed: %v", err)
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
		close(s.stopChan)
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

// CalculateForecast builds a simple forecast to the end of the month
func (s *ForecastService) CalculateForecast() (*TrafficForecast, error) {
	now := time.Now().UTC()
	year, month, _ := now.Date()
	loc := time.UTC
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, loc)

	snapshots, err := s.storage.GetTrafficSnapshots(monthStart, now)
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

	// time span
	duration := snapshots[len(snapshots)-1].Timestamp.Sub(snapshots[0].Timestamp)
	hours := duration.Hours()
	if hours <= 0 {
		return nil, fmt.Errorf("invalid snapshot time range")
	}
	bytesPerHour := float64(totalConsumed) / hours

	// Time to month end
	nextMonth := monthStart.AddDate(0, 1, 0)
	remainingHours := nextMonth.Sub(now).Hours()
	predictedExtra := int64(bytesPerHour * remainingHours)

	currentTotal := totalConsumed // Sum for the month (we interpret totalConsumed as current usage)
	predictedTotal := currentTotal + predictedExtra

	daysInMonth := int(nextMonth.Sub(monthStart).Hours() / 24)
	daysElapsed := int(now.Sub(monthStart).Hours() / 24)
	daysRemaining := daysInMonth - daysElapsed

	forecast := &TrafficForecast{
		CurrentTotal:   currentTotal,
		PredictedTotal: predictedTotal,
		AveragePerDay:  int64(bytesPerHour * 24),
		DaysInMonth:    daysInMonth,
		DaysElapsed:    daysElapsed,
		DaysRemaining:  daysRemaining,
		LastUpdate:     time.Now().UTC(),
	}

	return forecast, nil
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
