// Package store defines the port (interface) for song data access.
package store

import (
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned when a song cannot be found.
var ErrNotFound = errors.New("song not found")

// ErrUserNotFound is returned when a user cannot be found.
var ErrUserNotFound = errors.New("user not found")

// ErrArtistNotFound is returned when an artist cannot be found.
var ErrArtistNotFound = errors.New("artist not found")

// ErrArtistAlreadyExists is returned when creating a duplicate artist.
var ErrArtistAlreadyExists = errors.New("artist already exists")

// ErrUserAlreadyExists is returned when creating a duplicate user.
var ErrUserAlreadyExists = errors.New("user already exists")

// ErrUserInvitationAlreadyExists is returned when creating a duplicate invitation.
var ErrUserInvitationAlreadyExists = errors.New("user invitation already exists")

// ErrInvitationNotFound is returned when an invitation cannot be found.
var ErrInvitationNotFound = errors.New("invitation not found")

// ErrInvitationAlreadyAccepted is returned when an invitation has already been redeemed.
var ErrInvitationAlreadyAccepted = errors.New("invitation already accepted")

// ErrInvitationExpired is returned when an invitation has expired.
var ErrInvitationExpired = errors.New("invitation expired")

// ErrInvitationRevoked is returned when an invitation has been revoked.
var ErrInvitationRevoked = errors.New("invitation revoked")

// ErrSongAlreadyExists is returned when creating a duplicate song slug for an artist.
var ErrSongAlreadyExists = errors.New("song already exists")

// InvitationLifetime is how long a newly issued invitation remains valid.
const InvitationLifetime = 14 * 24 * time.Hour

// Song holds the data for a single song.
type Song struct {
	Title         string
	ArtistName    string
	Description   string
	ImageURL      string
	ArtworkPath   string
	YouTubeURL    string
	SpotifyURL    string
	AppleMusicURL string
	SongSlug      string
	ArtistSlug    string
	MetaPixelID   string
}

// User holds the subset of user data needed for authentication.
type User struct {
	ID           int64
	Email        string
	PasswordHash string
}

// UserArtist represents a membership record linking a user to an artist.
type UserArtist struct {
	UserID   int64
	ArtistID int64
}

// UserWithArtists is a user and their assigned artists.
type UserWithArtists struct {
	ID      int64
	Email   string
	Artists []Artist
}

// UserInvitation is a pending invitation that has not been redeemed yet.
type UserInvitation struct {
	ID                 int64
	Email              string
	ArtistID           int64
	ArtistName         string
	InvitationCodeHash string
	CreatedAt          string
	AcceptedAt         string
	ExpiresAt          string
	RevokedAt          string
	Status             string
}

// Artist holds the subset of artist data needed for admin actions.
type Artist struct {
	ID          int64
	Name        string
	Slug        string
	MetaPixelID string
}

// SongRepository is the port that any song datastore adapter must implement.
type SongRepository interface {
	FindBySlug(artistSlug, songSlug string) (*Song, error)
}

// NormalizeEmail applies the repository's canonical email representation.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
