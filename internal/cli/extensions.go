package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb/cli/internal/api"
)

func (a *app) newExtensionsCommand() *cobra.Command {
	command := &cobra.Command{
		Use:     "extensions",
		Aliases: []string{"extension", "ext"},
		Short:   "Manage Postgres extensions for a project",
	}

	var listProjectRef string
	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List available and enabled extensions for a project",
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

			extensions, err := client.ListProjectExtensions(ctx, project.ID)
			if err != nil {
				return fmt.Errorf("list extensions: %w", err)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"extensions": jsonList(extensions)})
			}
			if len(extensions) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No extensions available for project %s\n", project.Name)
				return nil
			}

			writeExtensionTable(cmd.OutOrStdout(), extensions)
			return nil
		},
	}
	listCommand.Flags().StringVar(&listProjectRef, "project", "", "Project id, slug, or name")

	var enableProjectRef string
	var enableWait bool
	var enableWaitTimeout time.Duration
	enableCommand := &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a Postgres extension on the project database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := strings.TrimSpace(args[0])
			if name == "" {
				return usageErrorf("extension name is required")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, enableProjectRef)
			if err != nil {
				return err
			}

			job, err := client.EnableProjectExtension(ctx, project.ID, name)
			if err != nil {
				return fmt.Errorf("enable extension: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued enable of extension %s for project %s\n", name, project.Name)
			}
			return a.maybeWaitForJob(cmd, client, job, enableWait, enableWaitTimeout, "extension enable")
		},
	}
	enableCommand.Flags().StringVar(&enableProjectRef, "project", "", "Project id, slug, or name")
	addWaitFlags(enableCommand, &enableWait, &enableWaitTimeout, "extension enable")

	var disableProjectRef string
	var disableWait bool
	var disableWaitTimeout time.Duration
	disableCommand := &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable (drop) a Postgres extension on the project database",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := strings.TrimSpace(args[0])
			if name == "" {
				return usageErrorf("extension name is required")
			}

			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}
			project, err := a.resolveProject(ctx, client, disableProjectRef)
			if err != nil {
				return err
			}

			job, err := client.DisableProjectExtension(ctx, project.ID, name)
			if err != nil {
				return fmt.Errorf("disable extension: %w", err)
			}
			if !a.jsonOutput() {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Queued disable of extension %s for project %s\n", name, project.Name)
			}
			return a.maybeWaitForJob(cmd, client, job, disableWait, disableWaitTimeout, "extension disable")
		},
	}
	disableCommand.Flags().StringVar(&disableProjectRef, "project", "", "Project id, slug, or name")
	addWaitFlags(disableCommand, &disableWait, &disableWaitTimeout, "extension disable")

	command.AddCommand(listCommand)
	command.AddCommand(enableCommand)
	command.AddCommand(disableCommand)
	return command
}

func writeExtensionTable(out io.Writer, extensions []api.ProjectExtension) {
	writer := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(writer, "NAME\tENABLED\tTRUSTED\tVERSION\tDESCRIPTION")
	for _, extension := range extensions {
		enabled := "no"
		if extension.Enabled {
			enabled = "yes"
		}
		trusted := "no"
		if extension.Trusted {
			trusted = "yes"
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\n",
			extension.Name,
			enabled,
			trusted,
			firstNonEmpty(extension.DefaultVersion, "-"),
			firstNonEmpty(extension.Description, "-"),
		)
	}
	_ = writer.Flush()
}
