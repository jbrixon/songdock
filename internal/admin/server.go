package admin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jbrixon/songdock/internal/artwork"
	"github.com/jbrixon/songdock/internal/store"
	songtemplates "github.com/jbrixon/songdock/internal/templates"
	"github.com/jbrixon/songdock/internal/urlpolicy"
)

// Server handles HTTP requests for the admin section of the site.
type Server struct {
	repo                Repository
	secret              []byte
	loginLimiter        *RateLimiter
	registrationLimiter *RateLimiter
	artwork             *artwork.Store
}

// New returns a new Server that uses repo for data access and secret for
// signing session tokens.
func New(repo Repository, secret []byte) *Server {
	return NewWithArtworkDir(repo, secret, "/data/uploads/artwork")
}

func NewWithArtworkDir(repo Repository, secret []byte, artworkDir string) *Server {
	return &Server{
		repo:                repo,
		secret:              secret,
		loginLimiter:        NewRateLimiter(5, 15*time.Minute),
		registrationLimiter: NewRateLimiter(5, 15*time.Minute),
		artwork:             artwork.NewStore(artworkDir),
	}
}

// HandleHome renders the admin home page. Unauthenticated requests are
// redirected to the login page.
func (s *Server) HandleHome(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	artists, err := s.repo.ListArtistsForUser(session.UserID)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	activeArtist := s.activeArtistFromRequest(r, session, artists)
	var songs []songListItem
	if activeArtist != nil {
		artistSongs, err := s.repo.ListSongsForArtist(activeArtist.ID)
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		songs = songListItems(artistSongs)
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := HomePage(homeView{
		Email:        session.Email,
		Artists:      artists,
		ActiveArtist: activeArtist,
		Songs:        songs,
	}).Render(r.Context(), w); err != nil {
		log.Printf("render admin home: %v", err)
	}
}

// HandleActiveArtistSubmit stores the user's active artist selection.
func (s *Server) HandleActiveArtistSubmit(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderHomePage(w, formErrorStatus(err), homeView{
			Email: session.Email,
			Error: "Invalid form submission.",
		})
		return
	}

	artists, err := s.repo.ListArtistsForUser(session.UserID)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	artistSlug := strings.TrimSpace(r.Form.Get("artist_slug"))
	activeArtist := findArtistBySlug(artists, artistSlug)
	if activeArtist == nil {
		s.renderHomePage(w, http.StatusForbidden, homeView{
			Email:   session.Email,
			Artists: artists,
			Error:   "Choose an artist assigned to your account.",
		})
		return
	}

	if err := s.setActiveArtistCookie(w, session.UserID, activeArtist.Slug); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("HX-Redirect", "/admin/")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// HandleLogoutSubmit clears admin cookies and returns the login page location.
func (s *Server) HandleLogoutSubmit(w http.ResponseWriter, r *http.Request) {
	expireCookie(w, SessionCookieName, "/admin")
	expireCookie(w, ActiveArtistCookieName, "/admin")

	w.Header().Set("Cache-Control", "no-store")
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/admin/login")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// HandleLoginPage renders the login form. Already-authenticated requests are
// redirected to the admin home page.
func (s *Server) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); ok {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}

	s.renderLoginPage(w, http.StatusOK, loginView{})
}

// HandleLoginSubmit processes the login form POST. On success a signed session
// cookie is set. HTMX requests receive an HX-Redirect header instead of a
// standard redirect.
func (s *Server) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderLoginResponse(w, r, formErrorStatus(err), loginView{
			Error: "Invalid form submission.",
		})
		return
	}

	email := store.NormalizeEmail(r.Form.Get("email"))
	password := r.Form.Get("password")
	if email == "" || password == "" {
		s.renderLoginResponse(w, r, http.StatusBadRequest, loginView{
			Email: email,
			Error: "Enter both email and password.",
		})
		return
	}
	loginKey := RequestKey(r, "admin-login", email)
	if !s.loginLimiter.Allow(loginKey) {
		s.renderLoginResponse(w, r, http.StatusTooManyRequests, loginView{
			Email: email,
			Error: "Too many login attempts. Try again later.",
		})
		return
	}

	user, err := s.repo.FindUserByEmail(email)
	if err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			s.renderLoginResponse(w, r, http.StatusUnauthorized, loginView{
				Email: email,
				Error: "Incorrect email or password.",
			})
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if !VerifyPassword(password, user.PasswordHash) {
		s.renderLoginResponse(w, r, http.StatusUnauthorized, loginView{
			Email: email,
			Error: "Incorrect email or password.",
		})
		return
	}
	s.loginLimiter.Reset(loginKey)

	token, err := NewToken(s.secret, user.ID, user.Email, time.Now().UTC())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   int(SessionLifetime.Seconds()),
		Expires:  time.Now().UTC().Add(SessionLifetime),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Cache-Control", "no-store")
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/admin/")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// HandleRegisterPage renders the account registration form. Already-authenticated
// requests are redirected to the admin home page.
func (s *Server) HandleRegisterPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); ok {
		http.Redirect(w, r, "/admin/", http.StatusSeeOther)
		return
	}

	s.renderRegisterPage(w, http.StatusOK, registerView{})
}

// HandleRegisterSubmit processes the registration form POST. On success a
// signed session cookie is set and the user is redirected to the admin home.
func (s *Server) HandleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderRegisterResponse(w, r, formErrorStatus(err), registerView{
			Error: "Invalid form submission.",
		})
		return
	}

	inviteCode := strings.TrimSpace(r.Form.Get("invite_code"))
	password := r.Form.Get("password")
	passwordConfirm := r.Form.Get("password_confirm")

	if inviteCode == "" || password == "" || passwordConfirm == "" {
		s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
			InviteCode: inviteCode,
			Error:      "All fields are required.",
		})
		return
	}

	if password != passwordConfirm {
		s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
			InviteCode: inviteCode,
			Error:      "Passwords do not match.",
		})
		return
	}
	if len(password) < 12 {
		s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
			InviteCode: inviteCode,
			Error:      "Password must be at least 12 characters.",
		})
		return
	}

	codeHash := hashInvitationCode(s.secret, inviteCode)
	registrationKey := RequestKey(r, "admin-register", codeHash)
	if !s.registrationLimiter.Allow(registrationKey) {
		s.renderRegisterResponse(w, r, http.StatusTooManyRequests, registerView{
			InviteCode: inviteCode,
			Error:      "Too many registration attempts. Try again later.",
		})
		return
	}
	invitation, err := s.repo.FindInvitationByCodeHash(codeHash)
	if err != nil {
		if errors.Is(err, store.ErrInvitationNotFound) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "Invalid invite code.",
			})
			return
		}
		if errors.Is(err, store.ErrInvitationExpired) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "This invite code has expired.",
			})
			return
		}
		if errors.Is(err, store.ErrInvitationRevoked) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "This invite code has been revoked.",
			})
			return
		}
		if errors.Is(err, store.ErrInvitationAlreadyAccepted) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "This invite code has already been used.",
			})
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	passwordHash, err := HashUserPassword(password)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	userID, err := s.repo.RedeemInvitation(invitation.ID, invitation.Email, passwordHash)
	if err != nil {
		if errors.Is(err, store.ErrInvitationAlreadyAccepted) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "This invite code has already been used.",
			})
			return
		}
		if errors.Is(err, store.ErrInvitationExpired) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "This invite code has expired.",
			})
			return
		}
		if errors.Is(err, store.ErrInvitationRevoked) {
			s.renderRegisterResponse(w, r, http.StatusBadRequest, registerView{
				InviteCode: inviteCode,
				Error:      "This invite code has been revoked.",
			})
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	s.registrationLimiter.Reset(registrationKey)

	token, err := NewToken(s.secret, userID, invitation.Email, time.Now().UTC())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   int(SessionLifetime.Seconds()),
		Expires:  time.Now().UTC().Add(SessionLifetime),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Cache-Control", "no-store")
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/admin/")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// HandleNewSongPage renders the create-song form.
func (s *Server) HandleNewSongPage(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	activeArtist, err := s.resolveActiveArtist(r, session)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	view := songFormView{ActiveArtist: activeArtist}
	if activeArtist == nil {
		view.Error = "No artists are assigned to your account."
	}
	s.renderSongFormPage(w, http.StatusOK, view)
}

// HandleCreateSongSubmit processes the create-song form POST. On success the
// client is redirected to the new song page.
func (s *Server) HandleCreateSongSubmit(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderSongFormPage(w, formErrorStatus(err), songFormView{
			Error: "Invalid form submission.",
		})
		return
	}

	activeArtist, err := s.resolveActiveArtist(r, session)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	view := songFormView{
		ActiveArtist:  activeArtist,
		Title:         strings.TrimSpace(r.Form.Get("title")),
		Description:   strings.TrimSpace(r.Form.Get("description")),
		YouTubeURL:    strings.TrimSpace(r.Form.Get("youtube_url")),
		SpotifyURL:    strings.TrimSpace(r.Form.Get("spotify_url")),
		AppleMusicURL: strings.TrimSpace(r.Form.Get("apple_music_url")),
	}
	if activeArtist == nil {
		view.Error = "No artists are assigned to your account."
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	if view.Title == "" {
		view.Error = "Title is required."
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	if err := normalizeSongURLs(&view); err != nil {
		view.Error = err.Error()
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	artworkPath, hasArtwork, err := s.uploadArtwork(r)
	if err != nil {
		view.Error = err.Error()
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	if hasArtwork {
		view.ArtworkPath = artworkPath
	}

	if _, err := insertSongWithUniqueSlug(s.repo, activeArtist, view); err != nil {
		if hasArtwork {
			_ = s.artwork.Remove(artworkPath)
		}
		view.Error = err.Error()
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}

	s.renderSongPreviewPage(w, r, http.StatusOK, songPreviewView{
		ArtistName:    activeArtist.Name,
		Title:         view.Title,
		Description:   view.Description,
		ArtworkPath:   view.ArtworkPath,
		YouTubeURL:    view.YouTubeURL,
		SpotifyURL:    view.SpotifyURL,
		AppleMusicURL: view.AppleMusicURL,
	})
}

func insertSongWithUniqueSlug(repo Repository, activeArtist *store.Artist, view songFormView) (string, error) {
	base := slugify(view.Title)
	if base == "" {
		return "", errors.New("unable to generate slug from title")
	}

	for attempt := 0; attempt < maxSlugAttempts; attempt++ {
		slug := slugCandidate(base, attempt)
		err := repo.InsertSongForArtist(activeArtist.ID, store.Song{
			Title:         view.Title,
			ArtistName:    activeArtist.Name,
			Description:   view.Description,
			ArtworkPath:   view.ArtworkPath,
			YouTubeURL:    view.YouTubeURL,
			SpotifyURL:    view.SpotifyURL,
			AppleMusicURL: view.AppleMusicURL,
			SongSlug:      slug,
			ArtistSlug:    activeArtist.Slug,
		})
		if errors.Is(err, store.ErrSongAlreadyExists) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("could not save song")
		}
		return slug, nil
	}
	return "", errors.New("could not generate a unique slug")
}

// HandleEditSongPage renders the edit-song form for an existing song.
func (s *Server) HandleEditSongPage(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	activeArtist, err := s.resolveActiveArtist(r, session)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if activeArtist == nil {
		s.renderSongFormPage(w, http.StatusForbidden, songFormView{
			Error: "No artists are assigned to your account.",
		})
		return
	}

	songSlug := strings.TrimSpace(chi.URLParam(r, "songSlug"))
	song, err := s.repo.FindBySlug(activeArtist.Slug, songSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderSongFormPage(w, http.StatusOK, songFormView{
		Mode:                "edit",
		ActiveArtist:        activeArtist,
		Title:               song.Title,
		Description:         song.Description,
		ArtworkPath:         song.ArtworkPath,
		YouTubeURL:          song.YouTubeURL,
		SpotifyURL:          song.SpotifyURL,
		AppleMusicURL:       song.AppleMusicURL,
		SongSlug:            song.SongSlug,
		Action:              "/admin/songs/" + url.PathEscape(song.SongSlug),
		DeleteAction:        "/admin/songs/" + url.PathEscape(song.SongSlug) + "/delete",
		RemoveArtworkAction: "/admin/songs/" + url.PathEscape(song.SongSlug) + "/artwork/delete",
		SubmitLabel:         "Save Changes",
	})
}

// HandleUpdateSongSubmit processes the edit-song form POST.
func (s *Server) HandleUpdateSongSubmit(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseMultipartForm(11 << 20); err != nil {
		s.renderSongFormPage(w, formErrorStatus(err), songFormView{
			Error: "Invalid form submission.",
		})
		return
	}

	activeArtist, err := s.resolveActiveArtist(r, session)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	songSlug := strings.TrimSpace(chi.URLParam(r, "songSlug"))
	view := songFormView{
		Mode:          "edit",
		ActiveArtist:  activeArtist,
		Title:         strings.TrimSpace(r.Form.Get("title")),
		Description:   strings.TrimSpace(r.Form.Get("description")),
		YouTubeURL:    strings.TrimSpace(r.Form.Get("youtube_url")),
		SpotifyURL:    strings.TrimSpace(r.Form.Get("spotify_url")),
		AppleMusicURL: strings.TrimSpace(r.Form.Get("apple_music_url")),
		SongSlug:      songSlug,
		Action:        "/admin/songs/" + url.PathEscape(songSlug),
		DeleteAction:  "/admin/songs/" + url.PathEscape(songSlug) + "/delete",
		SubmitLabel:   "Save Changes",
	}
	if activeArtist == nil {
		view.Error = "No artists are assigned to your account."
		s.renderSongFormPage(w, http.StatusForbidden, view)
		return
	}
	oldSong, err := s.repo.FindBySlug(activeArtist.Slug, songSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	view.ArtworkPath = oldSong.ArtworkPath
	if view.Title == "" {
		view.Error = "Title is required."
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	if err := normalizeSongURLs(&view); err != nil {
		view.Error = err.Error()
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	newArtworkPath, hasArtwork, err := s.uploadArtwork(r)
	if err != nil {
		view.Error = err.Error()
		s.renderSongFormPage(w, http.StatusBadRequest, view)
		return
	}
	if hasArtwork {
		view.ArtworkPath = newArtworkPath
	}

	if err := s.repo.UpdateSongForArtist(activeArtist.ID, songSlug, store.Song{
		Title:         view.Title,
		ArtistName:    activeArtist.Name,
		Description:   view.Description,
		ArtworkPath:   view.ArtworkPath,
		YouTubeURL:    view.YouTubeURL,
		SpotifyURL:    view.SpotifyURL,
		AppleMusicURL: view.AppleMusicURL,
		SongSlug:      songSlug,
		ArtistSlug:    activeArtist.Slug,
	}); err != nil {
		if hasArtwork {
			_ = s.artwork.Remove(newArtworkPath)
		}
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if hasArtwork && oldSong.ArtworkPath != "" {
		_ = s.artwork.Remove(oldSong.ArtworkPath)
	}

	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// HandleRemoveArtworkSubmit removes the current artwork from an existing song.
func (s *Server) HandleRemoveArtworkSubmit(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	activeArtist, err := s.resolveActiveArtist(r, session)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if activeArtist == nil {
		http.Error(w, "No artists are assigned to your account.", http.StatusForbidden)
		return
	}

	songSlug := strings.TrimSpace(chi.URLParam(r, "songSlug"))
	song, err := s.repo.FindBySlug(activeArtist.Slug, songSlug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	oldArtworkPath := song.ArtworkPath
	if oldArtworkPath != "" {
		song.ArtworkPath = ""
		if err := s.repo.UpdateSongForArtist(activeArtist.ID, songSlug, *song); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		_ = s.artwork.Remove(oldArtworkPath)
	}

	editURL := "/admin/songs/" + url.PathEscape(songSlug) + "/edit"
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("HX-Redirect", editURL)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, editURL, http.StatusSeeOther)
}

// HandleDeleteSongSubmit processes the delete-song form POST.
func (s *Server) HandleDeleteSongSubmit(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}

	activeArtist, err := s.resolveActiveArtist(r, session)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if activeArtist == nil {
		http.Error(w, "No artists are assigned to your account.", http.StatusForbidden)
		return
	}

	songSlug := strings.TrimSpace(chi.URLParam(r, "songSlug"))
	if err := s.repo.DeleteSongForArtist(activeArtist.ID, songSlug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("HX-Redirect", "/admin/")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// authenticatedSession reads and validates the session cookie from r.
func (s *Server) authenticatedSession(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return Session{}, false
	}

	session, err := ParseToken(s.secret, cookie.Value)
	if err != nil {
		return Session{}, false
	}

	return session, true
}

func (s *Server) resolveActiveArtist(r *http.Request, session Session) (*store.Artist, error) {
	artists, err := s.repo.ListArtistsForUser(session.UserID)
	if err != nil {
		return nil, err
	}
	return s.activeArtistFromRequest(r, session, artists), nil
}

func (s *Server) activeArtistFromRequest(r *http.Request, session Session, artists []store.Artist) *store.Artist {
	if cookie, err := r.Cookie(ActiveArtistCookieName); err == nil {
		activeArtist, err := ParseActiveArtistToken(s.secret, cookie.Value)
		if err == nil && activeArtist.UserID == session.UserID {
			if artist := findArtistBySlug(artists, activeArtist.ArtistSlug); artist != nil {
				return artist
			}
		}
	}

	if len(artists) == 0 {
		return nil
	}
	return &artists[0]
}

func findArtistBySlug(artists []store.Artist, slug string) *store.Artist {
	for i := range artists {
		if artists[i].Slug == slug {
			return &artists[i]
		}
	}
	return nil
}

func songListItems(songs []store.Song) []songListItem {
	items := make([]songListItem, 0, len(songs))
	for _, song := range songs {
		finalURL := "/s/" + url.PathEscape(song.ArtistSlug) + "/" + url.PathEscape(song.SongSlug)
		items = append(items, songListItem{
			Title:    song.Title,
			SongSlug: song.SongSlug,
			FinalURL: finalURL,
			EditURL:  "/admin/songs/" + url.PathEscape(song.SongSlug) + "/edit",
		})
	}
	return items
}

func formErrorStatus(err error) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func (s *Server) setActiveArtistCookie(w http.ResponseWriter, userID int64, artistSlug string) error {
	token, err := NewActiveArtistToken(s.secret, userID, artistSlug, time.Now().UTC())
	if err != nil {
		return err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     ActiveArtistCookieName,
		Value:    token,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   int(SessionLifetime.Seconds()),
		Expires:  time.Now().UTC().Add(SessionLifetime),
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

func expireCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		HttpOnly: true,
		Secure:   true,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		SameSite: http.SameSiteStrictMode,
	})
}

// renderLoginResponse renders either the login card fragment (HTMX) or the
// full login page.
func (s *Server) renderLoginResponse(w http.ResponseWriter, r *http.Request, status int, view loginView) {
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := LoginCard(view).Render(r.Context(), w); err != nil {
			log.Printf("render admin login card: %v", err)
		}
		return
	}

	s.renderLoginPage(w, status, view)
}

func (s *Server) renderLoginPage(w http.ResponseWriter, status int, view loginView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := LoginPage(view).Render(context.Background(), w); err != nil {
		log.Printf("render admin login page: %v", err)
	}
}

func (s *Server) renderSongFormPage(w http.ResponseWriter, status int, view songFormView) {
	if view.Mode == "" {
		view.Mode = "create"
	}
	if view.Action == "" {
		view.Action = "/admin/songs"
	}
	if view.DeleteAction == "" && view.Mode == "edit" && view.SongSlug != "" {
		view.DeleteAction = "/admin/songs/" + url.PathEscape(view.SongSlug) + "/delete"
	}
	if view.RemoveArtworkAction == "" && view.Mode == "edit" && view.SongSlug != "" {
		view.RemoveArtworkAction = "/admin/songs/" + url.PathEscape(view.SongSlug) + "/artwork/delete"
	}
	if view.SubmitLabel == "" {
		view.SubmitLabel = "Create Song"
	}
	if view.PageTitle == "" {
		if view.Mode == "edit" {
			view.PageTitle = "Edit Song"
		} else {
			view.PageTitle = "Create Song"
		}
	}
	if view.ArtworkURL == "" {
		view.ArtworkURL = artworkURL(view.ArtworkPath)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := SongFormPage(view).Render(context.Background(), w); err != nil {
		log.Printf("render admin song form page: %v", err)
	}
}

func (s *Server) renderSongPreviewPage(w http.ResponseWriter, r *http.Request, status int, view songPreviewView) {
	var body bytes.Buffer
	if err := songtemplates.SongPage(
		view.ArtistName,
		view.Title,
		strings.TrimSpace(view.Description),
		artworkURL(view.ArtworkPath),
		urlpolicy.SafeExternalURL(view.YouTubeURL),
		urlpolicy.SafeExternalURL(view.SpotifyURL),
		urlpolicy.SafeExternalURL(view.AppleMusicURL),
	).Render(r.Context(), &body); err != nil {
		log.Printf("render admin song preview body: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	preview := strings.Replace(body.String(), "</style>", previewBannerCSS+"</style>", 1)
	preview = strings.Replace(preview, "<body>", `<body><div class="admin-preview-banner"><a href="/admin/">Go back to dashboard</a></div>`, 1)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write([]byte(preview)); err != nil {
		log.Printf("write admin song preview page: %v", err)
	}
}

func (s *Server) uploadArtwork(r *http.Request) (string, bool, error) {
	file, _, err := r.FormFile("artwork")
	if errors.Is(err, http.ErrMissingFile) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("invalid artwork upload")
	}
	defer file.Close()
	key, err := s.artwork.Save(file)
	return key, true, err
}

func artworkURL(path string) string {
	if path != "" {
		return "/media/artwork/" + url.PathEscape(path)
	}
	return "/static/song_artwork_placeholder.png"
}

func (s *Server) renderHomePage(w http.ResponseWriter, status int, view homeView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := HomePage(view).Render(context.Background(), w); err != nil {
		log.Printf("render admin home page: %v", err)
	}
}

func (s *Server) renderRegisterResponse(w http.ResponseWriter, r *http.Request, status int, view registerView) {
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := RegisterCard(view).Render(r.Context(), w); err != nil {
			log.Printf("render admin register card: %v", err)
		}
		return
	}

	s.renderRegisterPage(w, status, view)
}

func (s *Server) renderRegisterPage(w http.ResponseWriter, status int, view registerView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := RegisterPage(view).Render(context.Background(), w); err != nil {
		log.Printf("render admin register page: %v", err)
	}
}

func normalizeSongURLs(view *songFormView) error {
	fields := []struct {
		name  string
		value *string
	}{
		{name: "YouTube URL", value: &view.YouTubeURL},
		{name: "Spotify URL", value: &view.SpotifyURL},
		{name: "Apple Music URL", value: &view.AppleMusicURL},
	}

	for _, field := range fields {
		normalized, err := urlpolicy.NormalizeExternalURL(*field.value)
		if err != nil {
			return fmt.Errorf("%s %s.", field.name, err.Error())
		}
		*field.value = normalized
	}
	return nil
}

// isHTMXRequest reports whether r was initiated by the HTMX library.
func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

const previewBannerCSS = `
				.admin-preview-banner {
				  position: fixed;
				  top: 0;
				  left: 0;
				  right: 0;
				  z-index: 20;
				  display: flex;
				  justify-content: center;
				  padding: 0.75rem 1rem;
				  background: rgba(9, 9, 15, 0.96);
				  border-bottom: 1px solid var(--color-border);
				  backdrop-filter: blur(12px);
				}

				.admin-preview-banner a {
				  display: inline-flex;
				  align-items: center;
				  justify-content: center;
				  min-height: 2.5rem;
				  padding: 0.65rem 1rem;
				  border-radius: 999px;
				  background: var(--color-text-primary);
				  color: var(--color-bg);
				  font-weight: 700;
				  text-decoration: none;
				}
`
