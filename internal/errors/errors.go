package errors

import (
	"errors"
	"fmt"
)

// Custom error types
var (
	ErrNotFound          = errors.New("not found")
	ErrUnauthorized      = errors.New("unauthorized")
	ErrInvalidInput      = errors.New("invalid input")
	ErrRateLimitExceeded = errors.New("rate limit exceeded")
	ErrClientBlocked     = errors.New("client blocked")
	ErrAPIError          = errors.New("API error")
	ErrDatabaseError     = errors.New("database error")
	ErrInternalError     = errors.New("internal error")
)

// BotError represents a structured bot error
type BotError struct {
	Code    string
	Message string
	Err     error
}

func (e *BotError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *BotError) Unwrap() error {
	return e.Err
}

// New creates a new BotError
func New(code, message string) *BotError {
	return &BotError{
		Code:    code,
		Message: message,
	}
}

// Wrap wraps an error with additional context
func Wrap(err error, code, message string) *BotError {
	return &BotError{
		Code:    code,
		Message: message,
		Err:     err,
	}
}

// Unauthorized creates an unauthorized error
func Unauthorized(message string) *BotError {
	return &BotError{
		Code:    "UNAUTHORIZED",
		Message: message,
		Err:     ErrUnauthorized,
	}
}

// RateLimitExceeded creates a rate limit exceeded error
func RateLimitExceeded(message string) *BotError {
	return &BotError{
		Code:    "RATE_LIMIT",
		Message: message,
		Err:     ErrRateLimitExceeded,
	}
}
