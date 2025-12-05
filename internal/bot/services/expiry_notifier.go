package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"x-ui-bot/internal/logger"
	"x-ui-bot/internal/storage"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// ExpiryNotifierService handles subscription expiry notifications
type ExpiryNotifierService struct {
	bot              *telego.Bot
	storage          storage.Storage
	logger           *logger.Logger
	warningDays      []int
	checkIntervalMin int
}

// NewExpiryNotifierService creates a new expiry notifier service
func NewExpiryNotifierService(bot *telego.Bot, storage storage.Storage, logger *logger.Logger, warningDays []int) *ExpiryNotifierService {
	return &ExpiryNotifierService{
		bot:              bot,
		storage:          storage,
		logger:           logger,
		warningDays:      warningDays,
		checkIntervalMin: 60, // Check every hour
	}
}

// Start begins the periodic check for expiring subscriptions
func (s *ExpiryNotifierService) Start(ctx context.Context) {
	s.logger.Info("Starting expiry notifier service")

	// Run immediately on start
	s.checkAndNotify()

	ticker := time.NewTicker(time.Duration(s.checkIntervalMin) * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping expiry notifier service")
			return
		case <-ticker.C:
			s.checkAndNotify()
		}
	}
}

// checkAndNotify checks for expiring subscriptions and sends notifications
func (s *ExpiryNotifierService) checkAndNotify() {
	s.logger.Debug("Checking for expiring subscriptions")

	for _, days := range s.warningDays {
		// Get subscriptions expiring in N days
		expiring, err := s.storage.GetExpiringSubscriptions(days)
		if err != nil {
			s.logger.Errorf("Failed to get expiring subscriptions for %d days: %v", days, err)
			continue
		}

		for _, sub := range expiring {
			// Check if already notified for this threshold
			if s.wasAlreadyNotified(sub.NotifiedDays, days) {
				continue
			}

			// Calculate days remaining
			expiryTime := time.UnixMilli(sub.ExpiryTime)
			daysRemaining := int(time.Until(expiryTime).Hours() / 24)

			// Skip if doesn't match threshold
			if daysRemaining != days {
				continue
			}

			// Send notification
			if err := s.sendExpiryWarning(sub.TgID, sub.Email, daysRemaining, expiryTime); err != nil {
				s.logger.Errorf("Failed to send expiry warning to user %d: %v", sub.TgID, err)
				continue
			}

			// Mark as notified
			if err := s.storage.MarkSubscriptionNotified(sub.Email, strconv.Itoa(days)); err != nil {
				s.logger.Errorf("Failed to mark subscription as notified: %v", err)
			}

			s.logger.Infof("Sent %d-day expiry warning to user %d (%s)", days, sub.TgID, sub.Email)
		}
	}

	// Clean up expired subscriptions
	if err := s.storage.DeleteExpiredSubscriptions(); err != nil {
		s.logger.Errorf("Failed to delete expired subscriptions: %v", err)
	}
}

// wasAlreadyNotified checks if user was already notified for this threshold
func (s *ExpiryNotifierService) wasAlreadyNotified(notifiedDays string, days int) bool {
	if notifiedDays == "" {
		return false
	}

	daysStr := strconv.Itoa(days)
	notifiedList := strings.Split(notifiedDays, ",")
	for _, d := range notifiedList {
		if d == daysStr {
			return true
		}
	}
	return false
}

// sendExpiryWarning sends expiry warning to user
func (s *ExpiryNotifierService) sendExpiryWarning(tgID int64, email string, daysRemaining int, expiryTime time.Time) error {
	var message string
	var emoji string

	if daysRemaining <= 1 {
		emoji = "🔴"
		message = fmt.Sprintf(
			"%s <b>Срочно! Ваша подписка истекает завтра!</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"⏰ Истекает: %s\n"+
				"📅 Осталось: менее 1 дня\n\n"+
				"⚠️ Для продления подписки нажмите кнопку ниже.",
			emoji,
			email,
			expiryTime.Format("02.01.2006 15:04"),
		)
	} else if daysRemaining <= 3 {
		emoji = "⚠️"
		message = fmt.Sprintf(
			"%s <b>Внимание! Ваша подписка скоро истечёт</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"⏰ Истекает: %s\n"+
				"📅 Осталось: %d дней\n\n"+
				"Не забудьте продлить подписку!",
			emoji,
			email,
			expiryTime.Format("02.01.2006 15:04"),
			daysRemaining,
		)
	} else {
		emoji = "📅"
		message = fmt.Sprintf(
			"%s <b>Напоминание о подписке</b>\n\n"+
				"👤 Аккаунт: %s\n"+
				"⏰ Истекает: %s\n"+
				"📅 Осталось: %d дней\n\n"+
				"Напоминаем, что скоро истечёт срок вашей подписки.",
			emoji,
			email,
			expiryTime.Format("02.01.2006 15:04"),
			daysRemaining,
		)
	}

	// Add button to extend subscription
	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("⏰ Продлить подписку").WithCallbackData("extend_subscription"),
		),
	)

	_, err := s.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(tgID),
		Text:        message,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})

	return err
}
