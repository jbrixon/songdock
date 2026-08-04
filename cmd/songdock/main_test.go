package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jbrixon/songdock/internal/admin"
	"github.com/jbrixon/songdock/internal/artwork"
	"github.com/jbrixon/songdock/internal/platformadmin"
	"github.com/jbrixon/songdock/internal/store"
	_ "modernc.org/sqlite"
)

func TestAdminLoginPageUsesHTMX(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/login: expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`hx-post="/admin/login"`,
		`hx-target="#login-card"`,
		`https://cdn.jsdelivr.net/npm/htmx.org@2.0.4/dist/htmx.min.js`,
		`integrity="sha384-HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/login: body missing %q", want)
		}
	}
}

func TestPlatformAdminLoginPageUsesHTMX(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/platform/admin/login", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /platform/admin/login: expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`hx-post="/platform/admin/login"`,
		`name="username"`,
		`https://cdn.jsdelivr.net/npm/htmx.org@2.0.4/dist/htmx.min.js`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /platform/admin/login: body missing %q", want)
		}
	}
}

func TestAdminLoginFailureShowsError(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{
		"email":    {"admin@example.com"},
		"password": {"wrong-password"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /admin/login: expected status 401, got %d", rec.Code)
	}
	if strings.Contains(rec.Header().Get("Set-Cookie"), admin.SessionCookieName) {
		t.Fatal("POST /admin/login: did not expect session cookie on failed login")
	}
	if !strings.Contains(rec.Body.String(), "Incorrect email or password.") {
		t.Fatal("POST /admin/login: expected login error message")
	}
}

func TestAdminLoginRateLimit(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{"email": {"missing@example.com"}, "password": {"wrong-password"}}
	for attempt := 1; attempt <= 6; attempt++ {
		rec := postForm(router, "/admin/login", form)
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("POST /admin/login attempt %d: expected status %d, got %d", attempt, want, rec.Code)
		}
	}
}

func TestPlatformAdminLoginRateLimitResetsAfterSuccess(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	wrongForm := url.Values{"username": {testPlatformAdminUsername}, "password": {"wrong-password"}}
	for attempt := 1; attempt <= 4; attempt++ {
		rec := postForm(router, "/platform/admin/login", wrongForm)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("failed platform login attempt %d: expected status 401, got %d", attempt, rec.Code)
		}
	}

	validForm := url.Values{"username": {testPlatformAdminUsername}, "password": {testPlatformAdminPassword}}
	if rec := postForm(router, "/platform/admin/login", validForm); rec.Code != http.StatusSeeOther {
		t.Fatalf("successful platform login after failures: expected status 303, got %d", rec.Code)
	}

	for attempt := 1; attempt <= 6; attempt++ {
		rec := postForm(router, "/platform/admin/login", wrongForm)
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("platform login after reset attempt %d: expected status %d, got %d", attempt, want, rec.Code)
		}
	}
}

func TestAdminAndPlatformSessionsCannotBeReusedAcrossCookieNames(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	adminToken, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	platformRequest := httptest.NewRequest(http.MethodGet, "/platform/admin/", nil)
	platformRequest.AddCookie(&http.Cookie{Name: platformadmin.SessionCookieName, Value: adminToken})
	platformResponse := httptest.NewRecorder()
	router.ServeHTTP(platformResponse, platformRequest)
	if platformResponse.Code != http.StatusSeeOther {
		t.Fatalf("admin token under platform cookie: expected redirect, got %d", platformResponse.Code)
	}

	platformToken, err := admin.NewPlatformToken([]byte(testAdminSecret), testPlatformAdminUsername, time.Now().UTC())
	if err != nil {
		t.Fatalf("create platform token: %v", err)
	}
	adminRequest := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	adminRequest.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: platformToken})
	adminResponse := httptest.NewRecorder()
	router.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusSeeOther {
		t.Fatalf("platform token under admin cookie: expected redirect, got %d", adminResponse.Code)
	}

	activeArtistToken, err := admin.NewActiveArtistToken([]byte(testAdminSecret), 1, "bluetooth-pony", time.Now().UTC())
	if err != nil {
		t.Fatalf("create active artist token: %v", err)
	}
	activeAsAdmin := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	activeAsAdmin.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: activeArtistToken})
	activeAsAdminResponse := httptest.NewRecorder()
	router.ServeHTTP(activeAsAdminResponse, activeAsAdmin)
	if activeAsAdminResponse.Code != http.StatusSeeOther {
		t.Fatalf("active artist token under admin cookie: expected redirect, got %d", activeAsAdminResponse.Code)
	}
	activeAsPlatform := httptest.NewRequest(http.MethodGet, "/platform/admin/", nil)
	activeAsPlatform.AddCookie(&http.Cookie{Name: platformadmin.SessionCookieName, Value: activeArtistToken})
	activeAsPlatformResponse := httptest.NewRecorder()
	router.ServeHTTP(activeAsPlatformResponse, activeAsPlatform)
	if activeAsPlatformResponse.Code != http.StatusSeeOther {
		t.Fatalf("active artist token under platform cookie: expected redirect, got %d", activeAsPlatformResponse.Code)
	}
}

func TestAdminPostRejectsOversizedForm(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(strings.Repeat("x", maxAdminRequestBody+1)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized admin form: expected status 413, got %d", rec.Code)
	}
}

func TestHTTPServerUsesProductionTimeouts(t *testing.T) {
	server := newHTTPServer(":8080", http.NotFoundHandler())

	if server.ReadHeaderTimeout != 5*time.Second ||
		server.ReadTimeout != 15*time.Second ||
		server.WriteTimeout != 30*time.Second ||
		server.IdleTimeout != 60*time.Second ||
		server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("unexpected HTTP server hardening settings: %#v", server)
	}
}

func TestSecurityHeadersAreApplied(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/admin/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	for header, want := range map[string]string{
		"Content-Security-Policy":   "default-src 'self'",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	} {
		if got := rec.Header().Get(header); !strings.Contains(got, want) {
			t.Fatalf("GET /admin/login: expected %s to contain %q, got %q", header, want, got)
		}
	}
}

func TestAdminLoginSuccessSetsSecureCookie(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{
		"email":    {"admin@example.com"},
		"password": {"correct horse battery staple"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /admin/login: expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/admin/" {
		t.Fatalf("POST /admin/login: expected HX-Redirect /admin/, got %q", got)
	}

	cookie := rec.Result().Cookies()
	if len(cookie) != 1 {
		t.Fatalf("POST /admin/login: expected 1 cookie, got %d", len(cookie))
	}
	if cookie[0].Name != admin.SessionCookieName {
		t.Fatalf("POST /admin/login: expected cookie %q, got %q", admin.SessionCookieName, cookie[0].Name)
	}
	if !cookie[0].HttpOnly || !cookie[0].Secure {
		t.Fatal("POST /admin/login: expected secure HttpOnly cookie")
	}
	if cookie[0].Path != "/admin" {
		t.Fatalf("POST /admin/login: expected cookie path /admin, got %q", cookie[0].Path)
	}
	if cookie[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("POST /admin/login: expected SameSite strict, got %v", cookie[0].SameSite)
	}
	if cookie[0].MaxAge != int(admin.SessionLifetime.Seconds()) {
		t.Fatalf("POST /admin/login: expected MaxAge %d, got %d", int(admin.SessionLifetime.Seconds()), cookie[0].MaxAge)
	}
	if cookie[0].Expires.IsZero() {
		t.Fatal("POST /admin/login: expected cookie expiry to be set")
	}

	session, err := admin.ParseToken([]byte(testAdminSecret), cookie[0].Value)
	if err != nil {
		t.Fatalf("POST /admin/login: parse session cookie: %v", err)
	}
	if session.Email != "admin@example.com" {
		t.Fatalf("POST /admin/login: expected session email admin@example.com, got %q", session.Email)
	}
}

func TestAdminLoginNormalizesEmailCase(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{
		"email":    {"ADMIN@EXAMPLE.COM"},
		"password": {"correct horse battery staple"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /admin/login with mixed-case email: expected status 204, got %d", rec.Code)
	}
}

func TestAdminHomeRedirectsWithoutSession(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /admin/: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/login" {
		t.Fatalf("GET /admin/: expected redirect to /admin/login, got %q", got)
	}
}

func TestAdminHomeRejectsExpiredSession(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC().Add(-admin.SessionLifetime-time.Minute))
	if err != nil {
		t.Fatalf("create expired token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /admin/ with expired session: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/login" {
		t.Fatalf("GET /admin/ with expired session: expected redirect to /admin/login, got %q", got)
	}
}

func TestAdminHomeRendersActiveArtistSelect(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/: expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`class="admin-logout-form"`,
		`action="/admin/active-artist"`,
		`hx-post="/admin/active-artist"`,
		`hx-trigger="change from:#artist_slug"`,
		`<select class="admin-disabled-control" id="artist_slug" name="artist_slug" required disabled>`,
		`<button class="admin-disabled-control" type="submit" disabled>Update artist</button>`,
		`value="bluetooth-pony"`,
		`value="hey-sis"`,
		`href="/admin/songs/new"`,
		`class="admin-create-song"`,
		`action="/admin/logout"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/: body missing %q", want)
		}
	}
}

func TestAdminHomeListsExistingSongsWithFinalURLAndEditLink(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/: expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`<h2 id="songs-title">Songs</h2>`,
		`Seed Song`,
		`href="/s/bluetooth-pony/seed-song"`,
		`href="/admin/songs/seed-song/edit"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/: body missing %q", want)
		}
	}
}

func TestAdminHomeDisablesCreateSongWithoutActiveArtist(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 999, "noartists@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/: expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `href="/admin/songs/new"`) {
		t.Fatal("GET /admin/: did not expect enabled create-song link without active artist")
	}
	for _, want := range []string{
		`aria-disabled="true"`,
		`Create a new song`,
		`No artists are assigned to your account.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/: body missing %q", want)
		}
	}
}

func TestPlatformAdminCanCreateArtistWithoutInitialAdmin(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	platformCookie := platformAdminSessionCookie(t, router)

	pageReq := httptest.NewRequest(http.MethodGet, "/platform/admin/artists", nil)
	pageReq.AddCookie(platformCookie)
	pageRec := httptest.NewRecorder()

	router.ServeHTTP(pageRec, pageReq)

	if pageRec.Code != http.StatusOK {
		t.Fatalf("GET /platform/admin/artists: expected status 200, got %d", pageRec.Code)
	}
	pageBody := pageRec.Body.String()
	for _, want := range []string{
		`hx-post="/platform/admin/artists"`,
		`name="slug"`,
		`id="slug-status"`,
		`name="slug_mode"`,
		`hx-get="/platform/admin/artists/slug/auto"`,
		`hx-get="/platform/admin/artists/slug/manual"`,
		`Bluetooth Pony`,
	} {
		if !strings.Contains(pageBody, want) {
			t.Fatalf("GET /platform/admin/artists: body missing %q", want)
		}
	}
	if strings.Contains(pageBody, `action="/platform/admin/logout"`) {
		t.Fatal("GET /platform/admin/artists: did not expect logout form")
	}

	form := url.Values{
		"name": {"New Artist"},
		"slug": {"edited-new-artist"},
	}
	req := httptest.NewRequest(http.MethodPost, "/platform/admin/artists", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(platformCookie)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /platform/admin/artists: expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Artist created: New Artist") {
		t.Fatalf("POST /platform/admin/artists: expected success message, got %s", body)
	}
	if !strings.Contains(body, `<td>New Artist</td>`) || !strings.Contains(body, `<td>edited-new-artist</td>`) {
		t.Fatalf("POST /platform/admin/artists: expected created artist in platform list")
	}

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec = httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/: expected status 200, got %d", rec.Code)
	}
	adminBody := rec.Body.String()
	if strings.Contains(adminBody, `value="edited-new-artist"`) || strings.Contains(adminBody, `>New Artist</option>`) {
		t.Fatalf("GET /admin/: did not expect artist creator membership, body: %s", adminBody)
	}
}

func TestPlatformAdminCanDeleteArtistAndAssociatedContent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	artworkDir := filepath.Join(t.TempDir(), "artwork")
	repo, err := store.NewSQLiteSongRepository(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()

	artist, err := repo.CreateArtist("Delete Artist", "delete-artist")
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	if err := repo.InsertSongForArtist(artist.ID, store.Song{
		Title:       "Delete Release",
		ArtistName:  artist.Name,
		SongSlug:    "delete-release",
		ArtworkPath: "delete-artwork.png",
	}); err != nil {
		t.Fatalf("insert artist song: %v", err)
	}
	if err := repo.CreateUserInvitation("delete-admin@example.com", "delete-invitation-hash", artist.ID); err != nil {
		t.Fatalf("create artist invitation: %v", err)
	}
	if err := os.MkdirAll(artworkDir, 0o755); err != nil {
		t.Fatalf("create artwork directory: %v", err)
	}
	artworkPath := filepath.Join(artworkDir, "delete-artwork.png")
	if err := os.WriteFile(artworkPath, []byte("artwork"), 0o644); err != nil {
		t.Fatalf("write artwork: %v", err)
	}

	router := newRouterWithArtworkDir(repo, repo, repo, []byte(testAdminSecret), testPlatformAdminUsername, testPlatformAdminPassword, artworkDir)
	platformCookie := platformAdminSessionCookie(t, router)
	req := httptest.NewRequest(http.MethodPost, "/platform/admin/artists/"+strconv.FormatInt(artist.ID, 10)+"/delete", nil)
	req.AddCookie(platformCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST artist delete: expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Artist deleted.") {
		t.Fatalf("POST artist delete: expected success message, got %s", rec.Body.String())
	}
	if _, err := os.Stat(artworkPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artist artwork: expected deleted file, got %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	for _, query := range []string{
		`SELECT COUNT(*) FROM artists WHERE id = ?`,
		`SELECT COUNT(*) FROM songs WHERE artist_id = ?`,
		`SELECT COUNT(*) FROM user_invitations WHERE artist_id = ?`,
	} {
		var count int
		if err := db.QueryRow(query, artist.ID).Scan(&count); err != nil {
			t.Fatalf("query deleted artist data: %v", err)
		}
		if count != 0 {
			t.Fatalf("%s: expected 0 rows, got %d", query, count)
		}
	}

	publicReq := httptest.NewRequest(http.MethodGet, "/s/delete-artist/delete-release", nil)
	publicRec := httptest.NewRecorder()
	router.ServeHTTP(publicRec, publicReq)
	if publicRec.Code != http.StatusNotFound {
		t.Fatalf("GET deleted artist page: expected status 404, got %d", publicRec.Code)
	}
}

func TestPlatformAdminCannotDeleteArtistWithAssignedAdmin(t *testing.T) {
	router, dbPath, cleanup := testRouterWithDBPath(t)
	defer cleanup()

	platformCookie := platformAdminSessionCookie(t, router)
	req := httptest.NewRequest(http.MethodPost, "/platform/admin/artists/1/delete", nil)
	req.AddCookie(platformCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("POST assigned artist delete: expected status 409, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Remove all assigned artist admins before deleting this artist.") {
		t.Fatalf("POST assigned artist delete: expected guard message, got %s", rec.Body.String())
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artists WHERE id = 1`).Scan(&count); err != nil {
		t.Fatalf("count guarded artist: %v", err)
	}
	if count != 1 {
		t.Fatalf("guarded artist: expected row to remain, got %d", count)
	}
}

func TestPlatformAdminCanCheckArtistSlugAvailability(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	platformCookie := platformAdminSessionCookie(t, router)

	cases := []struct {
		name      string
		path      string
		slug      string
		valid     bool
		available bool
	}{
		{name: "available", path: "/platform/admin/artists/slug/manual?slug=available-artist", slug: "available-artist", valid: true, available: true},
		{name: "taken", path: "/platform/admin/artists/slug/manual?slug=bluetooth-pony", slug: "bluetooth-pony", valid: true, available: false},
		{name: "normalized manual", path: "/platform/admin/artists/slug/manual?slug=Bad+Slug", slug: "bad-slug", valid: true, available: true},
		{name: "auto from name", path: "/platform/admin/artists/slug/auto?name=Fresh+Artist", slug: "fresh-artist", valid: true, available: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.AddCookie(platformCookie)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("GET /platform/admin/artists/slug: expected status 200, got %d", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
				t.Fatalf("GET /platform/admin/artists/slug: expected HTML content type, got %q", got)
			}
			body := rec.Body.String()
			if !strings.Contains(body, `id="artist-slug-field"`) || !strings.Contains(body, `id="slug-status"`) {
				t.Fatalf("GET /platform/admin/artists/slug: expected slug field fragment, got %s", body)
			}
			if !strings.Contains(body, `value="`+tc.slug+`"`) {
				t.Fatalf("GET /platform/admin/artists/slug: expected slug value %q, got %s", tc.slug, body)
			}
			wantState := "error"
			if tc.valid && tc.available {
				wantState = "ok"
			}
			if !strings.Contains(body, "platform-field-status--"+wantState) {
				t.Fatalf("GET /platform/admin/artists/slug: expected state %q, got %s", wantState, body)
			}
		})
	}
}

func TestArtistSlugAvailabilityRequiresPlatformAdmin(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/platform/admin/artists/slug/manual?slug=available-artist", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("GET /platform/admin/artists/slug unauthenticated: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/platform/admin/login" {
		t.Fatalf("GET /platform/admin/artists/slug unauthenticated: expected redirect to platform login, got %q", got)
	}
}

func TestNonPlatformAdminsCannotCreateArtists(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{"name": {"Unauthorized Artist"}}

	t.Run("unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/platform/admin/artists", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /platform/admin/artists unauthenticated: expected status 303, got %d", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/platform/admin/login" {
			t.Fatalf("POST /platform/admin/artists unauthenticated: expected redirect to platform login, got %q", got)
		}
	})

	t.Run("artist admin session", func(t *testing.T) {
		token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
		if err != nil {
			t.Fatalf("create token: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/platform/admin/artists", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /platform/admin/artists as artist admin: expected status 303, got %d", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/platform/admin/login" {
			t.Fatalf("POST /platform/admin/artists as artist admin: expected redirect to platform login, got %q", got)
		}
	})

	t.Run("ordinary authenticated session", func(t *testing.T) {
		token, err := admin.NewToken([]byte(testAdminSecret), 999, "ordinary@example.com", time.Now().UTC())
		if err != nil {
			t.Fatalf("create token: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/platform/admin/artists", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("POST /platform/admin/artists as ordinary user: expected status 303, got %d", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/platform/admin/login" {
			t.Fatalf("POST /platform/admin/artists as ordinary user: expected redirect to platform login, got %q", got)
		}
	})
}

func TestRedeemInvitationMembershipFailureDoesNotCreatePartialUser(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := store.NewSQLiteSongRepository(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	defer repo.Close()

	artist, err := repo.CreateArtist("Rollback Artist", "rollback-artist")
	if err != nil {
		t.Fatalf("create artist: %v", err)
	}
	if err := repo.CreateUserInvitation("partial@example.com", "partial-hash", artist.ID); err != nil {
		t.Fatalf("create invitation: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM artists WHERE id = ?`, artist.ID); err != nil {
		t.Fatalf("delete invitation artist: %v", err)
	}

	if _, err := repo.RedeemInvitation(1, "partial@example.com", "hash"); err == nil {
		t.Fatal("RedeemInvitation with missing artist: expected error")
	}

	if _, err := repo.FindUserByEmail("partial@example.com"); !errors.Is(err, store.ErrUserNotFound) {
		t.Fatalf("FindUserByEmail after failed redemption: expected ErrUserNotFound, got %v", err)
	}
}

func TestAdminCanSetActiveArtist(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	form := url.Values{"artist_slug": {"hey-sis"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/active-artist", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/active-artist: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/" {
		t.Fatalf("POST /admin/active-artist: expected redirect /admin/, got %q", got)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /admin/active-artist: expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != admin.ActiveArtistCookieName {
		t.Fatalf("POST /admin/active-artist: expected cookie %q, got %q", admin.ActiveArtistCookieName, cookies[0].Name)
	}
	activeArtist, err := admin.ParseActiveArtistToken([]byte(testAdminSecret), cookies[0].Value)
	if err != nil {
		t.Fatalf("POST /admin/active-artist: parse active artist cookie: %v", err)
	}
	if activeArtist.UserID != 1 || activeArtist.ArtistSlug != "hey-sis" {
		t.Fatalf("POST /admin/active-artist: expected user 1 hey-sis, got user %d artist %q", activeArtist.UserID, activeArtist.ArtistSlug)
	}
}

func TestAdminCanSetActiveArtistWithHTMXRedirect(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	form := url.Values{"artist_slug": {"hey-sis"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/active-artist", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /admin/active-artist HTMX: expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/admin/" {
		t.Fatalf("POST /admin/active-artist HTMX: expected HX-Redirect /admin/, got %q", got)
	}
}

func TestAdminLogoutClearsCookies(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: "session"})
	req.AddCookie(&http.Cookie{Name: admin.ActiveArtistCookieName, Value: "artist"})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/logout: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/login" {
		t.Fatalf("POST /admin/logout: expected redirect /admin/login, got %q", got)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("POST /admin/logout: expected 2 expired cookies, got %d", len(cookies))
	}
	for _, cookie := range cookies {
		if cookie.MaxAge != -1 {
			t.Fatalf("POST /admin/logout: expected cookie %q MaxAge -1, got %d", cookie.Name, cookie.MaxAge)
		}
	}
}

func TestAdminLogoutReturnsHTMXRedirect(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/admin/logout", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /admin/logout HTMX: expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/admin/login" {
		t.Fatalf("POST /admin/logout HTMX: expected HX-Redirect /admin/login, got %q", got)
	}
}

func TestAdminSongFormUsesActiveArtistWithoutSlugInput(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	activeArtistToken, err := admin.NewActiveArtistToken([]byte(testAdminSecret), 1, "hey-sis", time.Now().UTC())
	if err != nil {
		t.Fatalf("create active artist token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/songs/new", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	req.AddCookie(&http.Cookie{Name: admin.ActiveArtistCookieName, Value: activeArtistToken})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/songs/new: expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `name="artist_slug"`) {
		t.Fatal("GET /admin/songs/new: did not expect artist slug input")
	}
	if !strings.Contains(body, "Artist: Hey Sis") {
		t.Fatal("GET /admin/songs/new: expected active artist context")
	}
	for _, want := range []string{
		`class="admin-form"`,
		`class="admin-actions"`,
		`href="/admin/"`,
		`Cancel`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/songs/new: body missing %q", want)
		}
	}
}

func TestAdminCreateSongUsesActiveArtistCookie(t *testing.T) {
	router, dbPath, cleanup := testRouterWithDBPath(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	activeArtistToken, err := admin.NewActiveArtistToken([]byte(testAdminSecret), 1, "hey-sis", time.Now().UTC())
	if err != nil {
		t.Fatalf("create active artist token: %v", err)
	}

	form := url.Values{
		"title":       {"Active Artist Song"},
		"description": {"Created for the selected artist."},
	}
	req := newSongFormRequest(t, http.MethodPost, "/admin/songs", form)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	req.AddCookie(&http.Cookie{Name: admin.ActiveArtistCookieName, Value: activeArtistToken})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/songs: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/songs/active-artist-song/preview" {
		t.Fatalf("POST /admin/songs: expected redirect to preview, got %q", got)
	}

	previewReq := httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), nil)
	previewReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	previewReq.AddCookie(&http.Cookie{Name: admin.ActiveArtistCookieName, Value: activeArtistToken})
	previewRec := httptest.NewRecorder()
	router.ServeHTTP(previewRec, previewReq)

	if previewRec.Code != http.StatusOK {
		t.Fatalf("GET song preview: expected status 200, got %d", previewRec.Code)
	}
	body := previewRec.Body.String()
	for _, want := range []string{
		`class="admin-preview-banner"`,
		`Go back to dashboard`,
		`href="/admin/"`,
		`<title>Active Artist Song — Hey Sis</title>`,
		`class="band-name"`,
		`class="song-title"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /admin/songs: body missing %q", want)
		}
	}

	secondPreviewReq := httptest.NewRequest(http.MethodGet, rec.Header().Get("Location"), nil)
	secondPreviewReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	secondPreviewReq.AddCookie(&http.Cookie{Name: admin.ActiveArtistCookieName, Value: activeArtistToken})
	secondPreviewRec := httptest.NewRecorder()
	router.ServeHTTP(secondPreviewRec, secondPreviewReq)
	if secondPreviewRec.Code != http.StatusOK {
		t.Fatalf("refresh song preview: expected status 200, got %d", secondPreviewRec.Code)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer db.Close()
	var songCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM songs WHERE song_slug = ?`, "active-artist-song").Scan(&songCount); err != nil {
		t.Fatalf("count created songs: %v", err)
	}
	if songCount != 1 {
		t.Fatalf("refresh song preview: expected exactly 1 song, got %d", songCount)
	}
}

func TestAdminCreateSongRejectsInvalidExternalURL(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	form := url.Values{
		"title":       {"Bad URL Song"},
		"youtube_url": {"javascript:alert(1)"},
	}
	req := newSongFormRequest(t, http.MethodPost, "/admin/songs", form)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/songs invalid URL: expected status 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "YouTube URL must use http or https.") {
		t.Fatalf("POST /admin/songs invalid URL: expected validation message, got %s", rec.Body.String())
	}
}

func TestAdminEditSongUpdatesExistingSong(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/admin/songs/seed-song/edit", nil)
	getReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	getRec := httptest.NewRecorder()

	router.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("GET /admin/songs/seed-song/edit: expected status 200, got %d", getRec.Code)
	}
	getBody := getRec.Body.String()
	for _, want := range []string{
		`<title>Edit Song</title>`,
		`action="/admin/songs/seed-song"`,
		`action="/admin/songs/seed-song/delete"`,
		`hx-confirm="Delete this song? This cannot be undone."`,
		`hx-post="/admin/songs/seed-song/delete"`,
		`value="Seed Song"`,
		`Save Changes`,
		`Delete Song`,
	} {
		if !strings.Contains(getBody, want) {
			t.Fatalf("GET /admin/songs/seed-song/edit: body missing %q", want)
		}
	}
	if strings.Contains(getBody, `action="/admin/logout"`) {
		t.Fatal("GET /admin/songs/seed-song/edit: did not expect logout form")
	}

	form := url.Values{
		"title":           {"Updated Seed Song"},
		"description":     {"Updated description."},
		"youtube_url":     {"https://www.youtube.com/watch?v=updated-seed"},
		"spotify_url":     {"https://open.spotify.com/track/updated-seed"},
		"apple_music_url": {"https://music.apple.com/us/song/updated-seed/123"},
	}
	postReq := newSongFormRequest(t, http.MethodPost, "/admin/songs/seed-song", form)
	postReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	postRec := httptest.NewRecorder()

	router.ServeHTTP(postRec, postReq)

	if postRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/songs/seed-song: expected status 303, got %d", postRec.Code)
	}
	if got := postRec.Header().Get("Location"); got != "/admin/" {
		t.Fatalf("POST /admin/songs/seed-song: expected redirect to /admin/, got %q", got)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/s/bluetooth-pony/seed-song", nil)
	pageRec := httptest.NewRecorder()

	router.ServeHTTP(pageRec, pageReq)

	if pageRec.Code != http.StatusOK {
		t.Fatalf("GET /s/bluetooth-pony/seed-song: expected status 200, got %d", pageRec.Code)
	}
	pageBody := pageRec.Body.String()
	for _, want := range []string{
		`<title>Updated Seed Song — Bluetooth Pony</title>`,
		`<meta name="description" content="Updated description.">`,
	} {
		if !strings.Contains(pageBody, want) {
			t.Fatalf("GET /s/bluetooth-pony/seed-song: body missing %q", want)
		}
	}
}

func TestAdminArtistMetaPixelAppliesToSongPages(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/artist-settings", strings.NewReader(url.Values{
		"meta_pixel_id": {"123456789"},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/artist-settings: expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Meta Pixel settings saved.") {
		t.Fatal("POST /admin/artist-settings: expected success message")
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/s/bluetooth-pony/seed-song", nil)
	pageRec := httptest.NewRecorder()
	router.ServeHTTP(pageRec, pageReq)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("GET song page with pixel: expected status 200, got %d", pageRec.Code)
	}
	pageBody := pageRec.Body.String()
	pixel := "fbq('init', '123456789');"
	if !strings.Contains(pageBody, pixel) {
		t.Fatalf("GET song page with pixel: body missing %q", pixel)
	}
	if strings.Index(pageBody, pixel) > strings.Index(pageBody, "</head>") {
		t.Fatal("Meta Pixel script must be inside the page head")
	}

	clearReq := httptest.NewRequest(http.MethodPost, "/admin/artist-settings", strings.NewReader(url.Values{
		"meta_pixel_id": {""},
	}.Encode()))
	clearReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	clearReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	clearRec := httptest.NewRecorder()
	router.ServeHTTP(clearRec, clearReq)
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear Meta Pixel: expected status 200, got %d", clearRec.Code)
	}

	pageRec = httptest.NewRecorder()
	router.ServeHTTP(pageRec, pageReq)
	if strings.Contains(pageRec.Body.String(), "fbq('init'") {
		t.Fatal("GET song page after clearing pixel: did not expect Meta Pixel code")
	}

	invalidReq := httptest.NewRequest(http.MethodPost, "/admin/artist-settings", strings.NewReader(url.Values{
		"meta_pixel_id": {"123abc"},
	}.Encode()))
	invalidReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invalidReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	invalidRec := httptest.NewRecorder()
	router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid Meta Pixel ID: expected status 400, got %d", invalidRec.Code)
	}
	if !strings.Contains(invalidRec.Body.String(), "Meta Pixel ID must contain only numbers.") {
		t.Fatal("invalid Meta Pixel ID: expected validation message")
	}
}

func TestAdminDeleteSongRemovesExistingSong(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	token, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/songs/seed-song/delete", nil)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: token})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /admin/songs/seed-song/delete: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/admin/" {
		t.Fatalf("POST /admin/songs/seed-song/delete: expected redirect to /admin/, got %q", got)
	}

	pageReq := httptest.NewRequest(http.MethodGet, "/s/bluetooth-pony/seed-song", nil)
	pageRec := httptest.NewRecorder()

	router.ServeHTTP(pageRec, pageReq)

	if pageRec.Code != http.StatusNotFound {
		t.Fatalf("GET /s/bluetooth-pony/seed-song: expected status 404 after delete, got %d", pageRec.Code)
	}
}

func TestPlatformAdminLoginSuccessSetsScopedSecureCookie(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{
		"username": {testPlatformAdminUsername},
		"password": {testPlatformAdminPassword},
	}
	req := httptest.NewRequest(http.MethodPost, "/platform/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /platform/admin/login: expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/platform/admin/" {
		t.Fatalf("POST /platform/admin/login: expected HX-Redirect /platform/admin/, got %q", got)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /platform/admin/login: expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != platformadmin.SessionCookieName {
		t.Fatalf("POST /platform/admin/login: expected cookie %q, got %q", platformadmin.SessionCookieName, cookies[0].Name)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("POST /platform/admin/login: expected secure HttpOnly cookie")
	}
	if cookies[0].Path != "/platform/admin" {
		t.Fatalf("POST /platform/admin/login: expected cookie path /platform/admin, got %q", cookies[0].Path)
	}
	if cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("POST /platform/admin/login: expected SameSite strict, got %v", cookies[0].SameSite)
	}

	session, err := admin.ParsePlatformToken([]byte(testAdminSecret), cookies[0].Value)
	if err != nil {
		t.Fatalf("POST /platform/admin/login: parse session cookie: %v", err)
	}
	if session.Email != testPlatformAdminUsername {
		t.Fatalf("POST /platform/admin/login: expected session identity %q, got %q", testPlatformAdminUsername, session.Email)
	}
}

func TestPlatformAdminCanCreateArtistAdminUser(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	loginForm := url.Values{
		"username": {testPlatformAdminUsername},
		"password": {testPlatformAdminPassword},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/platform/admin/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()

	router.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /platform/admin/login: expected status 303, got %d", loginRec.Code)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /platform/admin/login: expected 1 cookie, got %d", len(cookies))
	}

	form := url.Values{
		"email":     {"new-artist-admin@example.com"},
		"artist_id": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/platform/admin/invitations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(cookies[0])
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /platform/admin/invitations: expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `name="password"`) {
		t.Fatal("POST /platform/admin/invitations: did not expect password input")
	}
	if strings.Contains(body, `name="artist_slug"`) {
		t.Fatal("POST /platform/admin/invitations: did not expect artist slug select")
	}
	if !strings.Contains(body, `name="artist_id"`) {
		t.Fatal("POST /platform/admin/invitations: expected artist select")
	}
	if !strings.Contains(body, "Invitation created for new-artist-admin@example.com. Code: ") {
		t.Fatal("POST /platform/admin/invitations: expected invitation success message")
	}
}

func TestPlatformAdminCanDeleteUserAndOrphanArtists(t *testing.T) {
	router, dbPath, cleanup := testRouterWithDBPath(t)
	defer cleanup()

	userToken, err := admin.NewToken([]byte(testAdminSecret), 1, "admin@example.com", time.Now().UTC())
	if err != nil {
		t.Fatalf("create user token: %v", err)
	}
	platformCookie := platformAdminSessionCookie(t, router)

	req := httptest.NewRequest(http.MethodPost, "/platform/admin/users/1/delete", nil)
	req.AddCookie(platformCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("POST /platform/admin/users/1/delete: expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "User deleted.") {
		t.Fatal("POST /platform/admin/users/1/delete: expected success message")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()

	for _, query := range []string{
		`SELECT COUNT(*) FROM users WHERE id = 1`,
		`SELECT COUNT(*) FROM user_artists WHERE user_id = 1`,
		`SELECT COUNT(*) FROM user_invitations WHERE email = 'admin@example.com'`,
	} {
		var count int
		if err := db.QueryRow(query).Scan(&count); err != nil {
			t.Fatalf("query deleted user data: %v", err)
		}
		if count != 0 {
			t.Fatalf("%s: expected 0 rows, got %d", query, count)
		}
	}

	var artists, songs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM artists`).Scan(&artists); err != nil {
		t.Fatalf("count artists: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM songs`).Scan(&songs); err != nil {
		t.Fatalf("count songs: %v", err)
	}
	if artists != 2 || songs != 1 {
		t.Fatalf("expected orphaned artists and songs to remain, got %d artists and %d songs", artists, songs)
	}

	adminReq := httptest.NewRequest(http.MethodGet, "/admin/", nil)
	adminReq.AddCookie(&http.Cookie{Name: admin.SessionCookieName, Value: userToken})
	adminRec := httptest.NewRecorder()
	router.ServeHTTP(adminRec, adminReq)
	if adminRec.Code != http.StatusSeeOther {
		t.Fatalf("GET /admin/ with deleted user's token: expected redirect, got %d", adminRec.Code)
	}
}

func TestPlatformAdminLogoutClearsCookie(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/platform/admin/logout", nil)
	req.AddCookie(&http.Cookie{Name: platformadmin.SessionCookieName, Value: "session"})
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /platform/admin/logout: expected status 303, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/platform/admin/login" {
		t.Fatalf("POST /platform/admin/logout: expected redirect /platform/admin/login, got %q", got)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /platform/admin/logout: expected 1 expired cookie, got %d", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Fatalf("POST /platform/admin/logout: expected expired cookie, got MaxAge %d", cookies[0].MaxAge)
	}
}

func TestPlatformAdminLogoutReturnsHTMXRedirect(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/platform/admin/logout", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /platform/admin/logout HTMX: expected status 204, got %d", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/platform/admin/login" {
		t.Fatalf("POST /platform/admin/logout HTMX: expected HX-Redirect /platform/admin/login, got %q", got)
	}
}

func TestAdminRegisterPageRendersForm(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/admin/register", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/register: expected status 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	for _, want := range []string{
		`name="invite_code"`,
		`name="password"`,
		`name="password_confirm"`,
		`hx-post="/admin/register"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /admin/register: body missing %q", want)
		}
	}
}

func TestAdminRegisterInvalidCodeReturnsError(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{
		"invite_code":      {"INVALID-CODE"},
		"password":         {"newpassword123"},
		"password_confirm": {"newpassword123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/register with invalid code: expected status 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid invite code.") {
		t.Fatal("POST /admin/register: expected invalid invite code error")
	}
}

func TestAdminRegisterPasswordMismatchReturnsError(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	form := url.Values{
		"invite_code":      {"SOME-CODE"},
		"password":         {"password1"},
		"password_confirm": {"password2"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/register with mismatched passwords: expected status 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Passwords do not match.") {
		t.Fatal("POST /admin/register: expected password mismatch error")
	}
}

func TestAdminRegisterValidCodeCreatesUserAndSetsSession(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	// First create an invitation via the platform admin.
	inviteCode, err := seedTestInvitation(t, router)
	if err != nil {
		t.Fatalf("seed test invitation: %v", err)
	}

	form := url.Values{
		"invite_code":      {inviteCode},
		"password":         {"newpassword123"},
		"password_confirm": {"newpassword123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /admin/register: expected status 204, got %d; body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/admin/" {
		t.Fatalf("POST /admin/register: expected HX-Redirect /admin/, got %q", got)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /admin/register: expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != admin.SessionCookieName {
		t.Fatalf("POST /admin/register: expected cookie %q, got %q", admin.SessionCookieName, cookies[0].Name)
	}
	if !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal("POST /admin/register: expected secure HttpOnly cookie")
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/", nil)
	req.AddCookie(cookies[0])
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/ after registration: expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="bluetooth-pony"`) || !strings.Contains(body, `>Bluetooth Pony</option>`) {
		t.Fatalf("GET /admin/ after registration: expected invited artist membership, body: %s", body)
	}
}

func TestAdminRegisterCodeCannotBeReusedAfterAccepted(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	inviteCode, err := seedTestInvitation(t, router)
	if err != nil {
		t.Fatalf("seed test invitation: %v", err)
	}

	// First registration should succeed.
	form := url.Values{
		"invite_code":      {inviteCode},
		"password":         {"newpassword123"},
		"password_confirm": {"newpassword123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Second attempt with the same code should fail.
	req2 := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("HX-Request", "true")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("second POST /admin/register: expected status 400, got %d", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "already been used") {
		t.Fatalf("second POST /admin/register: expected already-used error, got: %s", rec2.Body.String())
	}
}

func TestPlatformAdminInvitationCreationNormalizesEmail(t *testing.T) {
	router, cleanup := testRouter(t)
	defer cleanup()

	platformCookie := platformAdminSessionCookie(t, router)
	form := url.Values{
		"email":     {"MixedCase@example.com"},
		"artist_id": {"1"},
	}
	req := httptest.NewRequest(http.MethodPost, "/platform/admin/invitations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(platformCookie)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first POST /platform/admin/invitations: expected status 200, got %d", rec.Code)
	}

	form.Set("email", "mixedcase@EXAMPLE.com")
	req = httptest.NewRequest(http.MethodPost, "/platform/admin/invitations", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(platformCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate mixed-case invitation: expected status 409, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "An invitation for that email already exists.") {
		t.Fatalf("duplicate mixed-case invitation: unexpected body %s", rec.Body.String())
	}
}

func TestPlatformAdminCanRevokeInvitation(t *testing.T) {
	router, dbPath, cleanup := testRouterWithDBPath(t)
	defer cleanup()

	inviteCode, err := seedTestInvitation(t, router)
	if err != nil {
		t.Fatalf("seed test invitation: %v", err)
	}
	platformCookie := platformAdminSessionCookie(t, router)

	usersReq := httptest.NewRequest(http.MethodGet, "/platform/admin/invitations", nil)
	usersReq.AddCookie(platformCookie)
	usersRec := httptest.NewRecorder()
	router.ServeHTTP(usersRec, usersReq)

	body := usersRec.Body.String()
	if strings.Contains(body, `action="/platform/admin/logout"`) {
		t.Fatal("GET /platform/admin/invitations: did not expect logout form")
	}
	prefix := `action="/platform/admin/invitations/`
	idx := strings.Index(body, prefix)
	if idx == -1 {
		t.Fatalf("GET /platform/admin/invitations: expected revoke action, got %s", body)
	}
	idStart := idx + len(prefix)
	idEnd := strings.Index(body[idStart:], `/revoke"`)
	if idEnd == -1 {
		t.Fatalf("GET /platform/admin/invitations: could not parse invitation id from %s", body[idStart:])
	}
	invitationID := body[idStart : idStart+idEnd]

	revokeReq := httptest.NewRequest(http.MethodPost, "/platform/admin/invitations/"+invitationID+"/revoke", nil)
	revokeReq.Header.Set("HX-Request", "true")
	revokeReq.AddCookie(platformCookie)
	revokeRec := httptest.NewRecorder()
	router.ServeHTTP(revokeRec, revokeReq)

	if revokeRec.Code != http.StatusOK {
		t.Fatalf("POST /platform/admin/invitations/%s/revoke: expected status 200, got %d", invitationID, revokeRec.Code)
	}
	if !strings.Contains(revokeRec.Body.String(), "Invitation revoked.") {
		t.Fatalf("revoke invitation: expected success message, got %s", revokeRec.Body.String())
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var revokedAt string
	if err := db.QueryRow(`SELECT COALESCE(revoked_at, '') FROM user_invitations WHERE email = ?`, "newartist@example.com").Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked invitation: %v", err)
	}
	if revokedAt == "" {
		t.Fatal("expected revoked_at to be populated")
	}

	form := url.Values{
		"invite_code":      {inviteCode},
		"password":         {"newpassword123"},
		"password_confirm": {"newpassword123"},
	}
	registerReq := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	registerReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	registerReq.Header.Set("HX-Request", "true")
	registerRec := httptest.NewRecorder()
	router.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/register revoked invite: expected status 400, got %d", registerRec.Code)
	}
	if !strings.Contains(registerRec.Body.String(), "This invite code has been revoked.") {
		t.Fatalf("POST /admin/register revoked invite: unexpected body %s", registerRec.Body.String())
	}
}

func TestAdminRegisterExpiredInviteReturnsError(t *testing.T) {
	router, dbPath, cleanup := testRouterWithDBPath(t)
	defer cleanup()

	inviteCode, err := seedTestInvitation(t, router)
	if err != nil {
		t.Fatalf("seed test invitation: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE user_invitations SET expires_at = ? WHERE email = ?`, "2000-01-01 00:00:00", "newartist@example.com"); err != nil {
		t.Fatalf("expire invitation: %v", err)
	}

	form := url.Values{
		"invite_code":      {inviteCode},
		"password":         {"newpassword123"},
		"password_confirm": {"newpassword123"},
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/register expired invite: expected status 400, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "This invite code has expired.") {
		t.Fatalf("POST /admin/register expired invite: unexpected body %s", rec.Body.String())
	}
}

// seedTestInvitation creates a platform admin invitation and returns the
// plain-text invite code.
func seedTestInvitation(t *testing.T, router http.Handler) (string, error) {
	t.Helper()

	// Log in as platform admin.
	loginForm := url.Values{
		"username": {testPlatformAdminUsername},
		"password": {testPlatformAdminPassword},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/platform/admin/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)

	cookies := loginRec.Result().Cookies()
	if len(cookies) == 0 {
		return "", fmt.Errorf("platform admin login returned no cookies")
	}

	// Create an invitation.
	inviteForm := url.Values{
		"email":     {"newartist@example.com"},
		"artist_id": {"1"},
	}
	inviteReq := httptest.NewRequest(http.MethodPost, "/platform/admin/invitations", strings.NewReader(inviteForm.Encode()))
	inviteReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	inviteReq.AddCookie(cookies[0])
	inviteRec := httptest.NewRecorder()
	router.ServeHTTP(inviteRec, inviteReq)

	body := inviteRec.Body.String()
	prefix := "Invitation created for newartist@example.com. Code: "
	idx := strings.Index(body, prefix)
	if idx == -1 {
		return "", fmt.Errorf("could not find invitation code in response body: %s", body)
	}
	// Code runs to end of line (or end of string).
	codeStart := idx + len(prefix)
	codeEnd := strings.IndexAny(body[codeStart:], " \n\r<")
	if codeEnd == -1 {
		return body[codeStart:], nil
	}
	return body[codeStart : codeStart+codeEnd], nil
}

func platformAdminSessionCookie(t *testing.T, router http.Handler) *http.Cookie {
	t.Helper()

	loginForm := url.Values{
		"username": {testPlatformAdminUsername},
		"password": {testPlatformAdminPassword},
	}
	loginReq := httptest.NewRequest(http.MethodPost, "/platform/admin/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	router.ServeHTTP(loginRec, loginReq)

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("POST /platform/admin/login: expected status 303, got %d", loginRec.Code)
	}
	cookies := loginRec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("POST /platform/admin/login: expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != platformadmin.SessionCookieName {
		t.Fatalf("POST /platform/admin/login: expected cookie %q, got %q", platformadmin.SessionCookieName, cookies[0].Name)
	}
	return cookies[0]
}

const testAdminSecret = "test-admin-secret"
const testPlatformAdminUsername = "platform-root"
const testPlatformAdminPassword = "platform-password"

func postForm(router http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func testRouter(t *testing.T) (http.Handler, func()) {
	t.Helper()

	router, _, cleanup := testRouterWithDBPath(t)
	return router, cleanup
}

func testRouterWithDBPath(t *testing.T) (http.Handler, string, func()) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	repo, err := store.NewSQLiteSongRepository(dbPath)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}

	if err := seedTestUser(dbPath); err != nil {
		repo.Close()
		t.Fatalf("seed test user: %v", err)
	}

	return newRouter(repo, repo, repo, []byte(testAdminSecret), testPlatformAdminUsername, testPlatformAdminPassword), dbPath, func() {
		if err := repo.Close(); err != nil {
			t.Fatalf("close repo: %v", err)
		}
	}
}

func seedTestUser(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	if _, err := db.Exec(
		`INSERT INTO artists (name, slug) VALUES (?, ?), (?, ?)`,
		"Bluetooth Pony", "bluetooth-pony",
		"Hey Sis", "hey-sis",
	); err != nil {
		return err
	}
	if _, err := db.Exec(
		`INSERT INTO songs (
			title, artist_name, description, image_url, youtube_url, spotify_url, apple_music_url, song_slug, artist_id
		)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, id FROM artists WHERE slug = ?`,
		"Seed Song",
		"Bluetooth Pony",
		"Seed description.",
		"https://cdn.example.com/art/seed-song.jpg",
		"https://www.youtube.com/watch?v=seed-song",
		"https://open.spotify.com/track/seed-song",
		"https://music.apple.com/us/song/seed-song/123",
		"seed-song",
		"bluetooth-pony",
	); err != nil {
		return err
	}

	passwordHash, err := admin.HashUserPassword("correct horse battery staple")
	if err != nil {
		return err
	}
	result, err := db.Exec(
		`INSERT INTO users (email, password_hash) VALUES (?, ?)`,
		"admin@example.com",
		passwordHash,
	)
	if err != nil {
		return err
	}

	userID, err := result.LastInsertId()
	if err != nil {
		return err
	}

	_, err = db.Exec(
		`INSERT INTO user_artists (user_id, artist_id)
		 SELECT ?, id FROM artists WHERE slug IN (?, ?)`,
		userID,
		"bluetooth-pony",
		"hey-sis",
	)
	return err
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func testTableColumns(db *sql.DB, table string) (map[string]struct{}, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]struct{})
	for rows.Next() {
		var (
			cid          int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			pk           int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = struct{}{}
	}
	return cols, rows.Err()
}

func TestServeHTTPServerShutsDownGracefully(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	server := newHTTPServer(listener.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTPServer(ctx, server, func() error {
			return server.Serve(listener)
		})
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp, reqErr := http.Get("http://" + listener.Addr().String())
		if reqErr == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start before deadline: %v", reqErr)
		}
		time.Sleep(25 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serveHTTPServer: expected nil error on shutdown, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveHTTPServer: shutdown timed out")
	}
}

func TestRunRejectsMissingOrUnknownCommand(t *testing.T) {
	for _, args := range [][]string{nil, {"migrate"}, {"unknown"}} {
		if err := run(args); err == nil {
			t.Fatalf("run(%v): expected error", args)
		}
	}
}

func TestMigrateUpNeedsOnlyDatabasePath(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "songs.db")
	t.Setenv("DB_PATH", dbPath)
	t.Setenv("POSTGRES_URL", "")
	t.Setenv("ADMIN_BACKEND_SECRET", "")
	t.Setenv("PLATFORM_ADMIN_USERNAME", "")
	t.Setenv("PLATFORM_ADMIN_PASSWORD", "")

	if err := run([]string{"migrate", "up"}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	defer db.Close()

	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'songs'`).Scan(&tables); err != nil {
		t.Fatalf("check migrated schema: %v", err)
	}
	if tables != 1 {
		t.Fatalf("expected songs table after migration, got %d matches", tables)
	}
}

func TestDatabaseSelectionPrefersPostgresURL(t *testing.T) {
	if got := (store.DatabaseConfig{SQLitePath: "/tmp/songs.db", PostgresURL: "postgres://user:password@localhost/songdock"}).Backend(); got != "postgres" {
		t.Fatalf("database backend = %q, want postgres", got)
	}
	if got := (store.DatabaseConfig{SQLitePath: "/tmp/songs.db"}).Backend(); got != "sqlite" {
		t.Fatalf("database backend = %q, want sqlite", got)
	}
}

func TestInvalidPostgresURLDoesNotFallbackToSQLite(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "must-not-be-created.db")
	_, err := store.OpenConfiguredRepository(store.DatabaseConfig{
		SQLitePath:  dbPath,
		PostgresURL: "not-a-postgres-url",
	}, false)
	if err == nil {
		t.Fatal("expected invalid PostgreSQL URL error")
	}
	if _, statErr := os.Stat(dbPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected SQLite fallback database not to be created, stat error: %v", statErr)
	}
}

func TestDatabasePathDefaultRemainsSongsDB(t *testing.T) {
	t.Setenv("DB_PATH", "")
	if got := databasePath(); got != "songs.db" {
		t.Fatalf("databasePath() = %q, want songs.db", got)
	}
}

func TestNewArtworkStoreDefaultsEmptyDriverToFilesystem(t *testing.T) {
	if _, err := newArtworkStore(context.Background(), artwork.Config{Dir: t.TempDir()}); err != nil {
		t.Fatalf("newArtworkStore with empty driver: %v", err)
	}
}

func TestAutomaticMigrations(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "default", want: true},
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SONGDOCK_AUTO_MIGRATE", tt.value)
			got, err := automaticMigrations()
			if err != nil {
				t.Fatalf("automaticMigrations: %v", err)
			}
			if got != tt.want {
				t.Fatalf("automaticMigrations: got %t, want %t", got, tt.want)
			}
		})
	}

	t.Run("invalid", func(t *testing.T) {
		t.Setenv("SONGDOCK_AUTO_MIGRATE", "sometimes")
		if _, err := automaticMigrations(); err == nil {
			t.Fatal("automaticMigrations: expected invalid value error")
		}
	})
}

func TestSQLiteRepositoryCanOpenWithoutMigration(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "songs.db")
	repo, err := store.OpenSQLiteSongRepository(dbPath)
	if err != nil {
		t.Fatalf("open repository without migration: %v", err)
	}
	defer repo.Close()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer db.Close()

	var tables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'`).Scan(&tables); err != nil {
		t.Fatalf("check schema: %v", err)
	}
	if tables != 0 {
		t.Fatalf("expected no tables without migration, got %d", tables)
	}
}

func TestConfiguredSQLiteRepositoryRequiresSchemaWhenAutoMigrationDisabled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "songs.db")
	if _, err := store.OpenConfiguredRepository(store.DatabaseConfig{SQLitePath: dbPath}, false); err == nil {
		t.Fatal("expected missing schema error")
	} else if !strings.Contains(err.Error(), "run songdock migrate up") {
		t.Fatalf("missing schema error = %v, want migration guidance", err)
	}
}

func TestMigrateRemovesLegacyUsersArtistIDAndBackfillsMemberships(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}

	statements := []string{
		`CREATE TABLE artists (
			id   INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			slug TEXT NOT NULL UNIQUE
		)`,
		`CREATE TABLE users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			email         TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			artist_id     INTEGER REFERENCES artists(id)
		)`,
		`CREATE TABLE songs (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			title           TEXT NOT NULL,
			artist_name     TEXT NOT NULL,
			description     TEXT NOT NULL DEFAULT '',
			image_url       TEXT NOT NULL DEFAULT '',
			youtube_url     TEXT NOT NULL DEFAULT '',
			spotify_url     TEXT NOT NULL DEFAULT '',
			apple_music_url TEXT NOT NULL DEFAULT '',
			song_slug       TEXT NOT NULL,
			artist_id       INTEGER NOT NULL REFERENCES artists(id),
			UNIQUE (artist_id, song_slug)
		)`,
		`CREATE TABLE user_artists (
			user_id   INTEGER NOT NULL REFERENCES users(id),
			artist_id INTEGER NOT NULL REFERENCES artists(id),
			PRIMARY KEY (user_id, artist_id)
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed legacy schema: %v", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO artists (name, slug) VALUES (?, ?)`, "Legacy Artist", "legacy-artist"); err != nil {
		t.Fatalf("insert legacy artist: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, password_hash, artist_id) VALUES (?, ?, ?)`, "legacy@example.com", "hash", 1); err != nil {
		t.Fatalf("insert legacy user: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	repo, err := store.NewSQLiteSongRepository(dbPath)
	if err != nil {
		t.Fatalf("migrate legacy db: %v", err)
	}
	defer repo.Close()

	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen migrated db: %v", err)
	}
	defer db.Close()

	columns, err := testTableColumns(db, "users")
	if err != nil {
		t.Fatalf("users columns: %v", err)
	}
	if _, exists := columns["artist_id"]; exists {
		t.Fatal("users.artist_id should be removed after migration")
	}

	var memberships int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_artists WHERE user_id = 1 AND artist_id = 1`).Scan(&memberships); err != nil {
		t.Fatalf("query user_artists: %v", err)
	}
	if memberships != 1 {
		t.Fatalf("expected 1 backfilled membership, got %d", memberships)
	}
}

func newSongFormRequest(t *testing.T, method, target string, form url.Values) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, values := range form {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatalf("write form field %q: %v", key, err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart form: %v", err)
	}
	req := httptest.NewRequest(method, target, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
