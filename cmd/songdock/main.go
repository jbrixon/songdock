package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jbrixon/songdock/internal/store"
)

const minPlatformAdminPasswordLength = 16

func main() {
	dbPath := "songs.db"
	if p := os.Getenv("DB_PATH"); p != "" {
		dbPath = p
	}
	adminSecret := strings.TrimSpace(os.Getenv("ADMIN_BACKEND_SECRET"))
	if adminSecret == "" {
		log.Fatal("ADMIN_BACKEND_SECRET must be set")
	}
	if len(adminSecret) < 32 {
		log.Fatal("ADMIN_BACKEND_SECRET must be at least 32 characters")
	}
	platformAdminUsername := strings.TrimSpace(os.Getenv("PLATFORM_ADMIN_USERNAME"))
	if platformAdminUsername == "" {
		log.Fatal("PLATFORM_ADMIN_USERNAME must be set")
	}
	platformAdminPassword := os.Getenv("PLATFORM_ADMIN_PASSWORD")
	if platformAdminPassword == "" {
		log.Fatal("PLATFORM_ADMIN_PASSWORD must be set")
	}
	if len(platformAdminPassword) < minPlatformAdminPasswordLength {
		log.Fatalf("PLATFORM_ADMIN_PASSWORD must be at least %d characters", minPlatformAdminPasswordLength)
	}

	repo, err := store.NewSQLiteSongRepository(dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer repo.Close()

	r := newRouter(repo, repo, repo, []byte(adminSecret), platformAdminUsername, platformAdminPassword)

	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	log.Printf("server listening on %s", addr)
	server := newHTTPServer(addr, r)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serveHTTPServer(ctx, server, server.ListenAndServe); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func serveHTTPServer(ctx context.Context, server *http.Server, listen func() error) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- listen()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		shutdownErr := server.Shutdown(shutdownCtx)
		err := <-errCh
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		return err
	}
}
