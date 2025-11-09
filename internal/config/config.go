package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration
type Config struct {
	Panel    PanelConfig    `yaml:"panel"`
	Telegram TelegramConfig `yaml:"telegram"`
	Payment  PaymentConfig  `yaml:"payment"`
}

// PanelConfig holds 3x-ui panel configuration
type PanelConfig struct {
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	LimitIP  int    `yaml:"limit_ip"`
}

// TelegramConfig holds Telegram bot configuration
type TelegramConfig struct {
	Token     string  `yaml:"token"`
	AdminIDs  []int64 `yaml:"admin_ids"`
	Proxy     string  `yaml:"proxy"`
	APIServer string  `yaml:"api_server"`
}

// PaymentConfig holds payment information
type PaymentConfig struct {
	Bank            string       `yaml:"bank"`
	PhoneNumber     string       `yaml:"phone_number"`
	InstructionsURL string       `yaml:"instructions_url"`
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

	if cfg.Panel.URL == "" {
		return nil, fmt.Errorf("panel.url is required")
	}

	if cfg.Panel.Username == "" {
		return nil, fmt.Errorf("panel.username is required")
	}

	if cfg.Panel.Password == "" {
		return nil, fmt.Errorf("panel.password is required")
	}

	if cfg.Panel.LimitIP < 0 {
		cfg.Panel.LimitIP = 0 // Reset to 0 (unlimited) if negative
	}

	return &cfg, nil
}
