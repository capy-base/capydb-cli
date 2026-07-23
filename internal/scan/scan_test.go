package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanSupabaseClerkDataLayer(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env.local", `
NEXT_PUBLIC_SUPABASE_URL=https://abcdefghijklm.supabase.co
DATABASE_URL=postgres://postgres.abcdefghijklm:secret@aws-0-eu-north-1.pooler.supabase.com:6543/postgres
`)
	writeFile(t, root, "package.json", `{
  "dependencies": {
    "@clerk/nextjs": "canary",
    "@supabase/supabase-js": "^2.0.0",
    "react": "^19.0.0"
  }
}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	writeFile(t, root, "src/data.ts", `
const rows = await supabase.from('users').select()
await supabase.from('posts').insert({})
await supabase.from('posts').update({})
await supabase.from('posts').delete()
await supabase.from('comments').select()
await supabase.from('likes').select()
await supabaseAdmin.from('audit').select()
`)
	writeFile(t, root, "supabase/migrations/0001_init.sql", "CREATE TABLE users ();")

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Databases) != 1 {
		t.Fatalf("expected 1 database, got %d: %+v", len(report.Databases), report.Databases)
	}
	database := report.Databases[0]
	if database.Provider != "supabase" {
		t.Errorf("provider = %q, want supabase", database.Provider)
	}
	if !database.Pooled {
		t.Error("expected the :6543 pooler endpoint to be flagged pooled")
	}
	if !has(report.Repo.AuthSystems, "clerk") {
		t.Errorf("auth systems = %v, want clerk", report.Repo.AuthSystems)
	}
	if report.Repo.CallSites.SupabaseData < 6 {
		t.Errorf("supabase data call sites = %d, want >= 6", report.Repo.CallSites.SupabaseData)
	}
	if len(report.Repo.DistTagPins) != 1 || report.Repo.DistTagPins[0] != "@clerk/nextjs@canary" {
		t.Errorf("dist tag pins = %v, want [@clerk/nextjs@canary]", report.Repo.DistTagPins)
	}
	if report.Repo.SupabaseAssets.MigrationFiles != 1 {
		t.Errorf("migration files = %d, want 1", report.Repo.SupabaseAssets.MigrationFiles)
	}
	if report.Scenario.Effort != "M" {
		t.Errorf("effort = %q, want M (7 data call sites)", report.Scenario.Effort)
	}
	if report.Scenario.Name != "supabase + clerk + supabase-js data layer (rewrite, then import)" {
		t.Errorf("unexpected scenario: %q", report.Scenario.Name)
	}
}

func TestScanNeonDriver(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env", `DATABASE_URL='postgresql://user:pw@ep-some-thing-123-pooler.eu-west-2.aws.neon.tech/neondb?sslmode=require&channel_binding=require'`)
	writeFile(t, root, "package.json", `{"dependencies": {"@neondatabase/serverless": "^1.1.0", "drizzle-orm": "rc"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	writeFile(t, root, "src/db/index.ts", `
import { Pool } from '@neondatabase/serverless'
import { drizzle } from 'drizzle-orm/neon-serverless'
const result = await db.batch([a, b])
`)

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Databases) != 1 || report.Databases[0].Provider != "neon" {
		t.Fatalf("expected one neon database, got %+v", report.Databases)
	}
	if !report.Databases[0].Pooled {
		t.Error("expected -pooler neon endpoint to be flagged pooled")
	}
	if len(report.Repo.CallSites.NeonDriverFiles) != 1 {
		t.Errorf("neon driver files = %v, want one", report.Repo.CallSites.NeonDriverFiles)
	}
	if report.Repo.CallSites.NeonBatchCalls != 1 {
		t.Errorf("neon batch calls = %d, want 1", report.Repo.CallSites.NeonBatchCalls)
	}
	if report.Scenario.Effort != "S" {
		t.Errorf("effort = %q, want S", report.Scenario.Effort)
	}
}

func TestScanConsumersGate(t *testing.T) {
	portfolio := t.TempDir()
	appRoot := filepath.Join(portfolio, "app-a")
	writeFile(t, appRoot, ".env", `NEXT_PUBLIC_SUPABASE_URL=https://sharedrefabc.supabase.co`)
	writeFile(t, portfolio, "app-b/.env.production", `SUPABASE_URL=https://sharedrefabc.supabase.co`)
	writeFile(t, portfolio, "app-c/.env", `DATABASE_URL=postgres://u:p@somewhere-else.example.com/db`)

	report, err := Run(appRoot, portfolio)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Databases) != 1 {
		t.Fatalf("expected 1 database, got %+v", report.Databases)
	}
	consumers := report.Databases[0].Consumers
	if len(consumers) != 1 || consumers[0] != "app-b" {
		t.Errorf("consumers = %v, want [app-b]", consumers)
	}
}

// TestScanEnvShadowing reproduces the press-hub failure (2026-07-23): the CLI
// wrote CapyDB's URLs to .env.local while .env kept the old local database, and
// drizzle.config.ts + the import script both pin .env - so schema and ingest
// work silently targeted the old database while the app used the new one.
func TestScanEnvShadowing(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env", "DATABASE_URL='postgres://postgres:dev@localhost:55432/presshub'\n")
	writeFile(t, root, ".env.local", `DATABASE_URL="postgres://u:p@press-hub-da3684.db.capydb.dev:6432/db?sslmode=require"`+"\n")
	// Placeholders must never count as a competing definition.
	writeFile(t, root, ".env.example", "DATABASE_URL='postgres://USER:PASSWORD@SLUG.db.capydb.dev:6432/DB?sslmode=require'\n")
	writeFile(t, root, "drizzle.config.ts", "import { config } from \"dotenv\";\nconfig({ path: \".env\" });\n")
	writeFile(t, root, "package.json", `{"dependencies":{"drizzle-orm":"1.0.0","postgres":"3.4.9"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(report.EnvConflicts) != 1 {
		t.Fatalf("expected 1 env conflict, got %d: %+v", len(report.EnvConflicts), report.EnvConflicts)
	}
	conflict := report.EnvConflicts[0]
	if conflict.Key != "DATABASE_URL" {
		t.Errorf("conflict key = %q, want DATABASE_URL", conflict.Key)
	}
	// .env and .env.local only - .env.example is excluded.
	if len(conflict.Assignments) != 2 {
		t.Fatalf("expected 2 assignments, got %+v", conflict.Assignments)
	}
	for _, assignment := range conflict.Assignments {
		if assignment.File == ".env.example" {
			t.Error("placeholder env file must not count as a conflicting definition")
		}
	}

	if len(report.Repo.EnvLoaders) != 1 || report.Repo.EnvLoaders[0].PinnedPath != ".env" {
		t.Fatalf("expected drizzle.config.ts to be recorded as pinning .env, got %+v", report.Repo.EnvLoaders)
	}

	var shadowWarning string
	for _, warning := range report.Scenario.Warnings {
		if len(warning) > 14 && warning[:14] == "ENV SHADOWING:" {
			shadowWarning = warning
		}
	}
	if shadowWarning == "" {
		t.Fatalf("expected an ENV SHADOWING warning, got %+v", report.Scenario.Warnings)
	}
	// The warning must name the loader that gets misrouted, not just the keys.
	if !contains(shadowWarning, "drizzle.config.ts") {
		t.Errorf("warning should name the path-pinned loader, got %q", shadowWarning)
	}
}

// TestScanNoEnvShadowingWhenOneFileOwnsCredentials is the fixed press-hub shape:
// one file owns the database vars, and loaders may pin several files safely.
func TestScanNoEnvShadowingWhenOneFileOwnsCredentials(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env", "NEXT_PUBLIC_APP_URL=http://localhost:3001\n")
	writeFile(t, root, ".env.local", `DATABASE_URL="postgres://u:p@press-hub-da3684.db.capydb.dev:6432/db?sslmode=require"`+"\n")
	writeFile(t, root, "drizzle.config.ts", "config({ path: \".env.local\" });\nconfig({ path: \".env\" });\n")
	writeFile(t, root, "package.json", `{"dependencies":{"postgres":"3.4.9"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.EnvConflicts) != 0 {
		t.Fatalf("expected no conflicts, got %+v", report.EnvConflicts)
	}
}

// TestDetectEnvConflictsStandalone covers the cheap path used by link/create
// and doctor, which never parses source files.
func TestDetectEnvConflictsStandalone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env", "DATABASE_URL=postgres://u:p@old.neon.tech:5432/db\n")
	writeFile(t, root, ".env.production", "DATABASE_URL=postgres://u:p@new-a1b2c3.db.capydb.dev:6432/db\n")

	conflicts, err := DetectEnvConflicts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %+v", conflicts)
	}
	described := conflicts[0].Describe()
	for _, want := range []string{"DATABASE_URL", ".env", ".env.production", "old.neon.tech"} {
		if !contains(described, want) {
			t.Errorf("Describe() = %q, missing %q", described, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && strings.Contains(haystack, needle)
}
