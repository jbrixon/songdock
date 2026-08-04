package store

import "strings"

// DatabaseConfig selects the persistence adapter. PostgreSQL takes precedence
// whenever PostgresURL is non-empty.
type DatabaseConfig struct {
	SQLitePath  string
	PostgresURL string
}

// Backend returns the configured database backend without exposing connection
// details.
func (c DatabaseConfig) Backend() string {
	if strings.TrimSpace(c.PostgresURL) != "" {
		return "postgres"
	}
	return "sqlite"
}

// Repository is the application persistence port implemented by each adapter.
// The narrower admin and platformadmin ports are satisfied by this interface.
type Repository interface {
	SongRepository
	Close() error

	FindUserByEmail(email string) (*User, error)
	ListUsers() ([]UserWithArtists, error)
	CreateUser(email, passwordHash string) (int64, error)
	IsUserTokenRevoked(userID int64) (bool, error)
	DeleteUser(userID int64) error

	FindArtistBySlug(slug string) (*Artist, error)
	UpdateArtistMetaPixelID(artistID int64, pixelID string) error
	ListArtists() ([]Artist, error)
	CreateArtist(name, slug string) (*Artist, error)
	DeleteArtist(artistID int64) ([]string, error)

	ListArtistsForUser(userID int64) ([]Artist, error)
	AssignUserToArtist(userID int64, artistSlug string) error
	IsUserAssignedToArtist(userID, artistID int64) (bool, error)

	SongSlugExists(artistID int64, songSlug string) (bool, error)
	ListSongsForArtist(artistID int64) ([]Song, error)
	InsertSongForArtist(artistID int64, song Song) error
	UpdateSongForArtist(artistID int64, songSlug string, song Song) error
	DeleteSongForArtist(artistID int64, songSlug string) error
	Insert(songs ...Song) error

	CreateUserInvitation(email, invitationCodeHash string, artistID int64) error
	FindInvitationByCodeHash(codeHash string) (*UserInvitation, error)
	RedeemInvitation(invitationID int64, email, passwordHash string) (int64, error)
	ListPendingInvitations() ([]UserInvitation, error)
	RevokeInvitation(invitationID int64) error
}

// OpenConfiguredRepository opens the selected adapter and, when requested,
// applies its migrations. With automatic migrations disabled it validates the
// existing schema without modifying it.
func OpenConfiguredRepository(config DatabaseConfig, autoMigrate bool) (Repository, error) {
	if config.Backend() == "postgres" {
		if autoMigrate {
			return NewPostgresSongRepository(strings.TrimSpace(config.PostgresURL))
		}

		repo, err := OpenPostgresSongRepository(strings.TrimSpace(config.PostgresURL))
		if err != nil {
			return nil, err
		}
		if err := validatePostgresSchema(repo.db); err != nil {
			repo.Close()
			return nil, err
		}
		return repo, nil
	}

	path := config.SQLitePath
	if path == "" {
		path = "songs.db"
	}
	if autoMigrate {
		return NewSQLiteSongRepository(path)
	}

	repo, err := OpenSQLiteSongRepository(path)
	if err != nil {
		return nil, err
	}
	if err := validateSQLiteSchema(repo.db); err != nil {
		repo.Close()
		return nil, err
	}
	return repo, nil
}

// MigrateConfiguredDatabase applies migrations to exactly the selected
// backend. It opens no other database.
func MigrateConfiguredDatabase(config DatabaseConfig) error {
	if config.Backend() == "postgres" {
		return MigratePostgresDatabase(strings.TrimSpace(config.PostgresURL))
	}
	path := config.SQLitePath
	if path == "" {
		path = "songs.db"
	}
	return MigrateSQLiteDatabase(path)
}
