package logger

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Logger wraps zerolog.Logger with convenience methods
type Logger struct {
	logger zerolog.Logger
}

var defaultLogger *Logger

// Init initializes the global logger
func Init(level string, pretty bool) {
	var output io.Writer = os.Stdout

	if pretty {
		output = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}
	}

	// Parse log level
	logLevel, err := zerolog.ParseLevel(level)
	if err != nil {
		logLevel = zerolog.InfoLevel
	}

	zerolog.SetGlobalLevel(logLevel)
	logger := zerolog.New(output).With().Timestamp().Logger()

	defaultLogger = &Logger{logger: logger}
	log.Logger = logger
}

// GetLogger returns the global logger instance
func GetLogger() *Logger {
	if defaultLogger == nil {
		Init("info", true)
	}
	return defaultLogger
}

// WithField adds a field to the logger context
func (l *Logger) WithField(key string, value interface{}) *Logger {
	return &Logger{logger: l.logger.With().Interface(key, value).Logger()}
}

// WithFields adds multiple fields to the logger context
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	ctx := l.logger.With()
	for k, v := range fields {
		ctx = ctx.Interface(k, v)
	}
	return &Logger{logger: ctx.Logger()}
}

// Debug logs a debug message
func (l *Logger) Debug(msg string) {
	l.logger.Debug().Msg(msg)
}

// Debugf logs a formatted debug message
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.logger.Debug().Msgf(format, args...)
}

// Info logs an info message
func (l *Logger) Info(msg string) {
	l.logger.Info().Msg(msg)
}

// Infof logs a formatted info message
func (l *Logger) Infof(format string, args ...interface{}) {
	l.logger.Info().Msgf(format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string) {
	l.logger.Warn().Msg(msg)
}

// Warnf logs a formatted warning message
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.logger.Warn().Msgf(format, args...)
}

// Error logs an error message
func (l *Logger) Error(msg string) {
	l.logger.Error().Msg(msg)
}

// Errorf logs a formatted error message
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.logger.Error().Msgf(format, args...)
}

// ErrorErr logs an error with error object
func (l *Logger) ErrorErr(err error, msg string) {
	l.logger.Error().Err(err).Msg(msg)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string) {
	l.logger.Fatal().Msg(msg)
}

// Fatalf logs a formatted fatal message and exits
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.logger.Fatal().Msgf(format, args...)
}

// FatalErr logs a fatal error with error object and exits
func (l *Logger) FatalErr(err error, msg string) {
	l.logger.Fatal().Err(err).Msg(msg)
}

// Convenience functions for global logger
func Info(msg string) {
	GetLogger().Info(msg)
}

func Infof(format string, args ...interface{}) {
	GetLogger().Infof(format, args...)
}

func Warn(msg string) {
	GetLogger().Warn(msg)
}

func Warnf(format string, args ...interface{}) {
	GetLogger().Warnf(format, args...)
}

func Error(msg string) {
	GetLogger().Error(msg)
}

func Errorf(format string, args ...interface{}) {
	GetLogger().Errorf(format, args...)
}

func ErrorErr(err error, msg string) {
	GetLogger().ErrorErr(err, msg)
}

func Fatal(msg string) {
	GetLogger().Fatal(msg)
}

func Fatalf(format string, args ...interface{}) {
	GetLogger().Fatalf(format, args...)
}

func FatalErr(err error, msg string) {
	GetLogger().FatalErr(err, msg)
}

func WithField(key string, value interface{}) *Logger {
	return GetLogger().WithField(key, value)
}

func WithFields(fields map[string]interface{}) *Logger {
	return GetLogger().WithFields(fields)
}
