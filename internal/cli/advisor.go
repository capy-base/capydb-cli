package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

// newAdvisorCommand groups the read-only performance advisors.
func (a *app) newAdvisorCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "advisor",
		Aliases: []string{"advise"},
		Short:   "Performance advice for a project database",
	}
	command.AddCommand(a.newAdvisorIndexesCommand())
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
