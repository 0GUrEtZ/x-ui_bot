package middleware

import (
	"x-ui-bot/internal/config"
	"x-ui-bot/internal/errors"
)

// AuthMiddleware handles authorization checks
type AuthMiddleware struct {
	config *config.Config
}

// NewAuthMiddleware creates a new auth middleware
func NewAuthMiddleware(cfg *config.Config) *AuthMiddleware {
	return &AuthMiddleware{config: cfg}
}

// IsAdmin checks if user is an admin
func (m *AuthMiddleware) IsAdmin(userID int64) bool {
	for _, id := range m.config.Telegram.AdminIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// RequireAdmin returns an error if user is not an admin
func (m *AuthMiddleware) RequireAdmin(userID int64) error {
	if !m.IsAdmin(userID) {
		return errors.Unauthorized("admin access required")
	}
	return nil
}
