package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capydatabase/capydb-cli/internal/api"
)

// newCloudflareCommand is the entry point of Cloudflare's Hyperdrive
// database-integration flow: a customer creates a CapyDB database from the
// Cloudflare dashboard, Cloudflare mints a short-lived signed authorization,
// and it is handed to this CLI to do the creation. Cloudflare invoices the
// usage, so there is no CapyDB login involved - the authorization is the
// credential, and the control plane verifies it.
//
// The flag surface is CapyDB's own choice for now; how Cloudflare's dashboard
// invokes a partner CLI is settled at onboarding. See
// docs/cloudflare-hyperdrive-partner-submission.md.
func (a *app) newCloudflareCommand() *cobra.Command {
	command := &cobra.Command{
		Use:    "cloudflare",
		Short:  "Cloudflare-billed database provisioning (Hyperdrive integration partner flow)",
		Hidden: true,
	}

	var accountID string
	var accountName string
	var apiURL string
	var name string
	var plan string
	var postgresVersion string
	var region string
	var signature string
	var timestamp int64

	createCommand := &cobra.Command{
		Use:   "create-database",
		Short: "Create a database against a Cloudflare-issued authorization",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Ordered, so the first missing flag reported is always the same one.
			required := []struct {
				flag  string
				value string
			}{
				{flag: "--account-id", value: accountID},
				{flag: "--signature", value: signature},
				{flag: "--name", value: name},
			}
			for _, field := range required {
				if strings.TrimSpace(field.value) == "" {
					return fmt.Errorf("%s is required", field.flag)
				}
			}
			if timestamp <= 0 {
				return fmt.Errorf("--timestamp is required")
			}

			// No API key: this path is authenticated by Cloudflare's signature,
			// and the customer has no CapyDB credentials yet by definition.
			client, err := a.newAPIClient(a.resolveAPIURL(apiURL), "")
			if err != nil {
				return err
			}

			result, err := client.ProvisionCloudflareDatabase(cmd.Context(), api.ProvisionCloudflareDatabaseRequest{
				AccountID:       strings.TrimSpace(accountID),
				AccountName:     strings.TrimSpace(accountName),
				BillingPlan:     strings.TrimSpace(plan),
				Name:            strings.TrimSpace(name),
				PostgresVersion: strings.TrimSpace(postgresVersion),
				Region:          strings.TrimSpace(region),
				Signature:       strings.TrimSpace(signature),
				Timestamp:       timestamp,
			})
			if err != nil {
				return err
			}

			writeJSONPayload(cmd, result)
			_, _ = fmt.Fprintf(
				cmd.ErrOrStderr(),
				"database %s (%s) is provisioning; job %s\n",
				result.Project.Name, result.Project.ID, result.Job.ID,
			)
			return nil
		},
	}

	createCommand.Flags().StringVar(&accountID, "account-id", "", "Cloudflare account id the authorization was issued for")
	createCommand.Flags().StringVar(&signature, "signature", "", "Signature from Cloudflare's createDatabaseSignature response")
	createCommand.Flags().Int64Var(&timestamp, "timestamp", 0, "Issuance timestamp from Cloudflare's createDatabaseSignature response")
	createCommand.Flags().StringVar(&name, "name", "", "Database name")
	createCommand.Flags().StringVar(&accountName, "account-name", "", "Label for the CapyDB organization created on first use")
	createCommand.Flags().StringVar(&plan, "plan", "", "Plan Cloudflare invoices: vibe, ship, or business")
	createCommand.Flags().StringVar(&region, "region", "", "Region")
	createCommand.Flags().StringVar(&postgresVersion, "postgres-version", "", "Postgres major")
	createCommand.Flags().StringVar(&apiURL, "api-url", "", "Control-plane URL override")

	command.AddCommand(createCommand)
	return command
}
