package storage

import (
	"fmt"
	"sync"
	"time"
)

// MemoryStorage implements Storage interface with in-memory storage
type MemoryStorage struct {
	userStates        map[int64]string
	registrationReqs  map[int64]*RegistrationRequest
	adminMessageState map[int64]*AdminMessageState
	userMessageState  map[int64]*UserMessageState
	broadcastState    map[int64]*BroadcastState
	mu                sync.RWMutex
}

// NewMemoryStorage creates a new in-memory storage
func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{
		userStates:        make(map[int64]string),
		registrationReqs:  make(map[int64]*RegistrationRequest),
		adminMessageState: make(map[int64]*AdminMessageState),
		userMessageState:  make(map[int64]*UserMessageState),
		broadcastState:    make(map[int64]*BroadcastState),
	}
}

// User states
func (s *MemoryStorage) SetUserState(userID int64, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userStates[userID] = state
	return nil
}

func (s *MemoryStorage) GetUserState(userID int64) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, exists := s.userStates[userID]
	if !exists {
		return "", fmt.Errorf("state not found for user %d", userID)
	}
	return state, nil
}

func (s *MemoryStorage) DeleteUserState(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.userStates, userID)
	return nil
}

// Registration requests
func (s *MemoryStorage) SetRegistrationRequest(userID int64, req *RegistrationRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registrationReqs[userID] = req
	return nil
}

func (s *MemoryStorage) GetRegistrationRequest(userID int64) (*RegistrationRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	req, exists := s.registrationReqs[userID]
	if !exists {
		return nil, fmt.Errorf("registration request not found for user %d", userID)
	}
	return req, nil
}

func (s *MemoryStorage) DeleteRegistrationRequest(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registrationReqs, userID)
	return nil
}

func (s *MemoryStorage) GetAllRegistrationRequests() (map[int64]*RegistrationRequest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to avoid concurrent map access
	result := make(map[int64]*RegistrationRequest, len(s.registrationReqs))
	for k, v := range s.registrationReqs {
		result[k] = v
	}
	return result, nil
}

// Admin message states
func (s *MemoryStorage) SetAdminMessageState(adminID int64, state *AdminMessageState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adminMessageState[adminID] = state
	return nil
}

func (s *MemoryStorage) GetAdminMessageState(adminID int64) (*AdminMessageState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, exists := s.adminMessageState[adminID]
	if !exists {
		return nil, fmt.Errorf("admin message state not found for admin %d", adminID)
	}
	return state, nil
}

func (s *MemoryStorage) DeleteAdminMessageState(adminID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.adminMessageState, adminID)
	return nil
}

// User message states
func (s *MemoryStorage) SetUserMessageState(userID int64, state *UserMessageState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userMessageState[userID] = state
	return nil
}

func (s *MemoryStorage) GetUserMessageState(userID int64) (*UserMessageState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, exists := s.userMessageState[userID]
	if !exists {
		return nil, fmt.Errorf("user message state not found for user %d", userID)
	}
	return state, nil
}

func (s *MemoryStorage) DeleteUserMessageState(userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.userMessageState, userID)
	return nil
}

// Broadcast states
func (s *MemoryStorage) SetBroadcastState(adminID int64, state *BroadcastState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastState[adminID] = state
	return nil
}

func (s *MemoryStorage) GetBroadcastState(adminID int64) (*BroadcastState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, exists := s.broadcastState[adminID]
	if !exists {
		return nil, fmt.Errorf("broadcast state not found for admin %d", adminID)
	}
	return state, nil
}

func (s *MemoryStorage) DeleteBroadcastState(adminID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.broadcastState, adminID)
	return nil
}

// CleanupExpiredStates removes states older than maxAge
func (s *MemoryStorage) CleanupExpiredStates(maxAge time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	// Cleanup registration requests
	for userID, req := range s.registrationReqs {
		if now.Sub(req.Timestamp) > maxAge {
			delete(s.registrationReqs, userID)
		}
	}

	// Cleanup admin message states
	for adminID, state := range s.adminMessageState {
		if now.Sub(state.Timestamp) > maxAge {
			delete(s.adminMessageState, adminID)
		}
	}

	// Cleanup user message states
	for userID, state := range s.userMessageState {
		if now.Sub(state.Timestamp) > maxAge {
			delete(s.userMessageState, userID)
		}
	}

	// Cleanup broadcast states
	for adminID, state := range s.broadcastState {
		if now.Sub(state.Timestamp) > maxAge {
			delete(s.broadcastState, adminID)
		}
	}

	return nil
}

// Close closes the storage (no-op for memory storage)
func (s *MemoryStorage) Close() error {
	return nil
}
