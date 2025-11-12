package shutdown

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"x-ui-bot/internal/logger"
)

// Manager handles graceful shutdown coordination
type Manager struct {
	logger    *logger.Logger
	timeout   time.Duration
	signals   []os.Signal
	callbacks []func(context.Context) error
	mu        sync.Mutex
}

// NewManager creates a new shutdown manager
func NewManager(log *logger.Logger, timeout time.Duration) *Manager {
	return &Manager{
		logger:    log,
		timeout:   timeout,
		signals:   []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGINT},
		callbacks: make([]func(context.Context) error, 0),
	}
}

// Register registers a shutdown callback
func (m *Manager) Register(callback func(context.Context) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.callbacks = append(m.callbacks, callback)
}

// Wait blocks until a shutdown signal is received
func (m *Manager) Wait() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, m.signals...)

	sig := <-sigChan
	m.logger.WithField("signal", sig.String()).Info("Shutdown signal received")

	m.Shutdown()
}

// Shutdown executes all registered callbacks with timeout
func (m *Manager) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()

	m.logger.Info("Starting graceful shutdown")

	m.mu.Lock()
	callbacks := m.callbacks
	m.mu.Unlock()

	var wg sync.WaitGroup
	errors := make(chan error, len(callbacks))

	for i, callback := range callbacks {
		wg.Add(1)
		go func(idx int, cb func(context.Context) error) {
			defer wg.Done()
			if err := cb(ctx); err != nil {
				m.logger.WithFields(map[string]interface{}{
					"callback": idx,
					"error":    err,
				}).Error("Shutdown callback failed")
				errors <- err
			}
		}(i, callback)
	}

	// Wait for all callbacks to complete or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		m.logger.Info("Graceful shutdown completed successfully")
	case <-ctx.Done():
		m.logger.Warn("Shutdown timeout exceeded, forcing exit")
	}

	close(errors)
}

// SetTimeout updates the shutdown timeout
func (m *Manager) SetTimeout(timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeout = timeout
}

// AddSignal adds a custom signal to listen for
func (m *Manager) AddSignal(sig os.Signal) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.signals = append(m.signals, sig)
}
