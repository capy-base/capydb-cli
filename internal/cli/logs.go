package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

// logsFollowInterval paces the --follow poll loop. Each poll is one control-
// plane request that reads the tail from the database's host, so it mirrors
// the dashboard tail's cadence rather than hammering the API.
const logsFollowInterval = 4 * time.Second

func (a *app) newLogsCommand() *cobra.Command {
	var projectRef string
	var hours int
	var limit int
	var severity string
	var follow bool

	command := &cobra.Command{
		Use:   "logs",
		Short: "Show a project's database logs",
		Long: "Show the project's Postgres log stream: a trailing window by default, " +
			"or a live tail with --follow. Severity is parsed from the log format; " +
			"filter with --severity (e.g. --severity error,fatal).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if hours < 1 || hours > 168 {
				return usageErrorf("--hours must be between 1 and 168")
			}
			if limit < 0 || limit > 500 {
				return usageErrorf("--limit must be between 0 and 500")
			}
			severities := splitSeverities(severity)

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, projectRef)
			if err != nil {
				return err
			}

			logs, err := client.GetProjectLogs(ctx, project.ID, api.ProjectLogsQuery{
				Hours:      hours,
				Limit:      limit,
				Severities: severities,
			})
			if err != nil {
				return fmt.Errorf("get logs: %w", err)
			}

			if a.jsonOutput() && !follow {
				return printJSON(cmd.OutOrStdout(), map[string]any{"logs": map[string]any{
					"entries":     jsonList(logs.Entries),
					"next_cursor": logs.NextCursor,
				}})
			}

			out := cmd.OutOrStdout()
			writeLogEntries(out, logs.Entries, a.jsonOutput())
			if !follow {
				if len(logs.Entries) == 0 {
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "No log entries in the last %dh\n", hours)
				}
				return nil
			}

			return a.followProjectLogs(ctx, cmd.ErrOrStderr(), out, client, project.ID, logs.NextCursor, severities)
		},
	}

	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().IntVar(&hours, "hours", 1, "Trailing window in hours (1-168)")
	command.Flags().IntVar(&limit, "limit", 0, "Maximum entries per fetch (server default when omitted, max 500)")
	command.Flags().StringVar(&severity, "severity", "", "Comma-separated severity filter (debug, log, info, notice, warning, error, fatal, panic, detail)")
	command.Flags().BoolVarP(&follow, "follow", "f", false, "Keep the stream open and print new entries as they arrive")
	return command
}

// followProjectLogs tails the log stream by polling the cursor-resume fetch
// until the context is cancelled (Ctrl-C). Transient fetch errors are
// reported to stderr and retried - a brief control-plane or host blip must
// not kill a long-running tail.
func (a *app) followProjectLogs(ctx context.Context, errOut, out io.Writer, client *api.Client, projectID, cursor string, severities []string) error {
	if !a.jsonOutput() {
		_, _ = fmt.Fprintln(errOut, "Following logs... (Ctrl-C to stop)")
	}
	ticker := time.NewTicker(logsFollowInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		logs, err := client.GetProjectLogs(ctx, projectID, api.ProjectLogsQuery{
			Cursor:     cursor,
			Severities: severities,
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// Auth failures and a deleted project are permanent: retrying
			// forever would loop silently on a revoked key. Fail fast with the
			// semantic exit code; only genuinely transient errors retry.
			if apiErr, ok := errors.AsType[*api.APIError](err); ok {
				switch apiErr.StatusCode {
				case 401, 403:
					return authErrorf("log tail stopped: %v", err)
				case 404:
					return notFoundErrorf("log tail stopped: project no longer exists: %v", err)
				}
			}
			_, _ = fmt.Fprintf(errOut, "log fetch failed (retrying): %v\n", err)
			continue
		}
		if logs.NextCursor != "" {
			cursor = logs.NextCursor
		}
		writeLogEntries(out, logs.Entries, a.jsonOutput())
	}
}

// writeLogEntries prints entries either as human-readable lines (timestamp,
// upper-cased severity, message) or as one JSON document per entry (JSONL,
// the scriptable shape for --follow pipelines).
func writeLogEntries(out io.Writer, entries []api.ProjectLogEntry, asJSON bool) {
	for _, entry := range entries {
		if asJSON {
			_ = printJSON(out, entry)
			continue
		}
		severity := strings.ToUpper(entry.Severity)
		if entry.Severity == "detail" {
			severity = "  ..."
		}
		_, _ = fmt.Fprintf(out, "%s %-7s %s\n", entry.Timestamp.Local().Format("2006-01-02 15:04:05"), severity, entry.Message)
	}
}

func splitSeverities(raw string) []string {
	var severities []string
	for severity := range strings.SplitSeq(raw, ",") {
		severity = strings.TrimSpace(strings.ToLower(severity))
		if severity != "" {
			severities = append(severities, severity)
		}
	}
	return severities
}
