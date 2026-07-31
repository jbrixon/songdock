package store

import (
	"database/sql"
	"fmt"
)

// FindBySlug returns the song matching the given artist and song slugs, or
// ErrNotFound if no match exists.
func (r *SQLiteSongRepository) FindBySlug(artistSlug, songSlug string) (*Song, error) {
	row := r.db.QueryRow(
		`SELECT s.title, s.artist_name, s.description, s.image_url, s.artwork_path, s.youtube_url, s.spotify_url, s.apple_music_url, s.song_slug, a.slug
		   FROM songs s
		   JOIN artists a ON a.id = s.artist_id
		  WHERE a.slug = ? AND s.song_slug = ?`,
		artistSlug, songSlug,
	)

	var s Song
	if err := row.Scan(
		&s.Title,
		&s.ArtistName,
		&s.Description,
		&s.ImageURL,
		&s.ArtworkPath,
		&s.YouTubeURL,
		&s.SpotifyURL,
		&s.AppleMusicURL,
		&s.SongSlug,
		&s.ArtistSlug,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query song: %w", err)
	}
	return &s, nil
}

// SongSlugExists reports whether a song slug already exists for an artist.
func (r *SQLiteSongRepository) SongSlugExists(artistID int64, songSlug string) (bool, error) {
	row := r.db.QueryRow(
		`SELECT 1
		   FROM songs
		  WHERE artist_id = ? AND song_slug = ?`,
		artistID,
		songSlug,
	)

	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query song slug exists: %w", err)
	}

	return true, nil
}

// ListSongsForArtist returns all songs for the given artist.
func (r *SQLiteSongRepository) ListSongsForArtist(artistID int64) ([]Song, error) {
	rows, err := r.db.Query(
		`SELECT s.title, s.artist_name, s.description, s.image_url, s.artwork_path, s.youtube_url, s.spotify_url, s.apple_music_url, s.song_slug, a.slug
		   FROM songs s
		   JOIN artists a ON a.id = s.artist_id
		  WHERE s.artist_id = ?
		  ORDER BY s.title COLLATE NOCASE, s.song_slug`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("query artist songs: %w", err)
	}
	defer rows.Close()

	var songs []Song
	for rows.Next() {
		var song Song
		if err := rows.Scan(
			&song.Title,
			&song.ArtistName,
			&song.Description,
			&song.ImageURL,
			&song.ArtworkPath,
			&song.YouTubeURL,
			&song.SpotifyURL,
			&song.AppleMusicURL,
			&song.SongSlug,
			&song.ArtistSlug,
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

// InsertSongForArtist inserts a new song for a specific artist.
func (r *SQLiteSongRepository) InsertSongForArtist(artistID int64, song Song) error {
	if _, err := r.db.Exec(
		`INSERT INTO songs (
			title, artist_name, description, image_url, artwork_path, youtube_url, spotify_url, apple_music_url, song_slug, artist_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		song.Title,
		song.ArtistName,
		song.Description,
		song.ImageURL,
		song.ArtworkPath,
		song.YouTubeURL,
		song.SpotifyURL,
		song.AppleMusicURL,
		song.SongSlug,
		artistID,
	); err != nil {
		if isUniqueConstraintError(err) {
			return ErrSongAlreadyExists
		}
		return fmt.Errorf("insert song %q: %w", song.SongSlug, err)
	}

	return nil
}

// UpdateSongForArtist updates an existing song for a specific artist.
func (r *SQLiteSongRepository) UpdateSongForArtist(artistID int64, songSlug string, song Song) error {
	result, err := r.db.Exec(
		`UPDATE songs
		    SET title = ?,
		        artist_name = ?,
		        description = ?,
			    image_url = ?,
			    artwork_path = ?,
		        youtube_url = ?,
		        spotify_url = ?,
		        apple_music_url = ?
		  WHERE artist_id = ? AND song_slug = ?`,
		song.Title,
		song.ArtistName,
		song.Description,
		song.ImageURL,
		song.ArtworkPath,
		song.YouTubeURL,
		song.SpotifyURL,
		song.AppleMusicURL,
		artistID,
		songSlug,
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

// DeleteSongForArtist deletes an existing song for a specific artist.
func (r *SQLiteSongRepository) DeleteSongForArtist(artistID int64, songSlug string) error {
	result, err := r.db.Exec(
		`DELETE FROM songs WHERE artist_id = ? AND song_slug = ?`,
		artistID,
		songSlug,
	)
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

// Insert persists the given songs into the database, ignoring duplicates.
// It upserts the artist by slug before inserting each song.
func (r *SQLiteSongRepository) Insert(songs ...Song) error {
	artistIDs := make(map[string]int64)
	for _, s := range songs {
		if _, err := r.db.Exec(
			`INSERT INTO artists (name, slug) VALUES (?, ?)
			 ON CONFLICT(slug) DO UPDATE SET name = excluded.name`,
			s.ArtistName, s.ArtistSlug,
		); err != nil {
			return fmt.Errorf("upsert artist %q: %w", s.ArtistSlug, err)
		}

		if _, cached := artistIDs[s.ArtistSlug]; !cached {
			var id int64
			if err := r.db.QueryRow(
				`SELECT id FROM artists WHERE slug = ?`, s.ArtistSlug,
			).Scan(&id); err != nil {
				return fmt.Errorf("get artist id for %q: %w", s.ArtistSlug, err)
			}
			artistIDs[s.ArtistSlug] = id
		}

		if _, err := r.db.Exec(
			`INSERT OR IGNORE INTO songs (
				title, artist_name, description, image_url, artwork_path, youtube_url, spotify_url, apple_music_url, song_slug, artist_id
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.Title,
			s.ArtistName,
			s.Description,
			s.ImageURL,
			s.ArtworkPath,
			s.YouTubeURL,
			s.SpotifyURL,
			s.AppleMusicURL,
			s.SongSlug,
			artistIDs[s.ArtistSlug],
		); err != nil {
			return fmt.Errorf("insert song %q: %w", s.SongSlug, err)
		}
	}

	return nil
}
