package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ──────────────────────────────────────────────────────
// Store – manages the SQLite database.
// ──────────────────────────────────────────────────────

type Store struct {
	db *sql.DB
}

func NewStore(dataDir string) (*Store, error) {
	db, err := sql.Open("sqlite", dataDir+"/sessions.db")
	if err != nil {
		return nil, err
	}

	// Sessions table — multiple per chat, identified by name.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id    INTEGER NOT NULL,
			name       TEXT    NOT NULL DEFAULT 'default',
			messages   TEXT    NOT NULL DEFAULT '[]',
			updated_at INTEGER NOT NULL,
			UNIQUE(chat_id, name)
		)`); err != nil {
		return nil, err
	}

	// Active session pointer — one row per chat.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS session_active (
			chat_id    INTEGER PRIMARY KEY,
			session_id INTEGER NOT NULL
		)`); err != nil {
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// ──────────────────────────────────────────────────────
// Session — a named conversation for one chat.
// ──────────────────────────────────────────────────────

type Session struct {
	db        *sql.DB
	id        int64
	chatID    int64
	name      string
	messages  []ChatMessage
	updatedAt time.Time
}

// LoadOrCreate returns the named session, creating it (and
// making it active) if it doesn't exist yet.
func (s *Store) LoadOrCreate(chatID int64, name string) (*Session, error) {
	if name == "" {
		name = "default"
	}

	row := s.db.QueryRow("SELECT id, messages, updated_at FROM sessions WHERE chat_id = ? AND name = ?", chatID, name)
	var id int64
	var msgsJSON string
	var updatedAt int64
	err := row.Scan(&id, &msgsJSON, &updatedAt)
	if err == sql.ErrNoRows {
		if _, err := s.db.Exec("INSERT INTO sessions (chat_id, name, messages, updated_at) VALUES (?, ?, '[]', ?)",
			chatID, name, time.Now().Unix()); err != nil {
			return nil, err
		}
		// Make it the active session.
		_ = s.setActiveBySessionName(chatID, name)
		return s.LoadOrCreate(chatID, name)
	}
	if err != nil {
		return nil, err
	}
	var msgs []ChatMessage
	if err := json.Unmarshal([]byte(msgsJSON), &msgs); err != nil {
		return nil, err
	}
	return &Session{
		db:        s.db,
		id:        id,
		chatID:    chatID,
		name:      name,
		messages:  msgs,
		updatedAt: time.Unix(updatedAt, 0),
	}, nil
}

func (sess *Session) ID() int64       { return sess.id }
func (sess *Session) ChatID() int64   { return sess.chatID }
func (sess *Session) Name() string    { return sess.name }
func (sess *Session) Len() int        { return len(sess.messages) }
func (sess *Session) UpdatedAt() time.Time { return sess.updatedAt }

func (sess *Session) Append(msg ChatMessage) error {
	sess.messages = append(sess.messages, msg)
	return sess.save()
}

func (sess *Session) Save() error { return sess.save() }

func (sess *Session) save() error {
	data, err := json.Marshal(sess.messages)
	if err != nil {
		return err
	}
	_, err = sess.db.Exec(
		"UPDATE sessions SET messages = ?, updated_at = ? WHERE id = ?",
		string(data), time.Now().Unix(), sess.id)
	return err
}

func (sess *Session) Messages() []ChatMessage { return sess.messages }

func (sess *Session) Truncate(keep int) error {
	if keep < 0 || keep >= len(sess.messages) {
		sess.messages = nil
	} else {
		sess.messages = sess.messages[len(sess.messages)-keep:]
	}
	return sess.save()
}

// ──────────────────────────────────────────────────────
// Active session management
// ──────────────────────────────────────────────────────

// GetActiveSessionID returns the session ID currently active for chatID, or 0.
func (s *Store) GetActiveSessionID(chatID int64) int64 {
	var sid int64
	err := s.db.QueryRow("SELECT session_id FROM session_active WHERE chat_id = ?", chatID).Scan(&sid)
	if err != nil {
		return 0
	}
	return sid
}

// GetActive returns the active session for chatID (loads it).
func (s *Store) GetActive(chatID int64) (*Session, error) {
	sid := s.GetActiveSessionID(chatID)
	if sid == 0 {
		// Fall back to default.
		return s.LoadOrCreate(chatID, "default")
	}

	row := s.db.QueryRow("SELECT chat_id, name, messages, updated_at FROM sessions WHERE id = ?", sid)
	var cID int64
	var name string
	var msgsJSON string
	var updatedAt int64
	if err := row.Scan(&cID, &name, &msgsJSON, &updatedAt); err != nil {
		// Stale pointer — fall back to default.
		return s.LoadOrCreate(chatID, "default")
	}
	var msgs []ChatMessage
	if err := json.Unmarshal([]byte(msgsJSON), &msgs); err != nil {
		return nil, err
	}
	return &Session{
		db:        s.db,
		id:        sid,
		chatID:    cID,
		name:      name,
		messages:  msgs,
		updatedAt: time.Unix(updatedAt, 0),
	}, nil
}

// SwitchActive changes the active session for chatID.
func (s *Store) SwitchActive(chatID int64, name string) error {
	return s.setActiveBySessionName(chatID, name)
}

func (s *Store) setActiveBySessionName(chatID int64, name string) error {
	var sid int64
	if err := s.db.QueryRow("SELECT id FROM sessions WHERE chat_id = ? AND name = ?", chatID, name).Scan(&sid); err != nil {
		return fmt.Errorf("session %q not found", name)
	}
	_, err := s.db.Exec(
		`INSERT INTO session_active (chat_id, session_id) VALUES (?, ?)
		 ON CONFLICT(chat_id) DO UPDATE SET session_id = excluded.session_id`,
		chatID, sid)
	return err
}

// ──────────────────────────────────────────────────────
// List / rename / delete sessions
// ──────────────────────────────────────────────────────

type SessionInfo struct {
	ID        int64
	Name      string
	Messages  int
	UpdatedAt time.Time
	Active    bool
}

// ListSessions returns all sessions for chatID.
func (s *Store) ListSessions(chatID int64) ([]SessionInfo, error) {
	activeID := s.GetActiveSessionID(chatID)

	rows, err := s.db.Query(
		"SELECT id, name, messages, updated_at FROM sessions WHERE chat_id = ? ORDER BY updated_at DESC", chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionInfo
	for rows.Next() {
		var id int64
		var name string
		var msgsJSON string
		var updatedAt int64
		if err := rows.Scan(&id, &name, &msgsJSON, &updatedAt); err != nil {
			return nil, err
		}
		var msgs []ChatMessage
		_ = json.Unmarshal([]byte(msgsJSON), &msgs)
		out = append(out, SessionInfo{
			ID:        id,
			Name:      name,
			Messages:  len(msgs),
			UpdatedAt: time.Unix(updatedAt, 0),
			Active:    id == activeID,
		})
	}
	return out, rows.Err()
}

// RenameSession changes the name of a session.
func (s *Store) RenameSession(chatID int64, oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	res, err := s.db.Exec("UPDATE sessions SET name = ? WHERE chat_id = ? AND name = ?", newName, chatID, oldName)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session %q not found", oldName)
	}
	return nil
}

// DeleteSession removes a session. Cannot delete the last session.
func (s *Store) DeleteSession(chatID int64, name string) error {
	if name == "default" {
		return fmt.Errorf("cannot delete default session")
	}
	// Count sessions for this chat.
	var count int
	_ = s.db.QueryRow("SELECT COUNT(*) FROM sessions WHERE chat_id = ?", chatID).Scan(&count)
	if count <= 1 {
		return fmt.Errorf("cannot delete the only session")
	}

	// Get the id to delete.
	var delID int64
	if err := s.db.QueryRow("SELECT id FROM sessions WHERE chat_id = ? AND name = ?", chatID, name).Scan(&delID); err != nil {
		return fmt.Errorf("session %q not found", name)
	}

	// If it's active, switch to another one first.
	if s.GetActiveSessionID(chatID) == delID {
		// Find another session.
		var otherID int64
		_ = s.db.QueryRow("SELECT id FROM sessions WHERE chat_id = ? AND id != ? ORDER BY updated_at DESC LIMIT 1",
			chatID, delID).Scan(&otherID)
		if otherID != 0 {
			_, _ = s.db.Exec("INSERT OR REPLACE INTO session_active (chat_id, session_id) VALUES (?, ?)", chatID, otherID)
		}
	}

	_, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", delID)
	return err
}
