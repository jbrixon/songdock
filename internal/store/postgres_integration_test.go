//go:build integration

package store

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresConnectionErrorRedactsCredentials(t *testing.T) {
	connectionURL := "postgres://user:secret-password@db.example/songdock?sslmode=require"
	err := postgresConnectionError("connect", connectionURL, fmt.Errorf("failed to connect to %s", connectionURL))
	if got := err.Error(); got == "" || strings.Contains(got, "secret-password") || strings.Contains(got, connectionURL) {
		t.Fatalf("connection error leaked credentials or URL: %q", got)
	}
}

func TestPostgresRepositoryIntegration(t *testing.T) {
	connectionURL := os.Getenv("POSTGRES_URL")
	if connectionURL == "" {
		t.Skip("POSTGRES_URL is not set")
	}

	if err := MigratePostgresDatabase(connectionURL); err != nil {
		t.Fatalf("migrate postgres database: %v", err)
	}
	repo, err := OpenPostgresSongRepository(connectionURL)
	if err != nil {
		t.Fatalf("open postgres repository: %v", err)
	}
	defer repo.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	artist, err := repo.CreateArtist("Postgres Artist", "postgres-"+suffix)
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	if _, err := repo.CreateArtist("Duplicate", artist.Slug); !errors.Is(err, ErrArtistAlreadyExists) {
		t.Fatalf("duplicate artist error = %v, want ErrArtistAlreadyExists", err)
	}

	song := Song{
		Title:         "Postgres Song",
		ArtistName:    artist.Name,
		Description:   "description",
		ArtworkPath:   "artwork/path",
		YouTubeURL:    "https://youtube.example/song",
		SpotifyURL:    "https://spotify.example/song",
		AppleMusicURL: "https://music.example/song",
		SongSlug:      "song-" + suffix,
		ArtistSlug:    artist.Slug,
	}
	if err := repo.InsertSongForArtist(artist.ID, song); err != nil {
		t.Fatalf("insert song: %v", err)
	}
	found, err := repo.FindBySlug(artist.Slug, song.SongSlug)
	if err != nil {
		t.Fatalf("find song: %v", err)
	}
	if found.Description != song.Description || found.ArtworkPath != song.ArtworkPath {
		t.Fatalf("found song = %+v, want description/artwork preserved", found)
	}
	if err := repo.InsertSongForArtist(artist.ID, song); !errors.Is(err, ErrSongAlreadyExists) {
		t.Fatalf("duplicate song error = %v, want ErrSongAlreadyExists", err)
	}

	email := "postgres-" + suffix + "@example.com"
	userID, err := repo.CreateUser(email, "password-hash")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := repo.AssignUserToArtist(userID, artist.Slug); err != nil {
		t.Fatalf("assign user: %v", err)
	}
	assigned, err := repo.IsUserAssignedToArtist(userID, artist.ID)
	if err != nil || !assigned {
		t.Fatalf("membership = %t, error = %v; want assigned", assigned, err)
	}

	invitationEmail := "invite-" + suffix + "@example.com"
	if err := repo.CreateUserInvitation(invitationEmail, "hash-"+suffix, artist.ID); err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	invitations, err := repo.ListPendingInvitations()
	if err != nil || len(invitations) == 0 {
		t.Fatalf("list invitations: %d, %v", len(invitations), err)
	}
	invitation, err := repo.FindInvitationByCodeHash("hash-" + suffix)
	if err != nil {
		t.Fatalf("find invitation: %v", err)
	}
	if _, err := repo.RedeemInvitation(invitation.ID, invitationEmail, "invite-password-hash"); err != nil {
		t.Fatalf("redeem invitation: %v", err)
	}
	if _, err := repo.RedeemInvitation(invitation.ID, invitationEmail, "invite-password-hash"); !errors.Is(err, ErrInvitationAlreadyAccepted) {
		t.Fatalf("second redemption error = %v, want ErrInvitationAlreadyAccepted", err)
	}

	if err := repo.DeleteUser(userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	revoked, err := repo.IsUserTokenRevoked(userID)
	if err != nil || !revoked {
		t.Fatalf("token revocation = %t, error = %v; want revoked", revoked, err)
	}
}
