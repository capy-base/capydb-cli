package envfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ConflictResolver decides whether an existing key whose current non-empty value
// differs from the incoming value should be overwritten. It is invoked once per
// conflicting key with the key name, the existing value, and the new value.
// Returning false leaves the existing value untouched.
type ConflictResolver func(key, existing, incoming string) (overwrite bool, err error)

func Upsert(path string, updates map[string]string) error {
	return UpsertWithResolver(path, updates, nil)
}

// UpsertWithResolver writes the given key/value updates into the env file at path.
// When resolver is non-nil and an existing key already holds a non-empty value that
// differs from the incoming value, the resolver decides whether to overwrite it.
func UpsertWithResolver(path string, updates map[string]string, resolver ConflictResolver) error {
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
		if resolver != nil {
			existingValue := parseAssignmentValue(line)
			if existingValue != "" && existingValue != value {
				overwrite, err := resolver(key, existingValue, value)
				if err != nil {
					return err
				}
				if !overwrite {
					seen[key] = true
					continue
				}
			}
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

// parseAssignmentValue returns the unquoted value of an assignment line, or an
// empty string when the line is not an assignment or carries no value.
func parseAssignmentValue(line string) string {
	candidate := strings.TrimSpace(line)
	if candidate == "" || strings.HasPrefix(candidate, "#") {
		return ""
	}
	candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "export "))

	index := strings.IndexRune(candidate, '=')
	if index <= 0 {
		return ""
	}
	return unquote(strings.TrimSpace(candidate[index+1:]))
}

func unquote(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		inner := value[1 : len(value)-1]
		replacer := strings.NewReplacer(`\"`, `"`, `\\`, `\`)
		return replacer.Replace(inner)
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return value[1 : len(value)-1]
	}
	return value
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
