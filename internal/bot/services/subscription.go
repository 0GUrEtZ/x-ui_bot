package services

import (
	"fmt"
	"time"

	"x-ui-bot/internal/logger"
)

// SubscriptionService handles subscription-related logic
type SubscriptionService struct {
	logger *logger.Logger
}

// NewSubscriptionService creates a new subscription service
func NewSubscriptionService(log *logger.Logger) *SubscriptionService {
	return &SubscriptionService{
		logger: log,
	}
}

// CalculateTimeRemaining calculates days and hours remaining until expiry
func (s *SubscriptionService) CalculateTimeRemaining(expiryTime int64) (days int, hours int) {
	if expiryTime == 0 {
		return 0, 0
	}

	now := time.Now().Unix() * 1000 // Convert to milliseconds
	remaining := expiryTime - now

	if remaining <= 0 {
		return 0, 0
	}

	remainingSeconds := remaining / 1000
	days = int(remainingSeconds / 86400)
	hours = int((remainingSeconds % 86400) / 3600)

	return days, hours
}

// GetSubscriptionStatus returns status icon and text based on time remaining
func (s *SubscriptionService) GetSubscriptionStatus(expiryTime int64) (icon string, text string) {
	if expiryTime == 0 {
		return "♾️", "Безлимитная"
	}

	days, hours := s.CalculateTimeRemaining(expiryTime)

	if days <= 0 {
		return "⛔", "Истекла"
	} else if days <= 3 {
		return "❌", fmt.Sprintf("%d дн. %d ч. (критично!)", days, hours)
	} else if days <= 7 {
		return "⚠️", fmt.Sprintf("%d дн. %d ч.", days, hours)
	}

	return "✅", fmt.Sprintf("%d дн. %d ч.", days, hours)
}

// GetTrafficStatus returns traffic status with emoji
func (s *SubscriptionService) GetTrafficStatus(used, limit int64) (percentage float64, emoji string) {
	if limit == 0 {
		return 0, "✅"
	}

	percentage = float64(used) / float64(limit) * 100

	if percentage >= 90 {
		return percentage, "❌"
	} else if percentage >= 70 {
		return percentage, "⚠️"
	}

	return percentage, "✅"
}

// FormatSubscriptionInfo formats subscription information for display
func (s *SubscriptionService) FormatSubscriptionInfo(email string, expiryTime, totalBytes, usedBytes int64) string {
	statusIcon, statusText := s.GetSubscriptionStatus(expiryTime)

	msg := fmt.Sprintf("👤 Аккаунт: %s\n", email)
	msg += fmt.Sprintf("%s Подписка: %s\n", statusIcon, statusText)

	if totalBytes > 0 {
		percentage, emoji := s.GetTrafficStatus(usedBytes, totalBytes)
		msg += fmt.Sprintf("📊 Трафик: %s / %s %s (%.1f%%)\n",
			formatBytesHelper(usedBytes),
			formatBytesHelper(totalBytes),
			emoji,
			percentage,
		)
	} else {
		msg += fmt.Sprintf("📊 Трафик: %s (безлимит)\n", formatBytesHelper(usedBytes))
	}

	return msg
}

// Helper function for formatting bytes
func formatBytesHelper(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := float64(bytes) / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.2f %s", float64(bytes)/float64(div), units[exp])
}
