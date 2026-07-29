package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SQLiteSongRepository is a SQLite-backed adapter that satisfies both
// SongRepository and the admin.Repository port.
type SQLiteSongRepository struct {
	db *sql.DB
}

// NewSQLiteSongRepository opens (or creates) the SQLite database at the given
// path, applies performance/safety PRAGMAs, and runs schema migrations.
func NewSQLiteSongRepository(path string) (*SQLiteSongRepository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	pragmas := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec %q: %w", p, err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &SQLiteSongRepository{db: db}, nil
}

// Close releases the underlying database connection.
func (r *SQLiteSongRepository) Close() error {
	return r.db.Close()
}
