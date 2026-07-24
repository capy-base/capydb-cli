package configlint

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// LocalMigrationState is what the repository claims about migration history.
type LocalMigrationState struct {
	// Tool is the migration tool detected ("drizzle", "prisma") or "".
	Tool string
	// Count is how many migrations exist on disk.
	Count int
	// Dir is the folder they live in, for the message.
	Dir string
}

// migrationDirs are the conventional folders each tool generates into.
var migrationDirs = map[string]string{
	"drizzle": "drizzle",
	"prisma":  filepath.Join("prisma", "migrations"),
}

// DetectLocalMigrations reports the migrations a repo carries on disk.
//
// This is half of the push/migrate divergence check: the repo says "I have N
// migrations", and only the live database can say whether it has ever applied
// any of them. Neither half is conclusive alone, which is why the useful check
// needs both and lives in `capydb doctor` rather than the offline linter.
func DetectLocalMigrations(root string) LocalMigrationState {
	for tool, rel := range migrationDirs {
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		count := 0
		for _, e := range entries {
			// drizzle: one folder per migration (v1) or .sql files (v0).
			// prisma: one folder per migration.
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "meta" {
				count++
				continue
			}
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
				count++
			}
		}
		if count > 0 {
			return LocalMigrationState{Tool: tool, Count: count, Dir: rel}
		}
	}
	return LocalMigrationState{}
}

// MigrationStateQuery returns the SQL that reports, for the detected tool, how
// many migrations the LIVE database has recorded as applied, and how many user
// tables it has. Empty when there is no local migration history to compare.
//
// Both numbers matter: "0 applied migrations" is only a problem if the database
// actually has tables. A brand-new empty database with 0 applied migrations is
// simply one that has not been migrated yet, which is fine.
func MigrationStateQuery(tool string) string {
	switch tool {
	case "drizzle":
		return `SELECT
			  COALESCE((SELECT count(*) FROM drizzle.__drizzle_migrations), 0) AS applied,
			  (SELECT count(*) FROM pg_tables
			     WHERE schemaname NOT IN ('pg_catalog','information_schema','extensions','capydb','drizzle')) AS user_tables`
	case "prisma":
		return `SELECT
			  COALESCE((SELECT count(*) FROM "_prisma_migrations" WHERE finished_at IS NOT NULL), 0) AS applied,
			  (SELECT count(*) FROM pg_tables
			     WHERE schemaname NOT IN ('pg_catalog','information_schema','extensions','capydb')) AS user_tables`
	}
	return ""
}

// EvaluateMigrationState turns the local + live picture into a finding.
//
// The failure it catches: a database built with `push` (or by hand) has tables
// but no recorded migrations, so the first `migrate` replays from the first
// migration, tries to create objects that already exist, and fails. The database
// is fine - the history just never knew about it - and the fix is to baseline,
// not to drop anything.
func EvaluateMigrationState(local LocalMigrationState, applied, userTables int) *Finding {
	if local.Tool == "" || local.Count == 0 {
		return nil
	}
	// Nothing applied, but the database already has objects: migrate will replay
	// from #1 into a populated database.
	if applied == 0 && userTables > 0 {
		return &Finding{
			Rule: "migration_history_not_baselined", Severity: SeverityError,
			File: local.Dir,
			Message: "the database has " + strconv.Itoa(userTables) + " tables but no recorded migrations, while this repo carries " +
				strconv.Itoa(local.Count) + " - the schema was applied without recording history (typically `push`), so the next migrate replays from the first migration and fails on objects that already exist",
			Fix: "baseline before migrating: mark the existing migrations as already applied so migrate starts from the current schema",
		}
	}
	// Some applied, but fewer than the repo has: normal pending migrations, not
	// a divergence. Deliberately not reported - that is just work to do.
	return nil
}
