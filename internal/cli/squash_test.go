package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/capy-base/capydb-cli/internal/exitcode"
)

// fakeCapysquash writes an executable that records its argv and exits with
// the given code, and points the lookup override at it.
func fakeCapysquash(t *testing.T, exitCode int) (argvFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake binary")
	}
	dir := t.TempDir()
	argvFile = filepath.Join(dir, "argv")
	script := filepath.Join(dir, "capysquash")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\nexit " + map[int]string{0: "0", 2: "2"}[exitCode] + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := capysquashLookPath
	capysquashLookPath = func(name string) (string, error) {
		if name == "capysquash" {
			return script, nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { capysquashLookPath = previous })
	return argvFile
}

func TestMigrateSquashMissingBinaryExplainsInstall(t *testing.T) {
	previous := capysquashLookPath
	capysquashLookPath = func(string) (string, error) { return "", errors.New("not found") }
	t.Cleanup(func() { capysquashLookPath = previous })

	_, err := runCommand(t, t.TempDir(), "migrate", "squash")
	if err == nil || !strings.Contains(err.Error(), "go install github.com/capysquash/pgsquash-engine/cmd/pgsquash") {
		t.Fatalf("expected the install hint, got %v", err)
	}
}

func TestMigrateSquashAnalyzeResolvesSupabaseDirAndPrintsFooter(t *testing.T) {
	argvFile := fakeCapysquash(t, 0)
	root := t.TempDir()
	migrations := filepath.Join(root, "supabase", "migrations")
	if err := os.MkdirAll(migrations, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"0002_second.sql", "0001_first.sql"} {
		if err := os.WriteFile(filepath.Join(migrations, name), []byte("select 1;"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	output, err := runCommand(t, root, "migrate", "squash", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	argv, readErr := os.ReadFile(argvFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(argv)), "\n")
	want := []string{"analyze", filepath.Join(migrations, "0001_first.sql"), filepath.Join(migrations, "0002_second.sql")}
	if len(lines) != 3 || lines[0] != want[0] || lines[1] != want[1] || lines[2] != want[2] {
		t.Fatalf("child argv = %q, want %q (sorted files, not the directory)", lines, want)
	}
	if !strings.Contains(output, "nothing was changed") || !strings.Contains(output, "--workflow safe") {
		t.Fatalf("expected the analyze footer, got:\n%s", output)
	}
}

func TestMigrateSquashWorkflowSafePassesThroughAndSkipsFooter(t *testing.T) {
	argvFile := fakeCapysquash(t, 0)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "0001_init.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := runCommand(t, root, "migrate", "squash", "--workflow", "safe")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	argv, _ := os.ReadFile(argvFile)
	if !strings.HasPrefix(string(argv), "safe\n") {
		t.Fatalf("child argv = %q, want safe first", string(argv))
	}
	if strings.Contains(output, "nothing was changed") {
		t.Fatalf("safe workflow must not print the analyze footer:\n%s", output)
	}
}

func TestMigrateSquashChildFailureMapsToGenericExit(t *testing.T) {
	fakeCapysquash(t, 2)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "0001_init.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runCommand(t, root, "migrate", "squash")
	coded := &exitcode.Error{}
	if !errors.As(err, &coded) || coded.Code != exitcode.GenericError {
		t.Fatalf("expected a GenericError exit mapping, got %v", err)
	}
}

func TestMigrateSquashRejectsUnknownWorkflow(t *testing.T) {
	_, err := runCommand(t, t.TempDir(), "migrate", "squash", "--workflow", "yolo")
	if err == nil || !strings.Contains(err.Error(), "unknown --workflow") {
		t.Fatalf("expected a usage error, got %v", err)
	}
}
