package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	postgresMaxOpenConns = 10
	postgresMaxIdleConns = 5
)

// PostgresSongRepository is the PostgreSQL persistence adapter.
type PostgresSongRepository struct {
	db *sql.DB
}

// NewPostgresSongRepository opens PostgreSQL, verifies connectivity, and runs
// the PostgreSQL migrations.
func NewPostgresSongRepository(connectionURL string) (*PostgresSongRepository, error) {
	repo, err := OpenPostgresSongRepository(connectionURL)
	if err != nil {
		return nil, err
	}
	if err := migratePostgres(repo.db); err != nil {
		repo.Close()
		return nil, fmt.Errorf("migrate postgres database: %w", err)
	}
	return repo, nil
}

// OpenPostgresSongRepository opens PostgreSQL without modifying its schema.
func OpenPostgresSongRepository(connectionURL string) (*PostgresSongRepository, error) {
	connectionURL = strings.TrimSpace(connectionURL)
	if connectionURL == "" {
		return nil, errors.New("postgres connection URL must be set")
	}

	db, err := sql.Open("pgx", connectionURL)
	if err != nil {
		return nil, postgresConnectionError("open", connectionURL, err)
	}
	db.SetMaxOpenConns(postgresMaxOpenConns)
	db.SetMaxIdleConns(postgresMaxIdleConns)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, postgresConnectionError("connect", connectionURL, err)
	}
	return &PostgresSongRepository{db: db}, nil
}

// MigratePostgresDatabase applies PostgreSQL migrations and closes the pool.
func MigratePostgresDatabase(connectionURL string) error {
	repo, err := OpenPostgresSongRepository(connectionURL)
	if err != nil {
		return err
	}
	defer repo.Close()
	return migratePostgres(repo.db)
}

// Close releases the PostgreSQL connection pool.
func (r *PostgresSongRepository) Close() error {
	return r.db.Close()
}

func postgresConnectionError(action, connectionURL string, err error) error {
	message := err.Error()
	message = strings.ReplaceAll(message, connectionURL, "[redacted]")
	if parsed, parseErr := url.Parse(connectionURL); parseErr == nil && parsed.User != nil {
		message = strings.ReplaceAll(message, parsed.User.String(), "[redacted]")
		if password, ok := parsed.User.Password(); ok {
			message = strings.ReplaceAll(message, password, "[redacted]")
		}
	}
	return fmt.Errorf("%s postgres database: %s", action, message)
}

func isPostgresError(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

func postgresUniqueError(err error, fallback error) error {
	if !isPostgresError(err, "23505") {
		return nil
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "users_email_key", "users_email_lower_unique":
			return ErrUserAlreadyExists
		case "user_invitations_email_key", "user_invitations_email_lower_unique", "user_invitations_code_hash_unique":
			return ErrUserInvitationAlreadyExists
		}
	}
	return fallback
}

func postgresTimestamp(value sql.NullTime) string {
	if !value.Valid {
		return ""
	}
	return sqliteTimestamp(value.Time)
}

func postgresInvitationStatus(acceptedAt, expiresAt, revokedAt sql.NullTime) error {
	return invitationStatus(UserInvitation{
		AcceptedAt: postgresTimestamp(acceptedAt),
		ExpiresAt:  postgresTimestamp(expiresAt),
		RevokedAt:  postgresTimestamp(revokedAt),
	}, time.Now().UTC())
}

func (r *PostgresSongRepository) FindUserByEmail(email string) (*User, error) {
	var user User
	err := r.db.QueryRow(
		`SELECT id, email, password_hash FROM users WHERE email = $1`,
		NormalizeEmail(email),
	).Scan(&user.ID, &user.Email, &user.PasswordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("query user: %w", err)
	}
	return &user, nil
}

func (r *PostgresSongRepository) ListUsers() ([]UserWithArtists, error) {
	rows, err := r.db.Query(
		`SELECT u.id, u.email, a.id, a.name, a.slug
		   FROM users u
		   LEFT JOIN user_artists ua ON ua.user_id = u.id
		   LEFT JOIN artists a ON a.id = ua.artist_id
		  ORDER BY u.email, a.name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()

	var users []UserWithArtists
	byID := map[int64]int{}
	for rows.Next() {
		var (
			userID     int64
			email      string
			artistID   sql.NullInt64
			artistName sql.NullString
			artistSlug sql.NullString
		)
		if err := rows.Scan(&userID, &email, &artistID, &artistName, &artistSlug); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		idx, ok := byID[userID]
		if !ok {
			idx = len(users)
			byID[userID] = idx
			users = append(users, UserWithArtists{ID: userID, Email: email})
		}
		if artistID.Valid {
			users[idx].Artists = append(users[idx].Artists, Artist{
				ID: artistID.Int64, Name: artistName.String, Slug: artistSlug.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (r *PostgresSongRepository) CreateUser(email, passwordHash string) (int64, error) {
	email = NormalizeEmail(email)
	if existing, err := r.FindUserByEmail(email); err == nil {
		return existing.ID, ErrUserAlreadyExists
	} else if !errors.Is(err, ErrUserNotFound) {
		return 0, err
	}

	var id int64
	err := r.db.QueryRow(
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`,
		email, passwordHash,
	).Scan(&id)
	if err != nil {
		if mapped := postgresUniqueError(err, ErrUserAlreadyExists); mapped != nil {
			return 0, mapped
		}
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return id, nil
}

func (r *PostgresSongRepository) IsUserTokenRevoked(userID int64) (bool, error) {
	var revokedAt int64
	err := r.db.QueryRow(`SELECT revoked_at FROM user_token_revocations WHERE user_id = $1`, userID).Scan(&revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query user token revocation: %w", err)
	}
	return revokedAt > 0, nil
}

func (r *PostgresSongRepository) DeleteUser(userID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete user transaction: %w", err)
	}
	defer tx.Rollback()

	var email string
	if err := tx.QueryRow(`SELECT email FROM users WHERE id = $1`, userID).Scan(&email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("query user for deletion: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO user_token_revocations (user_id, revoked_at) VALUES ($1, $2)
		 ON CONFLICT (user_id) DO UPDATE SET revoked_at = EXCLUDED.revoked_at`,
		userID, time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("revoke user tokens: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM user_artists WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete user artist memberships: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM user_invitations WHERE email = $1`, email); err != nil {
		return fmt.Errorf("delete user invitations: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = $1`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete user transaction: %w", err)
	}
	return nil
}
func (r *PostgresSongRepository) ListArtistsForUser(userID int64) ([]Artist, error) {
	rows, err := r.db.Query(
		`SELECT a.id, a.name, a.slug, a.meta_pixel_id
		   FROM artists a JOIN user_artists ua ON ua.artist_id = a.id
		  WHERE ua.user_id = $1 ORDER BY a.name`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query user artists: %w", err)
	}
	defer rows.Close()
	var artists []Artist
	for rows.Next() {
		var artist Artist
		if err := rows.Scan(&artist.ID, &artist.Name, &artist.Slug, &artist.MetaPixelID); err != nil {
			return nil, fmt.Errorf("scan user artist: %w", err)
		}
		artists = append(artists, artist)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user artists: %w", err)
	}
	return artists, nil
}

func (r *PostgresSongRepository) AssignUserToArtist(userID int64, artistSlug string) error {
	var one int
	if err := r.db.QueryRow(`SELECT 1 FROM users WHERE id = $1`, userID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("query user for assignment: %w", err)
	}

	artist, err := r.FindArtistBySlug(artistSlug)
	if err != nil {
		return err
	}
	if _, err := r.db.Exec(
		`INSERT INTO user_artists (user_id, artist_id) VALUES ($1, $2)
		 ON CONFLICT (user_id, artist_id) DO NOTHING`, userID, artist.ID,
	); err != nil {
		return fmt.Errorf("assign user to artist: %w", err)
	}
	return nil
}

func (r *PostgresSongRepository) IsUserAssignedToArtist(userID, artistID int64) (bool, error) {
	var one int
	err := r.db.QueryRow(
		`SELECT 1 FROM user_artists WHERE user_id = $1 AND artist_id = $2`, userID, artistID,
	).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query user artist membership: %w", err)
	}
	return true, nil
}

func (r *PostgresSongRepository) FindBySlug(artistSlug, songSlug string) (*Song, error) {
	var song Song
	err := r.db.QueryRow(
		`SELECT s.title, s.artist_name, s.description, s.image_url, s.artwork_path,
		        s.youtube_url, s.spotify_url, s.apple_music_url, s.song_slug,
		        a.slug, a.meta_pixel_id
		   FROM songs s JOIN artists a ON a.id = s.artist_id
		  WHERE a.slug = $1 AND s.song_slug = $2`, artistSlug, songSlug,
	).Scan(
		&song.Title, &song.ArtistName, &song.Description, &song.ImageURL,
		&song.ArtworkPath, &song.YouTubeURL, &song.SpotifyURL,
		&song.AppleMusicURL, &song.SongSlug, &song.ArtistSlug, &song.MetaPixelID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query song: %w", err)
	}
	return &song, nil
}

func (r *PostgresSongRepository) SongSlugExists(artistID int64, songSlug string) (bool, error) {
	var one int
	err := r.db.QueryRow(`SELECT 1 FROM songs WHERE artist_id = $1 AND song_slug = $2`, artistID, songSlug).Scan(&one)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("query song slug exists: %w", err)
	}
	return true, nil
}

func (r *PostgresSongRepository) ListSongsForArtist(artistID int64) ([]Song, error) {
	rows, err := r.db.Query(
		`SELECT s.title, s.artist_name, s.description, s.image_url, s.artwork_path,
		        s.youtube_url, s.spotify_url, s.apple_music_url, s.song_slug, a.slug
		   FROM songs s JOIN artists a ON a.id = s.artist_id
		  WHERE s.artist_id = $1 ORDER BY lower(s.title), s.song_slug`, artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("query artist songs: %w", err)
	}
	defer rows.Close()
	var songs []Song
	for rows.Next() {
		var song Song
		if err := rows.Scan(
			&song.Title, &song.ArtistName, &song.Description, &song.ImageURL,
			&song.ArtworkPath, &song.YouTubeURL, &song.SpotifyURL,
			&song.AppleMusicURL, &song.SongSlug, &song.ArtistSlug,
		); err != nil {
			return nil, fmt.Errorf("scan artist song: %w", err)
		}
		songs = append(songs, song)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artist songs: %w", err)
	}
	return songs, nil
}

func (r *PostgresSongRepository) InsertSongForArtist(artistID int64, song Song) error {
	_, err := r.db.Exec(
		`INSERT INTO songs (title, artist_name, description, image_url, artwork_path,
		                   youtube_url, spotify_url, apple_music_url, song_slug, artist_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		song.Title, song.ArtistName, song.Description, song.ImageURL, song.ArtworkPath,
		song.YouTubeURL, song.SpotifyURL, song.AppleMusicURL, song.SongSlug, artistID,
	)
	if err != nil {
		if mapped := postgresUniqueError(err, ErrSongAlreadyExists); mapped != nil {
			return mapped
		}
		return fmt.Errorf("insert song %q: %w", song.SongSlug, err)
	}
	return nil
}

func (r *PostgresSongRepository) UpdateSongForArtist(artistID int64, songSlug string, song Song) error {
	result, err := r.db.Exec(
		`UPDATE songs SET title = $1, artist_name = $2, description = $3, image_url = $4,
		                  artwork_path = $5, youtube_url = $6, spotify_url = $7,
		                  apple_music_url = $8
		  WHERE artist_id = $9 AND song_slug = $10`,
		song.Title, song.ArtistName, song.Description, song.ImageURL, song.ArtworkPath,
		song.YouTubeURL, song.SpotifyURL, song.AppleMusicURL, artistID, songSlug,
	)
	if err != nil {
		return fmt.Errorf("update song %q: %w", songSlug, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get updated song row count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresSongRepository) DeleteSongForArtist(artistID int64, songSlug string) error {
	result, err := r.db.Exec(`DELETE FROM songs WHERE artist_id = $1 AND song_slug = $2`, artistID, songSlug)
	if err != nil {
		return fmt.Errorf("delete song %q: %w", songSlug, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted song row count: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresSongRepository) Insert(songs ...Song) error {
	artistIDs := make(map[string]int64)
	for _, song := range songs {
		if _, err := r.db.Exec(
			`INSERT INTO artists (name, slug) VALUES ($1, $2)
			 ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name`,
			song.ArtistName, song.ArtistSlug,
		); err != nil {
			return fmt.Errorf("upsert artist %q: %w", song.ArtistSlug, err)
		}
		if _, cached := artistIDs[song.ArtistSlug]; !cached {
			var artistID int64
			if err := r.db.QueryRow(`SELECT id FROM artists WHERE slug = $1`, song.ArtistSlug).Scan(&artistID); err != nil {
				return fmt.Errorf("get artist id for %q: %w", song.ArtistSlug, err)
			}
			artistIDs[song.ArtistSlug] = artistID
		}
		if _, err := r.db.Exec(
			`INSERT INTO songs (title, artist_name, description, image_url, artwork_path,
			                    youtube_url, spotify_url, apple_music_url, song_slug, artist_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) ON CONFLICT DO NOTHING`,
			song.Title, song.ArtistName, song.Description, song.ImageURL, song.ArtworkPath,
			song.YouTubeURL, song.SpotifyURL, song.AppleMusicURL, song.SongSlug,
			artistIDs[song.ArtistSlug],
		); err != nil {
			return fmt.Errorf("insert song %q: %w", song.SongSlug, err)
		}
	}
	return nil
}

func (r *PostgresSongRepository) FindArtistBySlug(slug string) (*Artist, error) {
	var artist Artist
	err := r.db.QueryRow(`SELECT id, name, slug, meta_pixel_id FROM artists WHERE slug = $1`, slug).Scan(
		&artist.ID, &artist.Name, &artist.Slug, &artist.MetaPixelID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("query artist: %w", err)
	}
	return &artist, nil
}

func (r *PostgresSongRepository) UpdateArtistMetaPixelID(artistID int64, pixelID string) error {
	result, err := r.db.Exec(`UPDATE artists SET meta_pixel_id = $1 WHERE id = $2`, pixelID, artistID)
	if err != nil {
		return fmt.Errorf("update artist Meta Pixel ID: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get updated artist row count: %w", err)
	}
	if affected == 0 {
		return ErrArtistNotFound
	}
	return nil
}

func (r *PostgresSongRepository) ListArtists() ([]Artist, error) {
	rows, err := r.db.Query(`SELECT id, name, slug FROM artists ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("query artists: %w", err)
	}
	defer rows.Close()
	var artists []Artist
	for rows.Next() {
		var artist Artist
		if err := rows.Scan(&artist.ID, &artist.Name, &artist.Slug); err != nil {
			return nil, fmt.Errorf("scan artist: %w", err)
		}
		artists = append(artists, artist)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate artists: %w", err)
	}
	return artists, nil
}

func (r *PostgresSongRepository) CreateArtist(name, slug string) (*Artist, error) {
	var artist Artist
	err := r.db.QueryRow(
		`INSERT INTO artists (name, slug) VALUES ($1, $2) RETURNING id, name, slug, meta_pixel_id`, name, slug,
	).Scan(&artist.ID, &artist.Name, &artist.Slug, &artist.MetaPixelID)
	if err != nil {
		if mapped := postgresUniqueError(err, ErrArtistAlreadyExists); mapped != nil {
			return nil, mapped
		}
		return nil, fmt.Errorf("insert artist: %w", err)
	}
	return &artist, nil
}

func (r *PostgresSongRepository) DeleteArtist(artistID int64) ([]string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin delete artist transaction: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM artists WHERE id = $1`, artistID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("query artist for deletion: %w", err)
	}
	var admins int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_artists WHERE artist_id = $1`, artistID).Scan(&admins); err != nil {
		return nil, fmt.Errorf("count artist admins: %w", err)
	}
	if admins > 0 {
		return nil, ErrArtistHasAdmins
	}

	rows, err := tx.Query(
		`SELECT DISTINCT artwork_path FROM songs WHERE artist_id = $1
		   AND artwork_path IS NOT NULL AND artwork_path <> ''
		   AND NOT EXISTS (SELECT 1 FROM songs other
		                    WHERE other.artist_id <> songs.artist_id
		                      AND other.artwork_path = songs.artwork_path)`, artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("query artist artwork: %w", err)
	}
	var artworkPaths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan artist artwork: %w", err)
		}
		artworkPaths = append(artworkPaths, path)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate artist artwork: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close artist artwork rows: %w", err)
	}

	for _, statement := range []string{
		`DELETE FROM user_invitations WHERE artist_id = $1`,
		`DELETE FROM user_artists WHERE artist_id = $1`,
		`DELETE FROM songs WHERE artist_id = $1`,
		`DELETE FROM artists WHERE id = $1`,
	} {
		if _, err := tx.Exec(statement, artistID); err != nil {
			return nil, fmt.Errorf("delete artist data: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit delete artist transaction: %w", err)
	}
	return artworkPaths, nil
}

func (r *PostgresSongRepository) CreateUserInvitation(email, invitationCodeHash string, artistID int64) error {
	email = NormalizeEmail(email)
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin create user invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var existingID int64
	if err := tx.QueryRow(`SELECT id FROM users WHERE email = $1`, email).Scan(&existingID); err == nil {
		return ErrUserAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query existing user: %w", err)
	}
	if err := tx.QueryRow(`SELECT id FROM user_invitations WHERE email = $1`, email).Scan(&existingID); err == nil {
		return ErrUserInvitationAlreadyExists
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query existing invitation: %w", err)
	}
	if err := tx.QueryRow(`SELECT id FROM artists WHERE id = $1`, artistID).Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrArtistNotFound
		}
		return fmt.Errorf("query invitation artist: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO user_invitations (email, artist_id, invitation_code_hash, expires_at)
		 VALUES ($1, $2, $3, $4)`, email, artistID, invitationCodeHash,
		time.Now().UTC().Add(InvitationLifetime),
	); err != nil {
		if mapped := postgresUniqueError(err, nil); mapped != nil {
			return mapped
		}
		return fmt.Errorf("insert user invitation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create user invitation transaction: %w", err)
	}
	return nil
}

func (r *PostgresSongRepository) ReissueUserInvitation(invitationID int64, invitationCodeHash string, artistID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reissue user invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var artistExists int64
	if err := tx.QueryRow(`SELECT id FROM artists WHERE id = $1`, artistID).Scan(&artistExists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrArtistNotFound
		}
		return fmt.Errorf("query reissue invitation artist: %w", err)
	}

	var acceptedAt, expiresAt, revokedAt sql.NullTime
	err = tx.QueryRow(
		`SELECT accepted_at, expires_at, revoked_at
		   FROM user_invitations WHERE id = $1 FOR UPDATE`,
		invitationID,
	).Scan(&acceptedAt, &expiresAt, &revokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationNotFound
		}
		return fmt.Errorf("query invitation for reissue: %w", err)
	}
	if err := invitationReissueStatus(UserInvitation{
		AcceptedAt: postgresTimestamp(acceptedAt),
		ExpiresAt:  postgresTimestamp(expiresAt),
		RevokedAt:  postgresTimestamp(revokedAt),
	}, time.Now().UTC()); err != nil {
		return err
	}

	now := time.Now().UTC()
	result, err := tx.Exec(
		`UPDATE user_invitations
		    SET artist_id = $1, invitation_code_hash = $2, created_at = $3, expires_at = $4, revoked_at = NULL
		  WHERE id = $5 AND accepted_at IS NULL`,
		artistID,
		invitationCodeHash,
		now,
		now.Add(InvitationLifetime),
		invitationID,
	)
	if err != nil {
		return fmt.Errorf("update invitation for reissue: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("check reissued invitation: %w", err)
	} else if rows != 1 {
		return ErrInvitationAlreadyAccepted
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reissue user invitation transaction: %w", err)
	}
	return nil
}

func (r *PostgresSongRepository) FindInvitationByCodeHash(codeHash string) (*UserInvitation, error) {
	var invitation UserInvitation
	err := r.db.QueryRow(
		`SELECT ui.id, ui.email, COALESCE(ui.artist_id, 0), COALESCE(a.name, ''),
		        ui.invitation_code_hash,
		        to_char(ui.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		        COALESCE(to_char(ui.accepted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        COALESCE(to_char(ui.expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        COALESCE(to_char(ui.revoked_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), '')
		   FROM user_invitations ui LEFT JOIN artists a ON a.id = ui.artist_id
		  WHERE ui.invitation_code_hash = $1`, codeHash,
	).Scan(
		&invitation.ID, &invitation.Email, &invitation.ArtistID, &invitation.ArtistName,
		&invitation.InvitationCodeHash, &invitation.CreatedAt, &invitation.AcceptedAt,
		&invitation.ExpiresAt, &invitation.RevokedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvitationNotFound
		}
		return nil, fmt.Errorf("query invitation: %w", err)
	}
	if err := invitationStatus(invitation, time.Now().UTC()); err != nil {
		return nil, err
	}
	return &invitation, nil
}

func (r *PostgresSongRepository) RedeemInvitation(invitationID int64, invitationCodeHash, email, passwordHash string) (int64, error) {
	email = NormalizeEmail(email)
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin redeem invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var acceptedAt, expiresAt, revokedAt sql.NullTime
	var artistID sql.NullInt64
	err = tx.QueryRow(
		`SELECT accepted_at, expires_at, revoked_at, artist_id
		   FROM user_invitations
		  WHERE id = $1 AND invitation_code_hash = $2 FOR UPDATE`, invitationID, invitationCodeHash,
	).Scan(&acceptedAt, &expiresAt, &revokedAt, &artistID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrInvitationNotFound
		}
		return 0, fmt.Errorf("query invitation for redemption: %w", err)
	}
	if err := postgresInvitationStatus(acceptedAt, expiresAt, revokedAt); err != nil {
		return 0, err
	}
	if !artistID.Valid || artistID.Int64 == 0 {
		return 0, ErrArtistNotFound
	}

	var userID int64
	if err := tx.QueryRow(
		`INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id`, email, passwordHash,
	).Scan(&userID); err != nil {
		if mapped := postgresUniqueError(err, ErrUserAlreadyExists); mapped != nil {
			return 0, mapped
		}
		return 0, fmt.Errorf("insert user during redemption: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO user_artists (user_id, artist_id) VALUES ($1, $2)`, userID, artistID.Int64); err != nil {
		return 0, fmt.Errorf("assign redeemed user to artist: %w", err)
	}
	result, err := tx.Exec(
		`UPDATE user_invitations
		    SET accepted_at = CURRENT_TIMESTAMP
		  WHERE id = $1 AND invitation_code_hash = $2 AND accepted_at IS NULL`,
		invitationID,
		invitationCodeHash,
	)
	if err != nil {
		return 0, fmt.Errorf("mark invitation accepted: %w", err)
	}
	if rows, err := result.RowsAffected(); err != nil {
		return 0, fmt.Errorf("check accepted invitation: %w", err)
	} else if rows != 1 {
		return 0, ErrInvitationNotFound
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit redeem invitation transaction: %w", err)
	}
	return userID, nil
}

func (r *PostgresSongRepository) ListPendingInvitations() ([]UserInvitation, error) {
	rows, err := r.db.Query(
		`SELECT ui.id, ui.email, ui.artist_id, COALESCE(a.name, ''),
		        ui.invitation_code_hash,
		        to_char(ui.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
		        COALESCE(to_char(ui.accepted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        COALESCE(to_char(ui.expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        COALESCE(to_char(ui.revoked_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        CASE WHEN ui.revoked_at IS NOT NULL THEN 'revoked'
		             WHEN ui.expires_at <= CURRENT_TIMESTAMP THEN 'expired'
		             ELSE 'active' END
		   FROM user_invitations ui LEFT JOIN artists a ON a.id = ui.artist_id
		  WHERE ui.accepted_at IS NULL
		  ORDER BY ui.created_at DESC, ui.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query invitations: %w", err)
	}
	defer rows.Close()
	var invitations []UserInvitation
	for rows.Next() {
		var invitation UserInvitation
		if err := rows.Scan(
			&invitation.ID, &invitation.Email, &invitation.ArtistID, &invitation.ArtistName,
			&invitation.InvitationCodeHash, &invitation.CreatedAt, &invitation.AcceptedAt,
			&invitation.ExpiresAt, &invitation.RevokedAt, &invitation.Status,
		); err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		invitations = append(invitations, invitation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitations: %w", err)
	}
	return invitations, nil
}

func (r *PostgresSongRepository) RevokeInvitation(invitationID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin revoke invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var invitation UserInvitation
	err = tx.QueryRow(
		`SELECT COALESCE(to_char(accepted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        COALESCE(to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
		        COALESCE(to_char(revoked_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), '')
		   FROM user_invitations WHERE id = $1 FOR UPDATE`, invitationID,
	).Scan(&invitation.AcceptedAt, &invitation.ExpiresAt, &invitation.RevokedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationNotFound
		}
		return fmt.Errorf("query invitation for revoke: %w", err)
	}
	if invitation.AcceptedAt != "" {
		return ErrInvitationAlreadyAccepted
	}
	if invitation.RevokedAt != "" {
		return ErrInvitationRevoked
	}
	if _, err := tx.Exec(`UPDATE user_invitations SET revoked_at = CURRENT_TIMESTAMP WHERE id = $1`, invitationID); err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke invitation transaction: %w", err)
	}
	return nil
}
