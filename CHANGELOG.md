# Changelog

All notable changes to the `capydb` CLI are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[SemVer](https://semver.org/).

Releases are cut with GoReleaser from a git tag; entries under **Unreleased** ship with the next tag.

## [Unreleased]

## [1.0.0] - 2026-08-31

### Added

- `capydb db sql --allow-unqualified-writes`. The control plane now refuses an `UPDATE` or
  `DELETE` with no `WHERE` and any `TRUNCATE`; the flag opts out. Guarded by default here rather
  than opted out like the dashboard console, because the CLI is as likely to be inside a script as
  under a person, and a script is exactly the caller that should have to say it meant it.

- `capydb export`: queue a logical export (`pg_dump` custom-format archive) of the project
  database, wait for the job, and download the artifact with TTY-aware progress - plus
  `capydb export list` and `capydb export download --export <id>` to re-download within the 7-day
  window. Downloads refuse to overwrite an existing file. (Needs capydbclient v1.7.0.)
- `capydb logs --hours` now accepts up to 720 (30 days); the control plane serves windows older
  than 7 days from the platform log archive where it is configured, and rejects windows beyond the
  deployment's cap.
- `capydb psql` prints a one-line notice on stderr (`Resuming your database (usually under a
  second)...`) when connecting to a paused project database, so the scale-to-zero wake pause is
  explained instead of looking like a hang.
- `capydb restore --wait` ends a successful restore with a plain-language outcome: what the target
  (live database or preview) now contains and the command to verify it or fetch its connection
  string, instead of only the bare job block.
- `capydb import --wait` ends a successful import by stating that the project's live database now
  contains the imported data, with the verify command.
- Cloudflare integration support: `capydb integrations env --target wrangler` prints the
  `wrangler.jsonc` Hyperdrive binding fragment for a linked project, alongside the existing Vercel
  and Netlify payloads.
- `capydb psql` resolves the CapyDB root certificate for `sslmode=verify-full` connection strings,
  so `psql` no longer fails against a verified-TLS connection string.

### Changed

- The API types the CLI used to declare itself are now aliases of the shared `capydbclient` module,
  the single Go mirror of the OpenAPI component schemas. The CLI keeps only three local shapes
  (`Client`, `PreviewDetails`, `ProjectLogsQuery`); everything else comes from one definition shared
  with the Terraform provider. Tracks `capydbclient` v1.6.0.
- Go directive raised to 1.27.0.

### Fixed

- The Go module path is now `github.com/capy-base/capydb-cli`, matching the repository, so
  `go install github.com/capy-base/capydb-cli/cmd/capydb@latest` resolves. The old path
  (`github.com/capy-base/capydb/cli`) named a repository that does not exist and never installed.
- `go.sum` was missing the module hash for `capydbclient` v1.7.0, so a clean checkout could not
  build the CLI.
- The dump-upload progress line (`capydb import --file`) is now TTY-aware: piped/CI runs get plain
  progress lines at most every 5 seconds and a final `Upload complete` line, instead of one
  carriage-return-garbled line in the log.
- `capydb backups list` can now report backup verification (`verified_at`, `verification_error`) —
  the CLI's local `Backup` type had been missing both fields.
- `capydb alerts` and `capydb sql` rendered `observed_value`, `limit_value` and `duration_ms` as
  decimals; the API sends integers. Values now render exactly as the API reports them.
- `capydb import preflight` surfaces the source's event triggers, which the local preflight type had
  been dropping.

## [2026-08-18]

### Fixed

- `capydb psql` builds a connection URL that carries the SSL root certificate, fixing connections to
  cells issued `sslmode=verify-full` strings.

## [2026-08-12]

### Added

- `capydb doctor` config-lint rules for `uuidv7()` defaults and missing `NOT NULL` constraints.

## [2026-08-11]

### Changed

- Tracks `capydbclient` v1.5.0 and `capyrls` v1.0.1.

## [2026-08-05]

### Added

- `capydb advisor` — index suggestions derived from the project's real query predicates, costed as
  hypothetical indexes so nothing is written to the database.
- `capydb migrate rls` — converts Supabase row-level-security policies to vanilla Postgres, backed
  by the `capyrls` engine, plus a matching `configlint` rule.

## [2026-07-29]

### Added

- `capydb upgrade major` with confirm and rollback, and webhook test-delivery support.

## [2026-07-24]

### Added

- `capydb doctor` and the `configlint` package: static inspection of a repo's database
  configuration, including drizzle/prisma migration-state checks that catch the `db:push` then
  `db:migrate` trap.
- `capydb upgrade minor` and `capydb extensions update` for managing Postgres versions and extension
  versions.

## [2026-07-23]

### Added

- Env-shadowing detection across `.env*` files — the same key pointing at different databases is
  reported by `migrate scan`, `link`, `create` and `doctor`.
- `drizzle-kit` table filter that excludes `pg_stat_statements` from push/pull, preventing a
  `DROP VIEW` against cells that still carry the views in `public`.

## [2026-07-22]

### Added

- Detection and warnings for pooler startup parameters that the pooled `:6432` endpoint rejects.
- Documented exit codes (0 success, 2 usage, 3 auth, 4 not found, 5 conflict, 6 timeout) and
  `capydb migrate scan` for planning a move from another provider.

## [2026-07-16]

### Added

- `capydb generate` (TypeScript, Zod, Drizzle types rendered server-side from the live schema),
  `capydb schema dump|diff`, `capydb init drizzle`, and `capydb migrate codemod neon`.

## [2026-06-03]

### Added

- First release: project linking, `env pull`, preview databases, imports, logs, and studio.
