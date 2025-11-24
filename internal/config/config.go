package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration
type Config struct {
	Panel    PanelConfig    `yaml:"panel"`  // Legacy single panel config
	Panels   []PanelConfig  `yaml:"panels"` // Multi-panel config
	Telegram TelegramConfig `yaml:"telegram"`
	Payment  PaymentConfig  `yaml:"payment"`
}

// PanelConfig holds 3x-ui panel configuration
type PanelConfig struct {
	Name           string `yaml:"name"` // Display name for multi-panel setup
	URL            string `yaml:"url"`
	Username       string `yaml:"username"`
	Password       string `yaml:"password"`
	Enabled        bool   `yaml:"enabled"` // For multi-panel setup (default: true)
	LimitIP        int    `yaml:"limit_ip"`
	TrafficLimitGB int    `yaml:"traffic_limit_gb"`
	BackupDays     int    `yaml:"backup_days"` // Backup interval in days (0 = disabled)
}

// TelegramConfig holds Telegram bot configuration
type TelegramConfig struct {
	Token       string  `yaml:"token"`
	AdminIDs    []int64 `yaml:"admin_ids"`
	Proxy       string  `yaml:"proxy"`
	APIServer   string  `yaml:"api_server"`
	WelcomeFile string  `yaml:"welcome_file"` // URL to welcome PDF file
}

// PaymentConfig holds payment information
type PaymentConfig struct {
	Bank            string       `yaml:"bank"`
	PhoneNumber     string       `yaml:"phone_number"`
	InstructionsURL string       `yaml:"instructions_url"`
	TrialDays       int          `yaml:"trial_days"`
	Prices          PricesConfig `yaml:"prices"`
}

// PricesConfig holds prices for different subscription periods
type PricesConfig struct {
	OneMonth   int `yaml:"one_month"`
	ThreeMonth int `yaml:"three_month"`
	SixMonth   int `yaml:"six_month"`
	OneYear    int `yaml:"one_year"`
}

// Load reads configuration from config.yaml file
func Load() (*Config, error) {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read config.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config.yaml: %w", err)
	}

	// Validate required fields
	if cfg.Telegram.Token == "" {
		return nil, fmt.Errorf("telegram.token is required")
	}

	if len(cfg.Telegram.AdminIDs) == 0 {
		return nil, fmt.Errorf("telegram.admin_ids is required")
	}

	// Handle backward compatibility: convert single panel to multi-panel format
	if cfg.Panel.URL != "" && len(cfg.Panels) == 0 {
		// Legacy single panel config - convert to multi-panel
		cfg.Panels = []PanelConfig{{
			Name:           "Main Server",
			URL:            cfg.Panel.URL,
			Username:       cfg.Panel.Username,
			Password:       cfg.Panel.Password,
			Enabled:        true,
			LimitIP:        cfg.Panel.LimitIP,
			TrafficLimitGB: cfg.Panel.TrafficLimitGB,
			BackupDays:     cfg.Panel.BackupDays,
		}}
	}

	// Validate panels configuration
	if len(cfg.Panels) == 0 {
		return nil, fmt.Errorf("either panel (legacy) or panels configuration is required")
	}

	// Validate each panel
	for i, panel := range cfg.Panels {
		if panel.URL == "" {
			return nil, fmt.Errorf("panels[%d].url is required", i)
		}
		if panel.Username == "" {
			return nil, fmt.Errorf("panels[%d].username is required", i)
		}
		if panel.Password == "" {
			return nil, fmt.Errorf("panels[%d].password is required", i)
		}
		if panel.Name == "" {
			// Auto-generate name if not provided
			cfg.Panels[i].Name = fmt.Sprintf("Server %d", i+1)
		}
		if panel.LimitIP < 0 {
			cfg.Panels[i].LimitIP = 0 // Reset to 0 (unlimited) if negative
		}
		// Set default enabled to true if not specified
		if i == 0 && !panel.Enabled && len(cfg.Panels) == 1 {
			// For single panel, always enable it
			cfg.Panels[i].Enabled = true
		}
	}

	return &cfg, nil
}
