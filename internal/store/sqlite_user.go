package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// FindUserByEmail returns the user matching the given email address, or
// ErrUserNotFound if no match exists.
func (r *SQLiteSongRepository) FindUserByEmail(email string) (*User, error) {
	email = NormalizeEmail(email)
	row := r.db.QueryRow(
		`SELECT id, email, password_hash
		   FROM users
		  WHERE email = ?`,
		email,
	)

	var user User
	if err := row.Scan(&user.ID, &user.Email, &user.PasswordHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("query user: %w", err)
	}

	return &user, nil
}

// ListUsers returns all users with their artist assignments.
func (r *SQLiteSongRepository) ListUsers() ([]UserWithArtists, error) {
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
				ID:   artistID.Int64,
				Name: artistName.String,
				Slug: artistSlug.String,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}

	return users, nil
}

// CreateUser inserts a new user.
func (r *SQLiteSongRepository) CreateUser(email, passwordHash string) (int64, error) {
	email = NormalizeEmail(email)
	existing, err := r.FindUserByEmail(email)
	if err == nil {
		return existing.ID, ErrUserAlreadyExists
	}
	if !errors.Is(err, ErrUserNotFound) {
		return 0, err
	}

	result, err := r.db.Exec(
		`INSERT INTO users (email, password_hash) VALUES (?, ?)`,
		email,
		passwordHash,
	)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted user id: %w", err)
	}
	return id, nil
}
