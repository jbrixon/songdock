//go:build acceptance

// Package acceptance contains end-to-end tests that run against a live instance
// of the songdock server.
//
// By default the tests target http://localhost:8080. Set ACCEPTANCE_BASE_URL to
// point them at a different host, e.g.:
//
//	ACCEPTANCE_BASE_URL=https://example.com go test -tags acceptance ./test/acceptance/...
//
// Set DB_PATH to point at the same SQLite file the server is using so that
// TestMain can seed the required songs before the tests run.
package acceptance_test

import (
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbrixon/songdock/internal/admin"
	"github.com/jbrixon/songdock/internal/platformadmin"
	"github.com/jbrixon/songdock/internal/store"
	_ "modernc.org/sqlite"
)

// TestMain seeds the database before running acceptance tests.
func TestMain(m *testing.M) {
	dbPath := "songs.db"
	if p := os.Getenv("DB_PATH"); p != "" {
		dbPath = p
	}

	repo, err := store.NewSQLiteSongRepository(dbPath)
	if err != nil {
		panic("acceptance setup: open db: " + err.Error())
	}

	err = repo.Insert(
		store.Song{
			Title:         "Not Cool",
			ArtistName:    "Bluetooth Pony",
			Description:   "A pulse-driven indie track from Bluetooth Pony.",
			ImageURL:      "https://cdn.example.com/art/bluetooth-pony-not-cool.jpg",
			YouTubeURL:    "https://www.youtube.com/watch?v=not-cool",
			SpotifyURL:    "https://open.spotify.com/track/not-cool",
			AppleMusicURL: "https://music.apple.com/us/song/not-cool/1",
			SongSlug:      "not-cool",
			ArtistSlug:    "bluetooth-pony",
		},
		store.Song{
			Title:         "Now or Never",
			ArtistName:    "Bluetooth Pony",
			Description:   "An anthemic release about risk and momentum.",
			ImageURL:      "https://cdn.example.com/art/bluetooth-pony-now-or-never.jpg",
			YouTubeURL:    "https://www.youtube.com/watch?v=now-or-never",
			SpotifyURL:    "https://open.spotify.com/track/now-or-never",
			AppleMusicURL: "https://music.apple.com/us/song/now-or-never/2",
			SongSlug:      "now-or-never",
			ArtistSlug:    "bluetooth-pony",
		},
		store.Song{
			Title:         "Frankfurt",
			ArtistName:    "Bluetooth Pony",
			Description:   "A late-night synth ride inspired by city lights.",
			ImageURL:      "https://cdn.example.com/art/bluetooth-pony-frankfurt.jpg",
			YouTubeURL:    "https://www.youtube.com/watch?v=frankfurt",
			SpotifyURL:    "https://open.spotify.com/track/frankfurt",
			AppleMusicURL: "https://music.apple.com/us/song/frankfurt/3",
			SongSlug:      "frankfurt",
			ArtistSlug:    "bluetooth-pony",
		},
		store.Song{
			Title:         "Landhausplatz",
			ArtistName:    "Hey Sis",
			Description:   "A warm, melodic single from Hey Sis.",
			ImageURL:      "https://cdn.example.com/art/hey-sis-landhausplatz.jpg",
			YouTubeURL:    "https://www.youtube.com/watch?v=landhausplatz",
			SpotifyURL:    "https://open.spotify.com/track/landhausplatz",
			AppleMusicURL: "https://music.apple.com/us/song/landhausplatz/4",
			SongSlug:      "landhausplatz",
			ArtistSlug:    "hey-sis",
		},
	)
	if err != nil {
		panic("acceptance setup: seed songs: " + err.Error())
	}

	secret := strings.TrimSpace(os.Getenv("ADMIN_BACKEND_SECRET"))
	if secret == "" {
		panic("acceptance setup: ADMIN_BACKEND_SECRET must be set")
	}
	if strings.TrimSpace(os.Getenv("PLATFORM_ADMIN_USERNAME")) == "" {
		panic("acceptance setup: PLATFORM_ADMIN_USERNAME must be set")
	}
	if os.Getenv("PLATFORM_ADMIN_PASSWORD") == "" {
		panic("acceptance setup: PLATFORM_ADMIN_PASSWORD must be set")
	}

	if err := seedAdminUser(dbPath, secret); err != nil {
		panic("acceptance setup: seed admin user: " + err.Error())
	}
	if err := repo.Close(); err != nil {
		panic("acceptance setup: close db: " + err.Error())
	}

	os.Exit(m.Run())
}

// baseURL returns the root URL of the server under test.
func baseURL() string {
	if u := os.Getenv("ACCEPTANCE_BASE_URL"); u != "" {
		return strings.TrimRight(u, "/")
	}
	return "http://localhost:8080"
}

// get is a helper that performs a GET request and fails the test on error.
func get(t *testing.T, path string) *http.Response {
	t.Helper()
	resp, err := http.Get(baseURL() + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func postFormNoRedirect(t *testing.T, path string, form url.Values, cookie string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, baseURL()+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func getNoRedirect(t *testing.T, path string, cookie string) *http.Response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, baseURL()+path, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

// readBody reads and closes the response body, failing the test on error.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestRootReturns404(t *testing.T) {
	resp := get(t, "/")
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /: expected status 404, got %d", resp.StatusCode)
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	resp := get(t, "/nonexistent")
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /nonexistent: expected status 404, got %d", resp.StatusCode)
	}
}

func TestSongPageStatus(t *testing.T) {
	paths := []string{
		"/s/bluetooth-pony/not-cool",
		"/s/bluetooth-pony/now-or-never",
		"/s/bluetooth-pony/frankfurt",
		"/s/hey-sis/landhausplatz",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			resp := get(t, path)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("GET %s: expected status 200, got %d", path, resp.StatusCode)
			}
		})
	}
}

func TestSongPageContentType(t *testing.T) {
	resp := get(t, "/s/bluetooth-pony/not-cool")
	resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("GET /s/bluetooth-pony/not-cool: expected Content-Type to contain text/html, got %q", ct)
	}
}

func TestSongPageContent(t *testing.T) {
	cases := []struct {
		path       string
		songTitle  string
		artistName string
		desc       string
		imageURL   string
		spotifyURL string
		appleURL   string
		youtubeURL string
	}{
		{
			path:       "/s/bluetooth-pony/not-cool",
			songTitle:  "Not Cool",
			artistName: "Bluetooth Pony",
			desc:       "A pulse-driven indie track from Bluetooth Pony.",
			imageURL:   "/static/song_artwork_placeholder.png",
			spotifyURL: "https://open.spotify.com/track/not-cool",
			appleURL:   "https://music.apple.com/us/song/not-cool/1",
			youtubeURL: "https://www.youtube.com/watch?v=not-cool",
		},
		{
			path:       "/s/bluetooth-pony/now-or-never",
			songTitle:  "Now or Never",
			artistName: "Bluetooth Pony",
			desc:       "An anthemic release about risk and momentum.",
			imageURL:   "/static/song_artwork_placeholder.png",
			spotifyURL: "https://open.spotify.com/track/now-or-never",
			appleURL:   "https://music.apple.com/us/song/now-or-never/2",
			youtubeURL: "https://www.youtube.com/watch?v=now-or-never",
		},
		{
			path:       "/s/bluetooth-pony/frankfurt",
			songTitle:  "Frankfurt",
			artistName: "Bluetooth Pony",
			desc:       "A late-night synth ride inspired by city lights.",
			imageURL:   "/static/song_artwork_placeholder.png",
			spotifyURL: "https://open.spotify.com/track/frankfurt",
			appleURL:   "https://music.apple.com/us/song/frankfurt/3",
			youtubeURL: "https://www.youtube.com/watch?v=frankfurt",
		},
		{
			path:       "/s/hey-sis/landhausplatz",
			songTitle:  "Landhausplatz",
			artistName: "Hey Sis",
			desc:       "A warm, melodic single from Hey Sis.",
			imageURL:   "/static/song_artwork_placeholder.png",
			spotifyURL: "https://open.spotify.com/track/landhausplatz",
			appleURL:   "https://music.apple.com/us/song/landhausplatz/4",
			youtubeURL: "https://www.youtube.com/watch?v=landhausplatz",
		},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			resp := get(t, tc.path)
			body := readBody(t, resp)

			checks := []struct {
				name string
				want string
			}{
				{"page title", "<title>" + tc.songTitle + " — " + tc.artistName + "</title>"},
				{"band name element", `class="band-name"`},
				{"song title element", `class="song-title"`},
				{"artist name in body", tc.artistName},
				{"song title in body", tc.songTitle},
				{"streaming links list", `aria-label="Listen on streaming platforms"`},
				{"artwork image", `src="` + tc.imageURL + `"`},
				{"Spotify link", `href="` + tc.spotifyURL + `"`},
				{"Apple Music link", `href="` + tc.appleURL + `"`},
				{"YouTube link", `href="` + tc.youtubeURL + `"`},
				{"Spotify aria-label", `aria-label="Listen on Spotify"`},
				{"Apple Music aria-label", `aria-label="Listen on Apple Music"`},
				{"YouTube aria-label", `aria-label="Watch on YouTube"`},
				{"meta description", `<meta name="description" content="` + tc.desc + `">`},
				{"og image", `<meta property="og:image" content="` + tc.imageURL + `">`},
			}

			for _, check := range checks {
				t.Run(check.name, func(t *testing.T) {
					if !strings.Contains(body, check.want) {
						t.Errorf("page does not contain %q", check.want)
					}
				})
			}
			if strings.Contains(body, `class="song-description"`) {
				t.Error("page should not render song description in the body")
			}
		})
	}
}

func TestUnknownSongReturns404(t *testing.T) {
	resp := get(t, "/s/bluetooth-pony/nonexistent-song")
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /s/bluetooth-pony/nonexistent-song: expected status 404, got %d", resp.StatusCode)
	}
}

func TestAdminCanCreateSongWithUniqueSlug(t *testing.T) {
	const collidingTitle = "Collision Probe Song"
	const collidingBaseSlug = "collision-probe-song"
	if err := seedSongCollision(testDBPath(), collidingTitle, collidingBaseSlug); err != nil {
		t.Fatalf("seed song collision: %v", err)
	}

	loginResp := postFormNoRedirect(t, "/admin/login", url.Values{
		"email":    {"acceptance-admin@example.com"},
		"password": {"acceptance-password"},
	}, "")
	defer loginResp.Body.Close()

	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /admin/login: expected status 303, got %d", loginResp.StatusCode)
	}
	cookies := loginResp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("POST /admin/login: expected session cookie")
	}
	sessionCookie := cookies[0].Name + "=" + cookies[0].Value

	createResp := postFormNoRedirect(t, "/admin/songs", url.Values{
		"title":       {collidingTitle},
		"description": {"A newly added acceptance test song."},
	}, sessionCookie)

	body := readBody(t, createResp)
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /admin/songs: expected status 200, got %d", createResp.StatusCode)
	}
	for _, want := range []string{
		`class="admin-preview-banner"`,
		`Go back to dashboard`,
		`href="/admin/"`,
		`<title>` + collidingTitle + ` — Bluetooth Pony</title>`,
		`<meta name="description" content="A newly added acceptance test song.">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /admin/songs: preview body missing %q", want)
		}
	}
}

func TestAdminCannotCreateSongForUnassignedArtist(t *testing.T) {
	loginResp := postFormNoRedirect(t, "/admin/login", url.Values{
		"email":    {"acceptance-admin@example.com"},
		"password": {"acceptance-password"},
	}, "")
	defer loginResp.Body.Close()

	cookies := loginResp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("POST /admin/login: expected session cookie")
	}
	sessionCookie := cookies[0].Name + "=" + cookies[0].Value

	createResp := postFormNoRedirect(t, "/admin/active-artist", url.Values{
		"artist_slug": {"hey-sis"},
	}, sessionCookie)
	body := readBody(t, createResp)

	if createResp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /admin/active-artist: expected status 403, got %d", createResp.StatusCode)
	}
	if !strings.Contains(body, "Choose an artist assigned to your account.") {
		t.Fatalf("POST /admin/active-artist: expected assignment error in response body")
	}
}

func TestPlatformAdminCanCreateArtistAdminUser(t *testing.T) {
	platformLoginResp := postFormNoRedirect(t, "/platform/admin/login", url.Values{
		"username": {strings.TrimSpace(os.Getenv("PLATFORM_ADMIN_USERNAME"))},
		"password": {os.Getenv("PLATFORM_ADMIN_PASSWORD")},
	}, "")
	defer platformLoginResp.Body.Close()

	if platformLoginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /platform/admin/login: expected status 303, got %d", platformLoginResp.StatusCode)
	}
	platformCookies := platformLoginResp.Cookies()
	if len(platformCookies) == 0 {
		t.Fatal("POST /platform/admin/login: expected platform session cookie")
	}
	if platformCookies[0].Name != "platform_admin_session" {
		t.Fatalf("POST /platform/admin/login: expected platform session cookie, got %q", platformCookies[0].Name)
	}
	platformSessionCookie := platformCookies[0].Name + "=" + platformCookies[0].Value

	usersPageResp := getNoRedirect(t, "/platform/admin/users", platformSessionCookie)
	usersPageBody := readBody(t, usersPageResp)
	if usersPageResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /platform/admin/users: expected status 200, got %d", usersPageResp.StatusCode)
	}
	if !strings.Contains(usersPageBody, `href="/platform/admin/invitations"`) {
		t.Fatal("GET /platform/admin/users: expected invitation page link")
	}
	if strings.Contains(usersPageBody, `id="invitations-panel"`) {
		t.Fatal("GET /platform/admin/users: did not expect invitation management panel")
	}

	usersResp := getNoRedirect(t, "/platform/admin/invitations", platformSessionCookie)
	usersBody := readBody(t, usersResp)
	if usersResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /platform/admin/invitations: expected status 200, got %d", usersResp.StatusCode)
	}
	if !strings.Contains(usersBody, `hx-post="/platform/admin/invitations"`) {
		t.Fatal("GET /platform/admin/invitations: expected HTMX invitation creation form")
	}
	if strings.Contains(usersBody, `name="password"`) {
		t.Fatal("GET /platform/admin/invitations: did not expect password input")
	}
	if !strings.Contains(usersBody, `name="artist_id"`) {
		t.Fatal("GET /platform/admin/invitations: expected artist selection")
	}

	db, err := sql.Open("sqlite", testDBPath())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	var artistID int64
	if err := db.QueryRow(`SELECT id FROM artists WHERE slug = ?`, "bluetooth-pony").Scan(&artistID); err != nil {
		t.Fatalf("query artist id: %v", err)
	}
	expectedArtistID := artistID

	unique := time.Now().UTC().UnixNano()
	email := "acceptance-created-" + strconv.FormatInt(unique, 10) + "@example.com"
	createUserResp := postFormNoRedirect(t, "/platform/admin/invitations", url.Values{
		"email":     {email},
		"artist_id": {strconv.FormatInt(artistID, 10)},
	}, platformSessionCookie)
	createUserBody := readBody(t, createUserResp)

	if createUserResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /platform/admin/invitations: expected status 200, got %d", createUserResp.StatusCode)
	}
	if !strings.Contains(createUserBody, "Invitation created for "+email+". Code: ") {
		t.Fatalf("POST /platform/admin/invitations: expected invitation code in response, got %q", createUserBody)
	}
	codePrefix := "Invitation created for " + email + ". Code: "
	codeIndex := strings.Index(createUserBody, codePrefix)
	if codeIndex == -1 {
		t.Fatalf("POST /platform/admin/invitations: expected invitation code prefix in response, got %q", createUserBody)
	}
	invitationCode := createUserBody[codeIndex+len(codePrefix):]
	if end := strings.Index(invitationCode, "<"); end >= 0 {
		invitationCode = invitationCode[:end]
	}
	invitationCode = strings.TrimSpace(invitationCode)
	if invitationCode == "" {
		t.Fatal("POST /platform/admin/invitations: expected non-empty invitation code")
	}

	var invitationHash string
	if err := db.QueryRow(
		`SELECT invitation_code_hash, artist_id FROM user_invitations WHERE email = ?`,
		email,
	).Scan(&invitationHash, &artistID); err != nil {
		t.Fatalf("query invitation row: %v", err)
	}
	if artistID != expectedArtistID {
		t.Fatalf("expected invitation artist_id %d, got %d", expectedArtistID, artistID)
	}
	if invitationHash == "" {
		t.Fatal("expected invitation_code_hash to be stored")
	}
	secret := strings.TrimSpace(os.Getenv("ADMIN_BACKEND_SECRET"))
	if invitationHash != platformadmin.HashInvitationCode([]byte(secret), invitationCode) {
		t.Fatal("expected stored invitation_code_hash to match the returned invitation code")
	}
}

func seedAdminUser(dbPath, secret string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	passwordHash, err := admin.HashUserPassword("acceptance-password")
	if err != nil {
		return err
	}

	if _, err := db.Exec(
		`INSERT OR IGNORE INTO users (email, password_hash) VALUES (?, ?)`,
		"acceptance-admin@example.com",
		passwordHash,
	); err != nil {
		return err
	}

	var userID int64
	if err := db.QueryRow(`SELECT id FROM users WHERE email = ?`, "acceptance-admin@example.com").Scan(&userID); err != nil {
		return err
	}

	var artistID int64
	if err := db.QueryRow(`SELECT id FROM artists WHERE slug = ?`, "bluetooth-pony").Scan(&artistID); err != nil {
		return err
	}

	_, err = db.Exec(
		`INSERT OR IGNORE INTO user_artists (user_id, artist_id) VALUES (?, ?)`,
		userID,
		artistID,
	)
	return err
}

func seedSongCollision(dbPath, title, slug string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	var artistID int64
	if err := db.QueryRow(`SELECT id FROM artists WHERE slug = ?`, "bluetooth-pony").Scan(&artistID); err != nil {
		return err
	}

	_, err = db.Exec(
		`INSERT OR IGNORE INTO songs (
			title, artist_name, description, image_url, youtube_url, spotify_url, apple_music_url, song_slug, artist_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		title,
		"Bluetooth Pony",
		"Collision seed song.",
		"https://cdn.example.com/art/collision-seed.jpg",
		"https://www.youtube.com/watch?v=collision-seed",
		"https://open.spotify.com/track/collision-seed",
		"https://music.apple.com/us/song/collision-seed/999",
		slug,
		artistID,
	)
	return err
}

func testDBPath() string {
	if p := os.Getenv("DB_PATH"); p != "" {
		return p
	}
	return "songs.db"
}
