package platformadmin

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/jbrixon/songdock/internal/admin"
	"github.com/jbrixon/songdock/internal/store"
)

// SessionCookieName is the name of the platform admin session cookie.
const SessionCookieName = "platform_admin_session"

// Server handles platform-level administration requests.
type Server struct {
	repo         Repository
	secret       []byte
	username     string
	password     string
	loginLimiter *admin.RateLimiter
}

// New returns a platform admin server.
func New(repo Repository, secret []byte, username, password string) *Server {
	return &Server{
		repo:         repo,
		secret:       secret,
		username:     strings.TrimSpace(username),
		password:     password,
		loginLimiter: admin.NewRateLimiter(5, 15*time.Minute),
	}
}

// HandleHome renders the platform dashboard.
func (s *Server) HandleHome(w http.ResponseWriter, r *http.Request) {
	session, ok := s.authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := HomePage(homeView{Email: session.Email}).Render(r.Context(), w); err != nil {
		log.Printf("render platform admin home: %v", err)
	}
}

// HandleLogoutSubmit clears the platform admin cookie and returns the login page location.
func (s *Server) HandleLogoutSubmit(w http.ResponseWriter, r *http.Request) {
	adminExpireCookie(w, SessionCookieName, "/platform/admin")

	w.Header().Set("Cache-Control", "no-store")
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/platform/admin/login")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
}

// HandleLoginPage renders the platform admin login form.
func (s *Server) HandleLoginPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); ok {
		http.Redirect(w, r, "/platform/admin/", http.StatusSeeOther)
		return
	}

	s.renderLoginPage(w, http.StatusOK, loginView{})
}

// HandleLoginSubmit processes platform admin login.
func (s *Server) HandleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderLoginResponse(w, r, formErrorStatus(err), loginView{
			Error: "Invalid form submission.",
		})
		return
	}

	username := strings.TrimSpace(r.Form.Get("username"))
	password := r.Form.Get("password")
	if username == "" || password == "" {
		s.renderLoginResponse(w, r, http.StatusBadRequest, loginView{
			Username: username,
			Error:    "Enter both username and password.",
		})
		return
	}
	loginKey := admin.RequestKey(r, "platform-admin-login", username)
	if !s.loginLimiter.Allow(loginKey) {
		s.renderLoginResponse(w, r, http.StatusTooManyRequests, loginView{
			Username: username,
			Error:    "Too many login attempts. Try again later.",
		})
		return
	}

	if subtle.ConstantTimeCompare([]byte(username), []byte(s.username)) != 1 ||
		subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) != 1 {
		s.renderLoginResponse(w, r, http.StatusUnauthorized, loginView{
			Username: username,
			Error:    "Incorrect username or password.",
		})
		return
	}
	s.loginLimiter.Reset(loginKey)

	token, err := admin.NewPlatformToken(s.secret, username, time.Now().UTC())
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/platform/admin",
		HttpOnly: true,
		Secure:   true,
		MaxAge:   int(admin.SessionLifetime.Seconds()),
		Expires:  time.Now().UTC().Add(admin.SessionLifetime),
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Cache-Control", "no-store")
	if isHTMXRequest(r) {
		w.Header().Set("HX-Redirect", "/platform/admin/")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/platform/admin/", http.StatusSeeOther)
}

// HandleUsers renders user management.
func (s *Server) HandleUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	view, err := s.usersView()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderUsersPage(w, http.StatusOK, view)
}

// HandleDeleteUserSubmit permanently deletes a user and their artist memberships.
func (s *Server) HandleDeleteUserSubmit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	userID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "userID")), 10, 64)
	if err != nil || userID <= 0 {
		s.renderUsersResponse(w, r, http.StatusBadRequest, "Choose an existing user.")
		return
	}
	if err := s.repo.DeleteUser(userID); err != nil {
		if errors.Is(err, store.ErrUserNotFound) {
			s.renderUsersResponse(w, r, http.StatusNotFound, "User not found.")
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderUsersResponse(w, r, http.StatusOK, "User deleted.")
}

// HandleInvitations renders invitation management.
func (s *Server) HandleInvitations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	view, err := s.invitationsView("", "", 0)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderInvitationsPage(w, http.StatusOK, view)
}

// HandleArtists renders platform artist management.
func (s *Server) HandleArtists(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	view, err := s.artistsView("", "", "")
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderArtistsPage(w, http.StatusOK, view)
}

// HandleArtistSlugAvailability reports whether a submitted artist slug can be
// used. It is advisory; creation still validates the slug on submit.
func (s *Server) HandleArtistSlugAvailability(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	mode := strings.TrimSpace(chi.URLParam(r, "mode"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	slug := strings.TrimSpace(r.URL.Query().Get("slug"))
	slugMode := strings.TrimSpace(r.URL.Query().Get("slug_mode"))
	if slugMode == "" {
		slugMode = "auto"
	}

	switch mode {
	case "manual":
		slug = slugify(slug)
		slugMode = "manual"
	case "auto":
		if slugMode != "manual" {
			slug = slugify(name)
			slugMode = "auto"
		} else {
			slug = slugify(slug)
		}
	default:
		http.NotFound(w, r)
		return
	}

	view := artistsView{
		Name:     name,
		Slug:     slug,
		SlugMode: slugMode,
	}

	response := slugAvailabilityResponse{Slug: slug}
	if slug == "" {
		s.renderSlugFields(w, http.StatusOK, view)
		return
	}
	if !validSlug(slug) {
		response.Valid = false
		response.Available = false
		response.Message = "Use lowercase letters, numbers, and single hyphens."
		view.SlugStatus = slugStatusFromResponse(response)
		s.renderSlugFields(w, http.StatusOK, view)
		return
	}

	if _, err := s.repo.FindArtistBySlug(slug); err != nil {
		if errors.Is(err, store.ErrArtistNotFound) {
			response.Valid = true
			response.Available = true
			response.Message = "Slug is available."
			view.SlugStatus = slugStatusFromResponse(response)
			s.renderSlugFields(w, http.StatusOK, view)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	response.Valid = true
	response.Available = false
	response.Message = "Slug is already taken."
	view.SlugStatus = slugStatusFromResponse(response)
	s.renderSlugFields(w, http.StatusOK, view)
}

// HandleCreateInvitationSubmit creates a pending invitation for an admin user.
func (s *Server) HandleCreateInvitationSubmit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderInvitationsResponse(w, r, formErrorStatus(err), "Invalid form submission.", "", 0)
		return
	}

	email := store.NormalizeEmail(r.Form.Get("email"))
	artistIDRaw := strings.TrimSpace(r.Form.Get("artist_id"))
	artistID, err := strconv.ParseInt(artistIDRaw, 10, 64)
	if email == "" {
		s.renderInvitationsResponse(w, r, http.StatusBadRequest, "Email is required.", email, artistID)
		return
	}
	if err != nil || artistID <= 0 {
		s.renderInvitationsResponse(w, r, http.StatusBadRequest, "Choose an artist for this administrator.", email, 0)
		return
	}

	invitationCode, err := newInvitationCode()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := s.repo.CreateUserInvitation(email, HashInvitationCode(s.secret, invitationCode), artistID); err != nil {
		if errors.Is(err, store.ErrUserAlreadyExists) {
			s.renderInvitationsResponse(w, r, http.StatusConflict, "A user with that email already exists.", email, artistID)
			return
		}
		if errors.Is(err, store.ErrUserInvitationAlreadyExists) {
			s.renderInvitationsResponse(w, r, http.StatusConflict, "An invitation for that email already exists.", email, artistID)
			return
		}
		if errors.Is(err, store.ErrArtistNotFound) {
			s.renderInvitationsResponse(w, r, http.StatusBadRequest, "Choose an existing artist for this administrator.", email, artistID)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderInvitationsResponse(w, r, http.StatusOK, fmt.Sprintf("Invitation created for %s. Code: %s", email, invitationCode), "", 0)
}

// HandleRevokeInvitationSubmit revokes a pending invitation.
func (s *Server) HandleRevokeInvitationSubmit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	invitationID, err := strconv.ParseInt(strings.TrimSpace(chi.URLParam(r, "invitationID")), 10, 64)
	if err != nil || invitationID <= 0 {
		s.renderInvitationsResponse(w, r, http.StatusBadRequest, "Choose an existing invitation.", "", 0)
		return
	}
	if err := s.repo.RevokeInvitation(invitationID); err != nil {
		switch {
		case errors.Is(err, store.ErrInvitationNotFound):
			s.renderInvitationsResponse(w, r, http.StatusNotFound, "Invitation not found.", "", 0)
		case errors.Is(err, store.ErrInvitationAlreadyAccepted):
			s.renderInvitationsResponse(w, r, http.StatusConflict, "Invitation has already been accepted.", "", 0)
		case errors.Is(err, store.ErrInvitationRevoked):
			s.renderInvitationsResponse(w, r, http.StatusConflict, "Invitation has already been revoked.", "", 0)
		default:
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
		return
	}

	s.renderInvitationsResponse(w, r, http.StatusOK, "Invitation revoked.", "", 0)
}

// HandleCreateArtistSubmit creates a platform-managed artist.
func (s *Server) HandleCreateArtistSubmit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticatedSession(r); !ok {
		http.Redirect(w, r, "/platform/admin/login", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		s.renderArtistsResponse(w, r, formErrorStatus(err), "Invalid form submission.", "", "")
		return
	}

	name := strings.TrimSpace(r.Form.Get("name"))
	slug := strings.TrimSpace(r.Form.Get("slug"))
	if name == "" {
		s.renderArtistsResponse(w, r, http.StatusBadRequest, "Artist name is required.", name, slug)
		return
	}
	if slug == "" {
		slug = slugify(name)
	}
	if !validSlug(slug) {
		s.renderArtistsResponse(w, r, http.StatusBadRequest, "Artist slug must use lowercase letters, numbers, and single hyphens.", name, slug)
		return
	}

	artist, err := s.repo.CreateArtist(name, slug)
	if err != nil {
		if errors.Is(err, store.ErrArtistAlreadyExists) {
			s.renderArtistsResponse(w, r, http.StatusConflict, "An artist with that slug already exists.", name, slug)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	s.renderArtistsResponse(w, r, http.StatusOK, fmt.Sprintf("Artist created: %s", artist.Name), "", "")
}

func (s *Server) authenticatedSession(r *http.Request) (admin.Session, bool) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return admin.Session{}, false
	}

	session, err := admin.ParsePlatformToken(s.secret, cookie.Value)
	if err != nil {
		return admin.Session{}, false
	}
	return session, true
}

func formErrorStatus(err error) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func (s *Server) usersView() (usersView, error) {
	users, err := s.repo.ListUsers()
	if err != nil {
		return usersView{}, err
	}
	return usersView{Users: users}, nil
}

func (s *Server) invitationsView(message, email string, artistID int64) (invitationsView, error) {
	artists, err := s.repo.ListArtists()
	if err != nil {
		return invitationsView{}, err
	}
	invitations, err := s.repo.ListPendingInvitations()
	if err != nil {
		return invitationsView{}, err
	}
	return invitationsView{
		Artists:            artists,
		PendingInvitations: invitations,
		Message:            message,
		Email:              email,
		ArtistID:           artistID,
	}, nil
}

func (s *Server) artistsView(message, name, slug string) (artistsView, error) {
	artists, err := s.repo.ListArtists()
	if err != nil {
		return artistsView{}, err
	}
	return artistsView{
		Artists:    artists,
		Message:    message,
		Name:       name,
		Slug:       slug,
		SlugMode:   "auto",
		SlugStatus: slugStatusView{},
	}, nil
}

func (s *Server) renderLoginResponse(w http.ResponseWriter, r *http.Request, status int, view loginView) {
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := LoginCard(view).Render(r.Context(), w); err != nil {
			log.Printf("render platform admin login card: %v", err)
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
		log.Printf("render platform admin login page: %v", err)
	}
}

func (s *Server) renderInvitationsResponse(w http.ResponseWriter, r *http.Request, status int, message, email string, artistID int64) {
	view, err := s.invitationsView(message, email, artistID)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := InvitationsPanel(view).Render(r.Context(), w); err != nil {
			log.Printf("render platform admin invitations panel: %v", err)
		}
		return
	}
	s.renderInvitationsPage(w, status, view)
}

func (s *Server) renderUsersPage(w http.ResponseWriter, status int, view usersView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := UsersPage(view).Render(context.Background(), w); err != nil {
		log.Printf("render platform admin users page: %v", err)
	}
}

func (s *Server) renderUsersResponse(w http.ResponseWriter, r *http.Request, status int, message string) {
	view, err := s.usersView()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	view.Message = message
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := UsersPanel(view).Render(r.Context(), w); err != nil {
			log.Printf("render platform admin users panel: %v", err)
		}
		return
	}
	s.renderUsersPage(w, status, view)
}

func (s *Server) renderInvitationsPage(w http.ResponseWriter, status int, view invitationsView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := InvitationsPage(view).Render(context.Background(), w); err != nil {
		log.Printf("render platform admin invitations page: %v", err)
	}
}

func (s *Server) renderArtistsResponse(w http.ResponseWriter, r *http.Request, status int, message, name, slug string) {
	view, err := s.artistsView(message, name, slug)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if isHTMXRequest(r) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		if err := ArtistsPanel(view).Render(r.Context(), w); err != nil {
			log.Printf("render platform admin artists panel: %v", err)
		}
		return
	}
	s.renderArtistsPage(w, status, view)
}

func (s *Server) renderArtistsPage(w http.ResponseWriter, status int, view artistsView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := ArtistsPage(view).Render(context.Background(), w); err != nil {
		log.Printf("render platform admin artists page: %v", err)
	}
}

func (s *Server) renderSlugFields(w http.ResponseWriter, status int, view artistsView) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := ArtistSlugFields(view).Render(context.Background(), w); err != nil {
		log.Printf("render artist slug fields: %v", err)
	}
}

func isHTMXRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

type slugAvailabilityResponse struct {
	Slug      string `json:"slug"`
	Valid     bool   `json:"valid"`
	Available bool   `json:"available"`
	Message   string `json:"message"`
}

func slugStatusFromResponse(response slugAvailabilityResponse) slugStatusView {
	view := slugStatusView{Message: response.Message}
	if response.Message == "" {
		return view
	}
	if response.Valid && response.Available {
		view.State = "ok"
		return view
	}
	view.State = "error"
	return view
}

func adminExpireCookie(w http.ResponseWriter, name, path string) {
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

func newInvitationCode() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("read invitation code entropy: %w", err)
	}
	return strings.ToUpper(hex.EncodeToString(buf[:])), nil
}
