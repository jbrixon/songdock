package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSQLiteConnectionPragmasApplyToEveryConnection(t *testing.T) {
	repo, err := OpenSQLiteSongRepository(filepath.Join(t.TempDir(), "songs.db"))
	if err != nil {
		t.Fatalf("open sqlite repository: %v", err)
	}
	defer repo.Close()

	repo.db.SetMaxOpenConns(2)
	ctx := context.Background()
	conn1, err := repo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire first sqlite connection: %v", err)
	}
	defer conn1.Close()
	conn2, err := repo.db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire second sqlite connection: %v", err)
	}
	defer conn2.Close()

	checkPragmas := func(name string, conn *sql.Conn) {
		t.Run(name, func(t *testing.T) {
			var (
				journalMode string
				foreignKeys int
				busyTimeout int
			)
			if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
				t.Fatalf("query journal mode: %v", err)
			}
			if err := conn.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
				t.Fatalf("query foreign keys: %v", err)
			}
			if err := conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
				t.Fatalf("query busy timeout: %v", err)
			}
			if journalMode != "wal" {
				t.Fatalf("journal mode = %q, want wal", journalMode)
			}
			if foreignKeys != 1 {
				t.Fatalf("foreign keys = %d, want 1", foreignKeys)
			}
			if busyTimeout != 5000 {
				t.Fatalf("busy timeout = %d, want 5000", busyTimeout)
			}
		})
	}
	checkPragmas("first", conn1)
	checkPragmas("second", conn2)
}
