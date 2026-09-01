package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb-cli/internal/api"
)

// newGenerateCommand groups the code generators that render the linked
// database's live schema as source code. Generation happens server-side (one
// implementation shared with the API and MCP server); the CLI fetches the
// result and writes the file.
func (a *app) newGenerateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "generate",
		Short: "Generate code from the database schema",
		Long:  "Generates typed source code (TypeScript interfaces, Zod schemas, or a Drizzle schema) from the live schema of the linked project or a preview database.",
	}
	command.AddCommand(a.newGenerateSubcommand(
		"types",
		"Generate TypeScript types from the database schema",
		"typescript",
		true,
	))
	command.AddCommand(a.newGenerateSubcommand(
		"zod",
		"Generate Zod schemas from the database schema",
		"zod",
		false,
	))
	command.AddCommand(a.newGenerateSubcommand(
		"drizzle",
		"Generate a Drizzle schema from the database schema",
		"drizzle",
		false,
	))
	return command
}

func (a *app) newGenerateSubcommand(use, short, language string, withStyle bool) *cobra.Command {
	var (
		outPath    string
		print      bool
		previewID  string
		projectRef string
		style      string
	)

	command := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
			if err != nil {
				return err
			}

			var types api.GeneratedTypes
			if trimmed := strings.TrimSpace(previewID); trimmed != "" {
				types, err = client.GeneratePreviewSchemaTypes(ctx, trimmed, language, style)
			} else {
				var project api.Project
				project, err = a.resolveProject(ctx, client, projectRef)
				if err != nil {
					return err
				}
				types, err = client.GenerateProjectSchemaTypes(ctx, project.ID, language, style)
			}
			if err != nil {
				return fmt.Errorf("generate %s: %w", language, err)
			}

			if print {
				if a.jsonOutput() {
					return printJSON(cmd.OutOrStdout(), types)
				}
				_, _ = fmt.Fprint(cmd.OutOrStdout(), types.Content)
				return nil
			}

			target := strings.TrimSpace(outPath)
			if target == "" {
				target = types.Filename
			}
			if dir := filepath.Dir(target); dir != "." && dir != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					return fmt.Errorf("create output directory: %w", err)
				}
			}
			if err := os.WriteFile(target, []byte(types.Content), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", target, err)
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{
					"language": types.Language,
					"path":     target,
					"style":    types.Style,
				})
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s\n", target)
			return nil
		},
	}

	// No -o shorthand: the root command's persistent --output (-o) owns it.
	command.Flags().StringVar(&outPath, "out", "", "Output file path (defaults to the generator's suggested filename)")
	command.Flags().BoolVar(&print, "print", false, "Print the generated code to stdout instead of writing a file")
	command.Flags().StringVar(&previewID, "preview", "", "Generate from a preview database instead of the project database")
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	if withStyle {
		command.Flags().StringVar(&style, "style", "", "TypeScript output shape: capydb (default) or supabase (compatible with supabase-js generics)")
	}
	return command
}
