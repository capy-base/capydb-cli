package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/capydatabase/capydb-cli/internal/api"
)

func (a *app) newCredentialsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "credentials",
		Short: "Manage project database credentials",
	}

	var confirmFlag bool
	var graceHours int
	var projectRef string
	var wait bool
	var waitTimeout time.Duration
	var yes bool
	rotateCommand := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the project's database password",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			project, err := a.resolveProject(ctx, client, projectRef)
			if err != nil {
				return err
			}

			confirmed, err := confirmCredentialRotation(cmd, project, graceHours, yes || confirmFlag)
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("credential rotation not confirmed; pass --confirm or confirm interactively")
			}

			if graceHours < 0 || graceHours > 720 {
				return fmt.Errorf("--grace-hours must be between 0 and 720")
			}

			job, err := client.RotateCredentials(ctx, project.ID, graceHours)
			if err != nil {
				return fmt.Errorf("rotate credentials: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued credential rotation job %s for project %s\n", job.ID, project.Name)
				if graceHours > 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "The current credential keeps working for %d more hours; a new database username is issued now.\n", graceHours)
				}
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Fetch the new connection string with `capydb connection-string` once the job completes.")
			}
			return a.maybeWaitForJob(cmd, client, job, wait, waitTimeout, "credential rotation")
		},
	}
	rotateCommand.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	rotateCommand.Flags().BoolVar(&confirmFlag, "confirm", false, "Confirm the rotation without prompting")
	rotateCommand.Flags().IntVar(&graceHours, "grace-hours", 0, "Keep the outgoing credential valid this many hours (0 = immediate cutover, max 720). Issues a new database username.")
	// Deprecated spelling kept as a hidden alias: --confirm is the standard
	// destructive-confirm flag across capydb commands.
	rotateCommand.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	_ = rotateCommand.Flags().MarkHidden("yes")
	addWaitFlags(rotateCommand, &wait, &waitTimeout, "credential rotation")

	command.AddCommand(rotateCommand)
	return command
}

// confirmCredentialRotation guards the destructive rotation: every existing
// connection string stops working once the job completes. The --yes flag is
// honoured for non-interactive (CI) usage. On an interactive terminal without
// the flag, the operator is prompted to retype the project name (or "yes") to
// confirm before the rotation is sent.
func confirmCredentialRotation(cmd *cobra.Command, project api.Project, graceHours int, flagConfirmed bool) (bool, error) {
	if flagConfirmed {
		return true, nil
	}
	if !stdinIsInteractive() {
		return false, nil
	}

	if graceHours > 0 {
		_, _ = fmt.Fprintf(
			cmd.ErrOrStderr(),
			"This will ROTATE the database credential for project %q (%s); the current connection string keeps working for %d more hours, then stops.\n",
			project.Name,
			project.ID,
			graceHours,
		)
	} else {
		_, _ = fmt.Fprintf(
			cmd.ErrOrStderr(),
			"This will ROTATE the database password for project %q (%s); existing connection strings stop working.\n",
			project.Name,
			project.ID,
		)
	}
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
