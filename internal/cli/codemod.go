package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// The neon codemod mechanizes the driver swap performed by hand across every
// dogfood migration (docs/capydb-migration-codemod-notes.md): postgres-js +
// drizzle-orm/postgres-js replace @neondatabase/serverless, with the CapyDB
// pooler rules (max: 1, prepare: false) applied at the client construction
// site. Anything that needs human judgment (Pool usage, db.batch rewrites,
// per-call client construction) is reported instead of guessed at.

type codemodChange struct {
	Description string `json:"description"`
	File        string `json:"file"`
}

type codemodNote struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

type codemodReport struct {
	Changes []codemodChange `json:"changes"`
	Manual  []codemodNote   `json:"manual"`
	Write   bool            `json:"write"`
}

func (a *app) newMigrateCodemodCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "codemod",
		Short: "Rewrite provider-specific driver code for CapyDB",
	}
	command.AddCommand(a.newMigrateCodemodNeonCommand())
	return command
}

func (a *app) newMigrateCodemodNeonCommand() *cobra.Command {
	var write bool

	command := &cobra.Command{
		Use:   "neon [path]",
		Short: "Rewrite @neondatabase/serverless usage to postgres-js",
		Long: "Rewrites Neon driver imports and client construction to postgres-js with CapyDB's pooler-safe defaults (max: 1, prepare: false), swaps the drizzle adapter to drizzle-orm/postgres-js, " +
			"updates package.json and drizzle.config, and strips Neon-specific connection parameters from env files. Call sites that need human judgment (Pool, db.batch, neonConfig) are reported, not rewritten. " +
			"Dry-run by default; pass --write to apply.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 && strings.TrimSpace(args[0]) != "" {
				root = args[0]
			}

			report, err := runNeonCodemod(root, write)
			if err != nil {
				return err
			}

			if a.jsonOutput() {
				return printJSON(cmd.OutOrStdout(), report)
			}

			out := cmd.OutOrStdout()
			if len(report.Changes) == 0 && len(report.Manual) == 0 {
				_, _ = fmt.Fprintln(out, "No Neon driver usage found.")
				return nil
			}
			if len(report.Changes) > 0 {
				verb := "Would change"
				if write {
					verb = "Changed"
				}
				_, _ = fmt.Fprintf(out, "%s:\n", verb)
				for _, change := range report.Changes {
					_, _ = fmt.Fprintf(out, "  %s: %s\n", change.File, change.Description)
				}
			}
			if len(report.Manual) > 0 {
				_, _ = fmt.Fprintln(out, "\nNeeds manual attention:")
				for _, note := range report.Manual {
					_, _ = fmt.Fprintf(out, "  %s:%d: %s\n", note.File, note.Line, note.Reason)
				}
			}
			if !write && len(report.Changes) > 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "\nDry run - re-run with --write to apply.")
			}
			return nil
		},
	}

	command.Flags().BoolVar(&write, "write", false, "Apply the rewrites (default is a dry run)")
	return command
}

var codemodSkipDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, ".next": {}, ".turbo": {}, ".vercel": {},
	"dist": {}, "build": {}, "out": {}, "coverage": {}, ".output": {},
}

func runNeonCodemod(root string, write bool) (codemodReport, error) {
	report := codemodReport{Changes: []codemodChange{}, Manual: []codemodNote{}, Write: write}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if _, skip := codemodSkipDirs[entry.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}

		name := entry.Name()
		var transform func(string, string, *codemodReport) string
		switch {
		case name == "package.json":
			transform = codemodPackageJSON
		case strings.HasPrefix(name, "drizzle.config."):
			transform = codemodDrizzleConfig
		case strings.HasPrefix(name, ".env"):
			transform = codemodEnvFile
		case hasAnySuffix(name, ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"):
			transform = codemodSourceFile
		default:
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		content := string(raw)
		rewritten := transform(path, content, &report)
		if rewritten != content && write {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("stat %s: %w", path, err)
			}
			if err := os.WriteFile(path, []byte(rewritten), info.Mode().Perm()); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return codemodReport{}, err
	}

	sort.Slice(report.Changes, func(i, j int) bool {
		if report.Changes[i].File != report.Changes[j].File {
			return report.Changes[i].File < report.Changes[j].File
		}
		return report.Changes[i].Description < report.Changes[j].Description
	})
	sort.Slice(report.Manual, func(i, j int) bool {
		if report.Manual[i].File != report.Manual[j].File {
			return report.Manual[i].File < report.Manual[j].File
		}
		return report.Manual[i].Line < report.Manual[j].Line
	})
	return report, nil
}

func hasAnySuffix(name string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

var (
	// import { neon } from "@neondatabase/serverless" (any specifier list,
	// single or double quotes, optional type modifier).
	neonImportPattern = regexp.MustCompile(`import\s+(?:type\s+)?(\{[^}]*\}|\*\s+as\s+\w+|\w+)\s+from\s+(['"])@neondatabase/serverless['"];?`)
	// const sql = neon(url) - single-line calls without an options object.
	neonCallPattern = regexp.MustCompile(`\bneon\(\s*([^(){}\n]+?)\s*\)`)
	// NeonHttpDatabase / NeonDatabase type references.
	neonTypePattern = regexp.MustCompile(`\bNeon(?:Http)?Database\b`)
	// drizzle-orm v1's postgres-js driver no longer accepts a schema map or a
	// schema-shaped PostgresJsDatabase generic. These cover the common
	// drizzle({ client, schema }) / drizzle(client, { schema }) forms emitted by
	// Neon templates without trying to parse arbitrary JavaScript.
	drizzleClientSchemaPattern      = regexp.MustCompile(`drizzle\(\s*\{\s*client:\s*([^,}\n]+)\s*,\s*schema\s*\}\s*\)`)
	drizzleSchemaClientPattern      = regexp.MustCompile(`drizzle\(\s*\{\s*schema\s*,\s*client:\s*([^,}\n]+)\s*\}\s*\)`)
	drizzlePositionalSchemaPattern  = regexp.MustCompile(`drizzle\(\s*([^,()\n]+)\s*,\s*\{\s*schema\s*\}\s*\)`)
	postgresJSDatabaseSchemaPattern = regexp.MustCompile(`PostgresJsDatabase\s*<\s*typeof\s+[A-Za-z_$][A-Za-z0-9_$]*\s*>`)
	// neonDependencyPattern matches the package.json dependency lines the Neon
	// driver swap removes. Kept line-based so the file's formatting survives.
	neonDependencyPattern = regexp.MustCompile(`(?m)^\s*"(@neondatabase/serverless|@vercel/postgres)"\s*:\s*"[^"]*"\s*,?\n`)
	// process.env.DATABASE_URL (with optional non-null assertion) in drizzle configs.
	drizzleURLPattern = regexp.MustCompile(`process\.env\.DATABASE_URL!?`)
	// channel_binding=require connection parameter in env files.
	channelBindingPattern = regexp.MustCompile(`([?&])channel_binding=require&?`)
)

func codemodSourceFile(path, content string, report *codemodReport) string {
	if !strings.Contains(content, "@neondatabase/serverless") &&
		!strings.Contains(content, "drizzle-orm/neon-") &&
		!neonTypePattern.MatchString(content) {
		return content
	}

	rewritten := content

	if strings.Contains(rewritten, "drizzle-orm/neon-http") {
		rewritten = strings.ReplaceAll(rewritten, "drizzle-orm/neon-http", "drizzle-orm/postgres-js")
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "drizzle-orm/neon-http -> drizzle-orm/postgres-js"})
	}
	if strings.Contains(rewritten, "drizzle-orm/neon-serverless") {
		rewritten = strings.ReplaceAll(rewritten, "drizzle-orm/neon-serverless", "drizzle-orm/postgres-js")
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "drizzle-orm/neon-serverless -> drizzle-orm/postgres-js"})
	}
	if neonTypePattern.MatchString(rewritten) {
		rewritten = neonTypePattern.ReplaceAllString(rewritten, "PostgresJsDatabase")
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "NeonHttpDatabase/NeonDatabase -> PostgresJsDatabase"})
	}

	if matches := neonImportPattern.FindStringSubmatch(rewritten); matches != nil {
		specifiers := matches[1]
		quote := matches[2]
		rewritten = neonImportPattern.ReplaceAllString(rewritten, "import postgres from "+quote+"postgres"+quote+";")
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "@neondatabase/serverless import -> postgres"})
		for _, identifier := range []string{"Pool", "Client", "neonConfig"} {
			if strings.Contains(specifiers, identifier) {
				report.Manual = append(report.Manual, codemodNote{
					File:   path,
					Line:   lineOf(content, matches[0]),
					Reason: fmt.Sprintf("imported %s from @neondatabase/serverless; rewrite its call sites to postgres-js (transactions work natively)", identifier),
				})
			}
		}
	}

	if neonCallPattern.MatchString(rewritten) {
		rewritten = neonCallPattern.ReplaceAllString(rewritten, "postgres($1, { max: 1, prepare: false })")
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "neon(url) -> postgres(url, { max: 1, prepare: false })"})
	}

	// Drizzle v1's postgres-js driver accepts { client, relations }, not the old
	// schema map. Removing the schema property keeps the query-builder path
	// valid; relational db.query users are called out for a defineRelations
	// migration below.
	drizzleConfigChanged := false
	for _, replacement := range []struct {
		pattern *regexp.Regexp
		value   string
	}{
		{drizzleClientSchemaPattern, `drizzle({ client: $1 })`},
		{drizzleSchemaClientPattern, `drizzle({ client: $1 })`},
		{drizzlePositionalSchemaPattern, `drizzle({ client: $1 })`},
	} {
		if replacement.pattern.MatchString(rewritten) {
			rewritten = replacement.pattern.ReplaceAllString(rewritten, replacement.value)
			drizzleConfigChanged = true
		}
	}
	if postgresJSDatabaseSchemaPattern.MatchString(rewritten) {
		rewritten = postgresJSDatabaseSchemaPattern.ReplaceAllString(rewritten, "PostgresJsDatabase")
		drizzleConfigChanged = true
	}
	if drizzleConfigChanged {
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "removed the legacy Drizzle schema-map config (v1 uses relations)"})
		if strings.Contains(rewritten, ".query.") {
			report.Manual = append(report.Manual, codemodNote{
				File:   path,
				Line:   lineOf(rewritten, ".query."),
				Reason: "db.query uses the relational API; replace the removed schema map with defineRelations(...) and pass relations to drizzle",
			})
		}
	}

	// A type imported from @neondatabase/serverless is removed with that
	// package import. Ensure the replacement type remains bound unless the
	// drizzle adapter import already supplies it.
	if strings.Contains(rewritten, "PostgresJsDatabase") &&
		!regexp.MustCompile(`import\s+(?:type\s+)?\{[^}]*\bPostgresJsDatabase\b[^}]*\}\s+from\s+['\"]drizzle-orm/postgres-js['\"]`).MatchString(rewritten) {
		rewritten = `import type { PostgresJsDatabase } from "drizzle-orm/postgres-js";` + "\n" + rewritten
	}
	// Multiple Neon imports otherwise become duplicate default imports.
	rewritten = dedupeExactLine(rewritten, `import postgres from "postgres";`)
	rewritten = dedupeExactLine(rewritten, `import postgres from 'postgres';`)

	// Patterns that need human judgment: report with line numbers, never guess.
	for _, manual := range []struct {
		needle string
		reason string
	}{
		{".batch(", "db.batch() does not exist on PostgresJsDatabase; rewrite to db.transaction(async (tx) => { ... }) and rebind statements to tx"},
		{"neonConfig", "remove neonConfig/WebSocket wiring; postgres-js connects over TCP"},
		{"new Pool(", "replace Neon Pool with the postgres-js client (max: 1, prepare: false through the pooled endpoint)"},
	} {
		for _, line := range findLines(rewritten, manual.needle) {
			report.Manual = append(report.Manual, codemodNote{File: path, Line: line, Reason: manual.reason})
		}
	}

	// A per-call neon() construction pattern that would leak connections with
	// postgres-js (see codemod notes A4): the swapped client must be a
	// module-level singleton.
	if len(neonCallPattern.FindAllString(content, -1))+strings.Count(content, "postgres(") > 1 && strings.Contains(rewritten, "postgres(") {
		if calls := findLines(rewritten, "postgres("); len(calls) > 1 {
			report.Manual = append(report.Manual, codemodNote{
				File:   path,
				Line:   calls[1],
				Reason: "multiple postgres() constructions in one module; hoist to a single module-level client (postgres-js must not be created per call)",
			})
		}
	}

	return rewritten
}

func dedupeExactLine(content, target string) string {
	seen := false
	lines := strings.Split(content, "\n")
	kept := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) == target {
			if seen {
				continue
			}
			seen = true
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func codemodPackageJSON(path, content string, report *codemodReport) string {
	rewritten, changed := swapNeonPackageJSON(content)
	if !changed {
		return content
	}
	report.Changes = append(report.Changes, codemodChange{File: path, Description: "removed Neon driver dependency, ensured \"postgres\""})
	return rewritten
}

// swapNeonPackageJSON removes the Neon driver dependencies from a
// package.json and adds postgres-js where one was removed. Line-based so the
// file's formatting survives. Shared by `migrate deps` (package.json only)
// and `migrate codemod neon` (full source rewrite).
func swapNeonPackageJSON(content string) (string, bool) {
	if !neonDependencyPattern.MatchString(content) {
		return content, false
	}
	next := neonDependencyPattern.ReplaceAllString(content, "")
	if !regexp.MustCompile(`(?m)^\s*"postgres"\s*:`).MatchString(next) {
		// Insert postgres after the "dependencies": { line - alphabetical
		// placement is cosmetic; formatters restore it.
		next = regexp.MustCompile(`(?m)^(\s*)"dependencies"\s*:\s*\{\n`).
			ReplaceAllString(next, "$0$1  \"postgres\": \"^3.4.9\",\n")
	}
	return next, true
}

func codemodDrizzleConfig(path, content string, report *codemodReport) string {
	if strings.Contains(content, "DATABASE_DIRECT_URL") || !drizzleURLPattern.MatchString(content) {
		return content
	}
	rewritten := drizzleURLPattern.ReplaceAllString(content, "(process.env.DATABASE_DIRECT_URL ?? process.env.DATABASE_URL!)")
	report.Changes = append(report.Changes, codemodChange{File: path, Description: "drizzle-kit credentials prefer DATABASE_DIRECT_URL (DDL must not go through the transaction pooler)"})
	return rewritten
}

func codemodEnvFile(path, content string, report *codemodReport) string {
	rewritten := content
	if channelBindingPattern.MatchString(rewritten) {
		rewritten = channelBindingPattern.ReplaceAllStringFunc(rewritten, func(match string) string {
			// "?param&" keeps "?", "&param" drops entirely, "?param" end drops "?".
			if strings.HasSuffix(match, "&") {
				return match[:1]
			}
			return ""
		})
		report.Changes = append(report.Changes, codemodChange{File: path, Description: "stripped channel_binding=require (not applicable to CapyDB's SCRAM relay)"})
	}
	for _, line := range findLines(rewritten, "neon.tech") {
		report.Manual = append(report.Manual, codemodNote{
			File:   path,
			Line:   line,
			Reason: "Neon connection string; point it at CapyDB (capydb env pull writes DATABASE_URL/DATABASE_DIRECT_URL) after importing the data",
		})
	}
	return rewritten
}

// findLines returns the 1-indexed line numbers containing needle.
func findLines(content, needle string) []int {
	var lines []int
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, needle) {
			lines = append(lines, i+1)
		}
	}
	return lines
}

// lineOf returns the 1-indexed line where fragment starts, or 1.
func lineOf(content, fragment string) int {
	before, _, found := strings.Cut(content, fragment)
	if !found {
		return 1
	}
	return strings.Count(before, "\n") + 1
}
