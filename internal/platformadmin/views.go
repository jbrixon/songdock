package platformadmin

import (
	"fmt"
	"strconv"
	"time"

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
	Users   []store.UserWithArtists
	Message string
}

type invitationsView struct {
	Artists                []store.Artist
	PendingInvitations     []store.UserInvitation
	PendingInvitationCount int
	Message                string
	Email                  string
	ArtistID               int64
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

func platformNavCurrent(active, page string) string {
	if active == page {
		return "page"
	}
	return "false"
}

func invitationStatusLabel(status string) string {
	switch status {
	case "active":
		return "Sent"
	case "expired":
		return "Expired"
	case "revoked":
		return "Revoked"
	default:
		return status
	}
}

func invitationExpiryLabel(value string) string {
	expiresAt, err := time.Parse("2006-01-02 15:04:05", value)
	if err != nil {
		return value
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	days := int(expiresAt.UTC().Truncate(24*time.Hour).Sub(today).Hours() / 24)
	switch days {
	case 0:
		return "Today"
	case 1:
		return "In 1 day"
	case 2, 3, 4, 5, 6:
		return fmt.Sprintf("In %d days", days)
	case -1:
		return "Yesterday"
	default:
		return expiresAt.UTC().Format("Jan 2")
	}
}
