package store

import (
	"database/sql"
	"fmt"
	"time"
)

// CreateUserInvitation inserts a pending invitation for the given email and
// artist administrator assignment.
func (r *SQLiteSongRepository) CreateUserInvitation(email, invitationCodeHash string, artistID int64) error {
	email = NormalizeEmail(email)
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin create user invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var existingID int64
	err = tx.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&existingID)
	if err == nil {
		return ErrUserAlreadyExists
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("query existing user: %w", err)
	}

	err = tx.QueryRow(`SELECT id FROM user_invitations WHERE email = ?`, email).Scan(&existingID)
	if err == nil {
		return ErrUserInvitationAlreadyExists
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("query existing invitation: %w", err)
	}

	err = tx.QueryRow(`SELECT id FROM artists WHERE id = ?`, artistID).Scan(&existingID)
	if err == sql.ErrNoRows {
		return ErrArtistNotFound
	}
	if err != nil {
		return fmt.Errorf("query invitation artist: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO user_invitations (email, artist_id, invitation_code_hash, expires_at) VALUES (?, ?, ?, ?)`,
		email,
		artistID,
		invitationCodeHash,
		sqliteTimestamp(time.Now().UTC().Add(InvitationLifetime)),
	); err != nil {
		return fmt.Errorf("insert user invitation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit create user invitation transaction: %w", err)
	}
	return nil
}

// ReissueUserInvitation replaces an expired or revoked invitation in place.
func (r *SQLiteSongRepository) ReissueUserInvitation(invitationID int64, invitationCodeHash string, artistID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin reissue user invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var artistExists int64
	if err := tx.QueryRow(`SELECT id FROM artists WHERE id = ?`, artistID).Scan(&artistExists); err != nil {
		if err == sql.ErrNoRows {
			return ErrArtistNotFound
		}
		return fmt.Errorf("query reissue invitation artist: %w", err)
	}

	var inv UserInvitation
	err = tx.QueryRow(
		`SELECT COALESCE(accepted_at, ''), COALESCE(expires_at, ''), COALESCE(revoked_at, '')
		   FROM user_invitations WHERE id = ?`,
		invitationID,
	).Scan(&inv.AcceptedAt, &inv.ExpiresAt, &inv.RevokedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrInvitationNotFound
		}
		return fmt.Errorf("query invitation for reissue: %w", err)
	}
	if err := invitationReissueStatus(inv, time.Now().UTC()); err != nil {
		return err
	}

	now := time.Now().UTC()
	result, err := tx.Exec(
		`UPDATE user_invitations
		    SET artist_id = ?, invitation_code_hash = ?, created_at = ?, expires_at = ?, revoked_at = NULL
		  WHERE id = ? AND accepted_at IS NULL`,
		artistID,
		invitationCodeHash,
		sqliteTimestamp(now),
		sqliteTimestamp(now.Add(InvitationLifetime)),
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

// FindInvitationByCodeHash returns the invitation whose stored hash matches
// codeHash, or ErrInvitationNotFound if no such record exists.
func (r *SQLiteSongRepository) FindInvitationByCodeHash(codeHash string) (*UserInvitation, error) {
	row := r.db.QueryRow(
		`SELECT ui.id, ui.email, COALESCE(ui.artist_id, 0), COALESCE(a.name, ''), ui.invitation_code_hash, ui.created_at,
		        COALESCE(ui.accepted_at, ''), COALESCE(ui.expires_at, ''), COALESCE(ui.revoked_at, '')
		   FROM user_invitations ui
		   LEFT JOIN artists a ON a.id = ui.artist_id
		  WHERE ui.invitation_code_hash = ?`,
		codeHash,
	)

	var inv UserInvitation
	if err := row.Scan(
		&inv.ID,
		&inv.Email,
		&inv.ArtistID,
		&inv.ArtistName,
		&inv.InvitationCodeHash,
		&inv.CreatedAt,
		&inv.AcceptedAt,
		&inv.ExpiresAt,
		&inv.RevokedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrInvitationNotFound
		}
		return nil, fmt.Errorf("query invitation: %w", err)
	}
	if err := invitationStatus(inv, time.Now().UTC()); err != nil {
		return nil, err
	}
	return &inv, nil
}

// RedeemInvitation atomically marks the invitation as accepted and creates the
// user account. It returns the new user's ID on success.
func (r *SQLiteSongRepository) RedeemInvitation(invitationID int64, invitationCodeHash, email, passwordHash string) (int64, error) {
	email = NormalizeEmail(email)
	tx, err := r.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin redeem invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var acceptedAt sql.NullString
	var expiresAt sql.NullString
	var revokedAt sql.NullString
	var artistID sql.NullInt64
	err = tx.QueryRow(
		`SELECT accepted_at, expires_at, revoked_at, artist_id
		   FROM user_invitations
		  WHERE id = ? AND invitation_code_hash = ?`, invitationID, invitationCodeHash,
	).Scan(&acceptedAt, &expiresAt, &revokedAt, &artistID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, ErrInvitationNotFound
		}
		return 0, fmt.Errorf("query invitation for redemption: %w", err)
	}
	inv := UserInvitation{
		AcceptedAt: acceptedAt.String,
		ExpiresAt:  expiresAt.String,
		RevokedAt:  revokedAt.String,
	}
	if err := invitationStatus(inv, time.Now().UTC()); err != nil {
		return 0, err
	}
	if !artistID.Valid || artistID.Int64 == 0 {
		return 0, ErrArtistNotFound
	}

	result, err := tx.Exec(
		`INSERT INTO users (email, password_hash) VALUES (?, ?)`,
		email,
		passwordHash,
	)
	if err != nil {
		return 0, fmt.Errorf("insert user during redemption: %w", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get inserted user id during redemption: %w", err)
	}

	if _, err := tx.Exec(
		`INSERT INTO user_artists (user_id, artist_id) VALUES (?, ?)`,
		userID,
		artistID.Int64,
	); err != nil {
		return 0, fmt.Errorf("assign redeemed user to artist: %w", err)
	}

	result, err = tx.Exec(
		`UPDATE user_invitations
		    SET accepted_at = CURRENT_TIMESTAMP
		  WHERE id = ? AND invitation_code_hash = ? AND accepted_at IS NULL`,
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

// ListPendingInvitations returns unaccepted invitations with their current state.
func (r *SQLiteSongRepository) ListPendingInvitations() ([]UserInvitation, error) {
	rows, err := r.db.Query(
		`SELECT ui.id,
		        ui.email,
		        ui.artist_id,
		        COALESCE(a.name, ''),
		        ui.invitation_code_hash,
		        ui.created_at,
		        COALESCE(ui.accepted_at, ''),
		        COALESCE(ui.expires_at, ''),
		        COALESCE(ui.revoked_at, ''),
		        CASE
		          WHEN ui.revoked_at IS NOT NULL AND ui.revoked_at != '' THEN 'revoked'
		          WHEN ui.expires_at IS NOT NULL AND ui.expires_at != '' AND ui.expires_at <= CURRENT_TIMESTAMP THEN 'expired'
		          ELSE 'active'
		        END AS status
		   FROM user_invitations ui
		   LEFT JOIN artists a ON a.id = ui.artist_id
		  WHERE ui.accepted_at IS NULL
		  ORDER BY ui.created_at DESC, ui.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query invitations: %w", err)
	}
	defer rows.Close()

	var invitations []UserInvitation
	for rows.Next() {
		var inv UserInvitation
		if err := rows.Scan(
			&inv.ID,
			&inv.Email,
			&inv.ArtistID,
			&inv.ArtistName,
			&inv.InvitationCodeHash,
			&inv.CreatedAt,
			&inv.AcceptedAt,
			&inv.ExpiresAt,
			&inv.RevokedAt,
			&inv.Status,
		); err != nil {
			return nil, fmt.Errorf("scan invitation: %w", err)
		}
		invitations = append(invitations, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invitations: %w", err)
	}

	return invitations, nil
}

// RevokeInvitation marks an unaccepted invitation as revoked.
func (r *SQLiteSongRepository) RevokeInvitation(invitationID int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin revoke invitation transaction: %w", err)
	}
	defer tx.Rollback()

	var inv UserInvitation
	err = tx.QueryRow(
		`SELECT COALESCE(accepted_at, ''), COALESCE(expires_at, ''), COALESCE(revoked_at, '')
		   FROM user_invitations
		  WHERE id = ?`,
		invitationID,
	).Scan(&inv.AcceptedAt, &inv.ExpiresAt, &inv.RevokedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrInvitationNotFound
		}
		return fmt.Errorf("query invitation for revoke: %w", err)
	}
	if inv.AcceptedAt != "" {
		return ErrInvitationAlreadyAccepted
	}
	if inv.RevokedAt != "" {
		return ErrInvitationRevoked
	}

	if _, err := tx.Exec(
		`UPDATE user_invitations SET revoked_at = CURRENT_TIMESTAMP WHERE id = ?`,
		invitationID,
	); err != nil {
		return fmt.Errorf("revoke invitation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit revoke invitation transaction: %w", err)
	}
	return nil
}
