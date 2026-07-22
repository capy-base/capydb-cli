package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// defaultReleaseRepo is the public GitHub repo release archives are published
// to (see .goreleaser.yaml / scripts/install.sh). CAPYDB_REPO overrides it,
// matching the install script's convention.
const defaultReleaseRepo = "capy-base/capydb-cli"

// releasesAPIBaseURL is a package variable so tests can point the update check
// at a local server.
var releasesAPIBaseURL = "https://api.github.com"

// updateCheckTimeout bounds the GitHub releases call so `version --check`
// never hangs offline.
const updateCheckTimeout = 5 * time.Second

// versionReport is the stable JSON shape for `capydb version`.
type versionReport struct {
	Version         string `json:"version"`
	GoVersion       string `json:"go_version"`
	Platform        string `json:"platform"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable *bool  `json:"update_available,omitempty"`
}

func (a *app) newVersionCommand() *cobra.Command {
	var check bool

	command := &cobra.Command{
		Use:   "version",
		Short: "Show the CLI version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			report := versionReport{
				Version:   a.version,
				GoVersion: runtime.Version(),
				Platform:  runtime.GOOS + "/" + runtime.GOARCH,
			}

			if check {
				latest, err := fetchLatestReleaseTag(cmd.Context())
				if err != nil {
					// Graceful offline failure: the version itself printed fine,
					// so an unreachable update check is a warning, not an error.
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: update check failed: %v\n", err)
				} else {
					report.LatestVersion = latest
					updateAvailable := updateAvailable(a.version, latest)
					report.UpdateAvailable = &updateAvailable
				}
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), report)
			}

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "capydb %s\n", report.Version)
			_, _ = fmt.Fprintf(out, "go: %s\n", report.GoVersion)
			_, _ = fmt.Fprintf(out, "platform: %s\n", report.Platform)
			if report.LatestVersion != "" {
				_, _ = fmt.Fprintf(out, "latest: %s\n", report.LatestVersion)
				if report.UpdateAvailable != nil && *report.UpdateAvailable {
					_, _ = fmt.Fprintf(out, "update available: run `brew upgrade capydb` or download from https://github.com/%s/releases\n", releaseRepo())
				} else {
					_, _ = fmt.Fprintln(out, "up to date")
				}
			}
			return nil
		},
	}

	command.Flags().BoolVar(&check, "check", false, "Check GitHub for a newer release")
	return command
}

func releaseRepo() string {
	if value := strings.TrimSpace(os.Getenv("CAPYDB_REPO")); value != "" {
		return value
	}
	return defaultReleaseRepo
}

// fetchLatestReleaseTag asks the GitHub releases API for the latest release
// tag of the CLI repo, bounded by updateCheckTimeout.
func fetchLatestReleaseTag(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()

	url := strings.TrimRight(releasesAPIBaseURL, "/") + "/repos/" + releaseRepo() + "/releases/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build release request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: updateCheckTimeout}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch latest release: %w", err)
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch latest release: github returned status %d", response.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode release response: %w", err)
	}
	if strings.TrimSpace(payload.TagName) == "" {
		return "", fmt.Errorf("fetch latest release: response had no tag_name")
	}
	return strings.TrimSpace(payload.TagName), nil
}

// updateAvailable compares the running build's version token against the
// latest release tag. Dev builds ("dev ...") never report an update because
// they have no comparable version.
func updateAvailable(current, latest string) bool {
	currentToken := ""
	if fields := strings.Fields(strings.TrimSpace(current)); len(fields) > 0 {
		currentToken = fields[0]
	}
	currentToken = strings.TrimPrefix(currentToken, "v")
	latestToken := strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if currentToken == "" || currentToken == "dev" || latestToken == "" {
		return false
	}
	return currentToken != latestToken
}
