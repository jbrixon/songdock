package store

import (
	"strings"
	"time"
)

func isUniqueConstraintError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "constraint failed") && strings.Contains(msg, "unique")
}

func sqliteTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

func invitationStatus(inv UserInvitation, now time.Time) error {
	if inv.AcceptedAt != "" {
		return ErrInvitationAlreadyAccepted
	}
	if inv.RevokedAt != "" {
		return ErrInvitationRevoked
	}
	if inv.ExpiresAt != "" && inv.ExpiresAt <= sqliteTimestamp(now) {
		return ErrInvitationExpired
	}
	return nil
}
