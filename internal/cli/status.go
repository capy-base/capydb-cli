package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
	"github.com/capy-base/capydb/cli/internal/config"
)

type statusOptions struct {
	projectRef string
	remote     bool
}

func (a *app) runStatus(cmd *cobra.Command, options statusOptions) error {
	ctx := cmd.Context()
	userConfig, userErr := config.LoadUserConfig()
	projectConfig, projectErr := config.LoadProjectConfig(a.cwd)

	if userErr != nil && !errors.Is(userErr, os.ErrNotExist) {
		return userErr
	}
	if projectErr != nil && !errors.Is(projectErr, os.ErrNotExist) {
		return projectErr
	}

	if strings.TrimSpace(userConfig.APIKey) != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged_in: yes\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_url: %s\n", userConfig.APIURL)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "app_url: %s\n", userConfig.AppURL)
		if strings.TrimSpace(userConfig.OrganizationID) != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "organization_id: %s\n", userConfig.OrganizationID)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "organization_name: %s\n", firstNonEmpty(userConfig.OrganizationName, "-"))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "organization_slug: %s\n", firstNonEmpty(userConfig.OrganizationSlug, "-"))
		}
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "logged_in: no\n")
	}

	linked := !errors.Is(projectErr, os.ErrNotExist)
	if !linked {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "linked_project: no\n")
	} else {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "linked_project: yes\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project_id: %s\n", projectConfig.ProjectID)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project_name: %s\n", projectConfig.ProjectName)
		if strings.TrimSpace(projectConfig.ProjectSlug) != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project_slug: %s\n", projectConfig.ProjectSlug)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "app_path: %s\n", firstNonEmpty(projectConfig.AppPath, "."))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "profile: %s\n", firstNonEmpty(projectConfig.Profile, projectConfig.Framework))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "framework: %s\n", firstNonEmpty(projectConfig.Framework, "-"))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "database_layer: %s\n", firstNonEmpty(projectConfig.DatabaseLayer, "-"))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "env_file: %s\n", projectConfig.EnvFile)
	}

	if !options.remote && strings.TrimSpace(options.projectRef) == "" {
		return nil
	}

	apiURL := a.resolveAPIURL(firstNonEmpty(projectConfig.APIURL, userConfig.APIURL))
	publicStatus, err := api.NewClient(apiURL, "").GetPublicStatus(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_status: unavailable\n")
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_status_error: %s\n", err)
	} else {
		writePublicStatus(cmd, publicStatus)
	}

	if strings.TrimSpace(options.projectRef) == "" && !linked {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project: not_checked\n")
		return nil
	}

	client, _, err := a.resolveClient(false, apiURL)
	if err != nil {
		return err
	}

	project, err := a.resolveProject(ctx, client, options.projectRef)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project: yes\n")
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project_id: %s\n", project.ID)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project_name: %s\n", project.Name)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project_state: %s\n", firstNonEmpty(project.State, "-"))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project_environment: %s\n", firstNonEmpty(project.Environment, "-"))
	if strings.TrimSpace(project.LastError) != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "remote_project_error: %s\n", project.LastError)
	}

	if strings.TrimSpace(project.LatestJobID) != "" {
		job, err := client.GetJob(ctx, project.LatestJobID)
		if err != nil {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_job_error: %s\n", err)
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_job_id: %s\n", job.ID)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_job_type: %s\n", firstNonEmpty(job.Type, "-"))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_job_state: %s\n", firstNonEmpty(job.State, "-"))
		}
	}

	observability, err := client.GetProjectObservability(ctx, project.ID)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "observability_error: %s\n", err)
	} else {
		writeProjectObservability(cmd, observability)
	}

	backups, err := client.ListBackups(ctx, project.ID)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "backups_error: %s\n", err)
		return nil
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "backup_count: %d\n", len(backups))
	if latest, ok := latestBackup(backups); ok {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_backup_id: %s\n", latest.ID)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_backup_state: %s\n", firstNonEmpty(latest.State, "-"))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_backup_verify: %s\n", firstNonEmpty(latest.VerificationState, "-"))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "latest_backup_created_at: %s\n", formatTime(latest.CreatedAt))
	}
	return nil
}

func writePublicStatus(cmd *cobra.Command, status api.PublicStatusResponse) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_status: %s\n", firstNonEmpty(status.Status, "-"))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_checked_at: %s\n", formatTime(status.UpdatedAt))
	for _, component := range status.Components {
		key := strings.ToLower(strings.NewReplacer(" ", "_", "-", "_").Replace(component.Name))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_component_%s: %s\n", key, firstNonEmpty(component.Status, "-"))
		if strings.TrimSpace(component.Message) != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_component_%s_message: %s\n", key, component.Message)
		}
	}
}

func writeProjectObservability(cmd *cobra.Command, observability api.ProjectObservability) {
	_, _ = fmt.Fprintf(
		cmd.OutOrStdout(),
		"connections: %d/%d\n",
		observability.ConnectionCount,
		observability.ConnectionLimit,
	)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "connection_usage: %s\n", formatPercent(observability.ConnectionUsagePercent))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "database_size: %s\n", formatBytes(observability.DatabaseSizeBytes))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "storage_limit: %s\n", formatBytes(observability.StorageLimitBytes))
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "storage_usage: %s\n", formatPercent(observability.StorageUsagePercent))
	for _, alert := range observability.Alerts {
		if strings.TrimSpace(alert) != "" {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "alert: %s\n", alert)
		}
	}
}

func latestBackup(backups []api.Backup) (api.Backup, bool) {
	if len(backups) == 0 {
		return api.Backup{}, false
	}

	latest := backups[0]
	for _, backup := range backups[1:] {
		if backup.CreatedAt.After(latest.CreatedAt) {
			latest = backup
		}
	}
	return latest, true
}

func formatPercent(value float64) string {
	return fmt.Sprintf("%.1f%%", value)
}
