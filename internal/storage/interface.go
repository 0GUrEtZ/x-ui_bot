package storage

import (
	"time"
)

// RegistrationRequest represents a user registration request
type RegistrationRequest struct {
	UserID     int64
	Username   string
	TgUsername string
	Email      string
	Duration   int
	Status     string
	Timestamp  time.Time
}

// AdminMessageState represents state for admin sending message to client
type AdminMessageState struct {
	ClientEmail string
	ClientTgID  string
	InboundID   int
	ClientIndex int
	Timestamp   time.Time
}

// UserMessageState represents state for user sending message to admin
type UserMessageState struct {
	UserID     int64
	Username   string
	TgUsername string
	Timestamp  time.Time
}

// BroadcastState represents state for admin creating broadcast
type BroadcastState struct {
	Message   string
	Timestamp time.Time
}

// TrafficSnapshot - snapshot of server traffic at a given time
type TrafficSnapshot struct {
	ID            int64
	InboundID     int
	Timestamp     time.Time
	UploadBytes   int64
	DownloadBytes int64
	TotalBytes    int64
}

// Storage defines the interface for state persistence
type Storage interface {
	// User states
	SetUserState(userID int64, state string) error
	GetUserState(userID int64) (string, error)
	DeleteUserState(userID int64) error

	// Registration requests
	SetRegistrationRequest(userID int64, req *RegistrationRequest) error
	GetRegistrationRequest(userID int64) (*RegistrationRequest, error)
	DeleteRegistrationRequest(userID int64) error
	GetAllRegistrationRequests() (map[int64]*RegistrationRequest, error)

	// Admin message states
	SetAdminMessageState(adminID int64, state *AdminMessageState) error
	GetAdminMessageState(adminID int64) (*AdminMessageState, error)
	DeleteAdminMessageState(adminID int64) error

	// User message states
	SetUserMessageState(userID int64, state *UserMessageState) error
	GetUserMessageState(userID int64) (*UserMessageState, error)
	DeleteUserMessageState(userID int64) error

	// Broadcast states
	SetBroadcastState(adminID int64, state *BroadcastState) error
	GetBroadcastState(adminID int64) (*BroadcastState, error)
	DeleteBroadcastState(adminID int64) error

	// Traffic snapshots
	SaveTrafficSnapshot(snapshot *TrafficSnapshot) error
	GetTrafficSnapshots(inboundID int, startTime, endTime time.Time) ([]*TrafficSnapshot, error)
	GetLatestTrafficSnapshot(inboundID int) (*TrafficSnapshot, error)
	DeleteOldTrafficSnapshots(beforeTime time.Time) error

	// Cleanup
	CleanupExpiredStates(maxAge time.Duration) error

	// Close the storage
	Close() error
}
