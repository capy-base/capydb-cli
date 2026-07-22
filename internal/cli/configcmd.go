package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/config"
)

func (a *app) newConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Inspect the resolved CLI configuration",
	}

	showCommand := &cobra.Command{
		Use:   "show",
		Short: "Show the resolved CLI configuration (the API key is never printed)",
		Args:  cobra.NoArgs,
		RunE:  a.runConfigShow,
	}

	command.AddCommand(showCommand)
	return command
}

// configReport is the stable JSON shape for `capydb config show`. The API key
// itself is never included - only a last-4 fingerprint.
type configReport struct {
	ConfigPath        string `json:"config_path"`
	APIURL            string `json:"api_url"`
	AppURL            string `json:"app_url"`
	ActiveOrgID       string `json:"active_org_id,omitempty"`
	ActiveOrgName     string `json:"active_org_name,omitempty"`
	ActiveOrgSlug     string `json:"active_org_slug,omitempty"`
	APIKeyFingerprint string `json:"api_key_fingerprint,omitempty"`
	SavedOrgCount     int    `json:"saved_org_count"`
	LinkedProjectID   string `json:"linked_project_id,omitempty"`
	LinkedProjectName string `json:"linked_project_name,omitempty"`
}

func (a *app) runConfigShow(cmd *cobra.Command, args []string) error {
	configPath, err := config.UserConfigPath()
	if err != nil {
		return err
	}
	userConfig, err := config.LoadUserConfig()
	if err != nil {
		return err
	}

	report := configReport{
		ConfigPath:    configPath,
		APIURL:        a.resolveAPIURL(""),
		AppURL:        a.resolveAppURL(a.resolveAPIURL("")),
		SavedOrgCount: len(userConfig.Organizations),
	}

	if orgID, entry, ok := userConfig.Active(); ok {
		if orgID != config.DefaultOrgKey {
			report.ActiveOrgID = orgID
		}
		report.ActiveOrgName = entry.Name
		report.ActiveOrgSlug = entry.Slug
		report.APIKeyFingerprint = keyFingerprint(entry.APIKey)
	}
	// Flag/env credentials override the saved config; fingerprint what is
	// actually in effect.
	if override := firstNonEmpty(a.apiKey, os.Getenv("CAPYDB_API_KEY")); strings.TrimSpace(override) != "" {
		report.APIKeyFingerprint = keyFingerprint(override)
	}

	if linkConfig, linkErr := config.LoadProjectConfig(a.cwd); linkErr == nil {
		report.LinkedProjectID = linkConfig.ProjectID
		report.LinkedProjectName = linkConfig.ProjectName
	} else if !errors.Is(linkErr, os.ErrNotExist) {
		return linkErr
	}

	if a.jsonOutput() {
		return printJSON(cmd.OutOrStdout(), report)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "config_path: %s\n", report.ConfigPath)
	_, _ = fmt.Fprintf(out, "api_url: %s\n", report.APIURL)
	_, _ = fmt.Fprintf(out, "app_url: %s\n", report.AppURL)
	_, _ = fmt.Fprintf(out, "active_org_id: %s\n", firstNonEmpty(report.ActiveOrgID, "-"))
	_, _ = fmt.Fprintf(out, "active_org_name: %s\n", firstNonEmpty(report.ActiveOrgName, "-"))
	_, _ = fmt.Fprintf(out, "active_org_slug: %s\n", firstNonEmpty(report.ActiveOrgSlug, "-"))
	_, _ = fmt.Fprintf(out, "api_key_fingerprint: %s\n", firstNonEmpty(report.APIKeyFingerprint, "-"))
	_, _ = fmt.Fprintf(out, "saved_org_count: %d\n", report.SavedOrgCount)
	if strings.TrimSpace(report.LinkedProjectID) != "" {
		_, _ = fmt.Fprintf(out, "linked_project_id: %s\n", report.LinkedProjectID)
		_, _ = fmt.Fprintf(out, "linked_project_name: %s\n", firstNonEmpty(report.LinkedProjectName, "-"))
	} else {
		_, _ = fmt.Fprintf(out, "linked_project: -\n")
	}
	return nil
}

// keyFingerprint masks an API key down to its last four characters so the
// active credential can be identified without ever printing the secret.
func keyFingerprint(key string) string {
	trimmed := strings.TrimSpace(key)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return "****"
	}
	return "****" + trimmed[len(trimmed)-4:]
}
