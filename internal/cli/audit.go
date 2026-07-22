package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

func (a *app) newAuditCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "audit",
		Short: "Inspect project audit events",
	}

	var listLimit int
	var listProjectRef string
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List audit events for a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if listLimit < 0 {
				return usageErrorf("--limit must be zero or positive")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, listProjectRef)
			if err != nil {
				return err
			}

			events, err := client.ListProjectAuditEvents(ctx, project.ID, listLimit)
			if err != nil {
				return fmt.Errorf("list audit events: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"audit_events": jsonList(events)})
			}
			if len(events) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No audit events for project %s\n", project.Name)
				return nil
			}

			writeAuditEventTable(cmd.OutOrStdout(), events)
			return nil
		},
	}
	listCommand.Flags().StringVar(&listProjectRef, "project", "", "Project id, slug, or name")
	listCommand.Flags().IntVar(&listLimit, "limit", 0, "Maximum number of events to return (server default when omitted)")

	command.AddCommand(listCommand)
	return command
}

func writeAuditEventTable(out io.Writer, events []api.ProjectAuditEvent) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "CREATED_AT\tACTION\tACTOR\tMETADATA")
	for _, event := range events {
		actor := event.ActorKind
		if strings.TrimSpace(event.ActorID) != "" {
			actor = event.ActorKind + ":" + event.ActorID
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\n",
			formatTime(event.CreatedAt),
			event.Action,
			firstNonEmpty(actor, "-"),
			formatAuditMetadata(event.Metadata),
		)
	}
	_ = writer.Flush()
}

// formatAuditMetadata renders event metadata as compact JSON, truncated for
// table display.
func formatAuditMetadata(metadata any) string {
	if metadata == nil {
		return "-"
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "-"
	}
	text := string(encoded)
	if text == "null" || text == "{}" {
		return "-"
	}
	const limit = 80
	if len(text) > limit {
		return text[:limit-3] + "..."
	}
	return text
}
