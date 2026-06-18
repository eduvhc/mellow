package db

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS search_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			query TEXT NOT NULL,
			provider TEXT NOT NULL,
			result_count INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS downloads (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			filename TEXT NOT NULL,
			file_size INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'pending',
			progress REAL NOT NULL DEFAULT 0,
			speed INTEGER NOT NULL DEFAULT 0,
			error TEXT,
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME
		);

		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`)
	return err
}

// --- Search history ---

func (db *DB) SaveSearch(query, provider string, resultCount int) error {
	_, err := db.conn.Exec(
		`INSERT INTO search_history (query, provider, result_count, created_at) VALUES (?, ?, ?, ?)`,
		query, provider, resultCount, time.Now(),
	)
	return err
}

func (db *DB) RecentSearches(limit int) ([]SearchRecord, error) {
	rows, err := db.conn.Query(
		`SELECT id, query, provider, result_count, created_at FROM search_history ORDER BY created_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []SearchRecord
	for rows.Next() {
		var r SearchRecord
		if err := rows.Scan(&r.ID, &r.Query, &r.Provider, &r.ResultCount, &r.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

type SearchRecord struct {
	ID          int64
	Query       string
	Provider    string
	ResultCount int
	CreatedAt   time.Time
}

// --- Downloads ---

func (db *DB) SaveDownload(id, provider, filename string, fileSize int64) error {
	_, err := db.conn.Exec(
		`INSERT OR REPLACE INTO downloads (id, provider, filename, file_size, status, started_at) VALUES (?, ?, ?, ?, 'pending', ?)`,
		id, provider, filename, fileSize, time.Now(),
	)
	return err
}

func (db *DB) UpdateDownloadStatus(id, status string, progress float64, speed int64, errMsg string) error {
	var err error
	if errMsg != "" {
		_, err = db.conn.Exec(
			`UPDATE downloads SET status = ?, progress = ?, speed = ?, error = ?, completed_at = ? WHERE id = ?`,
			status, progress, speed, errMsg, time.Now(), id,
		)
	} else {
		_, err = db.conn.Exec(
			`UPDATE downloads SET status = ?, progress = ?, speed = ? WHERE id = ?`,
			status, progress, speed, id,
		)
	}
	return err
}

func (db *DB) ListDownloads() ([]DownloadRecord, error) {
	rows, err := db.conn.Query(
		`SELECT id, provider, filename, file_size, status, progress, speed, error, started_at, completed_at FROM downloads ORDER BY started_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DownloadRecord
	for rows.Next() {
		var r DownloadRecord
		if err := rows.Scan(&r.ID, &r.Provider, &r.Filename, &r.FileSize, &r.Status, &r.Progress, &r.Speed, &r.Error, &r.StartedAt, &r.CompletedAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

type DownloadRecord struct {
	ID          string
	Provider    string
	Filename    string
	FileSize    int64
	Status      string
	Progress    float64
	Speed       int64
	Error       sql.NullString
	StartedAt   time.Time
	CompletedAt sql.NullTime
}

// --- Settings ---

func (db *DB) GetSetting(key string) (string, error) {
	var val string
	err := db.conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return val, err
}

func (db *DB) SetSetting(key, value string) error {
	_, err := db.conn.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)`, key, value)
	return err
}
