package middleware

import (
	"runtime/debug"

	"x-ui-bot/internal/logger"
)

// Recovery handles panics and recovers from them
type Recovery struct {
	logger *logger.Logger
}

// NewRecovery creates a new recovery middleware
func NewRecovery(log *logger.Logger) *Recovery {
	return &Recovery{logger: log}
}

// Recover recovers from a panic and logs it
func (r *Recovery) Recover() {
	if err := recover(); err != nil {
		r.logger.WithFields(map[string]interface{}{
			"panic": err,
			"stack": string(debug.Stack()),
		}).Error("Panic recovered")

		// Можно добавить дополнительную логику, например, отправку уведомления админу
	}
}

// WrapFunc wraps a function with panic recovery
func (r *Recovery) WrapFunc(fn func()) {
	defer r.Recover()
	fn()
}

// HandleError handles errors and logs them
func HandleError(log *logger.Logger, err error, context string) {
	if err != nil {
		log.WithField("context", context).ErrorErr(err, "Error occurred")
	}
}
