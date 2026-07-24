package configlint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectLocalMigrations(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"drizzle/0000_init", "drizzle/0001_add_contacts", "drizzle/meta"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := DetectLocalMigrations(root)
	if got.Tool != "drizzle" {
		t.Fatalf("tool = %q, want drizzle", got.Tool)
	}
	// `meta` is drizzle's bookkeeping folder, not a migration.
	if got.Count != 2 {
		t.Fatalf("count = %d, want 2 (meta must not count)", got.Count)
	}
}

func TestDetectLocalMigrationsNone(t *testing.T) {
	if got := DetectLocalMigrations(t.TempDir()); got.Tool != "" {
		t.Fatalf("expected no migrations, got %+v", got)
	}
}

func TestEvaluateMigrationState(t *testing.T) {
	local := LocalMigrationState{Tool: "drizzle", Count: 3, Dir: "drizzle"}

	// The investobase case: tables exist, nothing recorded as applied.
	if f := EvaluateMigrationState(local, 0, 12); f == nil {
		t.Fatal("expected a finding when the database has tables but no applied migrations")
	} else if f.Rule != "migration_history_not_baselined" || f.Severity != SeverityError {
		t.Fatalf("unexpected finding: %+v", f)
	} else if !strings.Contains(f.Message, "12") || !strings.Contains(f.Message, "3") {
		t.Fatalf("message should cite both counts: %q", f.Message)
	}

	// A brand-new empty database with nothing applied is NOT a problem - it just
	// has not been migrated yet. This is the false positive that would make the
	// check untrustworthy.
	if f := EvaluateMigrationState(local, 0, 0); f != nil {
		t.Fatalf("empty database must not be flagged: %+v", f)
	}

	// Partially applied (pending migrations) is normal work, not divergence.
	if f := EvaluateMigrationState(local, 1, 12); f != nil {
		t.Fatalf("pending migrations must not be flagged: %+v", f)
	}

	// Fully applied is clean.
	if f := EvaluateMigrationState(local, 3, 12); f != nil {
		t.Fatalf("fully migrated must not be flagged: %+v", f)
	}

	// No local migrations: nothing to compare against.
	if f := EvaluateMigrationState(LocalMigrationState{}, 0, 12); f != nil {
		t.Fatalf("no local migrations must not be flagged: %+v", f)
	}
}

func TestMigrationStateQuery(t *testing.T) {
	for _, tool := range []string{"drizzle", "prisma"} {
		q := MigrationStateQuery(tool)
		if !strings.Contains(q, "applied") || !strings.Contains(q, "user_tables") {
			t.Fatalf("%s query must select applied + user_tables: %q", tool, q)
		}
	}
	if MigrationStateQuery("unknown") != "" {
		t.Fatal("unknown tool must yield no query")
	}
}
