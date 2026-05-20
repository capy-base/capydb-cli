package gitignore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const capyDBIgnoreEntry = ".capydb/"

func EnsureLocalConfigIgnored(cwd string) error {
	path := filepath.Join(cwd, ".gitignore")

	lines := []string{}
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == capyDBIgnoreEntry || trimmed == ".capydb" {
			return nil
		}
	}

	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	lines = append(lines, "# CapyDB local project link", capyDBIgnoreEntry)

	content := strings.Join(trimTrailingEmptyLines(lines), "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write .gitignore: %w", err)
	}
	return nil
}

func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}
