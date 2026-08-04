package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
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

// IsUserTokenRevoked reports whether tokens for a user were revoked.
// Revocations remain after the user row is hard-deleted.
func (r *SQLiteSongRepository) IsUserTokenRevoked(userID int64) (bool, error) {
	var revokedAt int64
	err := r.db.QueryRow(
		`SELECT revoked_at FROM user_token_revocations WHERE user_id = ?`,
		userID,
	).Scan(&revokedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("query user token revocation: %w", err)
	}
	return revokedAt > 0, nil
}

// DeleteUser permanently removes a user, their artist memberships, and any
// invitations associated with their email. Artists and their songs remain.
func (r *SQLiteSongRepository) DeleteUser(userID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete user transaction: %w", err)
	}
	defer tx.Rollback()

	var email string
	if err := tx.QueryRow(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
		if err == sql.ErrNoRows {
			return ErrUserNotFound
		}
		return fmt.Errorf("query user for deletion: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO user_token_revocations (user_id, revoked_at) VALUES (?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET revoked_at = excluded.revoked_at`,
		userID,
		time.Now().UTC().Unix(),
	); err != nil {
		return fmt.Errorf("revoke user tokens: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM user_artists WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user artist memberships: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM user_invitations WHERE email = ?`, email); err != nil {
		return fmt.Errorf("delete user invitations: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete user transaction: %w", err)
	}
	return nil
}
