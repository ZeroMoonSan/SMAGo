package main

import (
	"database/sql"
	"encoding/json"
	"time"

	_ "modernc.org/sqlite"
)

type Session struct {
	db        *sql.DB
	id        int64
	chatID    int64
	messages  []ChatMessage
	updatedAt time.Time
}

type Store struct {
	db *sql.DB
}

func NewStore(dataDir string) (*Store, error) {
	db, err := sql.Open("sqlite", dataDir+"/sessions.db")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			chat_id INTEGER UNIQUE NOT NULL,
			messages TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) LoadOrCreate(chatID int64) (*Session, error) {
	row := s.db.QueryRow("SELECT id, messages, updated_at FROM sessions WHERE chat_id = ?", chatID)
	var id int64
	var msgsJSON string
	var updatedAt int64
	err := row.Scan(&id, &msgsJSON, &updatedAt)
	if err == sql.ErrNoRows {
		_, err := s.db.Exec("INSERT INTO sessions (chat_id, messages, updated_at) VALUES (?, ?, ?)",
			chatID, "[]", time.Now().Unix())
		if err != nil {
			return nil, err
		}
		return s.LoadOrCreate(chatID)
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
		messages:  msgs,
		updatedAt: time.Unix(updatedAt, 0),
	}, nil
}

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
		return nil
	}
	sess.messages = sess.messages[len(sess.messages)-keep:]
	return sess.save()
}
