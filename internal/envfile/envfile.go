package envfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func Upsert(path string, updates map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create env directory: %w", err)
	}

	lines := []string{}
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read env file: %w", err)
	}

	seen := make(map[string]bool, len(updates))
	for index, line := range lines {
		key, prefix, ok := parseAssignment(line)
		if !ok {
			continue
		}
		value, exists := updates[key]
		if !exists {
			continue
		}
		lines[index] = prefix + key + "=" + quote(value)
		seen[key] = true
	}

	missing := make([]string, 0, len(updates))
	for key := range updates {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)

	if len(lines) > 0 && lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	for _, key := range missing {
		lines = append(lines, key+"="+quote(updates[key]))
	}

	content := strings.Join(trimTrailingEmptyLines(lines), "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write env file: %w", err)
	}
	return nil
}

func parseAssignment(line string) (key string, prefix string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}

	prefix = ""
	candidate := trimmed
	if strings.HasPrefix(candidate, "export ") {
		prefix = "export "
		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "export "))
	}

	index := strings.IndexRune(candidate, '=')
	if index <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(candidate[:index])
	if key == "" {
		return "", "", false
	}
	return key, prefix, true
}

func quote(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}

func trimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}
