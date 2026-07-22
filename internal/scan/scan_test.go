package scan

import (
	"os"
	"path/filepath"
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
