package cli

import (
	"encoding/json"
	"fmt"
	"io"
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
			"require a preflight check and operator-assisted execution.",
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
	preflightWait := true
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
			if !preflightWait {
				return a.maybeWaitForJob(cmd, client, job, false, preflightWaitTimeout, "major upgrade preflight")
			}
			job, err = waitForJob(ctx, cmd.ErrOrStderr(), client, job.ID, preflightWaitTimeout)
			if err != nil {
				return err
			}
			result, resultErr := client.GetJobResult(ctx, job.ID)
			if a.jsonOutput() {
				if resultErr == nil && len(result) > 0 {
					if err := printJSON(cmd.OutOrStdout(), struct {
						Job    api.Job         `json:"job"`
						Result json.RawMessage `json:"result"`
					}{Job: job, Result: result}); err != nil {
						return err
					}
				} else if err := a.printJob(cmd, job); err != nil {
					return err
				}
			} else {
				writeJob(cmd.OutOrStdout(), job)
				if resultErr == nil && len(result) > 0 {
					writeMajorUpgradePreflight(cmd.OutOrStdout(), result)
				}
			}
			return ensureCompletedJob(job, "major upgrade preflight")
		},
	}
	preflightCommand.Flags().StringVar(&preflightProjectRef, "project", "", "Project id, slug, or name")
	preflightCommand.Flags().IntVar(&preflightTarget, "target-major", 0, "PostgreSQL major to evaluate (e.g. 18)")
	preflightCommand.Flags().BoolVar(&preflightWait, "wait", true, "Wait for the major upgrade preflight job to finish")
	preflightCommand.Flags().DurationVar(&preflightWaitTimeout, "wait-timeout", defaultWaitTimeout, "Maximum time to wait for the major upgrade preflight job")

	command.AddCommand(minorCommand)
	command.AddCommand(preflightCommand)
	return command
}

func writeMajorUpgradePreflight(out io.Writer, raw json.RawMessage) {
	var result struct {
		Blockers     []string `json:"blockers"`
		CurrentMajor string   `json:"current_major"`
		Status       string   `json:"status"`
		TargetMajor  string   `json:"target_major"`
		Warnings     []string `json:"warnings"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return
	}
	_, _ = fmt.Fprintf(out, "verdict: %s\n", result.Status)
	_, _ = fmt.Fprintf(out, "postgresql: %s -> %s\n", result.CurrentMajor, result.TargetMajor)
	for _, blocker := range result.Blockers {
		_, _ = fmt.Fprintf(out, "blocker: %s\n", blocker)
	}
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(out, "warning: %s\n", warning)
	}
}
