package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
	"github.com/capy-base/capydb/cli/internal/config"
)

func (a *app) newPreviewCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "preview",
		Short: "Manage preview databases",
	}

	var listProjectRef string
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List preview databases for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, listProjectRef)
			if err != nil {
				return err
			}

			previews, err := client.ListPreviewDatabases(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("list previews: %w", err)
			}
			if len(previews) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No preview databases for project %s\n", project.Name)
				return nil
			}

			writePreviewTable(cmd.OutOrStdout(), previews)
			return nil
		},
	}
	listCommand.Flags().StringVar(&listProjectRef, "project", "", "Project id, slug, or name")

	var createMode string
	var createName string
	var createProjectRef string
	var createTTLHours int
	var createWait bool
	createCommand := &cobra.Command{
		Use:   "create",
		Short: "Create a preview database for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, createProjectRef)
			if err != nil {
				return err
			}

			preview, job, err := client.CreatePreviewDatabase(ctx, project.ID, api.CreatePreviewRequest{
				Mode:     strings.TrimSpace(createMode),
				Name:     strings.TrimSpace(createName),
				TTLHours: createTTLHours,
			})
			if err != nil {
				return fmt.Errorf("create preview: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created preview %s (%s) for project %s\n", preview.Preview.Name, preview.Preview.ID, project.Name)
			writePreviewTable(cmd.OutOrStdout(), []api.PreviewDetails{preview})

			if createWait && job.ID != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Waiting for job %s\n", job.ID)
				job, err = waitForJob(ctx, client, job.ID)
				if err != nil {
					return err
				}
				if err := ensureCompletedJob(job, "preview creation"); err != nil {
					return err
				}
				latestPreview, err := a.resolvePreview(ctx, client, project.ID, preview.Preview.ID)
				if err == nil {
					writePreviewTable(cmd.OutOrStdout(), []api.PreviewDetails{latestPreview})
				}
			}
			return nil
		},
	}
	createCommand.Flags().StringVar(&createProjectRef, "project", "", "Project id, slug, or name")
	createCommand.Flags().StringVar(&createMode, "mode", "", "Preview mode: empty or clone")
	createCommand.Flags().StringVar(&createName, "name", "", "Preview name")
	createCommand.Flags().IntVar(&createTTLHours, "ttl-hours", 0, "Preview TTL in hours")
	createCommand.Flags().BoolVar(&createWait, "wait", false, "Wait for the preview create job to finish")

	var deletePreviewRef string
	var deleteProjectRef string
	var deleteWait bool
	deleteCommand := &cobra.Command{
		Use:   "delete",
		Short: "Delete a preview database",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, deleteProjectRef)
			if err != nil {
				return err
			}
			preview, err := a.resolvePreview(ctx, client, project.ID, deletePreviewRef)
			if err != nil {
				return err
			}

			job, err := client.DeletePreviewDatabase(ctx, preview.Preview.ID)
			if err != nil {
				return fmt.Errorf("delete preview: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued delete for preview %s (%s)\n", preview.Preview.Name, preview.Preview.ID)
			return maybeWaitForJob(ctx, cmd.OutOrStdout(), client, job, deleteWait, "preview delete")
		},
	}
	deleteCommand.Flags().StringVar(&deleteProjectRef, "project", "", "Project id, slug, or name")
	deleteCommand.Flags().StringVar(&deletePreviewRef, "preview", "", "Preview id or name")
	deleteCommand.Flags().BoolVar(&deleteWait, "wait", false, "Wait for the preview delete job to finish")

	var resetPreviewRef string
	var resetProjectRef string
	var resetWait bool
	resetCommand := &cobra.Command{
		Use:   "reset",
		Short: "Reset a preview database",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, resetProjectRef)
			if err != nil {
				return err
			}
			preview, err := a.resolvePreview(ctx, client, project.ID, resetPreviewRef)
			if err != nil {
				return err
			}

			job, err := client.ResetPreviewDatabase(ctx, preview.Preview.ID)
			if err != nil {
				return fmt.Errorf("reset preview: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued reset for preview %s (%s)\n", preview.Preview.Name, preview.Preview.ID)
			return maybeWaitForJob(ctx, cmd.OutOrStdout(), client, job, resetWait, "preview reset")
		},
	}
	resetCommand.Flags().StringVar(&resetProjectRef, "project", "", "Project id, slug, or name")
	resetCommand.Flags().StringVar(&resetPreviewRef, "preview", "", "Preview id or name")
	resetCommand.Flags().BoolVar(&resetWait, "wait", false, "Wait for the preview reset job to finish")

	var extendTTLHours int
	extendCommand := &cobra.Command{
		Use:   "extend <preview-id>",
		Short: "Extend the TTL of a preview database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			previewID := strings.TrimSpace(args[0])
			if previewID == "" {
				return fmt.Errorf("preview id is required")
			}
			if extendTTLHours < 1 || extendTTLHours > 168 {
				return fmt.Errorf("--ttl-hours must be between 1 and 168")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			preview, err := client.ExtendPreviewDatabase(ctx, previewID, extendTTLHours)
			if err != nil {
				return fmt.Errorf("extend preview: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Extended preview %s (%s) by %dh\n", preview.Name, preview.ID, extendTTLHours)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "ttl_expires_at: %s\n", formatTime(preview.TTLExpiresAt))
			return nil
		},
	}
	extendCommand.Flags().IntVar(&extendTTLHours, "ttl-hours", 0, "New TTL in hours (1-168)")

	command.AddCommand(listCommand)
	command.AddCommand(createCommand)
	command.AddCommand(deleteCommand)
	command.AddCommand(resetCommand)
	command.AddCommand(extendCommand)
	return command
}

func (a *app) newBackupsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "backups",
		Aliases: []string{"backup"},
		Short:   "Manage project backups",
	}

	var listProjectRef string
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List backups for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, listProjectRef)
			if err != nil {
				return err
			}

			backups, err := client.ListBackups(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("list backups: %w", err)
			}
			if len(backups) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No backups for project %s\n", project.Name)
				return nil
			}

			writeBackupTable(cmd.OutOrStdout(), backups)
			return nil
		},
	}
	listCommand.Flags().StringVar(&listProjectRef, "project", "", "Project id, slug, or name")

	var createLabel string
	var createProjectRef string
	var createWait bool
	createCommand := &cobra.Command{
		Use:   "create",
		Short: "Queue a backup for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, createProjectRef)
			if err != nil {
				return err
			}

			job, err := client.CreateBackup(ctx, project.ID, createLabel)
			if err != nil {
				return fmt.Errorf("create backup: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued backup job %s for project %s\n", job.ID, project.Name)
			return maybeWaitForJob(ctx, cmd.OutOrStdout(), client, job, createWait, "backup")
		},
	}
	createCommand.Flags().StringVar(&createProjectRef, "project", "", "Project id, slug, or name")
	createCommand.Flags().StringVar(&createLabel, "label", "", "Optional backup label")
	createCommand.Flags().BoolVar(&createWait, "wait", false, "Wait for the backup job to finish")

	command.AddCommand(listCommand)
	command.AddCommand(createCommand)
	return command
}

func (a *app) newImportCommand() *cobra.Command {
	var projectRef string
	var recreate bool
	var sourceURL string
	var wait bool

	command := &cobra.Command{
		Use:   "import",
		Short: "Import data from another Postgres database into a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(sourceURL) == "" {
				return fmt.Errorf("--source-url is required")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, projectRef)
			if err != nil {
				return err
			}

			job, err := client.CreateImport(ctx, project.ID, sourceURL, recreate)
			if err != nil {
				return fmt.Errorf("create import: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued import job %s for project %s\n", job.ID, project.Name)
			return maybeWaitForJob(ctx, cmd.OutOrStdout(), client, job, wait, "import")
		},
	}

	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().StringVar(&sourceURL, "source-url", "", "Source Postgres connection string")
	command.Flags().BoolVar(&recreate, "recreate", false, "Recreate the target database before import")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for the import job to finish")
	return command
}

func (a *app) newRestoreCommand() *cobra.Command {
	var backupKey string
	var confirmProjectOverwrite bool
	var previewName string
	var previewRef string
	var projectRef string
	var recreate bool
	var restorePointID string
	var restoreTime string
	var targetKind string
	var ttlHours int
	var wait bool

	command := &cobra.Command{
		Use:   "restore",
		Short: "Restore a backup into a project or preview database",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			trimmedBackupKey := strings.TrimSpace(backupKey)
			trimmedRestoreTime := strings.TrimSpace(restoreTime)
			trimmedRestorePoint := strings.TrimSpace(restorePointID)
			provided := 0
			if trimmedBackupKey != "" {
				provided++
			}
			if trimmedRestoreTime != "" {
				provided++
			}
			if trimmedRestorePoint != "" {
				provided++
			}
			if provided != 1 {
				return fmt.Errorf("exactly one of --backup-key, --restore-time, or --restore-point is required")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, projectRef)
			if err != nil {
				return err
			}

			resolvedKind, err := resolveRestoreTargetKind(targetKind, previewRef, previewName)
			if err != nil {
				return err
			}

			request := api.CreateRestoreRequest{
				BackupKey:               trimmedBackupKey,
				ConfirmProjectOverwrite: confirmProjectOverwrite,
				PreviewName:             strings.TrimSpace(previewName),
				Recreate:                recreate,
				RestorePointID:          trimmedRestorePoint,
				RestoreTime:             trimmedRestoreTime,
				TargetKind:              resolvedKind,
				TTLHours:                ttlHours,
			}
			if resolvedKind == "preview" {
				preview, err := a.resolvePreview(ctx, client, project.ID, previewRef)
				if err != nil {
					return err
				}
				request.PreviewID = preview.Preview.ID
			}

			if resolvedKind == "project" {
				confirmed, err := confirmProjectRestoreOverwrite(cmd, project, confirmProjectOverwrite)
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("project overwrite not confirmed; pass --confirm-project-overwrite or confirm interactively")
				}
				request.ConfirmProjectOverwrite = true
			}

			job, err := client.CreateRestore(ctx, project.ID, request)
			if err != nil {
				return fmt.Errorf("create restore: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued restore job %s for project %s\n", job.ID, project.Name)
			return maybeWaitForJob(ctx, cmd.OutOrStdout(), client, job, wait, "restore")
		},
	}

	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().StringVar(&backupKey, "backup-key", "", "Backup key to restore")
	command.Flags().StringVar(&restoreTime, "restore-time", "", "RFC3339 timestamp for point-in-time restore")
	command.Flags().StringVar(&restorePointID, "restore-point", "", "Labelled restore point id (alternative to --backup-key/--restore-time)")
	command.Flags().StringVar(&targetKind, "target-kind", "", "Restore target: project, preview, or new_preview (inferred from --preview/--preview-name; defaults to new_preview)")
	command.Flags().StringVar(&previewRef, "preview", "", "Existing preview id or name when target-kind is preview")
	command.Flags().StringVar(&previewName, "preview-name", "", "Preview name when target-kind is new_preview")
	command.Flags().IntVar(&ttlHours, "ttl-hours", 0, "Preview TTL in hours when target-kind is new_preview")
	command.Flags().BoolVar(&confirmProjectOverwrite, "confirm-project-overwrite", false, "Confirm overwriting the project database")
	command.Flags().BoolVar(&recreate, "recreate", false, "Recreate the target before restore")
	command.Flags().BoolVar(&wait, "wait", false, "Wait for the restore job to finish")
	return command
}

// resolveRestoreTargetKind validates an explicit --target-kind, or infers a safe
// default when one is not given. The backend requires target_kind to be one of
// project|preview|new_preview, so a bare `restore --backup-key X` would otherwise
// fail with a raw 400. Inference: --preview implies preview, --preview-name implies
// new_preview, and the safe non-destructive new_preview is used as the fallback.
func resolveRestoreTargetKind(targetKind, previewRef, previewName string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(targetKind)) {
	case "project", "preview", "new_preview":
		return strings.ToLower(strings.TrimSpace(targetKind)), nil
	case "":
		switch {
		case strings.TrimSpace(previewRef) != "":
			return "preview", nil
		case strings.TrimSpace(previewName) != "":
			return "new_preview", nil
		default:
			return "new_preview", nil
		}
	default:
		return "", fmt.Errorf("--target-kind must be one of project, preview, or new_preview")
	}
}

// confirmProjectRestoreOverwrite guards the destructive project restore path. The
// --confirm-project-overwrite flag is honoured for non-interactive (CI) usage. On
// an interactive terminal without the flag, the operator is prompted to retype the
// project name (or "yes") to confirm before the destructive restore is sent.
func confirmProjectRestoreOverwrite(cmd *cobra.Command, project api.Project, flagConfirmed bool) (bool, error) {
	if flagConfirmed {
		return true, nil
	}
	if !stdinIsInteractive() {
		return false, nil
	}

	_, _ = fmt.Fprintf(
		cmd.ErrOrStderr(),
		"This will OVERWRITE the live database for project %q (%s).\n",
		project.Name,
		project.ID,
	)
	answer, err := promptLine(fmt.Sprintf("Type the project name %q (or \"yes\") to confirm", project.Name))
	if err != nil {
		return false, err
	}
	trimmed := strings.TrimSpace(answer)
	if strings.EqualFold(trimmed, "yes") || trimmed == strings.TrimSpace(project.Name) {
		return true, nil
	}
	return false, nil
}

func (a *app) newRestorePointsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "restore-points",
		Aliases: []string{"restore-point"},
		Short:   "Manage labelled restore points",
	}

	var listProjectRef string
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List restore points for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, listProjectRef)
			if err != nil {
				return err
			}
			points, err := client.ListRestorePoints(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("list restore points: %w", err)
			}
			if len(points) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No restore points for project %s\n", project.Name)
				return nil
			}
			writeRestorePointTable(cmd.OutOrStdout(), points)
			return nil
		},
	}
	listCommand.Flags().StringVar(&listProjectRef, "project", "", "Project id, slug, or name")

	var createProjectRef string
	var createKind string
	var createLabel string
	var createBackupKey string
	var createPITRTime string
	var createNote string
	createCommand := &cobra.Command{
		Use:   "create",
		Short: "Create a labelled restore point",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(createLabel) == "" {
				return fmt.Errorf("--label is required")
			}
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, createProjectRef)
			if err != nil {
				return err
			}
			point, err := client.CreateRestorePoint(ctx, project.ID, api.CreateRestorePointRequest{
				BackupKey: strings.TrimSpace(createBackupKey),
				Kind:      strings.TrimSpace(createKind),
				Label:     strings.TrimSpace(createLabel),
				Note:      strings.TrimSpace(createNote),
				PITRTime:  strings.TrimSpace(createPITRTime),
			})
			if err != nil {
				return fmt.Errorf("create restore point: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created restore point %s (%s)\n", point.ID, point.Label)
			return nil
		},
	}
	createCommand.Flags().StringVar(&createProjectRef, "project", "", "Project id, slug, or name")
	createCommand.Flags().StringVar(&createLabel, "label", "", "Restore point label (required)")
	createCommand.Flags().StringVar(&createKind, "kind", "", "Restore point kind: backup or pitr (inferred if omitted)")
	createCommand.Flags().StringVar(&createBackupKey, "backup-key", "", "Backup key to anchor against (kind=backup)")
	createCommand.Flags().StringVar(&createPITRTime, "pitr-time", "", "RFC3339 timestamp (kind=pitr; defaults to now)")
	createCommand.Flags().StringVar(&createNote, "note", "", "Optional note")

	var deleteProjectRef string
	var deleteRestorePointID string
	deleteCommand := &cobra.Command{
		Use:   "delete",
		Short: "Delete a restore point",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(deleteRestorePointID) == "" {
				return fmt.Errorf("--id is required")
			}
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, deleteProjectRef)
			if err != nil {
				return err
			}
			if err := client.DeleteRestorePoint(ctx, project.ID, strings.TrimSpace(deleteRestorePointID)); err != nil {
				return fmt.Errorf("delete restore point: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted restore point %s\n", deleteRestorePointID)
			return nil
		},
	}
	deleteCommand.Flags().StringVar(&deleteProjectRef, "project", "", "Project id, slug, or name")
	deleteCommand.Flags().StringVar(&deleteRestorePointID, "id", "", "Restore point id")

	command.AddCommand(listCommand)
	command.AddCommand(createCommand)
	command.AddCommand(deleteCommand)
	return command
}

func writeRestorePointTable(out io.Writer, points []api.RestorePoint) {
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLABEL\tKIND\tANCHOR\tSTATE\tCREATED")
	for _, point := range points {
		anchor := ""
		switch point.Kind {
		case "backup":
			anchor = point.BackupKey
			if anchor == "" {
				anchor = point.BackupID
			}
		case "pitr":
			if point.PITRTime != nil {
				anchor = point.PITRTime.UTC().Format(time.RFC3339)
			}
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			point.ID,
			point.Label,
			point.Kind,
			anchor,
			point.State,
			point.CreatedAt.UTC().Format(time.RFC3339),
		)
	}
	_ = tw.Flush()
}

func (a *app) newJobsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "jobs",
		Short: "Inspect async jobs",
	}

	var jobID string
	var wait bool
	getCommand := &cobra.Command{
		Use:     "get",
		Aliases: []string{"status"},
		Short:   "Get the current state of a job",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(jobID) == "" {
				return fmt.Errorf("--job-id is required")
			}

			client, _, err := a.resolveClient(true)
			if err != nil {
				return err
			}

			job, err := client.GetJob(ctx, strings.TrimSpace(jobID))
			if err != nil {
				return fmt.Errorf("get job: %w", err)
			}
			if wait && !jobDone(job) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Waiting for job %s\n", job.ID)
				job, err = waitForJob(ctx, client, job.ID)
				if err != nil {
					return err
				}
			}

			writeJob(cmd.OutOrStdout(), job)
			return nil
		},
	}
	getCommand.Flags().StringVar(&jobID, "job-id", "", "Job id")
	getCommand.Flags().BoolVar(&wait, "wait", false, "Wait for the job to finish before printing")

	command.AddCommand(getCommand)
	return command
}

func (a *app) newStudioCommand() *cobra.Command {
	var page string
	var printOnly bool
	var projectRef string

	command := &cobra.Command{
		Use:   "studio",
		Short: "Open the dashboard page for a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			var authConfig resolvedAuth
			var projectID string
			if strings.TrimSpace(projectRef) == "" {
				linkConfig, linkErr := config.LoadProjectConfig(a.cwd)
				if linkErr != nil {
					if errors.Is(linkErr, os.ErrNotExist) {
						return fmt.Errorf("no linked project found; pass --project or run `capydb link` first")
					}
					return linkErr
				}
				projectID = linkConfig.ProjectID
				authConfig.APIURL = a.resolveAPIURL(linkConfig.APIURL)
			} else {
				client, resolved, err := a.resolveClient(true)
				if err != nil {
					return err
				}
				authConfig = resolved
				project, err := a.resolveProject(ctx, client, projectRef)
				if err != nil {
					return err
				}
				projectID = project.ID
			}

			rawURL, err := buildDashboardURL(a.resolveAppURL(authConfig.APIURL), projectID, page)
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintln(cmd.OutOrStdout(), rawURL)
			if printOnly {
				return nil
			}
			if err := openURL(rawURL); err != nil {
				return fmt.Errorf("open browser: %w", err)
			}
			return nil
		},
	}

	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().StringVar(&page, "page", "connections", "Dashboard page: overview, connections, studio, previews, backups, or settings")
	command.Flags().BoolVar(&printOnly, "print-only", false, "Print the URL without opening it")
	return command
}

func (a *app) resolveClient(prompt bool, fallbackAPIURL ...string) (*api.Client, resolvedAuth, error) {
	authConfig, err := a.resolveAuth(prompt, fallbackAPIURL...)
	if err != nil {
		return nil, resolvedAuth{}, err
	}

	client := api.NewClient(authConfig.APIURL, authConfig.APIKey)
	if err := a.persistResolvedAuth(authConfig); err != nil {
		return nil, resolvedAuth{}, err
	}
	return client, authConfig, nil
}

func (a *app) linkedProjectAPIURL() string {
	linkConfig, err := config.LoadProjectConfig(a.cwd)
	if err != nil {
		return ""
	}
	return linkConfig.APIURL
}

func (a *app) resolveProject(ctx context.Context, client *api.Client, ref string) (api.Project, error) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		linkConfig, err := config.LoadProjectConfig(a.cwd)
		if err == nil && strings.TrimSpace(linkConfig.ProjectID) != "" {
			project, _, err := client.GetProject(ctx, linkConfig.ProjectID)
			if err != nil {
				return api.Project{}, fmt.Errorf("fetch linked project: %w", err)
			}
			return project, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return api.Project{}, err
		}
	}

	projects, err := client.ListProjects(ctx, "")
	if err != nil {
		return api.Project{}, fmt.Errorf("list projects: %w", err)
	}
	if len(projects) == 0 {
		return api.Project{}, fmt.Errorf("no projects available for this api key")
	}

	if trimmed == "" {
		if len(projects) == 1 {
			return projects[0], nil
		}
		if stdinIsInteractive() {
			return selectProject(projects)
		}
		return api.Project{}, fmt.Errorf("multiple projects are available; pass --project or run the command inside a linked project")
	}

	var matches []api.Project
	for _, project := range projects {
		switch {
		case project.ID == trimmed:
			return project, nil
		case strings.EqualFold(project.Slug, trimmed):
			matches = append(matches, project)
		case strings.EqualFold(project.Name, trimmed):
			matches = append(matches, project)
		}
	}

	switch len(matches) {
	case 0:
		return api.Project{}, fmt.Errorf("project %q not found", trimmed)
	case 1:
		return matches[0], nil
	default:
		if stdinIsInteractive() {
			return selectProject(matches)
		}
		return api.Project{}, fmt.Errorf("project %q is ambiguous; use the project id instead", trimmed)
	}
}

func (a *app) resolvePreview(ctx context.Context, client *api.Client, projectID, ref string) (api.PreviewDetails, error) {
	previews, err := client.ListPreviewDatabases(ctx, projectID)
	if err != nil {
		return api.PreviewDetails{}, fmt.Errorf("list previews: %w", err)
	}
	if len(previews) == 0 {
		return api.PreviewDetails{}, fmt.Errorf("no preview databases found")
	}

	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		if len(previews) == 1 {
			return previews[0], nil
		}
		return api.PreviewDetails{}, fmt.Errorf("multiple preview databases are available; pass --preview")
	}

	var matches []api.PreviewDetails
	for _, preview := range previews {
		switch {
		case preview.Preview.ID == trimmed:
			return preview, nil
		case strings.EqualFold(preview.Preview.Name, trimmed):
			matches = append(matches, preview)
		}
	}

	switch len(matches) {
	case 0:
		return api.PreviewDetails{}, fmt.Errorf("preview %q not found", trimmed)
	case 1:
		return matches[0], nil
	default:
		return api.PreviewDetails{}, fmt.Errorf("preview %q is ambiguous; use the preview id instead", trimmed)
	}
}

func (a *app) resolveAppURL(apiURL string) string {
	if value := strings.TrimSpace(a.appURL); value != "" {
		return strings.TrimRight(value, "/")
	}
	if value := strings.TrimSpace(os.Getenv("CAPYDB_APP_URL")); value != "" {
		return strings.TrimRight(value, "/")
	}
	userConfig, err := config.LoadUserConfig()
	if err == nil && strings.TrimSpace(userConfig.AppURL) != "" {
		return strings.TrimRight(strings.TrimSpace(userConfig.AppURL), "/")
	}
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if trimmed, ok := strings.CutSuffix(apiURL, "/api/capydb"); ok {
		return trimmed
	}
	return apiURL
}

func buildDashboardURL(appURL, projectID, page string) (string, error) {
	appURL = strings.TrimRight(strings.TrimSpace(appURL), "/")
	if appURL == "" {
		return "", fmt.Errorf("could not determine the dashboard URL; set CAPYDB_APP_URL")
	}

	basePath := "/dashboard/projects/" + strings.TrimSpace(projectID)
	switch strings.ToLower(strings.TrimSpace(page)) {
	case "", "overview":
		return appURL + basePath, nil
	case "connections", "studio", "previews", "backups", "settings":
		return appURL + basePath + "/" + strings.ToLower(strings.TrimSpace(page)), nil
	default:
		return "", fmt.Errorf("page must be one of overview, connections, studio, previews, backups, or settings")
	}
}

func maybeWaitForJob(ctx context.Context, out io.Writer, client *api.Client, job api.Job, wait bool, action string) error {
	writeJob(out, job)
	if !wait || job.ID == "" || jobDone(job) {
		return nil
	}

	_, _ = fmt.Fprintf(out, "Waiting for job %s\n", job.ID)
	job, err := waitForJob(ctx, client, job.ID)
	if err != nil {
		return err
	}
	writeJob(out, job)
	return ensureCompletedJob(job, action)
}

func ensureCompletedJob(job api.Job, action string) error {
	if job.State != "completed" {
		return fmt.Errorf("%s ended in %s: %s", action, job.State, job.Error)
	}
	return nil
}

func selectProject(projects []api.Project) (api.Project, error) {
	fmt.Println("Select a project:")
	for index, project := range projects {
		fmt.Printf("  %d. %s (%s, %s)\n", index+1, project.Name, firstNonEmpty(project.Slug, "-"), project.ID)
	}

	value, err := promptLine("Project number")
	if err != nil {
		return api.Project{}, err
	}
	selection, err := strconv.Atoi(value)
	if err != nil || selection < 1 || selection > len(projects) {
		return api.Project{}, fmt.Errorf("invalid project selection")
	}
	return projects[selection-1], nil
}

func jobDone(job api.Job) bool {
	switch job.State {
	case "completed", "failed":
		return true
	default:
		return false
	}
}

func writeBackupTable(out io.Writer, backups []api.Backup) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tLABEL\tSTATE\tVERIFY\tSIZE\tCREATED_AT\tBACKUP_KEY")
	for _, backup := range backups {
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			backup.ID,
			firstNonEmpty(backup.Label, "-"),
			backup.State,
			firstNonEmpty(backup.VerificationState, "-"),
			formatBytes(backup.SizeBytes),
			formatTime(backup.CreatedAt),
			backup.BackupKey,
		)
	}
	_ = writer.Flush()
}

func writeJob(out io.Writer, job api.Job) {
	_, _ = fmt.Fprintf(out, "job_id: %s\n", job.ID)
	_, _ = fmt.Fprintf(out, "type: %s\n", firstNonEmpty(job.Type, "-"))
	_, _ = fmt.Fprintf(out, "state: %s\n", firstNonEmpty(job.State, "-"))
	if strings.TrimSpace(job.ProjectID) != "" {
		_, _ = fmt.Fprintf(out, "project_id: %s\n", job.ProjectID)
	}
	if strings.TrimSpace(job.Error) != "" {
		_, _ = fmt.Fprintf(out, "error: %s\n", job.Error)
	}
}

func writePreviewTable(out io.Writer, previews []api.PreviewDetails) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tNAME\tMODE\tSTATE\tTTL_EXPIRES_AT\tDIRECT_URL\tPOOLED_URL")
	for _, preview := range previews {
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			preview.Preview.ID,
			preview.Preview.Name,
			preview.Preview.Mode,
			preview.Preview.State,
			formatTime(preview.Preview.TTLExpiresAt),
			firstNonEmpty(preview.Connections.DirectURL, "-"),
			firstNonEmpty(preview.Connections.PooledURL, "-"),
		)
	}
	_ = writer.Flush()
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}

func formatBytes(value int64) string {
	if value <= 0 {
		return "0 B"
	}
	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	index := 0
	for size >= unit && index < len(units)-1 {
		size /= unit
		index++
	}
	if size >= 10 || index == 0 {
		return fmt.Sprintf("%.0f %s", size, units[index])
	}
	return fmt.Sprintf("%.1f %s", size, units[index])
}

func openURL(rawURL string) error {
	var command *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", rawURL)
	case "linux":
		command = exec.Command("xdg-open", rawURL)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}

	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
