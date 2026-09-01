package scan

import (
	"fmt"
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

// TestScanRPCSourceGate reproduces the roomie-radar finding (2026-09-01): the
// app called three DB-resident functions whose CREATE FUNCTION existed in no
// repo - a silent cutover breaker unless the scan names them.
func TestScanRPCSourceGate(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"dependencies":{"@supabase/supabase-js":"^2.0.0"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	writeFile(t, root, "src/api.ts", `
await supabase.rpc('handle_clerk_user', { id })
await supabase.rpc('get_or_create_conversation')
const rows = await supabase.from('profiles').select()
`)
	writeFile(t, root, "supabase/migrations/0001.sql",
		"CREATE OR REPLACE FUNCTION public.get_or_create_conversation() RETURNS uuid AS $$ $$ LANGUAGE sql;")

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Repo.RPCNames) != 2 {
		t.Fatalf("rpc names = %v, want 2", report.Repo.RPCNames)
	}
	if len(report.Repo.RPCsWithoutLocalSource) != 1 || report.Repo.RPCsWithoutLocalSource[0] != "handle_clerk_user" {
		t.Fatalf("rpcs without local source = %v, want [handle_clerk_user]", report.Repo.RPCsWithoutLocalSource)
	}
	var rpcWarning string
	for _, warning := range report.Scenario.Warnings {
		if contains(warning, "handle_clerk_user") {
			rpcWarning = warning
		}
	}
	if rpcWarning == "" || !contains(rpcWarning, "no CREATE FUNCTION") {
		t.Fatalf("expected an RPC-source warning naming handle_clerk_user, got %+v", report.Scenario.Warnings)
	}
}

// TestScanAnonKeyClientClassification: server code on the anon key delegates
// authorization to RLS - the discriminator for the RLS migration path.
func TestScanAnonKeyClientClassification(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"dependencies":{"@supabase/supabase-js":"^2.0.0"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	writeFile(t, root, "src/lib/server.ts", `
import 'server-only'
const client = createClient(process.env.NEXT_PUBLIC_SUPABASE_URL!, process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!)
`)
	writeFile(t, root, "src/lib/admin.ts", `
const admin = createClient(url, process.env.SUPABASE_SERVICE_ROLE_KEY!)
`)

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Repo.CallSites.AnonKeyClientFiles != 1 {
		t.Errorf("anon key client files = %d, want 1", report.Repo.CallSites.AnonKeyClientFiles)
	}
	if report.Repo.CallSites.ServiceRoleKeyFiles != 1 {
		t.Errorf("service role key files = %d, want 1", report.Repo.CallSites.ServiceRoleKeyFiles)
	}
}

// TestAttachSourceRLSPathAndGates drives the live-source scenario logic with
// the measured myroomiev3 shape: many policies resolved via a helper function,
// zero auth users, vestigial extensions, an import in flight, and persisted
// storage URLs.
func TestAttachSourceRLSPathAndGates(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env.local", "NEXT_PUBLIC_SUPABASE_URL=https://abcdefghijklm.supabase.co\n")
	writeFile(t, root, "package.json", `{"dependencies":{"@clerk/nextjs":"^6.0.0","@supabase/supabase-js":"^2.0.0"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	writeFile(t, root, "src/lib/server.ts", `
import 'server-only'
const client = createServerClient(url, process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY!)
const a = await supabase.from('profiles').select()
const b = await supabase.from('messages').select()
const c = await supabase.from('posts').select()
const d = await supabase.from('likes').select()
const e = await supabase.from('props').select()
const f = await supabase.from('bookings').select()
`)

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}
	report.AttachSource(&SourceFacts{
		ServerVersion: "17.6", DatabaseSizeBytes: 60 << 20, PublicTables: 169,
		Policies:  SourcePolicies{Total: 483, DirectAuthRefs: 12, ViaHelpers: 471, HelperNames: []string{"clerk_user_id"}},
		AuthUsers: &SourceAuthUsers{Count: 0},
		Extensions: []SourceExtension{
			{Name: "postgis", Version: "3.6", Dependents: 14, Available: true},
			{Name: "earthdistance", Version: "1.2", Dependents: 0, Available: false},
			{Name: "cube", Version: "1.5", Dependents: 3, Available: false},
		},
		ImportArtifactTables: []string{"wp_migration_map (840 rows)"},
		PersistedURLColumns:  []string{"community_posts.image_url"},
		PublicFunctions:      []string{"handle_clerk_user"},
	})

	var planText string
	for _, step := range report.Scenario.Plan {
		planText += step + "\n"
	}
	if !contains(planText, "KEEP the policies") || !contains(planText, "483 live policies") {
		t.Fatalf("expected a keep-the-policies plan step with the live count, got:\n%s", planText)
	}
	if !contains(planText, "clerk_user_id") {
		t.Errorf("plan step should name the helper function, got:\n%s", planText)
	}

	warnings := strings.Join(report.Scenario.Warnings, "\n")
	for _, want := range []string{
		"zero-user window",
		"earthdistance",
		"cube (3 dependent objects)",
		"wp_migration_map",
		"community_posts.image_url",
		"471 of 483",
	} {
		if !contains(warnings, want) {
			t.Errorf("warnings missing %q:\n%s", want, warnings)
		}
	}
	// earthdistance is vestigial (skip-from-dump), cube is blocking - they
	// must land in different warnings.
	if !contains(warnings, "LIKELY unused") {
		t.Errorf("expected the vestigial-extension warning, got:\n%s", warnings)
	}
}

// TestAttachSourceSmallCorpusRecommendsAppGuards: a small policy set with
// service-role-dominant code style keeps the app-guard recommendation.
func TestAttachSourceSmallCorpusRecommendsAppGuards(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env", "NEXT_PUBLIC_SUPABASE_URL=https://abcdefghijklm.supabase.co\n")
	writeFile(t, root, "package.json", `{"dependencies":{"@supabase/supabase-js":"^2.0.0"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	writeFile(t, root, "src/lib/admin.ts", `
const admin = createClient(url, process.env.SUPABASE_SERVICE_ROLE_KEY!)
const a = await supabase.from('a').select()
const b = await supabase.from('b').select()
const c = await supabase.from('c').select()
const d = await supabase.from('d').select()
const e = await supabase.from('e').select()
const f = await supabase.from('f').select()
`)

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}
	report.AttachSource(&SourceFacts{Policies: SourcePolicies{Total: 20}})

	var planText string
	for _, step := range report.Scenario.Plan {
		planText += step + "\n"
	}
	if !contains(planText, "app-layer guards") || contains(planText, "KEEP the policies") {
		t.Fatalf("expected the app-guard path for a small service-role-style corpus, got:\n%s", planText)
	}
}

func TestVestigialAndBlockingExtensions(t *testing.T) {
	facts := &SourceFacts{Extensions: []SourceExtension{
		{Name: "postgis", Dependents: 5, Available: true},
		{Name: "earthdistance", Dependents: 0, Available: false},
		{Name: "timescaledb", Dependents: 7, Available: false},
	}}
	if vestigial := facts.VestigialExtensions(); len(vestigial) != 1 || vestigial[0] != "earthdistance" {
		t.Errorf("vestigial = %v, want [earthdistance]", vestigial)
	}
	blocking := facts.BlockingExtensions()
	if len(blocking) != 1 || !contains(blocking[0], "timescaledb (7 dependent objects)") {
		t.Errorf("blocking = %v, want timescaledb with count", blocking)
	}
}

// TestScanLongHistoryRecommendsSquash: a long migration history gets the
// consolidate-before-moving warning pointing at `capydb migrate squash`.
func TestScanLongHistoryRecommendsSquash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".env", "NEXT_PUBLIC_SUPABASE_URL=https://abcdefghijklm.supabase.co\n")
	writeFile(t, root, "package.json", `{"dependencies":{"@supabase/supabase-js":"^2.0.0"}}`)
	writeFile(t, root, "pnpm-lock.yaml", "lockfileVersion: 9\n")
	for i := range 50 {
		writeFile(t, root, filepath.Join("supabase", "migrations", fmt.Sprintf("%04d_step.sql", i)), "select 1;")
	}

	report, err := Run(root, "")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, warning := range report.Scenario.Warnings {
		if strings.Contains(warning, "capydb migrate squash") && strings.Contains(warning, "50 migration files") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the squash recommendation for 50 migration files, got %+v", report.Scenario.Warnings)
	}
}
