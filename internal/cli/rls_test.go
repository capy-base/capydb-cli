package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rlsFixtureSQL = `
create table public.todos (
  id bigint generated always as identity primary key,
  owner_id uuid not null default auth.uid(),
  title text
);
alter table public.todos enable row level security;

create policy todos_owner on public.todos
  for all to authenticated
  using (auth.uid() = owner_id)
  with check (auth.uid() = owner_id);

create policy org_read on public.todos
  for select to authenticated
  using (owner_id::text = auth.jwt() ->> 'org_id');
`

func writeRLSFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	migrations := filepath.Join(dir, "supabase", "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(migrations, "0001_init.sql"), []byte(rlsFixtureSQL), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMigrateRLSFindsSupabaseMigrationsAndConverts(t *testing.T) {
	dir := writeRLSFixture(t)
	output, err := runCommand(t, dir, "migrate", "rls", dir, "--out", filepath.Join(dir, "capyrls"))
	if err != nil {
		t.Fatalf("migrate rls: %v\n%s", err, output)
	}
	for _, want := range []string{
		"supabase/migrations",
		"policies: 2 converted, 0 skipped, 0 need attention",
		"app.user_id (uuid",
		"app.org_id (text)",
	} {
		if !strings.Contains(output, filepath.FromSlash(want)) && !strings.Contains(output, want) {
			t.Errorf("output missing %q:\n%s", want, output)
		}
	}

	prelude, err := os.ReadFile(filepath.Join(dir, "capyrls", "capyrls_01_prelude.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prelude), "app.user_id()") {
		t.Error("prelude missing app.user_id accessor")
	}

	// CapyDB default is the single-role model: the app connects as the
	// owning credential, so the bundle must FORCE row security.
	force, err := os.ReadFile(filepath.Join(dir, "capyrls", "capyrls_02_force_rls.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(force), "force row level security") {
		t.Error("single-role bundle missing FORCE ROW LEVEL SECURITY")
	}

	policies, err := os.ReadFile(filepath.Join(dir, "capyrls", "capyrls_03_policies.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(policies), "auth.uid()") {
		t.Error("policies still reference auth.uid()")
	}
	if _, err := os.Stat(filepath.Join(dir, "capyrls", "capyrls_report.md")); err != nil {
		t.Error("report not written")
	}
}

func TestMigrateRLSSplitRoleModel(t *testing.T) {
	dir := writeRLSFixture(t)
	output, err := runCommand(t, dir, "migrate", "rls", dir, "--role-model", "split", "--out", filepath.Join(dir, "capyrls"))
	if err != nil {
		t.Fatalf("migrate rls: %v\n%s", err, output)
	}
	roles, err := os.ReadFile(filepath.Join(dir, "capyrls", "capyrls_02_roles.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(roles), "create role app_user") {
		t.Error("split model must create the runtime role")
	}
}

func TestMigrateRLSJSONOutput(t *testing.T) {
	dir := writeRLSFixture(t)
	output, err := runCommand(t, dir, "-o", "json", "migrate", "rls", dir, "--out", filepath.Join(dir, "capyrls"))
	if err != nil {
		t.Fatalf("migrate rls: %v\n%s", err, output)
	}
	var payload struct {
		RLS struct {
			Files  []string `json:"files"`
			Report struct {
				Mode      string `json:"mode"`
				RoleModel string `json:"role_model"`
				Policies  []struct {
					Status string `json:"status"`
				} `json:"policies"`
			} `json:"report"`
		} `json:"rls"`
	}
	// The apply-order hint goes to stderr, which runCommand merges into the
	// same buffer; decode only the JSON document.
	jsonStart := strings.Index(output, "{")
	decoder := json.NewDecoder(strings.NewReader(output[jsonStart:]))
	if err := decoder.Decode(&payload); err != nil {
		t.Fatalf("JSON output not parseable: %v\n%s", err, output)
	}
	if payload.RLS.Report.Mode != "vanilla" || payload.RLS.Report.RoleModel != "single" {
		t.Errorf("unexpected defaults in JSON report: %+v", payload.RLS.Report)
	}
	if len(payload.RLS.Files) == 0 || len(payload.RLS.Report.Policies) != 2 {
		t.Errorf("JSON payload incomplete: %+v", payload.RLS)
	}
}

func TestMigrateRLSInvalidFlagIsUsageError(t *testing.T) {
	dir := writeRLSFixture(t)
	_, err := runCommand(t, dir, "migrate", "rls", dir, "--mode", "nonsense")
	if err == nil || !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("expected a usage error for a bad --mode, got %v", err)
	}
}

func TestMigrateRLSNoSQLFilesIsError(t *testing.T) {
	dir := t.TempDir()
	_, err := runCommand(t, dir, "migrate", "rls", dir)
	if err == nil || !strings.Contains(err.Error(), "no .sql files") {
		t.Fatalf("expected a no-input error, got %v", err)
	}
}
