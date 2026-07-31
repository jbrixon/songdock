<p align="center">
  <img src="web/static/songdock_logo_dark.png" alt="SongDock" width="520">
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
- Links for Spotify, Apple Music, YouTube, and other safe external URLs.
- Multiple artist workspaces managed from one installation.
- Platform administration for creating artists and inviting artist administrators.
- Invite-only artist-admin registration and session-based authentication.
- Song creation, editing, and deletion for assigned artists.
- Small, self-hosted SQLite database with a single persistent file.
- Docker image published to GitHub Container Registry.
- Reverse-proxy friendly: bring your own domain, TLS, backups, and infrastructure.

## Quickstart

### Requirements

- Go 1.25 or newer for local development.
- [`mise`](https://mise.jdx.dev/) for the repository's development commands.
- Docker and Docker Compose for container deployment.

### Run locally

Clone the repository and create a local environment file:

```sh
git clone https://github.com/jbrixon/songdock.git
cd songdock
cp .env.example .env
```

Edit `.env` and replace the example values. Generate a strong backend secret with:

```sh
openssl rand -hex 32
```

Then start SongDock:

```sh
mise run dev
```

The server listens on [http://localhost:8080](http://localhost:8080). The first-run setup is described below.

To use another port for one invocation:

```sh
PORT=3000 mise run dev
```

## First-run setup

SongDock separates platform administration from artist administration. The platform administrator creates artist workspaces and invites the people who manage them.

1. Open [http://localhost:8080/platform/admin/login](http://localhost:8080/platform/admin/login).
2. Sign in with `PLATFORM_ADMIN_USERNAME` and `PLATFORM_ADMIN_PASSWORD` from `.env`.
3. Open **Manage artists** and create the first artist. The artist name is used to suggest its URL slug.
4. Open **Manage invitations**, enter the artist administrator's email address, select the artist, and create the invitation.
5. Copy the generated invitation code and give it to the invited administrator through a secure channel.
6. The invited administrator opens [http://localhost:8080/admin/register](http://localhost:8080/admin/register), enters the invitation code, and chooses a password.
7. The new artist administrator signs in at `/admin/login`, creates a song, and adds its streaming links.
8. Share the resulting public URL, for example `/s/example-artist/my-first-release`.

Platform administrators can manage artists, users, and invitations. Artist administrators can manage songs for their assigned artists, but cannot create additional artists or platform users.

## Configuration

SongDock reads environment variables from `.env` when started through `mise`. In a container, provide the same variables with `env_file`, `docker run --env-file`, or your deployment platform's secret manager.

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `ADMIN_BACKEND_SECRET` | Yes | — | At least 32 characters. Used to hash artist-admin passwords and sign admin sessions. |
| `PLATFORM_ADMIN_USERNAME` | Yes | — | Initial platform administrator username. |
| `PLATFORM_ADMIN_PASSWORD` | Yes | — | Initial platform administrator password; at least 16 characters. |
| `DB_PATH` | No | `songs.db` | Path to the SQLite database file. |
| `PORT` | No | `8080` | HTTP port inside the process. |
| `ACCEPTANCE_BASE_URL` | Tests only | `http://localhost:8080` | Base URL used by acceptance tests. |

Keep `ADMIN_BACKEND_SECRET` stable after creating users. Changing it invalidates existing admin sessions and prevents existing password hashes from being verified correctly.

## Docker deployment with Caddy

The release workflow publishes images such as `ghcr.io/jbrixon/songdock:v1.2.3` and `ghcr.io/jbrixon/songdock:latest`. The container listens on port `8080` and stores its SQLite database wherever `DB_PATH` points.

Create a deployment directory containing `.env`, `compose.yaml`, and `Caddyfile`:

`.env`:

```dotenv
ADMIN_BACKEND_SECRET=replace-with-a-random-secret-at-least-32-characters
PLATFORM_ADMIN_USERNAME=platform-root
PLATFORM_ADMIN_PASSWORD=replace-with-a-strong-password
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

GitHub Actions checks formatting, vet, unit tests, and acceptance tests for pushes to `main` and pull requests. To release, update the semantic version in [`VERSION`](VERSION) and merge it to `main`. After the `CI` workflow succeeds, if `v<VERSION>` has not been released yet, Actions runs the release acceptance tests, creates the tag and GitHub release, and publishes the matching image to GHCR. Reusing an already released version is skipped.

## Project status

SongDock is in early development. The core release-page and administration workflows are usable, but the application does not yet promise a stable 1.0 API or database schema. Back up the database before upgrades.

## License

SongDock is licensed under the [MIT License](LICENSE). Copyright © 2026 jbrixon.
