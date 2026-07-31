package store

import (
	"database/sql"
	"fmt"
)

// migrate creates the required tables if they do not already exist and applies
// any incremental schema changes needed for older databases.
func migrate(db *sql.DB) error {
	// artists: one row per musical act, identified by a unique slug.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS artists (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT    NOT NULL,
			slug TEXT    NOT NULL UNIQUE
		)
	`); err != nil {
		return err
	}
	artistColumns, err := tableColumns(db, "artists")
	if err != nil {
		return err
	}
	if _, exists := artistColumns["meta_pixel_id"]; !exists {
		if _, err := db.Exec(`ALTER TABLE artists ADD COLUMN meta_pixel_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}

	// users: platform accounts. Artist membership is stored in user_artists.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			email         TEXT    NOT NULL UNIQUE,
			password_hash TEXT    NOT NULL
		)
	`); err != nil {
		return err
	}

	// songs: for new databases the artist_slug column is omitted and
	// artist_id (FK → artists) is used instead.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS songs (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			title           TEXT    NOT NULL,
			artist_name     TEXT    NOT NULL,
			description     TEXT    NOT NULL DEFAULT '',
			image_url       TEXT    NOT NULL DEFAULT '',
			youtube_url     TEXT    NOT NULL DEFAULT '',
			spotify_url     TEXT    NOT NULL DEFAULT '',
			apple_music_url TEXT    NOT NULL DEFAULT '',
			song_slug       TEXT    NOT NULL,
			artist_id       INTEGER NOT NULL REFERENCES artists(id),
			UNIQUE (artist_id, song_slug)
		)
	`); err != nil {
		return err
	}

	// Inspect existing columns so we can apply incremental ALTER TABLE steps.
	existingColumns, err := songColumns(db)
	if err != nil {
		return err
	}

	// Optional columns added to old databases that pre-date them.
	optional := map[string]string{
		"description":     "ALTER TABLE songs ADD COLUMN description TEXT NOT NULL DEFAULT ''",
		"image_url":       "ALTER TABLE songs ADD COLUMN image_url TEXT NOT NULL DEFAULT ''",
		"artwork_path":    "ALTER TABLE songs ADD COLUMN artwork_path TEXT NOT NULL DEFAULT ''",
		"youtube_url":     "ALTER TABLE songs ADD COLUMN youtube_url TEXT NOT NULL DEFAULT ''",
		"spotify_url":     "ALTER TABLE songs ADD COLUMN spotify_url TEXT NOT NULL DEFAULT ''",
		"apple_music_url": "ALTER TABLE songs ADD COLUMN apple_music_url TEXT NOT NULL DEFAULT ''",
	}
	for name, ddl := range optional {
		if _, exists := existingColumns[name]; exists {
			continue
		}
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}

	// Migrate databases that still have artist_slug but lack artist_id.
	if _, hasArtistID := existingColumns["artist_id"]; !hasArtistID {
		if _, err := db.Exec(`ALTER TABLE songs ADD COLUMN artist_id INTEGER REFERENCES artists(id)`); err != nil {
			return err
		}
		// Populate artists from the distinct (artist_name, artist_slug) pairs already in songs.
		if _, err := db.Exec(`
			INSERT OR IGNORE INTO artists (name, slug)
			SELECT DISTINCT artist_name, artist_slug FROM songs
		`); err != nil {
			return err
		}
		// Back-fill artist_id on every existing song row.
		if _, err := db.Exec(`
			UPDATE songs SET artist_id = (
				SELECT id FROM artists WHERE slug = songs.artist_slug
			) WHERE artist_id IS NULL
		`); err != nil {
			return err
		}
	}

	userColumns, err := tableColumns(db, "users")
	if err != nil {
		return err
	}
	if _, hasArtistID := userColumns["artist_id"]; hasArtistID {
		if err := removeLegacyUserArtistID(db); err != nil {
			return err
		}
	}

	// user_artists: many-to-many membership of users to artists.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS user_artists (
			user_id   INTEGER NOT NULL REFERENCES users(id),
			artist_id INTEGER NOT NULL REFERENCES artists(id),
			PRIMARY KEY (user_id, artist_id)
		)
	`); err != nil {
		return err
	}

	// user_invitations: pending invite records before a user account is claimed.
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS user_invitations (
			id                   INTEGER PRIMARY KEY AUTOINCREMENT,
			email                TEXT    NOT NULL UNIQUE,
			artist_id            INTEGER NOT NULL REFERENCES artists(id),
			invitation_code_hash TEXT    NOT NULL,
			created_at           TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
			accepted_at          TEXT,
			expires_at           TEXT,
			revoked_at           TEXT
		)
	`); err != nil {
		return err
	}

	invitationColumns, err := tableColumns(db, "user_invitations")
	if err != nil {
		return err
	}
	if _, exists := invitationColumns["artist_id"]; !exists {
		if _, err := db.Exec(`ALTER TABLE user_invitations ADD COLUMN artist_id INTEGER REFERENCES artists(id)`); err != nil {
			return err
		}
	}
	if _, exists := invitationColumns["expires_at"]; !exists {
		if _, err := db.Exec(`ALTER TABLE user_invitations ADD COLUMN expires_at TEXT`); err != nil {
			return err
		}
	}
	if _, exists := invitationColumns["revoked_at"]; !exists {
		if _, err := db.Exec(`ALTER TABLE user_invitations ADD COLUMN revoked_at TEXT`); err != nil {
			return err
		}
	}

	if _, err := db.Exec(`
		UPDATE user_invitations
		   SET expires_at = datetime('now', '+14 days')
		 WHERE expires_at IS NULL AND accepted_at IS NULL
	`); err != nil {
		return err
	}

	if err := ensureNormalizedEmailUniqueness(db, "users"); err != nil {
		return err
	}
	if err := ensureNormalizedEmailUniqueness(db, "user_invitations"); err != nil {
		return err
	}

	indexes := []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique ON users (lower(email))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS user_invitations_email_lower_unique ON user_invitations (lower(email))`,
		`CREATE UNIQUE INDEX IF NOT EXISTS user_invitations_code_hash_unique ON user_invitations (invitation_code_hash)`,
	}
	for _, ddl := range indexes {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}

	return nil
}

func removeLegacyUserArtistID(db *sql.DB) error {
	hasUserArtists, err := tableExists(db, "user_artists")
	if err != nil {
		return err
	}

	if _, err := db.Exec(`
		CREATE TEMP TABLE user_artist_backfill (
			user_id   INTEGER NOT NULL,
			artist_id INTEGER NOT NULL,
			PRIMARY KEY (user_id, artist_id)
		)
	`); err != nil {
		return err
	}
	if hasUserArtists {
		if _, err := db.Exec(`
			INSERT OR IGNORE INTO user_artist_backfill (user_id, artist_id)
			SELECT user_id, artist_id
			  FROM user_artists
		`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(`
		INSERT OR IGNORE INTO user_artist_backfill (user_id, artist_id)
		SELECT id, artist_id
		  FROM users
		 WHERE artist_id IS NOT NULL
	`); err != nil {
		return err
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys = ON`)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`DROP TABLE IF EXISTS user_artists`,
		`ALTER TABLE users RENAME TO users_legacy`,
		`CREATE TABLE users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			email         TEXT    NOT NULL UNIQUE,
			password_hash TEXT    NOT NULL
		)`,
		`INSERT INTO users (id, email, password_hash)
		 SELECT id, email, password_hash
		   FROM users_legacy`,
		`DROP TABLE users_legacy`,
		`CREATE TABLE IF NOT EXISTS user_artists (
			user_id   INTEGER NOT NULL REFERENCES users(id),
			artist_id INTEGER NOT NULL REFERENCES artists(id),
			PRIMARY KEY (user_id, artist_id)
		)`,
		`INSERT OR IGNORE INTO user_artists (user_id, artist_id)
		 SELECT user_id, artist_id
		   FROM user_artist_backfill`,
	}
	for _, stmt := range statements {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	if _, err := db.Exec(`DROP TABLE user_artist_backfill`); err != nil {
		return err
	}
	return nil
}

// songColumns returns the set of column names present on the songs table.
func songColumns(db *sql.DB) (map[string]struct{}, error) {
	return tableColumns(db, "songs")
}

func tableColumns(db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]struct{})
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = struct{}{}
	}
	return cols, rows.Err()
}

func tableExists(db *sql.DB, table string) (bool, error) {
	row := db.QueryRow(`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, table)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func ensureNormalizedEmailUniqueness(db *sql.DB, table string) error {
	row := db.QueryRow(`
		SELECT lower(email)
		  FROM ` + table + `
		 GROUP BY lower(email)
		HAVING COUNT(*) > 1
		 LIMIT 1`)

	var duplicate string
	if err := row.Scan(&duplicate); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}

	return fmt.Errorf("%s contains case-colliding email addresses for %q; normalize the duplicates before running this build", table, duplicate)
}
