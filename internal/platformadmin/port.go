// Package platformadmin implements platform-level administration handlers.
package platformadmin

import "github.com/jbrixon/songdock/internal/store"

// Repository is the port that platform admin handlers require from the data layer.
type Repository interface {
	ListUsers() ([]store.UserWithArtists, error)
	DeleteUser(userID int64) error
	ListArtists() ([]store.Artist, error)
	ListPendingInvitations() ([]store.UserInvitation, error)
	CreateUserInvitation(email, invitationCodeHash string, artistID int64) error
	ReissueUserInvitation(invitationID int64, invitationCodeHash string, artistID int64) error
	RevokeInvitation(invitationID int64) error
	FindArtistBySlug(slug string) (*store.Artist, error)
	CreateArtist(name, slug string) (*store.Artist, error)
	DeleteArtist(artistID int64) ([]string, error)
}
