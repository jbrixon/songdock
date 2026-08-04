// Package admin implements the HTTP handlers and supporting logic for the
// admin section of the site.
package admin

import "github.com/jbrixon/songdock/internal/store"

// Repository is the port that admin handlers require from the data layer.
// Any adapter (e.g. SQLite) that satisfies this interface can be plugged in.
type Repository interface {
	FindUserByEmail(email string) (*store.User, error)
	IsUserTokenRevoked(userID int64) (bool, error)
	FindArtistBySlug(slug string) (*store.Artist, error)
	FindBySlug(artistSlug, songSlug string) (*store.Song, error)
	ListArtistsForUser(userID int64) ([]store.Artist, error)
	IsUserAssignedToArtist(userID, artistID int64) (bool, error)
	UpdateArtistMetaPixelID(artistID int64, pixelID string) error
	SongSlugExists(artistID int64, songSlug string) (bool, error)
	ListSongsForArtist(artistID int64) ([]store.Song, error)
	InsertSongForArtist(artistID int64, song store.Song) error
	UpdateSongForArtist(artistID int64, songSlug string, song store.Song) error
	DeleteSongForArtist(artistID int64, songSlug string) error
	FindInvitationByCodeHash(codeHash string) (*store.UserInvitation, error)
	RedeemInvitation(invitationID int64, email, passwordHash string) (int64, error)
}
