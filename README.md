<p align="center">
  <img src="web/static/songdock_logo_dark.v2.png" alt="SongDock" width="520">
</p>

<p align="center">Self-hosted song landing pages for independent musicians and small labels.</p>

<p align="center">
  <a href="https://github.com/jbrixon/songdock/actions/workflows/ci.yml"><img src="https://github.com/jbrixon/songdock/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

# SongDock

Create clean, shareable pages for your releases with links to Spotify,
Apple Music, YouTube, and more — without handing your audience to an
advertising platform.

## Why SongDock?

Most song-link services are hosted platforms that add branding, analytics
scripts, subscriptions, or feature restrictions.

SongDock gives independent artists and small labels a simple alternative:

- self-host it
- own the data
- use all available features
- customise the deployment
- avoid per-release fees

## Features

- Public, mobile-friendly release pages at `/s/{artist}/{song}`.
- Upload a unique cover artwork for each release.
- Links for Spotify, Apple Music, YouTube, and other safe external URLs.
- Multiple artist workspaces managed from one installation.
- Platform administration for creating artists and inviting artist administrators.
- Invite-only artist-admin registration and session-based authentication.
- Song creation, editing, and deletion for assigned artists.
- Optional per-artist Meta Pixel tracking for public song-page views.
- Small, self-hosted SQLite database by default, with optional PostgreSQL support.
- Docker image published to GitHub Container Registry.
- Reverse-proxy friendly: bring your own domain, TLS, backups, and infrastructure.

## Quickstart

### Requirements

- Docker.

### Run the container

Create a directory for the deployment and an environment file:

```sh
mkdir songdock
cd songdock
```

Create `.env` with the following values, replacing the example credentials:

```dotenv
ADMIN_BACKEND_SECRET=replace-with-a-random-secret
PLATFORM_ADMIN_USERNAME=platform-root
PLATFORM_ADMIN_PASSWORD=replace-with-a-strong-password
DB_PATH=/data/songs.db
ARTWORK_STORAGE_DRIVER=filesystem
ARTWORK_DIR=/data/uploads/artwork
```

Generate a strong value for `ADMIN_BACKEND_SECRET` with:

```sh
openssl rand -hex 32
```

Create a named volume for SongDock's SQLite database:

```sh
docker volume create songdock_data
```

Run the latest SongDock image:

```sh
docker run -d \
  --name songdock \
  --restart unless-stopped \
  --env-file .env \
  -e DB_PATH=/data/songs.db \
  -p 8080:8080 \
  -v songdock_data:/data \
  ghcr.io/jbrixon/songdock:latest
```

SongDock is now available at [http://localhost:8080](http://localhost:8080).
To create the first artist and invite an artist administrator, continue with
the [first-run setup](#first-run-setup) below.

To expose SongDock on another host port, change the first port in `-p`, for
example `-p 3000:8080`.

### Run migrations separately

The server command is `songdock serve`. Database migrations can be run without
starting the server:

```sh
docker run --rm \
  --env DB_PATH=/data/songs.db \
  -v songdock_data:/data \
  ghcr.io/jbrixon/songdock:latest migrate up
```

For multiple SongDock instances sharing one database, run `migrate up` once
before starting the instances and set `SONGDOCK_AUTO_MIGRATE=false` on each
server. Run migrations as a single one-off process during upgrades. This
process is the same for SQLite and PostgreSQL.

For PostgreSQL, configure the URL on the migration command itself:

```sh
POSTGRES_URL='postgres://user:password@db.example.com:5432/songdock?sslmode=require' \
  songdock migrate up
```

## First-run setup

SongDock separates platform administration from artist administration. The platform administrator creates artist workspaces and invites the people who manage them.

1. Open [http://localhost:8080/platform/admin/login](http://localhost:8080/platform/admin/login).
2. Sign in with `PLATFORM_ADMIN_USERNAME` and `PLATFORM_ADMIN_PASSWORD` from `.env`.
3. Open **Manage artists** and create the first artist. The artist name is used to suggest its URL slug.
4. Open **Manage invitations**, enter the artist administrator's email address, select the artist, and create the invitation.
5. Copy the generated invitation code and give it to the invited administrator through a secure channel.
6. The invited administrator opens [http://localhost:8080/admin/register](http://localhost:8080/admin/register), enters the invitation code, and chooses a password.
7. The new artist administrator signs in at `/admin/login`, creates a song, uploads its cover artwork, and adds its streaming links.
8. To enable Meta Pixel tracking, open **Artist settings**, enter the numeric Pixel ID, and save it. Clear the ID to disable tracking.
9. Share the resulting public URL, for example `/s/example-artist/my-first-release`.

Platform administrators can manage artists, users, and invitations. Artist administrators can manage songs for their assigned artists, but cannot create additional artists or platform users.

## Configuration

SongDock reads environment variables from `.env` when started through `mise`. In a container, provide the same variables with `env_file`, `docker run --env-file`, or your deployment platform's secret manager.

Database storage and artwork storage are configured independently. SQLite is
the default backend: `DB_PATH` selects its database file and defaults to
`songs.db`. Set `POSTGRES_URL` to select PostgreSQL; it takes precedence over
`DB_PATH` and accepts complete PostgreSQL URLs, including TLS parameters such
as `sslmode=require`. SongDock uses the configured PostgreSQL database as-is;
it does not copy an existing SQLite database into PostgreSQL.

`SONGDOCK_AUTO_MIGRATE` controls automatic migrations for both backends. It is
convenient for a simple single-instance deployment. For multiple SongDock
instances sharing one database, run `songdock migrate up` exactly once before
starting the instances and set `SONGDOCK_AUTO_MIGRATE=false` on every server.
Run migrations as one one-off process during upgrades; do not have every
server replica run migrations.

Artwork uses the filesystem by default and stores files in `ARTWORK_DIR`, which
defaults to `/data/uploads/artwork` for the server. `ARTWORK_DIR` is ignored
when the S3 driver is selected.

For local filesystem artwork storage:

```dotenv
ARTWORK_STORAGE_DRIVER=filesystem
ARTWORK_DIR=/data/uploads/artwork
```

For S3-compatible artwork storage:

```dotenv
ARTWORK_STORAGE_DRIVER=s3
S3_ENDPOINT=https://example-object-storage.invalid
S3_REGION=eu-central-1
S3_BUCKET=songdock-artwork
S3_ACCESS_KEY_ID=replace-me
S3_SECRET_ACCESS_KEY=replace-me
S3_PREFIX=artwork
S3_PUBLIC_URL=https://assets.example.com
S3_FORCE_PATH_STYLE=false
```

`S3_ENDPOINT` may point to AWS S3 or a compatible provider such as MinIO,
Cloudflare R2, Backblaze B2, or Hetzner Object Storage. If `S3_PUBLIC_URL` is
empty, SongDock serves private objects through the application instead of
assuming the bucket is publicly readable.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ADMIN_BACKEND_SECRET` | Yes | — | At least 32 characters. Used to hash artist-admin passwords and sign admin sessions. |
| `PLATFORM_ADMIN_USERNAME` | Yes | — | Initial platform administrator username. |
| `PLATFORM_ADMIN_PASSWORD` | Yes | — | Initial platform administrator password; at least 16 characters. |
| `DB_PATH` | No | `songs.db` | Path to the SQLite database file. |
| `POSTGRES_URL` | No | — | Complete PostgreSQL connection URL. When non-empty, PostgreSQL takes precedence over `DB_PATH`; URL parameters control TLS, such as `sslmode=require`. |
| `ARTWORK_STORAGE_DRIVER` | No | `filesystem` | Artwork storage driver. Supported values are `filesystem` and `s3`; it does not affect SQLite. |
| `ARTWORK_DIR` | No | `/data/uploads/artwork` | Filesystem artwork directory. Ignored when `ARTWORK_STORAGE_DRIVER=s3`. |
| `S3_ENDPOINT` | S3 only | — | Optional custom S3-compatible endpoint. |
| `S3_REGION` | S3 only | `us-east-1` | S3 signing region. |
| `S3_BUCKET` | S3 only | — | Required S3 bucket name. |
| `S3_ACCESS_KEY_ID` | S3 only | — | Required S3 access key. |
| `S3_SECRET_ACCESS_KEY` | S3 only | — | Required S3 secret key. |
| `S3_PREFIX` | S3 only | — | Optional prefix for artwork object keys. |
| `S3_PUBLIC_URL` | S3 only | — | Optional public base URL for artwork. Empty values use application serving. |
| `S3_FORCE_PATH_STYLE` | S3 only | `false` | Use path-style S3 addressing, useful for some compatible providers. |
| `PORT` | No | `8080` | HTTP port inside the process. |
| `SONGDOCK_AUTO_MIGRATE` | No | `true` | Whether `serve` runs database migrations during startup. Set to `false` when migrations run separately. |
| `ACCEPTANCE_BASE_URL` | Tests only | `http://localhost:8080` | Base URL used by acceptance tests. |

Keep `ADMIN_BACKEND_SECRET` stable after creating users. Changing it invalidates existing admin sessions and prevents existing password hashes from being verified correctly.

## Docker deployment with Caddy

The release workflow publishes images such as `ghcr.io/jbrixon/songdock:v1.2.3` and `ghcr.io/jbrixon/songdock:latest`. The container listens on port `8080` and uses SQLite at `DB_PATH` unless `POSTGRES_URL` is configured.

Create a deployment directory containing `.env`, `compose.yaml`, and `Caddyfile`:

`.env`:

```dotenv
ADMIN_BACKEND_SECRET=replace-with-a-random-secret-at-least-32-characters
PLATFORM_ADMIN_USERNAME=platform-root
PLATFORM_ADMIN_PASSWORD=replace-with-a-strong-password
DB_PATH=/data/songs.db
ARTWORK_STORAGE_DRIVER=filesystem
ARTWORK_DIR=/data/uploads/artwork
```

`compose.yaml`:

```yaml
services:
  songdock:
    image: ghcr.io/jbrixon/songdock:latest
    restart: unless-stopped
    env_file: .env
    environment:
      DB_PATH: /data/songs.db
      ARTWORK_STORAGE_DRIVER: filesystem
      ARTWORK_DIR: /data/uploads/artwork
      PORT: 8080
    expose:
      - "8080"
    volumes:
      - songdock_data:/data

  caddy:
    image: caddy:2-alpine
    restart: unless-stopped
    depends_on:
      - songdock
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config

volumes:
  songdock_data:
  caddy_data:
  caddy_config:
```

To use S3-compatible artwork storage instead, replace the artwork settings in
`.env` or `compose.yaml` with the following values. The SQLite database remains
on the `/data` volume and is unaffected:

```dotenv
ARTWORK_STORAGE_DRIVER=s3
S3_ENDPOINT=https://example-object-storage.invalid
S3_REGION=eu-central-1
S3_BUCKET=songdock-artwork
S3_ACCESS_KEY_ID=replace-me
S3_SECRET_ACCESS_KEY=replace-me
S3_PREFIX=artwork
S3_PUBLIC_URL=https://assets.example.com
S3_FORCE_PATH_STYLE=false
```

`Caddyfile`:

```caddyfile
music.example.com {
    reverse_proxy songdock:8080
}
```

Replace `music.example.com` with a DNS name pointing to the server, then start the stack:

```sh
docker compose up -d
```

Caddy provisions HTTPS automatically when the domain resolves to the server and ports 80/443 are reachable. The `songdock_data` volume is the important part for application data: it persists `songs.db` across container upgrades and restarts. Back it up regularly, and keep the environment secrets backed up separately.

For a pinned deployment, replace `latest` with a release tag such as `v1.2.3`.

## Development

The repository uses `mise` tasks for repeatable commands:

```sh
mise run dev         # start the development server
mise run build       # build ./songdock
mise run test        # run unit tests
mise run acceptance  # run acceptance tests against a live server
```

Before opening a pull request, also check formatting and static analysis:

```sh
test -z "$(gofmt -l .)"
go vet ./...
```

Acceptance tests require a running server and the following environment variables:

```sh
ADMIN_BACKEND_SECRET=... \
PLATFORM_ADMIN_USERNAME=... \
PLATFORM_ADMIN_PASSWORD=... \
mise run acceptance
```

Set `ACCEPTANCE_BASE_URL` and `DB_PATH` when testing a server outside the default local setup.

## Contributing

Contributions, bug reports, and documentation improvements are welcome.

1. Fork the repository and create a focused branch from `main`.
2. Make the smallest change that solves the problem and add or update tests where appropriate.
3. Run formatting, vet, unit tests, and any relevant acceptance tests locally.
4. Update the README or other documentation when behavior or configuration changes.
5. Open a pull request with a clear description of the problem, the change, and how it was tested.

Please avoid including real credentials, production databases, or generated local files in commits. Keep pull requests focused so they are easy to review.

GitHub Actions checks formatting, vet, unit tests, and acceptance tests for pushes to `main` and pull requests. Each successful push to `main` also builds and tests the release image and stores the binary artifact. To release, update the semantic version in [`VERSION`](VERSION) and merge it to `main`. After the `CI` workflow succeeds, if `v<VERSION>` has not been released yet, Actions creates the tag and GitHub release and publishes the matching image to GHCR. Reusing an already released version is skipped.

## Project status

SongDock is in early development. The core release-page and administration workflows are usable, but the application does not yet promise a stable 1.0 API or database schema. Back up the database before upgrades.

## License

SongDock is licensed under the [MIT License](LICENSE). Copyright © 2026 jbrixon.
