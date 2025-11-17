package services

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"x-ui-bot/internal/config"
	"x-ui-bot/internal/logger"
	"x-ui-bot/internal/panel"
	"x-ui-bot/pkg/client"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BackupService handles database backup logic
type BackupService struct {
	apiClient    *client.APIClient
	bot          *telego.Bot
	config       *config.Config
	logger       *logger.Logger
	stopChan     chan struct{}
	panelManager *panel.PanelManager
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

// NewBackupServiceWithPanelManager creates a new backup service with panel manager support
func NewBackupServiceWithPanelManager(panelManager *panel.PanelManager, bot *telego.Bot, cfg *config.Config, log *logger.Logger) *BackupService {
	return &BackupService{
		bot:          bot,
		config:       cfg,
		logger:       log,
		stopChan:     make(chan struct{}),
		panelManager: panelManager,
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
	// If we have panel manager, backup from all panels
	if s.panelManager != nil {
		return s.performMultiPanelBackup()
	}

	// Fallback to single panel backup
	if s.apiClient == nil {
		return fmt.Errorf("no API client available for backup")
	}

	dbFile, err := s.apiClient.GetDatabaseBackup()
	if err != nil {
		return fmt.Errorf("failed to get database backup: %w", err)
	}

	return s.sendBackupToAdmins(dbFile, "x-ui")
}

// performMultiPanelBackup performs backup from all healthy panels
func (s *BackupService) performMultiPanelBackup() error {
	panels := s.panelManager.GetHealthyPanels()

	if len(panels) == 0 {
		return fmt.Errorf("no healthy panels available for backup")
	}

	for _, panel := range panels {
		client, err := s.panelManager.GetClient(panel.Name)
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"panel": panel.Name,
				"error": err,
			}).Error("Failed to get client for panel backup")
			continue
		}

		dbFile, err := client.GetDatabaseBackup()
		if err != nil {
			s.logger.WithFields(map[string]interface{}{
				"panel": panel.Name,
				"error": err,
			}).Error("Failed to get database backup from panel")
			continue
		}

		filename := fmt.Sprintf("x-ui-%s", panel.Name)
		if err := s.sendBackupToAdmins(dbFile, filename); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"panel": panel.Name,
				"error": err,
			}).Error("Failed to send backup to admins")
		} else {
			s.logger.WithField("panel", panel.Name).Info("Backup sent to admins")
		}
	}

	return nil
}

// sendBackupToAdmins sends backup file to all admins
func (s *BackupService) sendBackupToAdmins(dbFile []byte, panelName string) error {
	// Send to all admins
	for _, adminID := range s.config.Telegram.AdminIDs {
		if err := s.SendBackupToAdmin(adminID, dbFile, panelName); err != nil {
			s.logger.WithFields(map[string]interface{}{
				"admin_id": adminID,
				"panel":    panelName,
				"error":    err,
			}).Error("Failed to send backup to admin")
			continue
		}
		s.logger.WithFields(map[string]interface{}{
			"admin_id": adminID,
			"panel":    panelName,
		}).Info("Backup sent to admin")
	}

	return nil
}

// SendBackupToAdmin sends backup file to a specific admin
func (s *BackupService) SendBackupToAdmin(adminID int64, dbFile []byte, panelName string) error {
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("x-ui-%s-backup_%s.db", panelName, timestamp)

	reader := bytes.NewReader(dbFile)

	_, err := s.bot.SendDocument(context.Background(), &telego.SendDocumentParams{
		ChatID: tu.ID(adminID),
		Document: telego.InputFile{
			File: tu.NameReader(reader, filename),
		},
		Caption: fmt.Sprintf("📦 Бэкап базы данных панели %s\n🕐 %s", panelName, time.Now().Format("2006-01-02 15:04:05")),
	})

	return err
}
