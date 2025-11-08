package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all application configuration
type Config struct {
	Panel    PanelConfig
	Telegram TelegramConfig
	Bot      BotConfig
}

// PanelConfig holds 3x-ui panel configuration
type PanelConfig struct {
	URL      string
	Username string
	Password string
}

// TelegramConfig holds Telegram bot configuration
type TelegramConfig struct {
	Token     string
	AdminIDs  []int64
	Proxy     string
	APIServer string
}

// BotConfig holds bot behavior configuration
type BotConfig struct {
	Language string
	Timezone string
}

// Load reads configuration from environment variables
func Load() (*Config, error) {
	// Try to load .env file (ignore error if it doesn't exist)
	_ = godotenv.Load()

	cfg := &Config{
		Panel: PanelConfig{
			URL:      getEnv("PANEL_URL", "http://localhost:2053"),
			Username: getEnv("PANEL_USERNAME", "admin"),
			Password: getEnv("PANEL_PASSWORD", "admin"),
		},
		Telegram: TelegramConfig{
			Token:     getEnv("TG_BOT_TOKEN", ""),
			Proxy:     getEnv("TG_BOT_PROXY", ""),
			APIServer: getEnv("TG_BOT_API_SERVER", ""),
		},
		Bot: BotConfig{
			Language: getEnv("BOT_LANGUAGE", "en"),
			Timezone: getEnv("BOT_TIMEZONE", "UTC"),
		},
	}

	// Parse admin IDs
	adminIDsStr := getEnv("TG_BOT_ADMIN_IDS", "")
	if adminIDsStr != "" {
		for _, idStr := range strings.Split(adminIDsStr, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid admin ID: %s", idStr)
			}
			cfg.Telegram.AdminIDs = append(cfg.Telegram.AdminIDs, id)
		}
	}

	// Validate required fields
	if cfg.Telegram.Token == "" {
		return nil, fmt.Errorf("TG_BOT_TOKEN is required")
	}

	if len(cfg.Telegram.AdminIDs) == 0 {
		return nil, fmt.Errorf("TG_BOT_ADMIN_IDS is required")
	}

	return cfg, nil
}

// getEnv gets an environment variable with a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
