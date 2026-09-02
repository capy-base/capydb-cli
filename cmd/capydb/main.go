package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/capydatabase/capydb-cli/internal/api"
	"github.com/capydatabase/capydb-cli/internal/cli"
	"github.com/capydatabase/capydb-cli/internal/exitcode"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	builtBy = "unknown"
)

func buildVersion() string {
	if version == "dev" {
		return fmt.Sprintf("%s (built from source)", version)
	}
	return fmt.Sprintf("%s (commit: %s, built: %s, by: %s)", version, commit, date, builtBy)
}

// exitCodeFor maps a command error onto the CLI's semantic exit codes:
// explicit exitcode.Error wins, API errors map by HTTP status, deadline
// expiry maps to the timeout code, and everything else is a generic failure.
func exitCodeFor(err error) int {
	if err == nil {
		return int(exitcode.Success)
	}

	if coded, ok := errors.AsType[*exitcode.Error](err); ok {
		return int(coded.Code)
	}

	if apiErr, ok := errors.AsType[*api.APIError](err); ok {
		return int(exitcode.FromHTTPStatus(apiErr.StatusCode))
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return int(exitcode.Timeout)
	}
	return int(exitcode.GenericError)
}

func main() {
	if err := cli.Execute(buildVersion()); err != nil {
		fmt.Fprintf(os.Stderr, "capydb: %v\n", err)
		os.Exit(exitCodeFor(err))
	}
}
