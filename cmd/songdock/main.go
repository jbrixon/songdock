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

	"github.com/jbrixon/songdock/internal/artwork"
	artworkfilesystem "github.com/jbrixon/songdock/internal/artwork/filesystem"
	artworks3 "github.com/jbrixon/songdock/internal/artwork/s3"
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
	config := databaseConfig()
	log.Printf("database backend: %s", config.Backend())
	if err := store.MigrateConfiguredDatabase(config); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func serve() error {
	artworkConfig, err := artwork.LoadConfig()
	if err != nil {
		return err
	}
	artworkStore, err := newArtworkStore(context.Background(), artworkConfig)
	if err != nil {
		return fmt.Errorf("configure artwork storage: %w", err)
	}

	shouldMigrate, err := automaticMigrations()
	if err != nil {
		return err
	}

	dbConfig := databaseConfig()
	log.Printf("database backend: %s", dbConfig.Backend())

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

	repo, err := store.OpenConfiguredRepository(dbConfig, shouldMigrate)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer repo.Close()

	r := newRouterWithArtworkStore(repo, repo, repo, []byte(adminSecret), platformAdminUsername, platformAdminPassword, artworkStore)

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

func newArtworkStore(ctx context.Context, cfg artwork.Config) (*artwork.Store, error) {
	switch cfg.Driver {
	case "", "filesystem":
		return artwork.NewStore(artworkfilesystem.New(cfg.Dir)), nil
	case "s3":
		storage, err := artworks3.New(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return artwork.NewStore(storage), nil
	default:
		return nil, fmt.Errorf("unsupported artwork storage driver %q", cfg.Driver)
	}
}

func databasePath() string {
	if path := os.Getenv("DB_PATH"); path != "" {
		return path
	}
	return "songs.db"
}

func databaseConfig() store.DatabaseConfig {
	return store.DatabaseConfig{
		SQLitePath:  databasePath(),
		PostgresURL: strings.TrimSpace(os.Getenv("POSTGRES_URL")),
	}
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
