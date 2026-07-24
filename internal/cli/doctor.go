package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/config"
	"github.com/capy-base/capydb/cli/internal/configlint"
	"github.com/capy-base/capydb/cli/internal/exitcode"
	"github.com/capy-base/capydb/cli/internal/scan"
)

const (
	doctorPass = "pass"
	doctorFail = "fail"
	doctorSkip = "skip"
)

// doctorCheck is the result of one environment diagnostic.
type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func (a *app) newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose the local CapyDB CLI environment",
		Long:  "Checks the saved config, API reachability, authentication, the local project link, and psql availability. Exits non-zero when any check fails.",
		Args:  cobra.NoArgs,
		RunE:  a.runDoctor,
	}
}

func (a *app) runDoctor(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	checks := make([]doctorCheck, 0, 5)

	// 1. Config readability.
	configPath, configErr := config.UserConfigPath()
	if configErr == nil {
		_, configErr = config.LoadUserConfig()
	}
	if configErr != nil {
		checks = append(checks, doctorCheck{Name: "config", Status: doctorFail, Detail: configErr.Error()})
	} else {
		checks = append(checks, doctorCheck{Name: "config", Status: doctorPass, Detail: configPath})
	}

	// 2. API reachability via the unauthenticated public status endpoint.
	apiURL := a.resolveAPIURL("")
	anonymousClient, err := a.newAPIClient(apiURL, "")
	if err != nil {
		checks = append(checks, doctorCheck{Name: "api", Status: doctorFail, Detail: err.Error()})
	} else if publicStatus, statusErr := anonymousClient.GetPublicStatus(ctx); statusErr != nil {
		checks = append(checks, doctorCheck{Name: "api", Status: doctorFail, Detail: fmt.Sprintf("%s unreachable: %v", apiURL, statusErr)})
	} else {
		checks = append(checks, doctorCheck{Name: "api", Status: doctorPass, Detail: fmt.Sprintf("%s (%s)", apiURL, firstNonEmpty(publicStatus.Status, "unknown"))})
	}

	// 3. Auth validity via the viewer endpoint.
	authValid := false
	authConfig, authErr := a.resolveAuth(false)
	switch {
	case authErr != nil:
		checks = append(checks, doctorCheck{Name: "auth", Status: doctorFail, Detail: authErr.Error()})
	default:
		client, clientErr := a.newAPIClient(authConfig.APIURL, authConfig.APIKey)
		if clientErr != nil {
			checks = append(checks, doctorCheck{Name: "auth", Status: doctorFail, Detail: clientErr.Error()})
			break
		}
		viewer, viewerErr := client.GetViewerResponse(ctx)
		if viewerErr != nil {
			checks = append(checks, doctorCheck{Name: "auth", Status: doctorFail, Detail: fmt.Sprintf("api key rejected: %v", viewerErr)})
			break
		}
		authValid = true
		detail := "api key accepted"
		if viewer.Organization != nil {
			detail = fmt.Sprintf("api key accepted for organization %s", firstNonEmpty(viewer.Organization.Name, viewer.Organization.ID))
		}
		checks = append(checks, doctorCheck{Name: "auth", Status: doctorPass, Detail: detail})
	}

	// 4. Local project link validity.
	linkConfig, linkErr := config.LoadProjectConfig(a.cwd)
	switch {
	case errors.Is(linkErr, os.ErrNotExist):
		checks = append(checks, doctorCheck{Name: "project_link", Status: doctorSkip, Detail: "no local project link (run `capydb link` to create one)"})
	case linkErr != nil:
		checks = append(checks, doctorCheck{Name: "project_link", Status: doctorFail, Detail: linkErr.Error()})
	case strings.TrimSpace(linkConfig.ProjectID) == "":
		checks = append(checks, doctorCheck{Name: "project_link", Status: doctorFail, Detail: "project link has no project_id; run `capydb link` again"})
	case !authValid:
		checks = append(checks, doctorCheck{Name: "project_link", Status: doctorSkip, Detail: fmt.Sprintf("linked to %s, but auth is unavailable to verify it", linkConfig.ProjectID)})
	default:
		client, clientErr := a.newAPIClient(a.resolveAPIURL(linkConfig.APIURL), authConfig.APIKey)
		if clientErr != nil {
			checks = append(checks, doctorCheck{Name: "project_link", Status: doctorFail, Detail: clientErr.Error()})
			break
		}
		project, _, projectErr := client.GetProject(ctx, linkConfig.ProjectID)
		if projectErr != nil {
			checks = append(checks, doctorCheck{Name: "project_link", Status: doctorFail, Detail: fmt.Sprintf("linked project %s: %v", linkConfig.ProjectID, projectErr)})
			break
		}
		checks = append(checks, doctorCheck{Name: "project_link", Status: doctorPass, Detail: fmt.Sprintf("linked to %s (%s)", project.Name, project.ID)})
	}

	// 5. psql on PATH.
	if psqlPath, lookErr := exec.LookPath("psql"); lookErr != nil {
		checks = append(checks, doctorCheck{Name: "psql", Status: doctorFail, Detail: "psql not found in PATH; install the Postgres client tools (e.g. `brew install libpq` or `apt install postgresql-client`)"})
	} else {
		checks = append(checks, doctorCheck{Name: "psql", Status: doctorPass, Detail: psqlPath})
	}

	// 6. Env shadowing: the same key pointing at two databases across env files.
	// A half-migrated repo is otherwise silent - the app reads one file, tools
	// that pin a path read another - so this belongs in a health check, not
	// only in the one-shot migration scan.
	if conflicts, conflictErr := scan.DetectEnvConflicts(a.cwd); conflictErr != nil {
		checks = append(checks, doctorCheck{Name: "env_shadowing", Status: doctorSkip, Detail: conflictErr.Error()})
	} else if len(conflicts) == 0 {
		checks = append(checks, doctorCheck{Name: "env_shadowing", Status: doctorPass, Detail: "no env key points at more than one database"})
	} else {
		details := make([]string, 0, len(conflicts))
		for _, conflict := range conflicts {
			details = append(details, conflict.Describe())
		}
		checks = append(checks, doctorCheck{Name: "env_shadowing", Status: doctorFail, Detail: strings.Join(details, "; ")})
	}

	// 7. Database configuration across every stack in the repo. Of the things
	// unique to CapyDB, only cold-start wake latency is a runtime concern -
	// which URL migrations use, prepared statements through the pooler, and
	// client pool sizing are all CONFIGURATION, readable from disk. That makes
	// one linter cover Drizzle, Prisma, Rails, Django and raw postgres.js
	// instead of a per-language wrapper covering one, and it cannot break
	// anyone's runtime because it never executes their code.
	if findings, lintErr := configlint.Run(a.cwd); lintErr != nil {
		checks = append(checks, doctorCheck{Name: "db_config", Status: doctorSkip, Detail: lintErr.Error()})
	} else if len(findings) == 0 {
		checks = append(checks, doctorCheck{Name: "db_config", Status: doctorPass, Detail: "no connection or migration misconfiguration found"})
	} else {
		errorCount := 0
		details := make([]string, 0, len(findings))
		for _, finding := range findings {
			if finding.Severity == configlint.SeverityError {
				errorCount++
			}
			location := finding.File
			if finding.Line > 0 {
				location = fmt.Sprintf("%s:%d", finding.File, finding.Line)
			}
			detail := fmt.Sprintf("%s [%s] %s", location, finding.Rule, finding.Message)
			if finding.Fix != "" {
				detail += " -> " + finding.Fix
			}
			details = append(details, detail)
		}
		// Warnings alone must not fail the command: `doctor` is run in CI by
		// people who cannot act on every advisory finding immediately.
		status := doctorPass
		if errorCount > 0 {
			status = doctorFail
		}
		checks = append(checks, doctorCheck{Name: "db_config", Status: status, Detail: strings.Join(details, "; ")})
	}

	failed := 0
	for _, check := range checks {
		if check.Status == doctorFail {
			failed++
		}
	}

	if a.jsonOutput() {
		if err := printJSON(cmd.OutOrStdout(), map[string]any{
			"checks": jsonList(checks),
			"ok":     failed == 0,
		}); err != nil {
			return err
		}
	} else {
		for _, check := range checks {
			if strings.TrimSpace(check.Detail) != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s: %s\n", check.Status, check.Name, check.Detail)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", check.Status, check.Name)
			}
		}
	}

	if failed > 0 {
		return exitcode.Errorf(exitcode.GenericError, "%d of %d checks failed", failed, len(checks))
	}
	if !a.jsonOutput() {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "All checks passed.")
	}
	return nil
}
