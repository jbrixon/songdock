package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jbrixon/songdock/internal/store"
)

const minPlatformAdminPasswordLength = 16

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	switch {
	case len(args) == 1 && args[0] == "serve":
		return serve()
	case len(args) == 2 && args[0] == "migrate" && args[1] == "up":
		return migrateUp()
	default:
		return fmt.Errorf("usage: songdock serve | songdock migrate up")
	}
}

func migrateUp() error {
	if err := store.MigrateSQLiteDatabase(databasePath()); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func serve() error {
	shouldMigrate, err := automaticMigrations()
	if err != nil {
		return err
	}
	if shouldMigrate {
		if err := store.MigrateSQLiteDatabase(databasePath()); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	adminSecret := strings.TrimSpace(os.Getenv("ADMIN_BACKEND_SECRET"))
	if adminSecret == "" {
		return errors.New("ADMIN_BACKEND_SECRET must be set")
	}
	if len(adminSecret) < 32 {
		return errors.New("ADMIN_BACKEND_SECRET must be at least 32 characters")
	}
	platformAdminUsername := strings.TrimSpace(os.Getenv("PLATFORM_ADMIN_USERNAME"))
	if platformAdminUsername == "" {
		return errors.New("PLATFORM_ADMIN_USERNAME must be set")
	}
	platformAdminPassword := os.Getenv("PLATFORM_ADMIN_PASSWORD")
	if platformAdminPassword == "" {
		return errors.New("PLATFORM_ADMIN_PASSWORD must be set")
	}
	if len(platformAdminPassword) < minPlatformAdminPasswordLength {
		return fmt.Errorf("PLATFORM_ADMIN_PASSWORD must be at least %d characters", minPlatformAdminPasswordLength)
	}

	repo, err := store.OpenSQLiteSongRepository(databasePath())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer repo.Close()

	artworkDir := "uploads/artwork"
	if configured := os.Getenv("ARTWORK_DIR"); configured != "" {
		artworkDir = configured
	}
	r := newRouterWithArtworkDir(repo, repo, repo, []byte(adminSecret), platformAdminUsername, platformAdminPassword, artworkDir)

	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	log.Printf("server listening on %s", addr)
	server := newHTTPServer(addr, r)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := serveHTTPServer(ctx, server, server.ListenAndServe); err != nil {
		return fmt.Errorf("server failed: %w", err)
	}
	return nil
}

func databasePath() string {
	if path := os.Getenv("DB_PATH"); path != "" {
		return path
	}
	return "songs.db"
}

func automaticMigrations() (bool, error) {
	value := strings.TrimSpace(os.Getenv("SONGDOCK_AUTO_MIGRATE"))
	if value == "" {
		return true, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("SONGDOCK_AUTO_MIGRATE must be a boolean: %w", err)
	}
	return parsed, nil
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
