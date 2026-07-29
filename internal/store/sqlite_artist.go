package store

import (
	"database/sql"
	"fmt"
)

// FindArtistBySlug returns the artist matching the given slug, or
// ErrArtistNotFound if no match exists.
func (r *SQLiteSongRepository) FindArtistBySlug(slug string) (*Artist, error) {
	row := r.db.QueryRow(
		`SELECT id, name, slug
		   FROM artists
		  WHERE slug = ?`,
		slug,
	)

	var artist Artist
	if err := row.Scan(&artist.ID, &artist.Name, &artist.Slug); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("query artist: %w", err)
	}

	return &artist, nil
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
