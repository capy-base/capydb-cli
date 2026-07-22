package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

func (a *app) newAlertsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "alerts",
		Aliases: []string{"alert"},
		Short:   "Inspect and acknowledge project resource alerts",
	}

	var listProjectRef string
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List resource alerts for a project",
		Args:  cobra.NoArgs,
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

			alerts, err := client.ListProjectAlerts(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("list alerts: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"alerts": jsonList(alerts)})
			}
			if len(alerts) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No alerts for project %s\n", project.Name)
				return nil
			}

			writeAlertTable(cmd.OutOrStdout(), alerts)
			return nil
		},
	}
	listCommand.Flags().StringVar(&listProjectRef, "project", "", "Project id, slug, or name")

	var ackProjectRef string
	ackCommand := &cobra.Command{
		Use:     "ack <alert-id>",
		Aliases: []string{"acknowledge"},
		Short:   "Acknowledge a project alert",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			alertID := strings.TrimSpace(args[0])
			if alertID == "" {
				return usageErrorf("alert id is required")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, ackProjectRef)
			if err != nil {
				return err
			}

			if err := client.AcknowledgeProjectAlert(ctx, project.ID, alertID); err != nil {
				return fmt.Errorf("acknowledge alert: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"acknowledged": true, "alert_id": alertID})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Acknowledged alert %s\n", alertID)
			return nil
		},
	}
	ackCommand.Flags().StringVar(&ackProjectRef, "project", "", "Project id, slug, or name")

	command.AddCommand(listCommand)
	command.AddCommand(ackCommand)
	return command
}

func writeAlertTable(out io.Writer, alerts []api.ProjectAlert) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tKIND\tSEVERITY\tOBSERVED\tLIMIT\tTRIGGERED_AT\tRESOLVED_AT\tACKNOWLEDGED_AT")
	for _, alert := range alerts {
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			alert.ID,
			alert.Kind,
			alert.Severity,
			formatAlertValue(alert.ObservedValue),
			formatAlertValue(alert.LimitValue),
			formatTime(alert.TriggeredAt),
			formatOptionalTime(alert.ResolvedAt),
			formatOptionalTime(alert.AcknowledgedAt),
		)
	}
	_ = writer.Flush()
}

// formatAlertValue renders an alert measurement without trailing decimal noise.
func formatAlertValue(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d", int64(value))
	}
	return fmt.Sprintf("%.2f", value)
}
