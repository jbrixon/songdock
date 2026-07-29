package admin

import "github.com/jbrixon/songdock/internal/store"

// loginView is the template data for the login page and login card.
type loginView struct {
	Email string
	Error string
}

// homeView is the template data for the admin dashboard.
type homeView struct {
	Email        string
	Artists      []store.Artist
	ActiveArtist *store.Artist
	Songs        []songListItem
	Error        string
}

// songListItem is the template data for a song listed on the admin dashboard.
type songListItem struct {
	Title    string
	SongSlug string
	FinalURL string
	EditURL  string
}

// songFormView is the template data for the create/edit song form page.
type songFormView struct {
	Mode          string
	ActiveArtist  *store.Artist
	Title         string
	Description   string
	ImageURL      string
	YouTubeURL    string
	SpotifyURL    string
	AppleMusicURL string
	SongSlug      string
	Action        string
	DeleteAction  string
	SubmitLabel   string
	PageTitle     string
	Error         string
}

// songPreviewView is the template data for the post-create landing page preview.
type songPreviewView struct {
	ArtistName    string
	Title         string
	Description   string
	ImageURL      string
	YouTubeURL    string
	SpotifyURL    string
	AppleMusicURL string
}

// registerView is the template data for the registration page and card.
type registerView struct {
	InviteCode string
	Error      string
}
