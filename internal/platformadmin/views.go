package platformadmin

import (
	"strconv"

	"github.com/jbrixon/songdock/internal/store"
)

type homeView struct {
	Email string
}

type loginView struct {
	Username string
	Error    string
}

type usersView struct {
	Users []store.UserWithArtists
}

type invitationsView struct {
	Artists            []store.Artist
	PendingInvitations []store.UserInvitation
	Message            string
	Email              string
	ArtistID           int64
}

type artistsView struct {
	Artists    []store.Artist
	Message    string
	Name       string
	Slug       string
	SlugMode   string
	SlugStatus slugStatusView
}

type slugStatusView struct {
	Message string
	State   string
}

func artistSlugStatusClass(state string) string {
	if state == "" {
		return ""
	}
	return " platform-field-status--" + state
}

func int64String(v int64) string {
	return strconv.FormatInt(v, 10)
}
