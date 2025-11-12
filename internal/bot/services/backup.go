package services

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"x-ui-bot/internal/config"
	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BackupService handles database backup logic
type BackupService struct {
	apiClient *client.APIClient
	bot       *telego.Bot
	config    *config.Config
	logger    *logger.Logger
	stopChan  chan struct{}
}

// NewBackupService creates a new backup service
func NewBackupService(apiClient *client.APIClient, bot *telego.Bot, cfg *config.Config, log *logger.Logger) *BackupService {
	return &BackupService{
		apiClient: apiClient,
		bot:       bot,
		config:    cfg,
		logger:    log,
		stopChan:  make(chan struct{}),
	}
}

// StartScheduler starts the backup scheduler
func (s *BackupService) StartScheduler(ctx context.Context) {
	if s.config.Panel.BackupDays <= 0 {
		s.logger.Info("Backup scheduler disabled")
		return
	}

	interval := time.Duration(s.config.Panel.BackupDays) * 24 * time.Hour
	s.logger.WithField("interval", interval).Info("Starting backup scheduler")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.logger.Info("Starting scheduled database backup")
			if err := s.PerformBackup(); err != nil {
				s.logger.ErrorErr(err, "Scheduled backup failed")
			}
		case <-s.stopChan:
			s.logger.Info("Stopping backup scheduler")
			return
		case <-ctx.Done():
			s.logger.Info("Backup scheduler stopped due to context cancellation")
			return
		}
	}
}

// Stop stops the backup scheduler
func (s *BackupService) Stop() {
	close(s.stopChan)
}

// PerformBackup performs a database backup and sends it to admins
func (s *BackupService) PerformBackup() error {
	dbFile, err := s.apiClient.GetDatabaseBackup()
	if err != nil {
		return fmt.Errorf("failed to get database backup: %w", err)
	}

	// Send to all admins
	for _, adminID := range s.config.Telegram.AdminIDs {
		if err := s.SendBackupToAdmin(adminID, dbFile); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"admin_id": adminID,
				"error":    err,
			}).Error("Failed to send backup to admin")
			continue
		}
		s.logger.WithField("admin_id", adminID).Info("Backup sent to admin")
	}

	return nil
}

// SendBackupToAdmin sends backup file to a specific admin
func (s *BackupService) SendBackupToAdmin(adminID int64, dbFile []byte) error {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("x-ui-backup_%s.db", timestamp)

	reader := bytes.NewReader(dbFile)

	_, err := s.bot.SendDocument(context.Background(), &telego.SendDocumentParams{
		ChatID: tu.ID(adminID),
		Document: telego.InputFile{
			File: tu.NameReader(reader, filename),
		},
		Caption: fmt.Sprintf("📦 Бэкап базы данных\n🕐 %s", time.Now().Format("2006-01-02 15:04:05")),
	})

	return err
}
