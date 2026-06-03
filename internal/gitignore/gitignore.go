package gitignore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const capyDBIgnoreEntry = ".capydb/"

// EnsureLocalConfigIgnored makes sure the CapyDB local project link directory
// (.capydb/) and any additional credential-bearing files (such as the env file
// the CLI writes, which contains the full postgres:// connection URL) are listed
// in the repo's .gitignore. It is idempotent: entries that are already present
// (in any equivalent form) are not duplicated.
func EnsureLocalConfigIgnored(cwd string, extraEntries ...string) error {
	path := filepath.Join(cwd, ".gitignore")

	lines := []string{}
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	existing := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		existing[strings.TrimSpace(line)] = struct{}{}
	}

	type pendingEntry struct {
		comment string
		value   string
	}

	pending := make([]pendingEntry, 0, 1+len(extraEntries))

	// The .capydb/ link directory holds no secrets but is local-only.
	if _, ok := existing[capyDBIgnoreEntry]; !ok {
		if _, ok := existing[".capydb"]; !ok {
			pending = append(pending, pendingEntry{comment: "# CapyDB local project link", value: capyDBIgnoreEntry})
		}
	}

	for _, raw := range extraEntries {
		entry := normalizeIgnoreEntry(cwd, raw)
		if entry == "" {
			continue
		}
		if _, ok := existing[entry]; ok {
			continue
		}
		// Avoid scheduling the same entry twice within this call.
		duplicate := false
		for _, p := range pending {
			if p.value == entry {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		existing[entry] = struct{}{}
		pending = append(pending, pendingEntry{comment: "# CapyDB connection env file (contains credentials)", value: entry})
	}

	if len(pending) == 0 {
		return nil
	}

	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	for _, entry := range pending {
		if entry.comment != "" {
			lines = append(lines, entry.comment)
		}
		lines = append(lines, entry.value)
	}

	content := strings.Join(trimTrailingEmptyLines(lines), "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

// normalizeIgnoreEntry resolves a candidate env file path (which may be absolute
// or relative to cwd) into a clean, forward-slash gitignore pattern relative to
// the repo root. Paths outside cwd are kept as-is (cleaned) so the caller's intent
// is preserved.
func normalizeIgnoreEntry(cwd, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	candidate := trimmed
	if filepath.IsAbs(candidate) {
		if rel, err := filepath.Rel(cwd, candidate); err == nil && !strings.HasPrefix(rel, "..") {
			candidate = rel
		}
	}
	candidate = filepath.ToSlash(filepath.Clean(candidate))
	candidate = strings.TrimPrefix(candidate, "./")
	return candidate
}

func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}
