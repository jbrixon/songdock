# Songdock

A minimal Go server that serves a single-page song/release landing page with links to streaming platforms.

## Running

This repo uses `mise` to load development environment variables from a local `.env` file.

1. Install and activate `mise` in your shell.
2. Create your local env file:

```sh
cp .env.example .env
```

3. Edit `.env` and set the required secrets.
4. Start the server through `mise`:

```sh
mise exec -- go run ./cmd/songdock # listens on :8080

PORT=3000 mise exec -- go run ./cmd/songdock # custom port for this invocation
```

`ADMIN_BACKEND_SECRET` is required, must be at least 32 characters, and is used to hash submitted artist-admin passwords and sign both admin session cookies.

`PLATFORM_ADMIN_USERNAME` and `PLATFORM_ADMIN_PASSWORD` are required. The platform password must be at least 16 characters. They authenticate the platform superuser login at `/platform/admin/login`; user management is available at `/platform/admin/users`, invitations at `/platform/admin/invitations`, and artist management at `/platform/admin/artists`.

## Self-hosted Bootstrap

For a new self-hosted installation:

1. Set `ADMIN_BACKEND_SECRET`, `PLATFORM_ADMIN_USERNAME`, and `PLATFORM_ADMIN_PASSWORD`.
2. Start the server and log in at `/platform/admin/login` with the platform admin credentials.
3. Create the first artist at `/platform/admin/artists`.
4. Create an invitation for the first artist administrator at `/platform/admin/invitations` and select that artist.
5. Have that user register through `/admin/register`.

Artist creation is a platform-admin operation. Artist administrators are created by invitation for a selected artist, can manage songs for artists assigned to them, and cannot provision additional artists.

`DB_PATH` is optional and defaults to `songs.db`. `PORT` is optional and defaults to `8080`.

## Building

```sh
mise exec -- go build -o songdock ./cmd/songdock
```

## Tests

Run unit tests:

```sh
mise exec -- go test ./...
```

### Acceptance tests

Acceptance tests run against a live server. Start the server first, then:

```sh
# against the default localhost:8080
mise exec -- go test -tags acceptance ./test/acceptance/...

# against a remote host
ACCEPTANCE_BASE_URL=https://example.com mise exec -- go test -tags acceptance ./test/acceptance/...
```

`ACCEPTANCE_BASE_URL` controls the target; it defaults to `http://localhost:8080`.
