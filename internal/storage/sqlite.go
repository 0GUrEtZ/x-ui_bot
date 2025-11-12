package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteStorage implements Storage interface with SQLite persistence
type SQLiteStorage struct {
	db *sql.DB
}

// NewSQLiteStorage creates a new SQLite storage
func NewSQLiteStorage(dbPath string) (*SQLiteStorage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	storage := &SQLiteStorage{db: db}
	if err := storage.initialize(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	return storage, nil
}

// initialize creates the necessary tables
func (s *SQLiteStorage) initialize() error {
	schema := `
	CREATE TABLE IF NOT EXISTS user_states (
		user_id INTEGER PRIMARY KEY,
		state TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS registration_requests (
		user_id INTEGER PRIMARY KEY,
		username TEXT NOT NULL,
		tg_username TEXT,
		email TEXT NOT NULL,
		duration INTEGER NOT NULL,
		status TEXT NOT NULL,
		timestamp DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS admin_message_states (
		admin_id INTEGER PRIMARY KEY,
		client_email TEXT NOT NULL,
		client_tg_id TEXT,
		inbound_id INTEGER NOT NULL,
		client_index INTEGER NOT NULL,
		timestamp DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS user_message_states (
		user_id INTEGER PRIMARY KEY,
		username TEXT NOT NULL,
		tg_username TEXT,
		timestamp DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS broadcast_states (
		admin_id INTEGER PRIMARY KEY,
		message TEXT NOT NULL,
		timestamp DATETIME NOT NULL
	);
	`

	_, err := s.db.Exec(schema)
	return err
}

// User states
func (s *SQLiteStorage) SetUserState(userID int64, state string) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO user_states (user_id, state) VALUES (?, ?)",
		userID, state,
	)
	return err
}

func (s *SQLiteStorage) GetUserState(userID int64) (string, error) {
	var state string
	err := s.db.QueryRow(
		"SELECT state FROM user_states WHERE user_id = ?",
		userID,
	).Scan(&state)

	if err == sql.ErrNoRows {
		return "", fmt.Errorf("state not found for user %d", userID)
	}
	return state, err
}

func (s *SQLiteStorage) DeleteUserState(userID int64) error {
	_, err := s.db.Exec("DELETE FROM user_states WHERE user_id = ?", userID)
	return err
}

// Registration requests
func (s *SQLiteStorage) SetRegistrationRequest(userID int64, req *RegistrationRequest) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO registration_requests 
		(user_id, username, tg_username, email, duration, status, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, req.Username, req.TgUsername, req.Email, req.Duration, req.Status, req.Timestamp,
	)
	return err
}

func (s *SQLiteStorage) GetRegistrationRequest(userID int64) (*RegistrationRequest, error) {
	req := &RegistrationRequest{}
	err := s.db.QueryRow(`
		SELECT user_id, username, tg_username, email, duration, status, timestamp
		FROM registration_requests WHERE user_id = ?`,
		userID,
	).Scan(&req.UserID, &req.Username, &req.TgUsername, &req.Email, &req.Duration, &req.Status, &req.Timestamp)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("registration request not found for user %d", userID)
	}
	return req, err
}

func (s *SQLiteStorage) DeleteRegistrationRequest(userID int64) error {
	_, err := s.db.Exec("DELETE FROM registration_requests WHERE user_id = ?", userID)
	return err
}

func (s *SQLiteStorage) GetAllRegistrationRequests() (map[int64]*RegistrationRequest, error) {
	rows, err := s.db.Query(`
		SELECT user_id, username, tg_username, email, duration, status, timestamp
		FROM registration_requests
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[int64]*RegistrationRequest)
	for rows.Next() {
		req := &RegistrationRequest{}
		if err := rows.Scan(&req.UserID, &req.Username, &req.TgUsername, &req.Email, &req.Duration, &req.Status, &req.Timestamp); err != nil {
			return nil, err
		}
		result[req.UserID] = req
	}
	return result, rows.Err()
}

// Admin message states
func (s *SQLiteStorage) SetAdminMessageState(adminID int64, state *AdminMessageState) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO admin_message_states
		(admin_id, client_email, client_tg_id, inbound_id, client_index, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)`,
		adminID, state.ClientEmail, state.ClientTgID, state.InboundID, state.ClientIndex, state.Timestamp,
	)
	return err
}

func (s *SQLiteStorage) GetAdminMessageState(adminID int64) (*AdminMessageState, error) {
	state := &AdminMessageState{}
	err := s.db.QueryRow(`
		SELECT client_email, client_tg_id, inbound_id, client_index, timestamp
		FROM admin_message_states WHERE admin_id = ?`,
		adminID,
	).Scan(&state.ClientEmail, &state.ClientTgID, &state.InboundID, &state.ClientIndex, &state.Timestamp)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("admin message state not found for admin %d", adminID)
	}
	return state, err
}

func (s *SQLiteStorage) DeleteAdminMessageState(adminID int64) error {
	_, err := s.db.Exec("DELETE FROM admin_message_states WHERE admin_id = ?", adminID)
	return err
}

// User message states
func (s *SQLiteStorage) SetUserMessageState(userID int64, state *UserMessageState) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO user_message_states
		(user_id, username, tg_username, timestamp)
		VALUES (?, ?, ?, ?)`,
		userID, state.Username, state.TgUsername, state.Timestamp,
	)
	return err
}

func (s *SQLiteStorage) GetUserMessageState(userID int64) (*UserMessageState, error) {
	state := &UserMessageState{}
	err := s.db.QueryRow(`
		SELECT user_id, username, tg_username, timestamp
		FROM user_message_states WHERE user_id = ?`,
		userID,
	).Scan(&state.UserID, &state.Username, &state.TgUsername, &state.Timestamp)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("user message state not found for user %d", userID)
	}
	return state, err
}

func (s *SQLiteStorage) DeleteUserMessageState(userID int64) error {
	_, err := s.db.Exec("DELETE FROM user_message_states WHERE user_id = ?", userID)
	return err
}

// Broadcast states
func (s *SQLiteStorage) SetBroadcastState(adminID int64, state *BroadcastState) error {
	_, err := s.db.Exec(`
		INSERT OR REPLACE INTO broadcast_states (admin_id, message, timestamp)
		VALUES (?, ?, ?)`,
		adminID, state.Message, state.Timestamp,
	)
	return err
}

func (s *SQLiteStorage) GetBroadcastState(adminID int64) (*BroadcastState, error) {
	state := &BroadcastState{}
	err := s.db.QueryRow(`
		SELECT message, timestamp FROM broadcast_states WHERE admin_id = ?`,
		adminID,
	).Scan(&state.Message, &state.Timestamp)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("broadcast state not found for admin %d", adminID)
	}
	return state, err
}

func (s *SQLiteStorage) DeleteBroadcastState(adminID int64) error {
	_, err := s.db.Exec("DELETE FROM broadcast_states WHERE admin_id = ?", adminID)
	return err
}

// CleanupExpiredStates removes states older than maxAge
func (s *SQLiteStorage) CleanupExpiredStates(maxAge time.Duration) error {
	cutoff := time.Now().Add(-maxAge)

	tables := []string{
		"registration_requests",
		"admin_message_states",
		"user_message_states",
		"broadcast_states",
	}

	for _, table := range tables {
		_, err := s.db.Exec(
			fmt.Sprintf("DELETE FROM %s WHERE timestamp < ?", table),
			cutoff,
		)
		if err != nil {
			return err
		}
	}

	return nil
}

// Close closes the database connection
func (s *SQLiteStorage) Close() error {
	return s.db.Close()
}

// Helper function to marshal/unmarshal complex types if needed
