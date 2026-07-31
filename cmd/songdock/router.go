package main

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jbrixon/songdock/internal/admin"
	"github.com/jbrixon/songdock/internal/platformadmin"
	"github.com/jbrixon/songdock/internal/store"
	"github.com/jbrixon/songdock/internal/templates"
	"github.com/jbrixon/songdock/internal/urlpolicy"
	static "github.com/jbrixon/songdock/web/static"
)

const maxAdminRequestBody = 64 << 10

func newRouter(songs store.SongRepository, adminRepo admin.Repository, platformRepo platformadmin.Repository, secret []byte, platformUsername, platformPassword string) http.Handler {
	r := chi.NewRouter()
	r.Use(securityHeaders)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))
	a := admin.New(adminRepo, secret)
	platform := platformadmin.New(
		platformRepo,
		secret,
		platformUsername,
		platformPassword,
	)

	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.NotFound(w, req)
	})

	r.Get("/s/{artistSlug}/{songSlug}", func(w http.ResponseWriter, req *http.Request) {
		artistSlug := chi.URLParam(req, "artistSlug")
		songSlug := chi.URLParam(req, "songSlug")

		song, err := songs.FindBySlug(artistSlug, songSlug)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				http.NotFound(w, req)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := templates.SongPage(
			song.ArtistName,
			song.Title,
			strings.TrimSpace(song.Description),
			urlpolicy.SafeExternalURL(song.ImageURL),
			urlpolicy.SafeExternalURL(song.YouTubeURL),
			urlpolicy.SafeExternalURL(song.SpotifyURL),
			urlpolicy.SafeExternalURL(song.AppleMusicURL),
		).Render(req.Context(), w); err != nil {
			log.Printf("render error: %v", err)
		}
	})

	r.Route("/admin", func(r chi.Router) {
		r.Use(limitAdminRequestBody)
		r.Get("/", a.HandleHome)
		r.Post("/active-artist", a.HandleActiveArtistSubmit)
		r.Post("/logout", a.HandleLogoutSubmit)
		r.Get("/login", a.HandleLoginPage)
		r.Post("/login", a.HandleLoginSubmit)
		r.Get("/register", a.HandleRegisterPage)
		r.Post("/register", a.HandleRegisterSubmit)
		r.Get("/songs/new", a.HandleNewSongPage)
		r.Post("/songs", a.HandleCreateSongSubmit)
		r.Get("/songs/{songSlug}/edit", a.HandleEditSongPage)
		r.Post("/songs/{songSlug}", a.HandleUpdateSongSubmit)
		r.Post("/songs/{songSlug}/delete", a.HandleDeleteSongSubmit)
	})

	r.Route("/platform/admin", func(r chi.Router) {
		r.Use(limitAdminRequestBody)
		r.Get("/", platform.HandleHome)
		r.Post("/logout", platform.HandleLogoutSubmit)
		r.Get("/login", platform.HandleLoginPage)
		r.Post("/login", platform.HandleLoginSubmit)
		r.Get("/users", platform.HandleUsers)
		// Keep the old submit endpoint compatible with existing clients; the form
		// itself now lives on the invitations page.
		r.Post("/users", platform.HandleCreateInvitationSubmit)
		r.Get("/invitations", platform.HandleInvitations)
		r.Post("/invitations", platform.HandleCreateInvitationSubmit)
		r.Post("/invitations/{invitationID}/revoke", platform.HandleRevokeInvitationSubmit)
		r.Get("/artists", platform.HandleArtists)
		r.Get("/artists/slug/{mode}", platform.HandleArtistSlugAvailability)
		r.Post("/artists", platform.HandleCreateArtistSubmit)
	})

	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		http.NotFound(w, req)
	})

	return r
}

func limitAdminRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxAdminRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := w.Header()
		headers.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; connect-src 'self'")
		headers.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		headers.Set("X-Content-Type-Options", "nosniff")
		headers.Set("X-Frame-Options", "DENY")
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			headers.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
