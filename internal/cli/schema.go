package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capydatabase/capydb-cli/internal/api"
	"github.com/capydatabase/capydb-cli/internal/exitcode"
)

// newSchemaCommand groups schema introspection utilities: dump writes the
// canonical schema document (committable, deterministic), diff compares two
// schema sources for drift detection in PRs and agent loops.
func (a *app) newSchemaCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "schema",
		Short: "Inspect and diff the database schema",
	}
	command.AddCommand(a.newSchemaDumpCommand())
	command.AddCommand(a.newSchemaDiffCommand())
	return command
}

func (a *app) newSchemaDumpCommand() *cobra.Command {
	var (
		outPath    string
		previewID  string
		projectRef string
	)

	command := &cobra.Command{
		Use:   "dump",
		Short: "Write the database schema as canonical JSON",
		Long:  "Introspects the linked project (or a preview database) and prints the canonical schema document. The output is deterministic, so it can be committed and diffed in pull requests.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			schema, err := a.fetchSchema(cmd, previewID, projectRef)
			if err != nil {
				return err
			}

			encoded, err := json.MarshalIndent(schema, "", "  ")
			if err != nil {
				return fmt.Errorf("encode schema: %w", err)
			}
			encoded = append(encoded, '\n')

			if target := strings.TrimSpace(outPath); target != "" {
				if dir := filepath.Dir(target); dir != "." && dir != "" {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						return fmt.Errorf("create output directory: %w", err)
					}
				}
				if err := os.WriteFile(target, encoded, 0o644); err != nil {
					return fmt.Errorf("write %s: %w", target, err)
				}
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %s\n", target)
				return nil
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), string(encoded))
			return nil
		},
	}

	// No -o shorthand: the root command's persistent --output (-o) owns it.
	command.Flags().StringVar(&outPath, "out", "", "Write to a file instead of stdout")
	command.Flags().StringVar(&previewID, "preview", "", "Dump a preview database instead of the project database")
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	return command
}

func (a *app) newSchemaDiffCommand() *cobra.Command {
	var (
		againstFile    string
		againstPreview string
		exitCode       bool
		previewID      string
		projectRef     string
	)

	command := &cobra.Command{
		Use:   "diff",
		Short: "Diff the database schema against a snapshot or preview database",
		Long: "Compares the linked project's live schema (or --preview) against a committed snapshot file (--against) or another preview database (--against-preview) and prints the structural differences. " +
			"With --exit-code the command exits 1 when differences are found, so CI can gate on drift.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if (againstFile == "") == (againstPreview == "") {
				return fmt.Errorf("exactly one of --against <file> or --against-preview <id> is required")
			}

			current, err := a.fetchSchema(cmd, previewID, projectRef)
			if err != nil {
				return err
			}

			var baseline api.DatabaseSchema
			var baselineLabel string
			if againstFile != "" {
				raw, err := os.ReadFile(againstFile)
				if err != nil {
					return fmt.Errorf("read %s: %w", againstFile, err)
				}
				// Accept either a bare schema document or the API envelope
				// ({"schema": ...}) so `capydb schema dump` output and raw API
				// responses both work.
				var envelope struct {
					Schema *api.DatabaseSchema `json:"schema"`
				}
				if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Schema != nil {
					baseline = *envelope.Schema
				} else if err := json.Unmarshal(raw, &baseline); err != nil {
					return fmt.Errorf("parse %s: %w", againstFile, err)
				}
				baselineLabel = againstFile
			} else {
				client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
				if err != nil {
					return err
				}
				baseline, err = client.GetPreviewSchema(cmd.Context(), strings.TrimSpace(againstPreview))
				if err != nil {
					return fmt.Errorf("fetch preview schema: %w", err)
				}
				baselineLabel = "preview " + againstPreview
			}

			differences := diffSchemas(baseline, current)

			if a.jsonOutput() {
				if err := printJSON(cmd.OutOrStdout(), map[string]any{
					"baseline":    baselineLabel,
					"differences": differences,
					"in_sync":     len(differences) == 0,
				}); err != nil {
					return err
				}
			} else if len(differences) == 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "No schema differences against %s.\n", baselineLabel)
			} else {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Schema differences against %s:\n", baselineLabel)
				for _, difference := range differences {
					_, _ = fmt.Fprintln(cmd.OutOrStdout(), difference)
				}
			}

			if exitCode && len(differences) > 0 {
				// Mirror `git diff --exit-code`: differences flip the process
				// exit status so CI can gate on drift.
				cmd.SilenceUsage = true
				return exitcode.Errorf(exitcode.GenericError, "schema differences found")
			}
			return nil
		},
	}

	command.Flags().StringVar(&againstFile, "against", "", "Baseline schema snapshot file (from `capydb schema dump`)")
	command.Flags().StringVar(&againstPreview, "against-preview", "", "Baseline preview database id")
	command.Flags().BoolVar(&exitCode, "exit-code", false, "Exit with status 1 when differences are found")
	command.Flags().StringVar(&previewID, "preview", "", "Diff a preview database instead of the project database")
	command.Flags().StringVar(&projectRef, "project", "", "Project id, slug, or name")
	return command
}

func (a *app) fetchSchema(cmd *cobra.Command, previewID, projectRef string) (api.DatabaseSchema, error) {
	ctx := cmd.Context()
	client, _, err := a.resolveClient(true, a.linkedProjectAPIURL())
	if err != nil {
		return api.DatabaseSchema{}, err
	}

	if trimmed := strings.TrimSpace(previewID); trimmed != "" {
		schema, err := client.GetPreviewSchema(ctx, trimmed)
		if err != nil {
			return api.DatabaseSchema{}, fmt.Errorf("fetch preview schema: %w", err)
		}
		return schema, nil
	}

	project, err := a.resolveProject(ctx, client, projectRef)
	if err != nil {
		return api.DatabaseSchema{}, err
	}
	schema, err := client.GetProjectSchema(ctx, project.ID)
	if err != nil {
		return api.DatabaseSchema{}, fmt.Errorf("fetch project schema: %w", err)
	}
	return schema, nil
}
