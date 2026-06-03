# CapyDB CLI

The CapyDB CLI is a Go binary for linking local projects to existing CapyDB projects, pulling connection strings into env files, and running repeatable project operations.

## Installation

From source:

```bash
git clone https://github.com/capy-base/cli
cd cli
make build
```

With `go install`:

```bash
go install github.com/capy-base/capydb-cli/cmd/capydb-cli@latest
```

Release archives are built for macOS, Linux, and Windows. Tagged releases also publish checksums, SBOMs, and a container image.

## Default flow

1. Sign up in CapyDB.
2. Create or join an organization in the dashboard.
3. Create a project in the dashboard.
4. Install the Go CLI binary.
5. Run `capydb login`.
6. Run `capydb link` inside your local app directory.

The CLI will:

- open a browser login flow,
- use the active dashboard organization,
- detect the local app profile,
- write the right env file,
- save a local `.capydb/project.json` link,
- keep secrets in the app env file instead of the local metadata file.

## Main commands

- `capydb login`
- `capydb logout`
- `capydb whoami`
- `capydb status`
- `capydb link`
- `capydb unlink`
- `capydb env pull`
- `capydb preview list`
- `capydb preview create`
- `capydb preview reset`
- `capydb preview delete`
- `capydb preview extend <preview-id> --ttl-hours N`
- `capydb backups list`
- `capydb backups create`
- `capydb import`
- `capydb restore`
- `capydb jobs get`
- `capydb studio`

Compatibility aliases:

- `capydb auth login`
- `capydb auth logout`
- `capydb auth whoami`
- `capydb connect` -> `capydb link`
- `capydb init` -> `capydb create`

## Linking a local project

```bash
cd /path/to/project
capydb login
capydb link --project my-project
```

`capydb link` accepts:

- project id
- project slug
- project name

If you omit `--project`, the CLI will use the linked project in `.capydb/project.json` when available, or prompt you to choose a project when multiple matches exist in an interactive terminal.

## Local files

Saved globally:

- CLI auth and active organization info go in the user config directory.

Saved locally:

- `.capydb/project.json` stores the linked project id, chosen env file, detected profile, and other non-secret metadata.
- The CLI adds both `.capydb/` and the credential-bearing env file (e.g. `.env.local`) to `.gitignore` so the connection URL with the database password is never committed.

Written into the app:

- `DATABASE_URL`
- `DATABASE_DIRECT_URL`
- `DATABASE_POOL_URL`
- framework-specific aliases such as `DIRECT_URL` for Prisma

## Detected profiles

The CLI currently detects common app/framework and DB-layer combinations, including:

- Next.js
- SvelteKit
- Remix
- Django
- Rails
- Go services
- generic Node apps
- generic Python apps
- Prisma
- Drizzle
- `pg`
- `pgx`
- SQLAlchemy
- Active Record

For monorepos, the CLI can detect nested app directories and prompt for the app to link when there is more than one candidate.

## Notes

- `capydb create` still exists for power users, but it is no longer the default onboarding path.
- `capydb studio` opens the dashboard page for the linked project. If your CLI API URL points directly at the backend instead of the frontend proxy, set `CAPYDB_APP_URL` or pass `--app-url`.

## Development

```bash
cd /cli
make build
./capydb-cli --version
```

Useful targets:

- `make fmt`
- `make lint`
- `make test`
- `make check`
- `make build-all`
- `make release-check`
- `make release-snapshot`

Release automation lives in [`.github/workflows/ci.yml`](.github/workflows/ci.yml) and [`.github/workflows/release.yml`](.github/workflows/release.yml). Pushing a tag that matches `v*.*.*` runs the release workflow.
