package store

import (
	"database/sql"
	"fmt"
)

// ListArtistsForUser returns all artists assigned to the given user.
func (r *SQLiteSongRepository) ListArtistsForUser(userID int64) ([]Artist, error) {
	rows, err := r.db.Query(
		`SELECT a.id, a.name, a.slug, a.meta_pixel_id
		   FROM artists a
		   JOIN user_artists ua ON ua.artist_id = a.id
		  WHERE ua.user_id = ?
		  ORDER BY a.name`,
		userID,
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

// AssignUserToArtist assigns a user to an artist identified by slug.
func (r *SQLiteSongRepository) AssignUserToArtist(userID int64, artistSlug string) error {
	var one int
	if err := r.db.QueryRow(`SELECT 1 FROM users WHERE id = ?`, userID).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return ErrUserNotFound
		}
		return fmt.Errorf("query user for assignment: %w", err)
	}

	artist, err := r.FindArtistBySlug(artistSlug)
	if err != nil {
		return err
	}

	if _, err := r.db.Exec(
		`INSERT OR IGNORE INTO user_artists (user_id, artist_id) VALUES (?, ?)`,
		userID,
		artist.ID,
	); err != nil {
		return fmt.Errorf("assign user to artist: %w", err)
	}
	return nil
}

// IsUserAssignedToArtist reports whether the given user belongs to the given
// artist through the user_artists table.
func (r *SQLiteSongRepository) IsUserAssignedToArtist(userID, artistID int64) (bool, error) {
	row := r.db.QueryRow(
		`SELECT 1
		   FROM user_artists
		  WHERE user_id = ? AND artist_id = ?`,
		userID,
		artistID,
	)

	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query user artist membership: %w", err)
	}

	return true, nil
}
