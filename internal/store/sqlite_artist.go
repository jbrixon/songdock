package store

import (
	"database/sql"
	"fmt"
)

// FindArtistBySlug returns the artist matching the given slug, or
// ErrArtistNotFound if no match exists.
func (r *SQLiteSongRepository) FindArtistBySlug(slug string) (*Artist, error) {
	row := r.db.QueryRow(
		`SELECT id, name, slug, meta_pixel_id
		   FROM artists
		  WHERE slug = ?`,
		slug,
	)

	var artist Artist
	if err := row.Scan(&artist.ID, &artist.Name, &artist.Slug, &artist.MetaPixelID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("query artist: %w", err)
	}

	return &artist, nil
}

// UpdateArtistMetaPixelID updates the Meta Pixel ID for an artist.
func (r *SQLiteSongRepository) UpdateArtistMetaPixelID(artistID int64, pixelID string) error {
	result, err := r.db.Exec(`UPDATE artists SET meta_pixel_id = ? WHERE id = ?`, pixelID, artistID)
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

// ListArtists returns all artists.
func (r *SQLiteSongRepository) ListArtists() ([]Artist, error) {
	rows, err := r.db.Query(
		`SELECT id, name, slug
		   FROM artists
		  ORDER BY name`,
	)
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

// CreateArtist creates a platform-managed artist.
func (r *SQLiteSongRepository) CreateArtist(name, slug string) (*Artist, error) {
	result, err := r.db.Exec(
		`INSERT INTO artists (name, slug) VALUES (?, ?)`,
		name,
		slug,
	)
	if err != nil {
		if isUniqueConstraintError(err) {
			return nil, ErrArtistAlreadyExists
		}
		return nil, fmt.Errorf("insert artist: %w", err)
	}

	artistID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("get inserted artist id: %w", err)
	}

	return &Artist{ID: artistID, Name: name, Slug: slug}, nil
}

// DeleteArtist permanently removes an artist, its songs, memberships, and
// invitations. It returns the artwork keys that must be removed from storage.
func (r *SQLiteSongRepository) DeleteArtist(artistID int64) ([]string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin delete artist transaction: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM artists WHERE id = ?`, artistID).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("query artist for deletion: %w", err)
	}

	var admins int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM user_artists WHERE artist_id = ?`, artistID).Scan(&admins); err != nil {
		return nil, fmt.Errorf("count artist admins: %w", err)
	}
	if admins > 0 {
		return nil, ErrArtistHasAdmins
	}

	rows, err := tx.Query(
		`SELECT DISTINCT artwork_path
		   FROM songs
		  WHERE artist_id = ?
		    AND artwork_path IS NOT NULL
		    AND artwork_path != ''
		    AND NOT EXISTS (
				SELECT 1 FROM songs other
				 WHERE other.artist_id != songs.artist_id
				   AND other.artwork_path = songs.artwork_path
			)`,
		artistID,
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
		`DELETE FROM user_invitations WHERE artist_id = ?`,
		`DELETE FROM user_artists WHERE artist_id = ?`,
		`DELETE FROM songs WHERE artist_id = ?`,
		`DELETE FROM artists WHERE id = ?`,
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
