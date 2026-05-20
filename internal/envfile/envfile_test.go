package envfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpsertPreservesCommentsAndUpdatesExistingKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	input := "# existing comment\nDATABASE_URL=\"old\"\n\nNEXT_PUBLIC=true\n"
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	err := Upsert(path, map[string]string{
		"DATABASE_URL":        "postgres://new",
		"DATABASE_POOLED_URL": "postgres://pooled",
	})
	if err != nil {
		t.Fatalf("upsert env: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	content := string(raw)

	if !strings.Contains(content, "# existing comment") {
		t.Fatalf("expected comment to be preserved, got:\n%s", content)
	}
	if !strings.Contains(content, "DATABASE_URL=\"postgres://new\"") {
		t.Fatalf("expected DATABASE_URL to be updated, got:\n%s", content)
	}
	if !strings.Contains(content, "DATABASE_POOLED_URL=\"postgres://pooled\"") {
		t.Fatalf("expected DATABASE_POOLED_URL to be appended, got:\n%s", content)
	}
}
