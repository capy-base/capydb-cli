package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

// newUpgradeCommand groups the PostgreSQL version operations.
//
// Minor and major are separate commands because they are separate risks, and
// collapsing them behind one verb would hide that. A minor is a restart onto a
// binary-compatible release; a major rewrites the on-disk format and is a
// migration that must be checked first.
func (a *app) newUpgradeCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Manage PostgreSQL version upgrades",
		Long: "PostgreSQL versions move in two very different ways.\n\n" +
			"Minor releases (17.4 -> 17.5) are binary-compatible: your data is untouched and the " +
			"upgrade is a restart. Security fixes ship as minors. A paused database picks the new " +
			"version up on its own when it next resumes, so most databases need no action at all.\n\n" +
			"Major releases (17 -> 18) change the on-disk format and are a real migration, so they " +
			"are always started by you and are checked before anything moves.",
	}

	var minorProjectRef string
	var minorWait bool
	var minorWaitTimeout time.Duration
	minorCommand := &cobra.Command{
		Use:   "minor",
		Short: "Restart the database onto the latest available PostgreSQL minor",
		Long: "Applies a pending minor upgrade by restarting the database onto the version the " +
			"platform already has installed.\n\n" +
			"Minors are binary-compatible, so nothing is migrated - only the server binary changes. " +
			"The cost is a brief interruption to open connections. A database that is currently " +
			"paused does not need this: it starts on the new version when it next resumes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, minorProjectRef)
			if err != nil {
				return err
			}

			job, err := client.UpgradeProjectMinor(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("upgrade minor: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued minor upgrade for project %s\n", project.Name)
			}
			return a.maybeWaitForJob(cmd, client, job, minorWait, minorWaitTimeout, "minor upgrade")
		},
	}
	minorCommand.Flags().StringVar(&minorProjectRef, "project", "", "Project id, slug, or name")
	addWaitFlags(minorCommand, &minorWait, &minorWaitTimeout, "minor upgrade")

	var preflightProjectRef string
	var preflightTarget int
	var preflightWait bool
	var preflightWaitTimeout time.Duration
	preflightCommand := &cobra.Command{
		Use:   "preflight",
		Short: "Check whether the database can move to a PostgreSQL major",
		Long: "Runs a read-only check and reports whether a major upgrade would succeed.\n\n" +
			"Nothing is changed, so it is safe to run repeatedly. The blocker that matters most in " +
			"practice is extensions: if one you use has no build for the target major, migrating " +
			"would leave your schema referencing types and functions that no longer exist.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if preflightTarget <= 0 {
				return usageErrorf("--target-major is required (e.g. --target-major 18)")
			}
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, preflightProjectRef)
			if err != nil {
				return err
			}

			job, err := client.MajorUpgradePreflight(ctx, project.ID, preflightTarget)
			if err != nil {
				return fmt.Errorf("major upgrade preflight: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"Queued major-upgrade preflight (target %d) for project %s\n", preflightTarget, project.Name)
			}
			// Default to waiting: a preflight the caller never reads is useless,
			// and the verdict lives in the job result.
			return a.maybeWaitForJob(cmd, client, job, preflightWait, preflightWaitTimeout, "major upgrade preflight")
		},
	}
	preflightCommand.Flags().StringVar(&preflightProjectRef, "project", "", "Project id, slug, or name")
	preflightCommand.Flags().IntVar(&preflightTarget, "target-major", 0, "PostgreSQL major to evaluate (e.g. 18)")
	addWaitFlags(preflightCommand, &preflightWait, &preflightWaitTimeout, "major upgrade preflight")

	var majorProjectRef string
	var majorTarget int
	var majorConfirm bool
	var majorWait bool
	var majorWaitTimeout time.Duration
	majorCommand := &cobra.Command{
		Use:   "major",
		Short: "Upgrade the database to a new PostgreSQL major",
		Long: "Starts a major-version upgrade. Your database is not upgraded in place: a new " +
			"database is built on the target version, your data is copied into it and verified, " +
			"and only then is the project pointed at it. The connection string does not change.\n\n" +
			"The previous version is kept until you run `capydb upgrade confirm`, so reverting " +
			"with `capydb upgrade rollback` is instant and loses nothing until then.\n\n" +
			"Run `capydb upgrade preflight` first: it reports every reason the migration could " +
			"fail - most importantly an extension with no build for the target major - before " +
			"anything moves.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if majorTarget <= 0 {
				return usageErrorf("--target-major is required (e.g. --target-major 18)")
			}
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, majorProjectRef)
			if err != nil {
				return err
			}

			message := fmt.Sprintf(
				"This will UPGRADE project %q (%s) to PostgreSQL %d: a brief scheduled interruption while your data is copied and verified.\n"+
					"The previous version is kept for rollback until you run `capydb upgrade confirm`.\n"+
					"If you have not already, run `capydb upgrade preflight --target-major %d` first.",
				project.Name, project.ID, majorTarget, majorTarget,
			)
			confirmed, err := confirmUpgradeAction(cmd, project, message, majorConfirm)
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("major upgrade not confirmed; pass --confirm or confirm interactively")
			}

			job, err := client.UpgradeProjectMajor(ctx, project.ID, majorTarget)
			if err != nil {
				return fmt.Errorf("major upgrade: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"Queued major upgrade to PostgreSQL %d for project %s\n", majorTarget, project.Name)
				_, _ = fmt.Fprintln(cmd.OutOrStdout(),
					"The previous version stays available until you run `capydb upgrade confirm`; revert any time before that with `capydb upgrade rollback`.")
			}
			return a.maybeWaitForJob(cmd, client, job, majorWait, majorWaitTimeout, "major upgrade")
		},
	}
	majorCommand.Flags().StringVar(&majorProjectRef, "project", "", "Project id, slug, or name")
	majorCommand.Flags().IntVar(&majorTarget, "target-major", 0, "PostgreSQL major to upgrade to (e.g. 18)")
	majorCommand.Flags().BoolVar(&majorConfirm, "confirm", false, "Confirm the upgrade without prompting")
	addWaitFlags(majorCommand, &majorWait, &majorWaitTimeout, "major upgrade")

	var confirmProjectRef string
	var confirmConfirm bool
	var confirmWait bool
	var confirmWaitTimeout time.Duration
	confirmCommand := &cobra.Command{
		Use:   "confirm",
		Short: "Finalize a major upgrade (rollback stops being possible)",
		Long: "Finalizes a completed major upgrade by discarding the previous version that was " +
			"kept for rollback.\n\n" +
			"Run this once you have verified your application against the upgraded database. " +
			"After it completes, `capydb upgrade rollback` is no longer available.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, confirmProjectRef)
			if err != nil {
				return err
			}

			message := fmt.Sprintf(
				"This will FINALIZE the major upgrade for project %q (%s): the previous version kept for rollback is discarded and the upgrade can no longer be rolled back.",
				project.Name, project.ID,
			)
			confirmed, err := confirmUpgradeAction(cmd, project, message, confirmConfirm)
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("upgrade confirm not confirmed; pass --confirm or confirm interactively")
			}

			job, err := client.ConfirmProjectMajorUpgrade(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("confirm major upgrade: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"Queued major-upgrade confirmation for project %s\n", project.Name)
			}
			return a.maybeWaitForJob(cmd, client, job, confirmWait, confirmWaitTimeout, "major upgrade confirmation")
		},
	}
	confirmCommand.Flags().StringVar(&confirmProjectRef, "project", "", "Project id, slug, or name")
	confirmCommand.Flags().BoolVar(&confirmConfirm, "confirm", false, "Confirm the finalization without prompting")
	addWaitFlags(confirmCommand, &confirmWait, &confirmWaitTimeout, "major upgrade confirmation")

	var rollbackProjectRef string
	var rollbackConfirm bool
	var rollbackWait bool
	var rollbackWaitTimeout time.Duration
	rollbackCommand := &cobra.Command{
		Use:   "rollback",
		Short: "Roll a major upgrade back to the previous PostgreSQL version",
		Long: "Reverts a major upgrade by pointing the project back at the previous version, " +
			"which was kept untouched throughout the migration. The upgraded copy is discarded.\n\n" +
			"Rollback is lossless while the upgrade has not been confirmed; after " +
			"`capydb upgrade confirm` the previous version is gone and this command can no " +
			"longer be used.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, rollbackProjectRef)
			if err != nil {
				return err
			}

			message := fmt.Sprintf(
				"This will ROLL BACK project %q (%s) to the previous PostgreSQL version: the upgraded copy is discarded, including anything written to it since the upgrade.",
				project.Name, project.ID,
			)
			confirmed, err := confirmUpgradeAction(cmd, project, message, rollbackConfirm)
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("major upgrade rollback not confirmed; pass --confirm or confirm interactively")
			}

			job, err := client.RollbackProjectMajorUpgrade(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("rollback major upgrade: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"Queued major-upgrade rollback for project %s\n", project.Name)
			}
			return a.maybeWaitForJob(cmd, client, job, rollbackWait, rollbackWaitTimeout, "major upgrade rollback")
		},
	}
	rollbackCommand.Flags().StringVar(&rollbackProjectRef, "project", "", "Project id, slug, or name")
	rollbackCommand.Flags().BoolVar(&rollbackConfirm, "confirm", false, "Confirm the rollback without prompting")
	addWaitFlags(rollbackCommand, &rollbackWait, &rollbackWaitTimeout, "major upgrade rollback")

	command.AddCommand(minorCommand)
	command.AddCommand(preflightCommand)
	command.AddCommand(majorCommand)
	command.AddCommand(confirmCommand)
	command.AddCommand(rollbackCommand)
	return command
}

// confirmUpgradeAction guards the destructive upgrade verbs. The --confirm
// flag is honoured for non-interactive (CI) usage. On an interactive terminal
// without the flag, the operator is prompted to retype the project name (or
// "yes") before the job is sent - the same gate the other destructive capydb
// commands use.
func confirmUpgradeAction(cmd *cobra.Command, project api.Project, message string, flagConfirmed bool) (bool, error) {
	if flagConfirmed {
		return true, nil
	}
	if !stdinIsInteractive() {
		return false, nil
	}

	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), message)
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
