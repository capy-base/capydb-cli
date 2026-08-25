package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
	"github.com/capy-base/capydb/cli/internal/config"
)

// orgListEntry is the stable JSON shape for `capydb orgs list`.
type orgListEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Slug   string `json:"slug,omitempty"`
	APIURL string `json:"api_url"`
	Active bool   `json:"active"`
}

func (a *app) newOrgsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "orgs",
		Aliases: []string{"org", "organizations"},
		Short:   "Manage the organizations the CLI is logged in to",
	}

	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List organizations with saved CLI credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			userConfig, err := config.LoadUserConfig()
			if err != nil {
				return err
			}

			entries := orgConfigEntries(userConfig)
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"organizations": jsonList(entries)})
			}
			if len(entries) == 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "No saved organizations; run `capydb login`.")
				return nil
			}

			writeOrgTable(cmd.OutOrStdout(), entries)
			return nil
		},
	}

	switchCommand := &cobra.Command{
		Use:   "switch <org-id|slug>",
		Short: "Switch the active organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := strings.TrimSpace(args[0])
			if ref == "" {
				return usageErrorf("organization id or slug is required")
			}

			userConfig, err := config.LoadUserConfig()
			if err != nil {
				return err
			}
			if len(userConfig.Organizations) == 0 {
				return notFoundErrorf("no saved organizations; run `capydb login` first")
			}

			targetID, err := matchOrgRef(userConfig, ref)
			if err != nil {
				return err
			}

			userConfig.ActiveOrg = targetID
			if err := config.SaveUserConfig(userConfig); err != nil {
				return err
			}

			entry := userConfig.Organizations[targetID]
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), orgListEntry{
					ID:     targetID,
					Name:   entry.Name,
					Slug:   entry.Slug,
					APIURL: entry.APIURL,
					Active: true,
				})
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Switched active organization to %s (%s)\n", firstNonEmpty(entry.Name, targetID), firstNonEmpty(entry.Slug, targetID))
			return nil
		},
	}

	command.AddCommand(listCommand)
	command.AddCommand(switchCommand)
	return command
}

// matchOrgRef resolves an org reference (id or slug) against the saved
// organizations. Ambiguous slugs require the id instead.
func matchOrgRef(userConfig config.UserConfig, ref string) (string, error) {
	if _, found := userConfig.Organizations[ref]; found {
		return ref, nil
	}

	var matches []string
	for orgID, entry := range userConfig.Organizations {
		if strings.EqualFold(entry.Slug, ref) || strings.EqualFold(entry.Name, ref) {
			matches = append(matches, orgID)
		}
	}
	switch len(matches) {
	case 0:
		return "", notFoundErrorf("organization %q not found in the saved CLI config; run `capydb login` to add it", ref)
	case 1:
		return matches[0], nil
	default:
		return "", usageErrorf("organization %q is ambiguous; use the organization id instead", ref)
	}
}

func orgConfigEntries(userConfig config.UserConfig) []orgListEntry {
	activeOrgID, _, _ := userConfig.Active()

	entries := make([]orgListEntry, 0, len(userConfig.Organizations))
	for orgID, entry := range userConfig.Organizations {
		entries = append(entries, orgListEntry{
			ID:     orgID,
			Name:   entry.Name,
			Slug:   entry.Slug,
			APIURL: entry.APIURL,
			Active: orgID == activeOrgID,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries
}

// resolveOrgID returns the organization id org-scoped endpoints should target:
// the resolved auth's organization when known, otherwise the viewer's.
func (a *app) resolveOrgID(ctx context.Context, client *api.Client, authConfig resolvedAuth) (string, error) {
	if trimmed := strings.TrimSpace(authConfig.OrganizationID); trimmed != "" && trimmed != config.DefaultOrgKey {
		return trimmed, nil
	}

	viewer, err := client.GetViewer(ctx)
	if err != nil {
		return "", fmt.Errorf("load viewer: %w", err)
	}
	if viewer.Organization == nil || strings.TrimSpace(viewer.Organization.ID) == "" {
		return "", notFoundErrorf("no organization is associated with this api key")
	}
	return viewer.Organization.ID, nil
}

func writeOrgTable(out io.Writer, entries []orgListEntry) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "ID\tNAME\tSLUG\tAPI_URL\tACTIVE")
	for _, entry := range entries {
		active := ""
		if entry.Active {
			active = "*"
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\n",
			entry.ID,
			firstNonEmpty(entry.Name, "-"),
			firstNonEmpty(entry.Slug, "-"),
			entry.APIURL,
			active,
		)
	}
	_ = writer.Flush()
}
