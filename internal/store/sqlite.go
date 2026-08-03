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
	if err := MigrateSQLiteDatabase(path); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return OpenSQLiteSongRepository(path)
}

// OpenSQLiteSongRepository opens the database without changing its schema.
func OpenSQLiteSongRepository(path string) (*SQLiteSongRepository, error) {
	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	return &SQLiteSongRepository{db: db}, nil
}

// MigrateSQLiteDatabase opens the database, applies its PRAGMAs and runs all
// schema migrations before closing it.
func MigrateSQLiteDatabase(path string) error {
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()

	return migrate(db)
}

func openSQLite(path string) (*sql.DB, error) {
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

	return db, nil
}

// Close releases the underlying database connection.
func (r *SQLiteSongRepository) Close() error {
	return r.db.Close()
}
