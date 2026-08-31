package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

// resolveConnectionURL resolves the direct or pooled connection URL for the
// linked project, an explicit --project, or an explicit --preview id. The
// second return value is the project's runtime status ("active", "paused",
// "resuming"; empty for previews and while provisioning) so callers can
// explain a scale-to-zero wake - the project is already fetched here, so this
// costs no extra API call.
func (a *app) resolveConnectionURL(cmd *cobra.Command, pooled bool, previewID, projectRef string) (string, string, error) {
	ctx := cmd.Context()
	client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
	if err != nil {
		return "", "", err
	}

	var connections api.ConnectionInfo
	var runtimeStatus string
	if trimmed := strings.TrimSpace(previewID); trimmed != "" {
		connections, err = client.GetPreviewConnection(ctx, trimmed)
		if err != nil {
			return "", "", fmt.Errorf("fetch preview connections: %w", err)
		}
	} else {
		project, err := a.resolveProject(ctx, client, projectRef)
		if err != nil {
			return "", "", err
		}
		runtimeStatus = project.RuntimeStatus
		connections, err = client.GetProjectConnection(ctx, project.ID)
		if err != nil {
			return "", "", fmt.Errorf("fetch project connections: %w", err)
		}
	}

	if pooled {
		if strings.TrimSpace(connections.PooledURL) == "" {
			return "", "", fmt.Errorf("no pooled connection URL available")
		}
		return connections.PooledURL, runtimeStatus, nil
	}
	if strings.TrimSpace(connections.DirectURL) == "" {
		return "", "", fmt.Errorf("no direct connection URL available")
	}
	return connections.DirectURL, runtimeStatus, nil
}

// resumingNotice returns the one-line stderr notice shown before connecting to
// a paused project database, or "" when no notice is needed.
func resumingNotice(runtimeStatus string) string {
	if runtimeStatus == "paused" {
		return "Resuming your database (usually under a second)..."
	}
	return ""
}

// psqlConnectionURL makes an issued connection URL usable by psql out of the box.
// Issued URLs carry sslmode=verify-full, but libpq does not read the OS trust
// store by default (it wants ~/.postgresql/root.crt), so bare psql fails with
// "root certificate file does not exist". sslrootcert=system (libpq 16+) points
// it at the system roots; it is consumed locally and never sent to the server.
// URLs that already pin an sslrootcert, or that don't verify, pass through as-is.
func psqlConnectionURL(connectionURL string) string {
	if strings.Contains(connectionURL, "sslrootcert=") {
		return connectionURL
	}
	if !strings.Contains(connectionURL, "sslmode=verify-full") &&
		!strings.Contains(connectionURL, "sslmode=verify-ca") {
		return connectionURL
	}
	separator := "?"
	if strings.Contains(connectionURL, "?") {
		separator = "&"
	}
	return connectionURL + separator + "sslrootcert=system"
}

func (a *app) newConnectionStringCommand() *cobra.Command {
	var pooled bool
	var previewID string
	var projectRef string

	command := &cobra.Command{
		Use:   "connection-string",
		Short: "Print the connection URL for a project or preview database",
		Long:  "Prints only the connection URL to stdout so it can be used in scripts; everything else goes to stderr. Defaults to the direct URL.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			connectionURL, _, err := a.resolveConnectionURL(cmd, pooled, previewID, projectRef)
			if err != nil {
				return err
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"connection_string": connectionURL,
					"pooled":            pooled,
				})
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), connectionURL)
			return nil
		},
	}

	command.Flags().BoolVar(&pooled, "pooled", false, "Print the pooled (pgbouncer) URL instead of the direct URL")
	command.Flags().StringVar(&previewID, "preview", "", "Preview database id")
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	return command
}

func (a *app) newPsqlCommand() *cobra.Command {
	var pooled bool
	var previewID string
	var projectRef string

	command := &cobra.Command{
		Use:   "psql [-- <extra psql args>]",
		Short: "Open psql connected to a project or preview database",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			connectionURL, runtimeStatus, err := a.resolveConnectionURL(cmd, pooled, previewID, projectRef)
			if err != nil {
				return err
			}
			if notice := resumingNotice(runtimeStatus); notice != "" {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), notice)
			}
			connectionURL = psqlConnectionURL(connectionURL)

			psqlPath, err := exec.LookPath("psql")
			if err != nil {
				return fmt.Errorf("psql not found in PATH; install the Postgres client tools (e.g. `brew install libpq` or `apt install postgresql-client`)")
			}

			// exec.Command (not CommandContext) on purpose: Ctrl-C must reach
			// psql to cancel the running query instead of killing the session.
			psql := exec.Command(psqlPath, append([]string{connectionURL}, args...)...)
			psql.Stdin = os.Stdin
			psql.Stdout = cmd.OutOrStdout()
			psql.Stderr = cmd.ErrOrStderr()
			if err := psql.Run(); err != nil {
				return fmt.Errorf("psql: %w", err)
			}
			return nil
		},
	}

	command.Flags().BoolVar(&pooled, "pooled", false, "Connect through the pooled (pgbouncer) URL instead of the direct URL")
	command.Flags().StringVar(&previewID, "preview", "", "Preview database id")
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	return command
}

func (a *app) newSQLCommand() *cobra.Command {
	var allowUnqualifiedWrites bool
	var asJSON bool
	var maxRows int
	var projectRef string

	command := &cobra.Command{
		Use:   "sql <query>",
		Short: "Run a SQL query against the project database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			query := strings.TrimSpace(args[0])
			if query == "" {
				return usageErrorf("query cannot be empty")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, projectRef)
			if err != nil {
				return err
			}

			result, err := client.RunSQL(ctx, project.ID, query, maxRows, allowUnqualifiedWrites)
			if err != nil {
				return fmt.Errorf("run sql: %w", err)
			}

			if asJSON {
				a.useJSONOutput()
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), result)
			}
			writeSQLResultTable(cmd.OutOrStdout(), result)
			return nil
		},
	}

	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().IntVar(&maxRows, "max-rows", 0, "Maximum number of rows to return (server default when omitted)")
	// Unlike the dashboard's SQL console, the CLI is guarded by default: it is
	// as likely to be running inside a script as under a person, and a script
	// is exactly the caller that should have to say it meant to empty a table.
	command.Flags().BoolVar(&allowUnqualifiedWrites, "allow-unqualified-writes", false,
		"Permit an UPDATE or DELETE with no WHERE clause, or a TRUNCATE (refused by default)")
	command.Flags().BoolVar(&asJSON, "json", false, "Print the raw JSON result (alias for --output json)")
	return command
}

func (a *app) newMetricsCommand() *cobra.Command {
	var asJSON bool
	var projectRef string

	command := &cobra.Command{
		Use:     "metrics",
		Aliases: []string{"observability"},
		Short:   "Show storage, connection, and query metrics for a project",
		Args:    cobra.NoArgs,
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

			observability, err := client.GetProjectObservability(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("fetch observability: %w", err)
			}

			if asJSON {
				a.useJSONOutput()
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), observability)
			}
			writeObservabilityReport(cmd.OutOrStdout(), observability)
			return nil
		},
	}

	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().BoolVar(&asJSON, "json", false, "Print the raw JSON observability payload (alias for --output json)")
	return command
}

func writeSQLResultTable(out io.Writer, result api.SQLResult) {
	if len(result.Columns) > 0 {
		writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, strings.Join(result.Columns, "\t"))
		separators := make([]string, len(result.Columns))
		for index, column := range result.Columns {
			separators[index] = strings.Repeat("-", len(column))
		}
		_, _ = fmt.Fprintln(writer, strings.Join(separators, "\t"))
		for _, row := range result.Rows {
			values := make([]string, len(result.Columns))
			for index, column := range result.Columns {
				values[index] = formatSQLValue(row[column])
			}
			_, _ = fmt.Fprintln(writer, strings.Join(values, "\t"))
		}
		_ = writer.Flush()
	}

	_, _ = fmt.Fprintf(out, "(%d rows, %dms)\n", result.RowCount, result.DurationMs)
	if result.Truncated {
		_, _ = fmt.Fprintln(out, "note: result truncated; raise --max-rows to fetch more")
	}
}

func formatSQLValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return "NULL"
	case string:
		return typed
	case float64:
		// JSON numbers decode as float64; render integers without a decimal point.
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}

// truncateQuery flattens whitespace and truncates a SQL query for table display.
func truncateQuery(query string, limit int) string {
	flattened := strings.Join(strings.Fields(query), " ")
	if limit > 3 && len(flattened) > limit {
		return flattened[:limit-3] + "..."
	}
	return flattened
}

func writeObservabilityReport(out io.Writer, observability api.ProjectObservability) {
	_, _ = fmt.Fprintf(
		out,
		"storage: %s / %s (%s)\n",
		formatBytes(observability.DatabaseSizeBytes),
		formatBytes(observability.StorageLimitBytes),
		formatPercent(observability.StorageUsagePercent),
	)
	_, _ = fmt.Fprintf(
		out,
		"connections: %d/%d (%s)\n",
		observability.ConnectionCount,
		observability.ConnectionLimit,
		formatPercent(observability.ConnectionUsagePercent),
	)

	if len(observability.Alerts) == 0 {
		_, _ = fmt.Fprintln(out, "alerts: none")
	} else {
		for _, alert := range observability.Alerts {
			if strings.TrimSpace(alert) != "" {
				_, _ = fmt.Fprintf(out, "alert: %s\n", alert)
			}
		}
	}

	_, _ = fmt.Fprintln(out, "")
	switch {
	case !observability.PgStatStatements:
		_, _ = fmt.Fprintln(out, "slow queries: unavailable (pg_stat_statements is not enabled)")
	case len(observability.SlowQueries) == 0:
		_, _ = fmt.Fprintln(out, "slow queries: none")
	default:
		_, _ = fmt.Fprintln(out, "Top slow queries:")
		writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "CALLS\tMEAN_MS\tQUERY")
		for _, slow := range observability.SlowQueries {
			_, _ = fmt.Fprintf(writer, "%d\t%.1f\t%s\n", slow.Calls, slow.MeanTimeMs, truncateQuery(slow.Query, 80))
		}
		_ = writer.Flush()
	}

	_, _ = fmt.Fprintln(out, "")
	if len(observability.ActiveQueries) == 0 {
		_, _ = fmt.Fprintln(out, "active queries: none")
		return
	}
	_, _ = fmt.Fprintln(out, "Active queries:")
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "PID\tUSER\tSTATE\tDURATION_MS\tWAIT\tQUERY")
	for _, active := range observability.ActiveQueries {
		wait := "-"
		if strings.TrimSpace(active.WaitEvent) != "" {
			wait = firstNonEmpty(active.WaitEventType, "") + ":" + active.WaitEvent
			wait = strings.TrimPrefix(wait, ":")
		}
		_, _ = fmt.Fprintf(
			writer,
			"%d\t%s\t%s\t%d\t%s\t%s\n",
			active.PID,
			firstNonEmpty(active.Username, "-"),
			firstNonEmpty(active.State, "-"),
			active.DurationMs,
			wait,
			truncateQuery(active.Query, 80),
		)
	}
	_ = writer.Flush()
}
