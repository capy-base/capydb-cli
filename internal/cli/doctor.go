package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/config"
	"github.com/capy-base/capydb/cli/internal/exitcode"
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
