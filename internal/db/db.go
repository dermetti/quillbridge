package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    quota_bytes INTEGER DEFAULT 104857600,
    is_admin BOOLEAN DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS sessions (
    session_token TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    expires_at DATETIME NOT NULL,
    FOREIGN KEY(user_id) REFERENCES users(id)
);

CREATE TABLE IF NOT EXISTS notes_meta (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    filename TEXT NOT NULL,
    category TEXT,
    is_favorite BOOLEAN DEFAULT FALSE,
    modified_at INTEGER NOT NULL,
    FOREIGN KEY(user_id) REFERENCES users(id)
);
`

// DB wraps sql.DB with Quillbridge-specific methods.
type DB struct {
	*sql.DB
}

// User represents a row in the users table.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	QuotaBytes   int64
	IsAdmin      bool
}

// Session represents a row in the sessions table.
type Session struct {
	Token     string
	UserID    int64
	ExpiresAt time.Time
}

// NoteMeta represents a row in the notes_meta table.
type NoteMeta struct {
	ID         int64
	UserID     int64
	Filename   string
	Category   string
	IsFavorite bool
	ModifiedAt int64 // Unix timestamp
}

// Open opens (or creates) the SQLite database at path and applies the schema.
func Open(path string) (*DB, error) {
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite performs best with a single connection for writes.
	sqldb.SetMaxOpenConns(1)

	if _, err := sqldb.Exec(schema); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &DB{sqldb}, nil
}

// --- User methods ---

func (db *DB) CreateUser(username, passwordHash string, quotaBytes int64, isAdmin bool) error {
	_, err := db.Exec(
		`INSERT INTO users (username, password_hash, quota_bytes, is_admin) VALUES (?, ?, ?, ?)`,
		username, passwordHash, quotaBytes, isAdmin,
	)
	return err
}

func (db *DB) GetUserByUsername(username string) (*User, error) {
	u := &User{}
	err := db.QueryRow(
		`SELECT id, username, password_hash, quota_bytes, is_admin FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.QuotaBytes, &u.IsAdmin)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db *DB) GetUserByID(id int64) (*User, error) {
	u := &User{}
	err := db.QueryRow(
		`SELECT id, username, password_hash, quota_bytes, is_admin FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.QuotaBytes, &u.IsAdmin)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (db *DB) GetAllUsers() ([]*User, error) {
	rows, err := db.Query(`SELECT id, username, password_hash, quota_bytes, is_admin FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		u := &User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.QuotaBytes, &u.IsAdmin); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) UpdateUserPassword(userID int64, passwordHash string) error {
	_, err := db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return err
}

func (db *DB) UpdateUserQuota(userID int64, quotaBytes int64) error {
	_, err := db.Exec(`UPDATE users SET quota_bytes = ? WHERE id = ?`, quotaBytes, userID)
	return err
}

func (db *DB) DeleteUser(userID int64) error {
	_, err := db.Exec(`DELETE FROM users WHERE id = ?`, userID)
	return err
}

// --- Session methods ---

func (db *DB) CreateSession(token string, userID int64, expiresAt time.Time) error {
	_, err := db.Exec(
		`INSERT INTO sessions (session_token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expiresAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (db *DB) GetSession(token string) (*Session, error) {
	s := &Session{}
	var expiresStr string
	err := db.QueryRow(
		`SELECT session_token, user_id, expires_at FROM sessions WHERE session_token = ?`,
		token,
	).Scan(&s.Token, &s.UserID, &expiresStr)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.ExpiresAt, err = time.Parse(time.RFC3339, expiresStr)
	if err != nil {
		return nil, fmt.Errorf("parse expires_at: %w", err)
	}
	return s, nil
}

func (db *DB) DeleteSession(token string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE session_token = ?`, token)
	return err
}

// --- NoteMeta methods ---

func (db *DB) CreateNoteMeta(userID int64, filename, category string, isFavorite bool, modifiedAt int64) (int64, error) {
	res, err := db.Exec(
		`INSERT INTO notes_meta (user_id, filename, category, is_favorite, modified_at) VALUES (?, ?, ?, ?, ?)`,
		userID, filename, category, isFavorite, modifiedAt,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (db *DB) GetNoteMetaByID(id, userID int64) (*NoteMeta, error) {
	n := &NoteMeta{}
	err := db.QueryRow(
		`SELECT id, user_id, filename, category, is_favorite, modified_at FROM notes_meta WHERE id = ? AND user_id = ?`,
		id, userID,
	).Scan(&n.ID, &n.UserID, &n.Filename, &n.Category, &n.IsFavorite, &n.ModifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return n, nil
}

func (db *DB) GetAllNotesMeta(userID int64) ([]*NoteMeta, error) {
	rows, err := db.Query(
		`SELECT id, user_id, filename, category, is_favorite, modified_at FROM notes_meta WHERE user_id = ?`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []*NoteMeta
	for rows.Next() {
		n := &NoteMeta{}
		if err := rows.Scan(&n.ID, &n.UserID, &n.Filename, &n.Category, &n.IsFavorite, &n.ModifiedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

func (db *DB) UpdateNoteMeta(id, userID int64, category string, isFavorite bool, modifiedAt int64) error {
	_, err := db.Exec(
		`UPDATE notes_meta SET category = ?, is_favorite = ?, modified_at = ? WHERE id = ? AND user_id = ?`,
		category, isFavorite, modifiedAt, id, userID,
	)
	return err
}

func (db *DB) UpdateNoteFilename(id, userID int64, filename string) error {
	_, err := db.Exec(
		`UPDATE notes_meta SET filename = ? WHERE id = ? AND user_id = ?`,
		filename, id, userID,
	)
	return err
}

func (db *DB) DeleteNoteMeta(id, userID int64) error {
	_, err := db.Exec(`DELETE FROM notes_meta WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
