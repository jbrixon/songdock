package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestSQLiteReissueUserInvitation(t *testing.T) {
	tests := []struct {
		name   string
		revoke bool
		expire bool
	}{
		{name: "revoked", revoke: true},
		{name: "expired", expire: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, err := NewSQLiteSongRepository(filepath.Join(t.TempDir(), "songs.db"))
			if err != nil {
				t.Fatalf("open repo: %v", err)
			}
			defer repo.Close()

			oldArtist, err := repo.CreateArtist("Old Artist", "old-artist")
			if err != nil {
				t.Fatalf("create old artist: %v", err)
			}
			newArtist, err := repo.CreateArtist("New Artist", "new-artist")
			if err != nil {
				t.Fatalf("create new artist: %v", err)
			}
			if err := repo.CreateUserInvitation("reissue@example.com", "old-hash", oldArtist.ID); err != nil {
				t.Fatalf("create invitation: %v", err)
			}
			invitation, err := repo.FindInvitationByCodeHash("old-hash")
			if err != nil {
				t.Fatalf("find invitation: %v", err)
			}

			if test.revoke {
				if err := repo.RevokeInvitation(invitation.ID); err != nil {
					t.Fatalf("revoke invitation: %v", err)
				}
			}
			if test.expire {
				if _, err := repo.db.Exec(`UPDATE user_invitations SET expires_at = ? WHERE id = ?`, "2000-01-01 00:00:00", invitation.ID); err != nil {
					t.Fatalf("expire invitation: %v", err)
				}
			}

			if err := repo.ReissueUserInvitation(invitation.ID, "new-hash", newArtist.ID); err != nil {
				t.Fatalf("reissue invitation: %v", err)
			}
			if _, err := repo.FindInvitationByCodeHash("old-hash"); !errors.Is(err, ErrInvitationNotFound) {
				t.Fatalf("old invitation lookup error = %v, want ErrInvitationNotFound", err)
			}
			if _, err := repo.RedeemInvitation(invitation.ID, "old-hash", "reissue@example.com", "password-hash"); !errors.Is(err, ErrInvitationNotFound) {
				t.Fatalf("old invitation redemption error = %v, want ErrInvitationNotFound", err)
			}

			reissued, err := repo.FindInvitationByCodeHash("new-hash")
			if err != nil {
				t.Fatalf("find reissued invitation: %v", err)
			}
			if reissued.ArtistID != newArtist.ID {
				t.Fatalf("reissued artist = %d, want %d", reissued.ArtistID, newArtist.ID)
			}
			if reissued.Status != "" {
				t.Fatalf("reissued lookup should not expose list status, got %q", reissued.Status)
			}
			var invitations int
			if err := repo.db.QueryRow(`SELECT COUNT(*) FROM user_invitations WHERE email = ?`, "reissue@example.com").Scan(&invitations); err != nil {
				t.Fatalf("count invitations: %v", err)
			}
			if invitations != 1 {
				t.Fatalf("invitation rows = %d, want 1", invitations)
			}
		})
	}
}

func TestSQLiteReissueUserInvitationRejectsActiveAndAccepted(t *testing.T) {
	repo, err := NewSQLiteSongRepository(filepath.Join(t.TempDir(), "songs.db"))
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()

	artist, err := repo.CreateArtist("Artist", "artist")
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	if err := repo.CreateUserInvitation("active@example.com", "active-hash", artist.ID); err != nil {
		t.Fatalf("create active invitation: %v", err)
	}
	active, err := repo.FindInvitationByCodeHash("active-hash")
	if err != nil {
		t.Fatalf("find active invitation: %v", err)
	}
	if err := repo.ReissueUserInvitation(active.ID, "new-active-hash", artist.ID); !errors.Is(err, ErrInvitationStillActive) {
		t.Fatalf("active reissue error = %v, want ErrInvitationStillActive", err)
	}

	if err := repo.CreateUserInvitation("accepted@example.com", "accepted-hash", artist.ID); err != nil {
		t.Fatalf("create accepted invitation: %v", err)
	}
	accepted, err := repo.FindInvitationByCodeHash("accepted-hash")
	if err != nil {
		t.Fatalf("find accepted invitation: %v", err)
	}
	if _, err := repo.RedeemInvitation(accepted.ID, "accepted-hash", "accepted@example.com", "password-hash"); err != nil {
		t.Fatalf("redeem invitation: %v", err)
	}
	if err := repo.ReissueUserInvitation(accepted.ID, "new-accepted-hash", artist.ID); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Fatalf("accepted reissue error = %v, want ErrInvitationAlreadyAccepted", err)
	}
}
