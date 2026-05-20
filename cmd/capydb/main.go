package main

import (
	"fmt"
	"os"

	"github.com/capy-base/capydb/cli/internal/cli"
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

func main() {
	if err := cli.Execute(buildVersion()); err != nil {
		fmt.Fprintf(os.Stderr, "capydb: %v\n", err)
		os.Exit(1)
	}
}
