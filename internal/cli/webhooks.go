package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

func (a *app) newWebhooksCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "webhooks",
		Aliases: []string{"webhook"},
		Short:   "Manage organization webhook endpoints",
	}

	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List webhook endpoints for the active organization",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			endpoints, err := client.ListWebhookEndpoints(ctx, orgID)
			if err != nil {
				return fmt.Errorf("list webhook endpoints: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"webhook_endpoints": jsonList(endpoints)})
			}
			if len(endpoints) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No webhook endpoints configured.")
				return nil
			}

			writeWebhookEndpointTable(cmd.OutOrStdout(), endpoints)
			return nil
		},
	}

	var createURL string
	var createEventTypes []string
	var createDescription string
	createCommand := &cobra.Command{
		Use:   "create",
		Short: "Create a webhook endpoint (the signing secret is shown exactly once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(createURL) == "" {
				return usageErrorf("--url is required")
			}

			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			endpoint, secret, err := client.CreateWebhookEndpoint(ctx, orgID, api.CreateWebhookEndpointRequest{
				Description: strings.TrimSpace(createDescription),
				EventTypes:  createEventTypes,
				URL:         strings.TrimSpace(createURL),
			})
			if err != nil {
				return fmt.Errorf("create webhook endpoint: %w", err)
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"webhook_endpoint": endpoint,
					"plaintext_secret": secret,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created webhook endpoint %s for %s\n", endpoint.ID, endpoint.URL)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "signing_secret: %s\n", secret)
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Store the signing secret now; it will not be shown again.")
			return nil
		},
	}
	createCommand.Flags().StringVar(&createURL, "url", "", "HTTPS receiver URL (required)")
	createCommand.Flags().StringSliceVar(&createEventTypes, "event", nil, "Event types to deliver (repeatable; all events when omitted)")
	createCommand.Flags().StringVar(&createDescription, "description", "", "Optional endpoint description")

	deleteCommand := &cobra.Command{
		Use:   "delete <endpoint-id>",
		Short: "Delete a webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			endpointID := strings.TrimSpace(args[0])
			if endpointID == "" {
				return usageErrorf("endpoint id is required")
			}

			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			if err := client.DeleteWebhookEndpoint(ctx, orgID, endpointID); err != nil {
				return fmt.Errorf("delete webhook endpoint: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"deleted": true, "endpoint_id": endpointID})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Deleted webhook endpoint %s\n", endpointID)
			return nil
		},
	}

	rotateCommand := &cobra.Command{
		Use:   "rotate-secret <endpoint-id>",
		Short: "Rotate the signing secret of a webhook endpoint (the new secret is shown exactly once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			endpointID := strings.TrimSpace(args[0])
			if endpointID == "" {
				return usageErrorf("endpoint id is required")
			}

			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			endpoint, secret, err := client.RotateWebhookEndpointSecret(ctx, orgID, endpointID)
			if err != nil {
				return fmt.Errorf("rotate webhook secret: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"webhook_endpoint": endpoint,
					"plaintext_secret": secret,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Rotated signing secret for webhook endpoint %s\n", endpoint.ID)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "signing_secret: %s\n", secret)
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Store the signing secret now; it will not be shown again.")
			return nil
		},
	}

	var deliveriesLimit int
	deliveriesCommand := &cobra.Command{
		Use:   "deliveries <endpoint-id>",
		Short: "List recent deliveries for a webhook endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			endpointID := strings.TrimSpace(args[0])
			if endpointID == "" {
				return usageErrorf("endpoint id is required")
			}
			if deliveriesLimit < 0 {
				return usageErrorf("--limit must be zero or positive")
			}

			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			deliveries, err := client.ListWebhookDeliveries(ctx, orgID, endpointID, deliveriesLimit)
			if err != nil {
				return fmt.Errorf("list webhook deliveries: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"deliveries": jsonList(deliveries)})
			}
			if len(deliveries) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No deliveries for this webhook endpoint.")
				return nil
			}

			writeWebhookDeliveryTable(cmd.OutOrStdout(), deliveries)
			return nil
		},
	}
	deliveriesCommand.Flags().IntVar(&deliveriesLimit, "limit", 0, "Maximum number of deliveries to return (server default when omitted)")

	command.AddCommand(listCommand)
	command.AddCommand(createCommand)
	command.AddCommand(deleteCommand)
	command.AddCommand(rotateCommand)
	command.AddCommand(deliveriesCommand)
	return command
}

func writeWebhookEndpointTable(out io.Writer, endpoints []api.WebhookEndpoint) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tURL\tEVENTS\tACTIVE\tCREATED_AT")
	for _, endpoint := range endpoints {
		events := "all"
		if len(endpoint.EventTypes) > 0 {
			events = strings.Join(endpoint.EventTypes, ",")
		}
		active := "no"
		if endpoint.IsActive {
			active = "yes"
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\n",
			endpoint.ID,
			endpoint.URL,
			events,
			active,
			formatTime(endpoint.CreatedAt),
		)
	}
	_ = writer.Flush()
}

func writeWebhookDeliveryTable(out io.Writer, deliveries []api.WebhookDelivery) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tEVENT\tSTATE\tATTEMPTS\tSTATUS\tCREATED_AT\tLAST_ERROR")
	for _, delivery := range deliveries {
		status := "-"
		if delivery.ResponseStatus > 0 {
			status = fmt.Sprintf("%d", delivery.ResponseStatus)
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%d/%d\t%s\t%s\t%s\n",
			delivery.ID,
			delivery.EventType,
			delivery.State,
			delivery.Attempts,
			delivery.MaxAttempts,
			status,
			formatTime(delivery.CreatedAt),
			firstNonEmpty(delivery.LastError, "-"),
		)
	}
	_ = writer.Flush()
}
