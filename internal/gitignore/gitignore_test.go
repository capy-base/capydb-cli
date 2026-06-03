package gitignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureLocalConfigIgnoredAddsEnvFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := EnsureLocalConfigIgnored(dir, ".env.local"); err != nil {
		t.Fatalf("ensure ignored: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, ".capydb/") {
		t.Fatalf("expected .capydb/ entry:\n%s", content)
	}
	if !strings.Contains(content, ".env.local") {
		t.Fatalf("expected env file entry:\n%s", content)
	}
}

func TestEnsureLocalConfigIgnoredIsIdempotent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for range 3 {
		if err := EnsureLocalConfigIgnored(dir, "apps/web/.env"); err != nil {
			t.Fatalf("ensure ignored: %v", err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if got := strings.Count(content, ".capydb/"); got != 1 {
		t.Fatalf("expected exactly one .capydb/ entry, got %d:\n%s", got, content)
	}
	if got := strings.Count(content, "apps/web/.env"); got != 1 {
		t.Fatalf("expected exactly one env entry, got %d:\n%s", got, content)
	}
}

func TestEnsureLocalConfigIgnoredRespectsExistingEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte("node_modules\n.env\n"), 0o644); err != nil {
		t.Fatalf("seed .gitignore: %v", err)
	}

	if err := EnsureLocalConfigIgnored(dir, ".env"); err != nil {
		t.Fatalf("ensure ignored: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	if got := strings.Count(content, ".env"); got != 1 {
		t.Fatalf("expected the existing .env entry not to be duplicated, got %d:\n%s", got, content)
	}
}

func TestNormalizeIgnoreEntryAbsolutePathWithinRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	got := normalizeIgnoreEntry(dir, filepath.Join(dir, "apps", "api", ".env"))
	if got != "apps/api/.env" {
		t.Fatalf("unexpected normalized entry: %q", got)
	}
}
