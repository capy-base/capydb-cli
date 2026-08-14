package configlint

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeProject materialises a fake repo on disk. Every case shares the same
// .env so the linter has to resolve env keys -> pooled/direct, which is the
// part that makes the rules work across stacks.
func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	base := map[string]string{
		".env": "DATABASE_URL=postgres://u:p@db.capydb.dev:6432/app?sslmode=require\n" +
			"DATABASE_DIRECT_URL=postgres://u:p@db.capydb.dev:5432/app?sslmode=require\n",
	}
	maps.Copy(base, files)
	for name, body := range base {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func rules(findings []Finding) map[string]Finding {
	byRule := map[string]Finding{}
	for _, f := range findings {
		byRule[f.Rule] = f
	}
	return byRule
}

func TestDrizzleConfigPooledURLForMigrations(t *testing.T) {
	root := writeProject(t, map[string]string{
		"drizzle.config.ts": `import { defineConfig } from 'drizzle-kit'
export default defineConfig({
  dialect: 'postgresql',
  schema: './src/db/schema.ts',
  schemaFilter: ['public'],
  dbCredentials: { url: process.env.DATABASE_URL! },
})`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	found := rules(findings)
	if _, ok := found["pooled_url_for_migrations"]; !ok {
		t.Fatalf("expected pooled_url_for_migrations, got %+v", findings)
	}
	if found["pooled_url_for_migrations"].Severity != SeverityError {
		t.Fatal("pointing migrations at the pooler is an error, not a warning")
	}
	if _, ok := found["missing_schema_filter"]; ok {
		t.Fatal("schemaFilter was present; should not be flagged")
	}
}

func TestDrizzleConfigDirectURLIsClean(t *testing.T) {
	root := writeProject(t, map[string]string{
		"drizzle.config.ts": `export default defineConfig({
  schemaFilter: ['public'],
  dbCredentials: { url: process.env.DATABASE_DIRECT_URL ?? process.env.DATABASE_URL! },
})`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules(findings)["pooled_url_for_migrations"]; ok {
		t.Fatalf("direct URL must not be flagged: %+v", findings)
	}
}

func TestDrizzleConfigMissingSchemaFilter(t *testing.T) {
	root := writeProject(t, map[string]string{
		"drizzle.config.ts": `export default defineConfig({
  dbCredentials: { url: process.env.DATABASE_DIRECT_URL! },
})`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules(findings)["missing_schema_filter"]; !ok {
		t.Fatalf("drizzle-kit v1 manages all schemas; expected the warning: %+v", findings)
	}
}

func TestPrismaPooledWithoutDirectURL(t *testing.T) {
	root := writeProject(t, map[string]string{
		"prisma/schema.prisma": `datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	found := rules(findings)
	if _, ok := found["prisma_missing_direct_url"]; !ok {
		t.Fatalf("expected prisma_missing_direct_url: %+v", findings)
	}
	if _, ok := found["prisma_missing_pgbouncer_flag"]; !ok {
		t.Fatalf("expected prisma_missing_pgbouncer_flag: %+v", findings)
	}
}

func TestPostgresJSMissingPrepareFalseAndOversizedPool(t *testing.T) {
	root := writeProject(t, map[string]string{
		"src/db.ts": `import postgres from 'postgres'
const sql = postgres(process.env.DATABASE_URL!, {
  max: 50,
})
export default sql`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	found := rules(findings)
	if _, ok := found["missing_prepare_false"]; !ok {
		t.Fatalf("expected missing_prepare_false: %+v", findings)
	}
	if _, ok := found["oversized_pool"]; !ok {
		t.Fatalf("expected oversized_pool: %+v", findings)
	}
}

func TestPostgresJSWithPrepareFalseIsClean(t *testing.T) {
	root := writeProject(t, map[string]string{
		"src/db.ts": `import postgres from 'postgres'
const sql = postgres(process.env.DATABASE_URL!, { prepare: false, max: 1 })`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	found := rules(findings)
	if _, ok := found["missing_prepare_false"]; ok {
		t.Fatalf("prepare: false was set; must not be flagged: %+v", findings)
	}
	if _, ok := found["oversized_pool"]; ok {
		t.Fatalf("max: 1 must not be flagged: %+v", findings)
	}
}

// The investobase failure mode: one transaction wrapping a per-row write loop,
// which the plan's idle_in_transaction_session_timeout cuts partway through.
func TestUnbatchedBulkTransaction(t *testing.T) {
	root := writeProject(t, map[string]string{
		"scripts/backfill.mjs": `const sql = postgres(process.env.DATABASE_DIRECT_URL, { max: 1 })
await sql.begin(async (tx) => {
  for (const row of rows) {
    await tx` + "`INSERT INTO target (id) VALUES (${row.id})`" + `
  }
})`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules(findings)["unbatched_bulk_transaction"]; !ok {
		t.Fatalf("expected unbatched_bulk_transaction: %+v", findings)
	}
}

func TestSkipsVendoredTrees(t *testing.T) {
	root := writeProject(t, map[string]string{
		"node_modules/pkg/drizzle.config.ts": `export default defineConfig({
  dbCredentials: { url: process.env.DATABASE_URL! },
})`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("node_modules must be skipped, got %+v", findings)
	}
}

func TestCleanProjectHasNoFindings(t *testing.T) {
	root := writeProject(t, map[string]string{
		"drizzle.config.ts": `export default defineConfig({
  schemaFilter: ['public'],
  dbCredentials: { url: process.env.DATABASE_DIRECT_URL! },
})`,
		"src/db.ts": `const sql = postgres(process.env.DATABASE_URL!, { prepare: false, max: 1 })`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("a correctly configured project must be silent, got %+v", findings)
	}
}

// The investobase failure: a database built with `db:push`, then `db:migrate`
// replays from migration #1 and dies on `relation "contacts" already exists`.
func TestDrizzlePushAndMigrateMixed(t *testing.T) {
	root := writeProject(t, map[string]string{
		"package.json": `{"scripts":{"db:push":"drizzle-kit push","db:migrate":"drizzle-kit migrate"}}`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules(findings)["drizzle_push_and_migrate_mixed"]; !ok {
		t.Fatalf("expected drizzle_push_and_migrate_mixed: %+v", findings)
	}
}

func TestDrizzlePushOnlyIsClean(t *testing.T) {
	root := writeProject(t, map[string]string{
		"package.json": `{"scripts":{"db:push":"drizzle-kit push"}}`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules(findings)["drizzle_push_and_migrate_mixed"]; ok {
		t.Fatalf("push alone is a valid workflow; must not be flagged: %+v", findings)
	}
}

func TestNonDrizzleScriptsIgnored(t *testing.T) {
	// "migrate" and "push" in unrelated scripts must not trigger the rule.
	root := writeProject(t, map[string]string{
		"package.json": `{"scripts":{"deploy":"git push","data:migrate":"node scripts/migrate.mjs"}}`,
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rules(findings)["drizzle_push_and_migrate_mixed"]; ok {
		t.Fatalf("non-drizzle scripts must not be flagged: %+v", findings)
	}
}

func TestSupabaseRLSUnconverted(t *testing.T) {
	root := writeProject(t, map[string]string{
		"supabase/migrations/0001_init.sql": "create policy p on t using (auth.uid() = owner_id);\n",
		"db/plain.sql":                      "create table t (id int);\n",
	})
	findings, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	byRule := rules(findings)
	finding, ok := byRule["supabase_rls_unconverted"]
	if !ok {
		t.Fatal("SQL calling auth.uid() must be flagged; those policies cannot work outside Supabase")
	}
	if finding.Severity != SeverityWarning {
		t.Fatalf("unconverted RLS is a warning (the import drops it loudly), got %s", finding.Severity)
	}
	if finding.Line != 1 {
		t.Fatalf("finding should anchor to the first auth.* line, got %d", finding.Line)
	}
	for _, f := range findings {
		if f.Rule == "supabase_rls_unconverted" && strings.Contains(f.File, "plain.sql") {
			t.Fatal("plain SQL without auth helpers must not be flagged")
		}
	}
}

// A schema pinned to one major's uuidv7 spelling breaks on the other, and a
// CapyDB major upgrade is a logical dump/restore that carries the name across.
func TestUUIDv7NotPortable(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"pg18 builtin", "create table t (id uuid default uuidv7());\n", true},
		{"extension spelling", "create table t (id uuid default uuid_generate_v7());\n", true},
		{"schema-qualified extension call", "create table t (id uuid default extensions.uuid_generate_v7());\n", true},
		{"portable wrapper is the fix", "create table t (id uuid default capydb.uuidv7());\n", false},
		{"unrelated sql", "create table t (id uuid default gen_random_uuid());\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeProject(t, map[string]string{"db/0001.sql": tc.sql})
			findings, err := Run(root)
			if err != nil {
				t.Fatal(err)
			}
			_, got := rules(findings)["uuidv7_not_portable"]
			if got != tc.want {
				t.Fatalf("uuidv7_not_portable = %v, want %v: %+v", got, tc.want, findings)
			}
		})
	}
}

// SET NOT NULL on a populated table is a write outage for the length of a full
// scan; 18 lets it be split into NOT VALID + VALIDATE.
func TestSetNotNullLocksTable(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want bool
	}{
		{"bare set not null", "alter table users alter column email set not null;\n", true},
		{"not valid is the fix", "alter table users add constraint email_nn not null email not valid;\n", false},
		{"unrelated alter", "alter table users add column nickname text;\n", false},
		{"create table not null is fine", "create table users (email text not null);\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeProject(t, map[string]string{"db/0002.sql": tc.sql})
			findings, err := Run(root)
			if err != nil {
				t.Fatal(err)
			}
			_, got := rules(findings)["set_not_null_locks_table"]
			if got != tc.want {
				t.Fatalf("set_not_null_locks_table = %v, want %v: %+v", got, tc.want, findings)
			}
		})
	}
}
