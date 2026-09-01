package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/capy-base/capydb-cli/internal/exitcode"
)

// `capydb migrate squash` wraps the capysquash engine (the sibling MIT
// migration-consolidation product, github.com/capysquash) the same way
// `migrate rls` wraps capyrls: the scan finds the drifted history, this verb
// points the tool at it. It stays an exec wrapper - the engine parses SQL
// with pg_query_go (cgo) and this CLI is CGO_ENABLED=0 cross-compiled, so
// linking it in-process is off the table.
//
// Read-only by default (analyze); consolidation runs only through an explicit
// --workflow safe|fast. The wrapper deliberately does not mirror the
// capysquash flag surface - custom rules, configs and linting are its own job.

const capysquashInstallHint = "go install github.com/capysquash/pgsquash-engine/cmd/pgsquash@latest\n" +
	"  (needs Go and a C toolchain - the SQL parser is cgo; macOS/Linux are fine)"

func (a *app) newMigrateSquashCommand() *cobra.Command {
	var workflow string

	command := &cobra.Command{
		Use:   "squash [migrations-dir]",
		Short: "Analyze or consolidate a migration history via the capysquash engine",
		Long: "Long migration histories drift from the schema they claim to describe -\n" +
			"`capydb migrate scan` measures the drift, and the fix before a move is to\n" +
			"consolidate the history into a clean baseline. This command hands the\n" +
			"directory to the open-source capysquash engine (AST-level consolidation with\n" +
			"catalog-proven equivalence; Supabase/Drizzle/Prisma aware).\n" +
			"\n" +
			"By default it runs the read-only ANALYZE workflow and changes nothing. Pass\n" +
			"--workflow safe (conservative consolidation, full Docker validation) or\n" +
			"--workflow fast (development-speed consolidation) to actually consolidate.\n" +
			"\n" +
			"Requires the capysquash CLI on PATH; install with:\n" +
			"  " + capysquashInstallHint,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch workflow {
			case "analyze", "safe", "fast":
			default:
				return usageErrorf("unknown --workflow %q (analyze, safe or fast)", workflow)
			}

			binary, err := findCapysquashBinary()
			if err != nil {
				return err
			}

			root := a.cwd
			if len(args) == 1 {
				root = args[0]
			}
			target := resolveMigrationsDir(root)
			// The engine's commands take migration FILES, not a directory.
			files, err := listMigrationFiles(target)
			if err != nil {
				return err
			}

			child := exec.CommandContext(cmd.Context(), binary, append([]string{workflow}, files...)...)
			child.Stdout = cmd.OutOrStdout()
			child.Stderr = cmd.ErrOrStderr()
			child.Stdin = cmd.InOrStdin()
			if err := child.Run(); err != nil {
				cmd.SilenceUsage = true
				return &exitcode.Error{Code: exitcode.GenericError,
					Err: fmt.Errorf("%s %s failed: %w", filepath.Base(binary), workflow, err)}
			}
			if workflow == "analyze" {
				name := filepath.Base(binary)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(),
					"\nanalysis only - nothing was changed. To consolidate:\n"+
						"  capydb migrate squash --workflow safe %s   # conservative, full Docker validation\n"+
						"  capydb migrate squash --workflow fast %s   # development-speed, schema-diff validation\n"+
						"(or use %s directly for custom rules and configs)\n",
					target, target, name)
			}
			return nil
		},
	}

	command.Flags().StringVar(&workflow, "workflow", "analyze", "analyze (read-only, default), safe or fast (both consolidate)")
	return command
}

// capysquashLookPath is swapped in tests.
var capysquashLookPath = exec.LookPath

// findCapysquashBinary prefers the platform CLI name and falls back to the
// bare engine binary.
func findCapysquashBinary() (string, error) {
	for _, name := range []string{"capysquash", "pgsquash"} {
		if path, err := capysquashLookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("capysquash is not on PATH - install it with:\n  %s", capysquashInstallHint)
}

// listMigrationFiles expands the target directory into its .sql files, sorted
// so migration ordering holds; a file target passes through as-is.
func listMigrationFiles(target string) ([]string, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, fmt.Errorf("read migrations path: %w", err)
	}
	if !info.IsDir() {
		return []string{target}, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".sql") {
			files = append(files, filepath.Join(target, entry.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no .sql files under %s - point me at a migrations directory or a single SQL file", target)
	}
	return files, nil
}

// resolveMigrationsDir mirrors `migrate rls`: pointed at a repo root, find the
// conventional migrations directory on its own.
func resolveMigrationsDir(root string) string {
	for _, candidate := range []string{
		filepath.Join(root, "supabase", "migrations"),
		filepath.Join(root, "migrations"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return root
}
