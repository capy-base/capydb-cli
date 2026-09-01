package cli

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for --source-url
	"github.com/spf13/cobra"

	"github.com/capy-base/capydb-cli/internal/scan"
)

// newMigrateCommand groups the provider-migration tooling: `scan` (read-only
// repo classification + plan) and `verify` (post-cutover source watch). Both
// work without CapyDB credentials - they operate on the user's repo and the
// user's OLD database.
func (a *app) newMigrateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Plan and verify a migration from another Postgres provider",
	}
	command.AddCommand(a.newMigrateScanCommand())
	command.AddCommand(a.newMigrateVerifyCommand())
	command.AddCommand(a.newMigrateDepsCommand())
	command.AddCommand(a.newMigrateCodemodCommand())
	command.AddCommand(a.newMigrateRLSCommand())
	command.AddCommand(a.newMigrateSquashCommand())
	return command
}

func (a *app) newMigrateScanCommand() *cobra.Command {
	var portfolioDir string
	var sourceURL string

	command := &cobra.Command{
		Use:   "scan [path]",
		Short: "Classify a repository and emit a CapyDB migration plan (read-only)",
		Long: `Scans a repository (nothing mutated) and classifies it across the three
migration axes - source database provider, auth system, data-access layer -
plus provider-coupled services, then emits a per-database migration plan with
an effort grade. The repo scan is fully offline.

Pass --source-url with the OLD database's DIRECT connection string to also
measure what the repo cannot show: the live RLS corpus and how it resolves the
caller (which decides the RLS migration path), whether real users exist yet,
extensions that are installed but unused, provider URLs persisted in data
rows, and signs of another data import in flight. The probes are plain
read-only SELECTs with a statement timeout; anything the role cannot see is
reported as skipped.

Pass --portfolio <dir> to also grep sibling repos' env files for the same
database hostnames: a database's cutover must swap EVERY consumer in one step.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := a.cwd
			if len(args) == 1 {
				root = args[0]
			}
			report, err := scan.Run(root, strings.TrimSpace(portfolioDir))
			if err != nil {
				return fmt.Errorf("migrate scan: %w", err)
			}
			if url := strings.TrimSpace(sourceURL); url != "" {
				db, err := sql.Open("pgx", url)
				if err != nil {
					return fmt.Errorf("migrate scan: open source database: %w", err)
				}
				defer func() { _ = db.Close() }()
				facts, err := scan.ProbeSource(cmd.Context(), db)
				if err != nil {
					return fmt.Errorf("migrate scan: %w", err)
				}
				report.AttachSource(facts)
			}
			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), map[string]any{"scan": report})
			}
			writeMigrateScan(cmd, report)
			return nil
		},
	}

	command.Flags().StringVar(&portfolioDir, "portfolio", "", "Directory of sibling repos to check for other consumers of the same databases")
	command.Flags().StringVar(&sourceURL, "source-url", "", "The OLD provider's DIRECT connection string - adds read-only live-database probes to the plan")
	return command
}

func writeMigrateScan(cmd *cobra.Command, report scan.Report) {
	out := cmd.OutOrStdout()

	_, _ = fmt.Fprintf(out, "scenario: %s\n", report.Scenario.Name)
	_, _ = fmt.Fprintf(out, "effort: %s\n\n", report.Scenario.Effort)

	if len(report.Databases) == 0 {
		_, _ = fmt.Fprintln(out, "databases: none found in env files")
	}
	for _, database := range report.Databases {
		pooled := ""
		if database.Pooled {
			pooled = " [pooler endpoint - do not dump from this]"
		}
		_, _ = fmt.Fprintf(out, "database: %s (%s)%s\n", database.Hostname, database.Provider, pooled)
		if len(database.EnvKeys) > 0 {
			_, _ = fmt.Fprintf(out, "  env keys: %s\n", strings.Join(database.EnvKeys, ", "))
		}
		if len(database.Consumers) > 0 {
			_, _ = fmt.Fprintf(out, "  ALSO CONSUMED BY: %s\n", strings.Join(database.Consumers, ", "))
		}
	}

	_, _ = fmt.Fprintf(out, "\nauth: %s\n", joinOrDash(report.Repo.AuthSystems))
	_, _ = fmt.Fprintf(out, "data layers: %s\n", joinOrDash(report.Repo.DataLayers))
	sites := report.Repo.CallSites
	_, _ = fmt.Fprintf(out, "call sites: supabase data=%d auth=%d storage=%d realtime=%d | neon driver files=%d batch calls=%d\n",
		sites.SupabaseData, sites.SupabaseAuth, sites.SupabaseStorage, sites.SupabaseRealtime,
		len(sites.NeonDriverFiles), sites.NeonBatchCalls)
	if report.Repo.SupabaseAssets.MigrationFiles > 0 || report.Repo.SupabaseAssets.EdgeFunctions > 0 {
		_, _ = fmt.Fprintf(out, "supabase assets: %d migration file(s), %d edge function(s)\n",
			report.Repo.SupabaseAssets.MigrationFiles, report.Repo.SupabaseAssets.EdgeFunctions)
	}
	writeMigrateScanSource(out, report.Source)

	_, _ = fmt.Fprintln(out, "\nplan:")
	for index, step := range report.Scenario.Plan {
		_, _ = fmt.Fprintf(out, "  %d. %s\n", index+1, step)
	}
	if len(report.Scenario.Warnings) > 0 {
		_, _ = fmt.Fprintln(out, "\nwarnings:")
		for _, warning := range report.Scenario.Warnings {
			_, _ = fmt.Fprintf(out, "  - %s\n", warning)
		}
	}
}

// writeMigrateScanSource renders the live-database probe block. The repo says
// what the code does; this block says what the database says - when they
// disagree, the database wins.
func writeMigrateScanSource(out io.Writer, source *scan.SourceFacts) {
	if source == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "\nsource database (live, read-only probes):\n")
	_, _ = fmt.Fprintf(out, "  postgres %s, %s, %d public table(s)\n",
		source.ServerVersion, formatBytes(source.DatabaseSizeBytes), source.PublicTables)
	policies := source.Policies
	if policies.Total > 0 {
		helperSuffix := ""
		if policies.ViaHelpers > 0 {
			helperSuffix = fmt.Sprintf(" via helpers (%s)", strings.Join(policies.HelperNames, ", "))
		}
		_, _ = fmt.Fprintf(out, "  rls policies: %d live - %d direct auth.*, %d%s\n",
			policies.Total, policies.DirectAuthRefs, policies.ViaHelpers, helperSuffix)
	}
	if users := source.AuthUsers; users != nil {
		lastSignIn := users.LastSignIn
		if lastSignIn == "" {
			lastSignIn = "never"
		}
		_, _ = fmt.Fprintf(out, "  auth users: %d (last sign-in: %s)\n", users.Count, lastSignIn)
	}
	for _, extension := range source.Extensions {
		if extension.Available {
			continue
		}
		usage := "likely unused (0 dependent objects)"
		if extension.Dependents > 0 {
			usage = fmt.Sprintf("%d dependent object(s)", extension.Dependents)
		}
		_, _ = fmt.Fprintf(out, "  extension not on CapyDB: %s %s - %s\n", extension.Name, extension.Version, usage)
	}
	for _, bucket := range source.StorageBuckets {
		_, _ = fmt.Fprintf(out, "  storage bucket: %s (%d object(s), %s)\n", bucket.Name, bucket.Objects, formatBytes(bucket.Bytes))
	}
	for _, note := range source.Notes {
		_, _ = fmt.Fprintf(out, "  note: %s\n", note)
	}
}

func joinOrDash(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}

// placeholderSecretPattern flags env values that were clearly never replaced.
var placeholderSecretPattern = regexp.MustCompile(`(?i)^(|replace([-_ ]?me)?.*|change[-_ ]?me.*|placeholder.*|your[-_].*|<[^>]*>|xxx+|dummy.*|example.*|todo.*|fixme.*)$`)

// secretKeyPattern selects env keys whose values must be real secrets.
var secretKeyPattern = regexp.MustCompile(`(?i)(SECRET|PASSWORD|_KEY$|_TOKEN)`)

func (a *app) newMigrateVerifyCommand() *cobra.Command {
	var sourceURL string
	var watch time.Duration
	var interval time.Duration
	var envPaths []string

	command := &cobra.Command{
		Use:   "verify",
		Short: "Verify a completed cutover: watch the OLD source for leftover consumers",
		Long: `After swapping every consumer to CapyDB, point this at the OLD database.
It samples pg_stat_activity over the watch window: zero client connections
(other than this check) means every consumer moved; anything still connecting
is a deployable you missed. Optionally validates env files for placeholder
secrets (--env-file, repeatable).

Requires psql on PATH; connects only to the URL you pass.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			leftovers := map[string]bool{}

			if strings.TrimSpace(sourceURL) != "" {
				psqlPath, err := exec.LookPath("psql")
				if err != nil {
					return fmt.Errorf("psql is required for migrate verify: %w", err)
				}
				samples := max(1, int(watch/interval))
				_, _ = fmt.Fprintf(out, "watching source pg_stat_activity: %d sample(s) over %s\n", samples, watch)
				// Provider-internal services (Supabase's PostgREST, exporters,
				// admin roles, pooler auth probes) hold connections on every
				// project regardless of app consumers - filter them so the
				// verdict reflects YOUR deployables only.
				query := `SELECT usename || '|' || COALESCE(application_name,'') || '|' || COALESCE(client_addr::text,'local')
FROM pg_stat_activity
WHERE pid <> pg_backend_pid() AND backend_type = 'client backend'
  AND COALESCE(application_name,'') NOT LIKE 'capydb_%'
  AND usename NOT IN ('supabase_admin', 'supabase_auth_admin', 'supabase_storage_admin',
                      'supabase_replication_admin', 'supabase_read_only_user',
                      'authenticator', 'pgbouncer', 'rdsadmin', 'azuresu', 'cloudsqladmin')
  AND COALESCE(application_name,'') NOT IN ('postgres_exporter', 'pg_cron scheduler')
  AND COALESCE(application_name,'') NOT LIKE 'PostgREST%'
  AND COALESCE(application_name,'') NOT LIKE 'Supavisor (auth_query)%'`
				for sample := range samples {
					if sample > 0 {
						select {
						case <-cmd.Context().Done():
							return cmd.Context().Err()
						case <-time.After(interval):
						}
					}
					output, err := exec.CommandContext(cmd.Context(), psqlPath, sourceURL, "-tAc", query).CombinedOutput()
					if err != nil {
						return fmt.Errorf("query source pg_stat_activity: %s", strings.TrimSpace(string(output)))
					}
					for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
						if line = strings.TrimSpace(line); line != "" {
							leftovers[line] = true
						}
					}
				}
				if len(leftovers) == 0 {
					_, _ = fmt.Fprintln(out, "source connections: none - every consumer has moved")
				} else {
					_, _ = fmt.Fprintln(out, "source connections STILL ACTIVE (user|application|address):")
					for connection := range leftovers {
						_, _ = fmt.Fprintf(out, "  - %s\n", connection)
					}
					_, _ = fmt.Fprintln(out, "a deployable is still using the old database - find and swap it before decommissioning")
				}
			}

			placeholderFindings := 0
			for _, envPath := range envPaths {
				resolved := envPath
				if !filepath.IsAbs(resolved) {
					resolved = filepath.Join(a.cwd, envPath)
				}
				findings, err := findPlaceholderSecrets(resolved)
				if err != nil {
					return err
				}
				for _, finding := range findings {
					placeholderFindings++
					_, _ = fmt.Fprintf(out, "placeholder secret: %s in %s\n", finding, envPath)
				}
			}
			if len(envPaths) > 0 && placeholderFindings == 0 {
				_, _ = fmt.Fprintln(out, "env secrets: no placeholder values detected")
			}

			if len(leftovers) > 0 || placeholderFindings > 0 {
				cmd.SilenceUsage = true
				return fmt.Errorf("verify found %d leftover connection(s) and %d placeholder secret(s)", len(leftovers), placeholderFindings)
			}
			if strings.TrimSpace(sourceURL) == "" && len(envPaths) == 0 {
				return fmt.Errorf("nothing to verify: pass --source-url and/or --env-file")
			}
			_, _ = fmt.Fprintln(out, "verify: ok")
			return nil
		},
	}

	command.Flags().StringVar(&sourceURL, "source-url", "", "The OLD provider's connection string (direct endpoint)")
	command.Flags().DurationVar(&watch, "watch", 30*time.Second, "How long to watch the source for connections")
	command.Flags().DurationVar(&interval, "interval", 5*time.Second, "Sampling interval")
	command.Flags().StringArrayVar(&envPaths, "env-file", nil, "Env file(s) to check for placeholder secrets (repeatable)")
	return command
}

func findPlaceholderSecrets(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read env file: %w", err)
	}
	var findings []string
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(trimmed, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if secretKeyPattern.MatchString(key) && placeholderSecretPattern.MatchString(value) {
			findings = append(findings, key)
		}
	}
	return findings, nil
}

func (a *app) newMigrateDepsCommand() *cobra.Command {
	var dir string
	var dryRun bool

	command := &cobra.Command{
		Use:   "deps",
		Short: "Swap Neon driver dependencies for postgres-js in package.json files",
		Long: `The mechanical half of the Neon driver swap: removes @neondatabase/serverless
(and @vercel/postgres) from every package.json in the tree and adds
"postgres" where one was removed. For the code half (imports, client
construction, drizzle adapter, env cleanup) run 'capydb migrate codemod neon';
'capydb migrate scan' still gives the per-repo plan.

Run your package manager's install afterwards, capture a typecheck baseline
first if the repo pins dist-tags (see scan warnings).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root := a.cwd
			if strings.TrimSpace(dir) != "" {
				root = dir
			}
			out := cmd.OutOrStdout()
			changed := 0
			walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if entry.IsDir() {
					if entry.Name() == "node_modules" || entry.Name() == ".git" {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.Name() != "package.json" {
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				content := string(data)
				next, changedFile := swapNeonPackageJSON(content)
				if !changedFile {
					return nil
				}
				relative, relErr := filepath.Rel(root, path)
				if relErr != nil {
					relative = path
				}
				if dryRun {
					_, _ = fmt.Fprintf(out, "would update %s\n", relative)
					changed++
					return nil
				}
				if err := os.WriteFile(path, []byte(next), 0o644); err != nil {
					return fmt.Errorf("write %s: %w", relative, err)
				}
				_, _ = fmt.Fprintf(out, "updated %s (removed neon driver, ensured \"postgres\")\n", relative)
				changed++
				return nil
			})
			if walkErr != nil {
				return walkErr
			}
			if changed == 0 {
				_, _ = fmt.Fprintln(out, "no package.json references @neondatabase/serverless or @vercel/postgres")
				return nil
			}
			_, _ = fmt.Fprintln(out, "next: run your package manager install, then apply the code changes from `capydb migrate scan`")
			return nil
		},
	}

	command.Flags().StringVar(&dir, "dir", "", "Directory to scan (defaults to the current directory)")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Report the files that would change without writing")
	return command
}
