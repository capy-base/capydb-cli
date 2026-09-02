package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/capydatabase/capydb-cli/internal/exitcode"
)

const (
	outputText = "text"
	outputJSON = "json"
)

// validateOutputMode rejects unknown --output values with a usage error.
func validateOutputMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", outputText, outputJSON:
		return nil
	default:
		return usageErrorf("--output must be %q or %q", outputText, outputJSON)
	}
}

// jsonOutput reports whether the user asked for machine-readable JSON output.
func (a *app) jsonOutput() bool {
	return strings.EqualFold(strings.TrimSpace(a.output), outputJSON)
}

// useJSONOutput switches the app into JSON output mode; legacy per-command
// --json flags map onto it.
func (a *app) useJSONOutput() {
	a.output = outputJSON
}

// printJSON writes an indented JSON document to out (stdout for commands).
// JSON is the only thing written to stdout in JSON mode so output stays
// script-friendly.
func printJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode json output: %w", err)
	}
	return nil
}

// jsonList returns a non-nil slice so empty lists marshal as [] instead of
// null, which breaks consumers that .map/.filter the result.
func jsonList[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

// usageErrorf builds a validation error that exits with the usage exit code.
func usageErrorf(format string, args ...any) error {
	return exitcode.Errorf(exitcode.UsageError, format, args...)
}

// notFoundErrorf builds an error that exits with the not-found exit code.
func notFoundErrorf(format string, args ...any) error {
	return exitcode.Errorf(exitcode.NotFound, format, args...)
}

// authErrorf builds an error that exits with the auth exit code.
func authErrorf(format string, args ...any) error {
	return exitcode.Errorf(exitcode.AuthError, format, args...)
}
