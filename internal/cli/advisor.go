package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capydatabase/capydb-cli/internal/api"
)

// newAdvisorCommand groups the read-only performance advisors.
func (a *app) newAdvisorCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "advisor",
		Aliases: []string{"advise"},
		Short:   "Performance advice for a project database",
	}
	command.AddCommand(a.newAdvisorIndexesCommand())
	command.AddCommand(a.newAdvisorIndexHygieneCommand())
	return command
}

func (a *app) newAdvisorIndexesCommand() *cobra.Command {
	var projectRef string
	var minFilter int
	var minSelectivity int

	command := &cobra.Command{
		Use:   "indexes",
		Short: "Suggest indexes based on the queries your database actually ran",
		Long: "Suggest indexes based on the queries your database actually ran.\n\n" +
			"Suggestions come from the predicates your queries filtered on, and each candidate is\n" +
			"measured by building it as a hypothetical index - nothing is created and nothing is\n" +
			"written to your database, so this is safe to run against production.\n\n" +
			"Requires the pg_qualstats extension (and hypopg for size estimates); enable them with\n" +
			"`capydb extensions enable`. Note that enabling pg_qualstats restarts the database.",
		Args: cobra.NoArgs,
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

			report, err := client.GetProjectIndexAdvisor(ctx, project.ID, minFilter, minSelectivity)
			if err != nil {
				return fmt.Errorf("index advisor: %w", err)
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"advisor": map[string]any{
						"available":                report.Available,
						"min_filter":               report.MinFilter,
						"min_selectivity":          report.MinSelectivity,
						"missing_extensions":       jsonList(report.MissingExtensions),
						"reason":                   report.Reason,
						"size_estimates_available": report.SizeEstimatesAvailable,
						"suggestions":              jsonList(report.Suggestions),
					},
				})
			}

			writeIndexAdvisorReport(cmd.OutOrStdout(), report)
			return nil
		},
	}
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	command.Flags().IntVar(&minFilter, "min-filter", 0,
		"Minimum average rows a predicate must filter to be considered (default 1000; lower it on a quiet database)")
	command.Flags().IntVar(&minSelectivity, "min-selectivity", 0,
		"Minimum average selectivity percentage for a predicate to be considered (default 30)")
	return command
}

func writeIndexAdvisorReport(out io.Writer, report api.IndexAdvisorReport) {
	if !report.Available {
		if report.Reason != "" {
			_, _ = fmt.Fprintln(out, report.Reason)
		} else {
			_, _ = fmt.Fprintln(out, "The index advisor is not available for this database.")
		}
		if len(report.MissingExtensions) > 0 {
			_, _ = fmt.Fprintf(
				out,
				"\nEnable it with:\n  capydb extensions enable %s\n",
				strings.Join(report.MissingExtensions, "\n  capydb extensions enable "),
			)
			_, _ = fmt.Fprintln(out, "\nNote: enabling pg_qualstats restarts the database (open connections drop briefly).")
		}
		return
	}

	if len(report.Suggestions) == 0 {
		_, _ = fmt.Fprintln(out, "No missing indexes found - your queries are served by the indexes you already have.")
		return
	}

	if !report.SizeEstimatesAvailable {
		_, _ = fmt.Fprintln(out, "Size estimates unavailable (enable hypopg to see them).")
		_, _ = fmt.Fprintln(out)
	}

	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "TABLE\tMETHOD\tEST. SIZE\tSTATEMENT")
	for _, suggestion := range report.Suggestions {
		// An unknown size must not print as "0 B" - that would read as "this
		// index is free".
		size := "-"
		if report.SizeEstimatesAvailable && suggestion.EstimatedSizeBytes > 0 {
			size = formatBytes(suggestion.EstimatedSizeBytes)
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s;\n",
			firstNonEmpty(suggestion.Table, "-"),
			firstNonEmpty(suggestion.IndexMethod, "-"),
			size,
			suggestion.DDL,
		)
	}
	_ = writer.Flush()

	_, _ = fmt.Fprintln(out, "\nCreating an index locks writes on that table while it builds.")
	_, _ = fmt.Fprintln(out, "On a large table use CREATE INDEX CONCURRENTLY instead.")
}

func (a *app) newAdvisorIndexHygieneCommand() *cobra.Command {
	var projectRef string

	command := &cobra.Command{
		Use:     "index-hygiene",
		Aliases: []string{"unused-indexes"},
		Short:   "Find indexes your database is paying for but not using",
		Long: "Find indexes your database is paying for but not using.\n\n" +
			"Two kinds: indexes with no recorded scans, and indexes whose columns are a leading\n" +
			"subset of another index on the same table. Both cost storage and slow every write to\n" +
			"the table. This is the counterpart to `capydb advisor indexes`, which only ever\n" +
			"suggests new ones.\n\n" +
			"Read-only, and it needs no extensions. UNIQUE, primary-key and exclusion indexes are\n" +
			"never listed - they are constraints, not just access paths. Nothing is reported until\n" +
			"a week of query statistics has accumulated, so an index a weekly job uses is not\n" +
			"mistaken for a dead one.",
		Args: cobra.NoArgs,
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

			report, err := client.GetProjectIndexHygiene(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("index hygiene: %w", err)
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"hygiene": map[string]any{
						"available":                  report.Available,
						"min_index_size_bytes":       report.MinIndexSizeBytes,
						"observation_window_seconds": report.ObservationWindowSeconds,
						"reason":                     report.Reason,
						"reclaimable_bytes":          report.ReclaimableBytes,
						"redundant_indexes":          jsonList(report.RedundantIndexes),
						"stats_reset_at":             report.StatsResetAt,
						"unused_indexes":             jsonList(report.UnusedIndexes),
					},
				})
			}

			writeIndexHygieneReport(cmd.OutOrStdout(), report)
			return nil
		},
	}
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	return command
}

func writeIndexHygieneReport(out io.Writer, report api.IndexHygieneReport) {
	if !report.Available {
		if report.Reason != "" {
			_, _ = fmt.Fprintln(out, report.Reason)
		} else {
			_, _ = fmt.Fprintln(out, "Index hygiene is not available for this database yet.")
		}
		return
	}

	if len(report.UnusedIndexes) == 0 && len(report.RedundantIndexes) == 0 {
		_, _ = fmt.Fprintln(out, "Every index on this database is being used and none duplicates another.")
		return
	}

	if len(report.UnusedIndexes) > 0 {
		_, _ = fmt.Fprintf(out, "Never scanned (%d days of query history):\n", report.ObservationWindowSeconds/86400)
		writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "TABLE\tINDEX\tSIZE")
		for _, index := range report.UnusedIndexes {
			_, _ = fmt.Fprintf(writer, "%s.%s\t%s\t%s\n", index.Schema, index.Table, index.Index, formatBytes(index.SizeBytes))
		}
		_ = writer.Flush()
		_, _ = fmt.Fprintln(out)
	}

	if len(report.RedundantIndexes) > 0 {
		_, _ = fmt.Fprintln(out, "Covered by a wider index:")
		writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "TABLE\tINDEX\tSIZE\tCOVERED BY")
		for _, index := range report.RedundantIndexes {
			_, _ = fmt.Fprintf(writer, "%s.%s\t%s\t%s\t%s\n",
				index.Schema, index.Table, index.Index, formatBytes(index.SizeBytes), index.CoveredBy)
		}
		_ = writer.Flush()
		_, _ = fmt.Fprintln(out)
	}

	_, _ = fmt.Fprintf(out, "Reclaimable: %s\n\n", formatBytes(report.ReclaimableBytes))
	_, _ = fmt.Fprintln(out, "Drop one with (CONCURRENTLY, so writes to the table keep working):")
	for _, index := range report.UnusedIndexes {
		_, _ = fmt.Fprintf(out, "  %s\n", index.DropStatement)
	}
	for _, index := range report.RedundantIndexes {
		_, _ = fmt.Fprintf(out, "  %s\n", index.DropStatement)
	}
}
