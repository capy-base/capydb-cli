package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb-cli/internal/api"
)

func (a *app) newAPIKeysCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "api-keys",
		Aliases: []string{"api-key", "keys"},
		Short:   "Manage organization API keys",
	}

	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List API keys for the active organization",
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

			keys, err := client.ListAPIKeys(ctx, orgID)
			if err != nil {
				return fmt.Errorf("list api keys: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"api_keys": jsonList(keys)})
			}
			if len(keys) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No API keys for this organization.")
				return nil
			}

			writeAPIKeyTable(cmd.OutOrStdout(), keys)
			return nil
		},
	}

	var createDeviceName string
	var createExpiresIn string
	var createName string
	var createProjectRef string
	var createScopes []string
	createCommand := &cobra.Command{
		Use:   "create",
		Short: "Create an API key (the plaintext key is shown exactly once)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if strings.TrimSpace(createName) == "" {
				return usageErrorf("--name is required")
			}
			scopes := make([]string, 0, len(createScopes))
			for _, scope := range createScopes {
				if trimmed := strings.TrimSpace(scope); trimmed != "" {
					scopes = append(scopes, trimmed)
				}
			}
			if len(scopes) == 0 {
				return usageErrorf("--scopes is required (e.g. --scopes projects:read,backups:read)")
			}
			expiresAt, err := resolveCLILoginExpiry(createExpiresIn)
			if err != nil {
				return err
			}

			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			request := api.CreateAPIKeyRequest{
				DeviceName: strings.TrimSpace(createDeviceName),
				ExpiresAt:  expiresAt,
				Name:       strings.TrimSpace(createName),
				Scopes:     scopes,
			}
			if trimmed := strings.TrimSpace(createProjectRef); trimmed != "" {
				project, err := a.resolveProject(ctx, client, trimmed)
				if err != nil {
					return err
				}
				request.ProjectID = project.ID
			}

			key, plaintext, err := client.CreateAPIKey(ctx, orgID, request)
			if err != nil {
				return fmt.Errorf("create api key: %w", err)
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"api_key":           key,
					"plaintext_api_key": plaintext,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Created API key %s (%s)\n", key.ID, key.Name)
			if strings.TrimSpace(key.ProjectID) != "" {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "project_id: %s\n", key.ProjectID)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "scopes: %s\n", strings.Join(key.Scopes, ","))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "api_key: %s\n", plaintext)
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "Store the API key now; it will not be shown again.")
			return nil
		},
	}
	createCommand.Flags().StringVar(&createName, "name", "", "Human-readable key name (required)")
	createCommand.Flags().StringSliceVar(&createScopes, "scopes", nil, "Key scopes such as projects:read, backups:write (required; comma-separated or repeatable)")
	createCommand.Flags().StringVar(&createProjectRef, "project", "", "Restrict the key to a single project (id, slug, or name)")
	createCommand.Flags().StringVar(&createExpiresIn, "expires-in", "", "Optional key lifetime such as 24h, 7d, 30d, or an RFC3339 timestamp")
	createCommand.Flags().StringVar(&createDeviceName, "device-name", "", "Device label stored with the key")

	revokeCommand := &cobra.Command{
		Use:   "revoke <key-id>",
		Short: "Revoke an API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			keyID := strings.TrimSpace(args[0])
			if keyID == "" {
				return usageErrorf("key id is required")
			}

			client, authConfig, err := a.resolveClient(true)
			if err != nil {
				return err
			}
			orgID, err := a.resolveOrgID(ctx, client, authConfig)
			if err != nil {
				return err
			}

			if err := client.RevokeAPIKey(ctx, orgID, keyID); err != nil {
				return fmt.Errorf("revoke api key: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"revoked": true, "key_id": keyID})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Revoked API key %s\n", keyID)
			return nil
		},
	}

	command.AddCommand(listCommand)
	command.AddCommand(createCommand)
	command.AddCommand(revokeCommand)
	return command
}

func writeAPIKeyTable(out io.Writer, keys []api.APIKey) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tNAME\tPREFIX\tSCOPES\tPROJECT\tACTIVE\tEXPIRES_AT\tLAST_USED_AT")
	for _, key := range keys {
		active := "no"
		if key.IsActive {
			active = "yes"
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			key.ID,
			key.Name,
			firstNonEmpty(key.KeyPrefix, "-"),
			firstNonEmpty(strings.Join(key.Scopes, ","), "-"),
			firstNonEmpty(key.ProjectID, "-"),
			active,
			formatOptionalTime(key.ExpiresAt),
			formatOptionalTime(key.LastUsedAt),
		)
	}
	_ = writer.Flush()
}

// formatOptionalTime renders a nullable timestamp as RFC3339 or "-".
func formatOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return formatTime(*value)
}
