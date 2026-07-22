// Package scan implements `capydb migrate scan`: a read-only repository scan
// that classifies an app across the three migration axes (source database
// provider x auth system x data-access layer), runs the migration gates
// (consumers, hygiene, coupling), and emits a per-database migration plan.
// Design and scenario vocabulary: docs/capydb-migration-scenarios-spec.md in
// the capydb repo. The scan never talks to a network or mutates anything.
package scan

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Report is the scan's full result. Databases is the primary output: the unit
// of cutover is the database, not the repo.
type Report struct {
	Path      string     `json:"path"`
	Databases []Database `json:"databases"`
	Repo      RepoFacts  `json:"repo"`
	Scenario  Scenario   `json:"scenario"`
}

// Database is one distinct database hostname the repo references.
type Database struct {
	Hostname  string   `json:"hostname"`
	Provider  string   `json:"provider"` // neon | supabase | azure | rds | capydb | local | other
	Pooled    bool     `json:"pooled"`   // provider pooler endpoint (must not be used for dumps)
	EnvKeys   []string `json:"env_keys"`
	EnvFiles  []string `json:"env_files"`
	Consumers []string `json:"consumers,omitempty"` // other portfolio repos referencing this hostname
}

// RepoFacts are the axis-B/C classification inputs plus the hygiene gate.
type RepoFacts struct {
	AuthSystems    []string  `json:"auth_systems"`
	DataLayers     []string  `json:"data_layers"`
	CallSites      CallSites `json:"call_sites"`
	DistTagPins    []string  `json:"dist_tag_pins"` // "pkg@tag" - moving pointers; lockfile regen imports drift
	EnvFiles       []string  `json:"env_files"`
	Lockfiles      []string  `json:"lockfiles"`
	PackageDirs    []string  `json:"package_dirs"`
	SupabaseAssets Supabase  `json:"supabase_assets"`
}

// CallSites counts provider-coupled call sites per KIND - dependency presence
// alone misclassifies (e.g. supabase-js installed only for storage).
type CallSites struct {
	SupabaseData     int      `json:"supabase_data"`     // supabase.from(...)
	SupabaseAuth     int      `json:"supabase_auth"`     // *.auth.getUser / signIn / onAuthStateChange...
	SupabaseStorage  int      `json:"supabase_storage"`  // storage.from(...)
	SupabaseRealtime int      `json:"supabase_realtime"` // .channel( / postgres_changes
	NeonDriverFiles  []string `json:"neon_driver_files"` // files importing @neondatabase/serverless or drizzle-orm/neon-*
	NeonBatchCalls   int      `json:"neon_batch_calls"`  // db.batch( / sql.transaction([ - need transaction rewrites
}

// Supabase inventories provider-project assets in the repo.
type Supabase struct {
	MigrationFiles int  `json:"migration_files"`
	EdgeFunctions  int  `json:"edge_functions"`
	ConfigPresent  bool `json:"config_present"`
}

// Scenario is the derived plan.
type Scenario struct {
	Name     string   `json:"name"`
	Effort   string   `json:"effort"` // S | M | L | XL
	Plan     []string `json:"plan"`
	Warnings []string `json:"warnings"`
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, ".next": true, ".turbo": true,
	"build": true, "vendor": true, "target": true, "__pycache__": true, ".venv": true,
	"venv": true, "coverage": true, "Pods": true, ".vercel": true, "out": true,
	".output": true, ".cache": true, "DerivedData": true,
}

var sourceExtensions = map[string]bool{
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".mts": true,
	".cjs": true, ".svelte": true, ".vue": true, ".py": true, ".go": true,
}

const maxSourceFileBytes = 1 << 20

var (
	postgresURLPattern = regexp.MustCompile(`postgres(?:ql)?(?:\+[a-z]+)?://[^@\s"']+@([a-zA-Z0-9.-]+)(?::(\d+))?/`)
	supabaseRefPattern = regexp.MustCompile(`https://([a-z0-9]+)\.supabase\.co`)
	envKeyPattern      = regexp.MustCompile(`(?m)^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)
	distTagPattern     = regexp.MustCompile(`^(alpha|beta|rc|canary|next|latest|experimental|nightly|dev)$`)

	supabaseDataPattern     = regexp.MustCompile(`\bsupabase(?:Admin|Client|Server)?\s*\.\s*from\s*\(|\.schema\s*\(\s*['"][^'"]+['"]\s*\)\s*\.\s*from\s*\(`)
	supabaseAuthPattern     = regexp.MustCompile(`\.auth\s*\.\s*(getUser|getSession|signIn|signUp|signOut|onAuthStateChange|admin|exchangeCodeForSession|refreshSession|setSession|getClaims)\b`)
	supabaseStoragePattern  = regexp.MustCompile(`\.storage\s*\.\s*from\s*\(`)
	supabaseRealtimePattern = regexp.MustCompile(`\.channel\s*\(|postgres_changes`)
	neonImportPattern       = regexp.MustCompile(`@neondatabase/serverless|drizzle-orm/neon-`)
	neonBatchPattern        = regexp.MustCompile(`\bdb\s*\.\s*batch\s*\(|\bsql\s*\.\s*transaction\s*\(\s*\[`)
)

// interestingDeps maps dependency names to the classification they signal.
var authDeps = map[string]string{
	"@clerk/nextjs": "clerk", "@clerk/clerk-react": "clerk", "@clerk/backend": "clerk",
	"@clerk/clerk-sdk-node": "clerk", "@clerk/express": "clerk",
	"better-auth": "better-auth",
	"next-auth":   "authjs", "@auth/core": "authjs",
	"lucia": "lucia",
}

var dataLayerDeps = map[string]string{
	"@neondatabase/serverless": "neon-driver",
	"@vercel/postgres":         "neon-driver",
	"postgres":                 "postgres-js",
	"pg":                       "node-postgres",
	"@prisma/client":           "prisma",
	"drizzle-orm":              "drizzle",
	"kysely":                   "kysely",
	"@supabase/supabase-js":    "supabase-js",
	"@supabase/ssr":            "supabase-js",
}

// Run scans root and, when portfolioDir is non-empty, greps sibling repos'
// env files for the same database hostnames (the consumers gate).
func Run(root, portfolioDir string) (Report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return Report{}, err
	}
	report := Report{Path: root}

	databasesByHost := map[string]*Database{}
	supabaseRefs := map[string]bool{}

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relative = path
		}

		switch {
		case name == ".env" || strings.HasPrefix(name, ".env."):
			report.Repo.EnvFiles = append(report.Repo.EnvFiles, relative)
			scanEnvFile(path, relative, databasesByHost, supabaseRefs)
		case name == "package.json":
			report.Repo.PackageDirs = append(report.Repo.PackageDirs, filepath.Dir(relative))
			scanPackageJSON(path, &report.Repo)
		case name == "pnpm-lock.yaml" || name == "package-lock.json" || name == "yarn.lock" || name == "bun.lockb" || name == "go.sum" || name == "uv.lock" || name == "poetry.lock":
			report.Repo.Lockfiles = append(report.Repo.Lockfiles, relative)
		case name == "go.mod":
			scanGoMod(path, &report.Repo)
		case name == "pyproject.toml" || name == "requirements.txt":
			scanPythonDeps(path, &report.Repo)
		}

		if sourceExtensions[filepath.Ext(name)] {
			scanSourceFile(path, relative, &report.Repo.CallSites)
		}
		if strings.Contains(relative, "supabase"+string(filepath.Separator)+"migrations") && strings.HasSuffix(name, ".sql") {
			report.Repo.SupabaseAssets.MigrationFiles++
		}
		if name == "config.toml" && strings.Contains(relative, "supabase") {
			report.Repo.SupabaseAssets.ConfigPresent = true
		}
		return nil
	})
	if walkErr != nil {
		return Report{}, walkErr
	}

	// Edge functions: one directory per function under supabase/functions.
	if entries, err := os.ReadDir(filepath.Join(root, "supabase", "functions")); err == nil {
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), "_") {
				report.Repo.SupabaseAssets.EdgeFunctions++
			}
		}
	}

	// Supabase project refs referenced only by NEXT_PUBLIC_SUPABASE_URL still
	// mean a Supabase database exists even without a postgres:// URL in env.
	for ref := range supabaseRefs {
		host := "db." + ref + ".supabase.co"
		alreadySeen := false
		for existing := range databasesByHost {
			if strings.Contains(existing, ref) || strings.Contains(existing, "pooler.supabase.com") {
				alreadySeen = true
			}
		}
		if !alreadySeen {
			databasesByHost[host] = &Database{Hostname: host, Provider: "supabase", EnvKeys: []string{"NEXT_PUBLIC_SUPABASE_URL"}}
		}
	}

	for _, database := range databasesByHost {
		sort.Strings(database.EnvKeys)
		database.EnvKeys = dedupe(database.EnvKeys)
		sort.Strings(database.EnvFiles)
		database.EnvFiles = dedupe(database.EnvFiles)
		if portfolioDir != "" && database.Provider != "local" {
			database.Consumers = findConsumers(portfolioDir, root, consumerSearchToken(database.Hostname))
		}
		report.Databases = append(report.Databases, *database)
	}
	sort.Slice(report.Databases, func(i, j int) bool { return report.Databases[i].Hostname < report.Databases[j].Hostname })

	normalizeRepoFacts(&report.Repo)
	report.Scenario = deriveScenario(report)
	return report, nil
}

func scanEnvFile(path, relative string, databases map[string]*Database, supabaseRefs map[string]bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key := ""
		if match := envKeyPattern.FindStringSubmatch(line); match != nil {
			key = match[1]
		}
		if match := postgresURLPattern.FindStringSubmatch(trimmed); match != nil {
			host, port := match[1], match[2]
			database, ok := databases[host]
			if !ok {
				database = &Database{Hostname: host, Provider: classifyHost(host)}
				databases[host] = database
			}
			if key != "" {
				database.EnvKeys = append(database.EnvKeys, key)
			}
			database.EnvFiles = append(database.EnvFiles, relative)
			if isPooledEndpoint(host, port) {
				database.Pooled = true
			}
		}
		if match := supabaseRefPattern.FindStringSubmatch(trimmed); match != nil {
			supabaseRefs[match[1]] = true
		}
	}
}

func classifyHost(host string) string {
	switch {
	case strings.HasSuffix(host, ".neon.tech"):
		return "neon"
	case strings.HasSuffix(host, ".supabase.com") || strings.HasSuffix(host, ".supabase.co"):
		return "supabase"
	case strings.HasSuffix(host, ".postgres.database.azure.com"):
		return "azure"
	case strings.Contains(host, ".rds.amazonaws.com"):
		return "rds"
	case strings.HasSuffix(host, ".db.capydb.dev") || host == "db1.capydb.dev":
		return "capydb"
	case host == "localhost" || host == "127.0.0.1" || !strings.Contains(host, "."):
		return "local"
	default:
		return "other"
	}
}

func isPooledEndpoint(host, port string) bool {
	if strings.Contains(host, "-pooler.") || strings.Contains(host, "pooler.supabase.com") {
		// Supabase's session pooler on :5432 is dump-safe; the transaction
		// pooler on :6543 is not. Neon -pooler endpoints are never dump-safe.
		if strings.Contains(host, "pooler.supabase.com") && port == "5432" {
			return false
		}
		return true
	}
	return port == "6543" || port == "6432"
}

type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func scanPackageJSON(path string, facts *RepoFacts) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var parsed packageJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		return
	}
	all := map[string]string{}
	maps.Copy(all, parsed.Dependencies)
	maps.Copy(all, parsed.DevDependencies)
	for name, version := range all {
		if system, ok := authDeps[name]; ok {
			facts.AuthSystems = append(facts.AuthSystems, system)
		}
		if strings.HasPrefix(name, "@clerk/") {
			facts.AuthSystems = append(facts.AuthSystems, "clerk")
		}
		if layer, ok := dataLayerDeps[name]; ok {
			facts.DataLayers = append(facts.DataLayers, layer)
		}
		if distTagPattern.MatchString(strings.TrimSpace(version)) {
			facts.DistTagPins = append(facts.DistTagPins, name+"@"+version)
		}
	}
}

func scanGoMod(path string, facts *RepoFacts) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	if strings.Contains(content, "jackc/pgx") || strings.Contains(content, "lib/pq") {
		facts.DataLayers = append(facts.DataLayers, "go-pgx")
	}
}

func scanPythonDeps(path string, facts *RepoFacts) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	if strings.Contains(content, "asyncpg") || strings.Contains(content, "psycopg") {
		facts.DataLayers = append(facts.DataLayers, "python-postgres")
	}
	if strings.Contains(content, "sqlalchemy") || strings.Contains(content, "SQLAlchemy") {
		facts.DataLayers = append(facts.DataLayers, "sqlalchemy")
	}
	if strings.Contains(content, "supabase") {
		facts.DataLayers = append(facts.DataLayers, "supabase-py")
	}
}

func scanSourceFile(path, relative string, sites *CallSites) {
	info, err := os.Stat(path)
	if err != nil || info.Size() > maxSourceFileBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	content := string(data)
	sites.SupabaseData += len(supabaseDataPattern.FindAllStringIndex(content, -1))
	sites.SupabaseAuth += len(supabaseAuthPattern.FindAllStringIndex(content, -1))
	sites.SupabaseStorage += len(supabaseStoragePattern.FindAllStringIndex(content, -1))
	sites.SupabaseRealtime += len(supabaseRealtimePattern.FindAllStringIndex(content, -1))
	sites.NeonBatchCalls += len(neonBatchPattern.FindAllStringIndex(content, -1))
	if neonImportPattern.MatchString(content) {
		sites.NeonDriverFiles = append(sites.NeonDriverFiles, relative)
	}
}

// consumerSearchToken picks the string to grep sibling repos for. Supabase
// projects are referenced by ref in several URL shapes (db.<ref>.supabase.co,
// https://<ref>.supabase.co, postgres.<ref>@pooler...), so match on the bare
// project ref rather than one synthesized hostname.
func consumerSearchToken(hostname string) string {
	if strings.HasSuffix(hostname, ".supabase.co") {
		trimmed := strings.TrimSuffix(hostname, ".supabase.co")
		trimmed = strings.TrimPrefix(trimmed, "db.")
		if trimmed != "" && !strings.Contains(trimmed, ".") {
			return trimmed
		}
	}
	return hostname
}

// findConsumers greps sibling repos' env files for the token - the
// consumers gate. Shallow by design: env files up to 3 levels deep per repo.
func findConsumers(portfolioDir, selfRoot, token string) []string {
	entries, err := os.ReadDir(portfolioDir)
	if err != nil {
		return nil
	}
	var consumers []string
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		repoPath := filepath.Join(portfolioDir, entry.Name())
		if same, _ := filepath.Abs(repoPath); same == selfRoot {
			continue
		}
		if repoReferencesHost(repoPath, token, 0) {
			consumers = append(consumers, entry.Name())
		}
	}
	sort.Strings(consumers)
	return consumers
}

func repoReferencesHost(dir, token string, depth int) bool {
	if depth > 3 {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				continue
			}
			if repoReferencesHost(filepath.Join(dir, name), token, depth+1) {
				return true
			}
			continue
		}
		if name == ".env" || strings.HasPrefix(name, ".env.") {
			if data, err := os.ReadFile(filepath.Join(dir, name)); err == nil && strings.Contains(string(data), token) {
				return true
			}
		}
	}
	return false
}

func normalizeRepoFacts(facts *RepoFacts) {
	facts.AuthSystems = dedupe(facts.AuthSystems)
	facts.DataLayers = dedupe(facts.DataLayers)
	facts.DistTagPins = dedupe(facts.DistTagPins)
	sort.Strings(facts.AuthSystems)
	sort.Strings(facts.DataLayers)
	sort.Strings(facts.DistTagPins)
	sort.Strings(facts.EnvFiles)
	sort.Strings(facts.Lockfiles)
	sort.Strings(facts.PackageDirs)
	sort.Strings(facts.CallSites.NeonDriverFiles)
	if facts.AuthSystems == nil {
		facts.AuthSystems = []string{}
	}
	if facts.DataLayers == nil {
		facts.DataLayers = []string{}
	}
	if facts.DistTagPins == nil {
		facts.DistTagPins = []string{}
	}
}

func dedupe(values []string) []string {
	seen := map[string]bool{}
	result := values[:0]
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func has(values []string, value string) bool {
	return slices.Contains(values, value)
}

// deriveScenario maps the classification onto the scenario playbooks from the
// migration-scenarios spec, with an effort grade calibrated on the 2026-07
// dogfood migrations.
func deriveScenario(report Report) Scenario {
	scenario := Scenario{Warnings: []string{}}
	sites := report.Repo.CallSites

	provider := "plain-postgres"
	for _, database := range report.Databases {
		switch database.Provider {
		case "supabase":
			provider = "supabase"
		case "neon":
			if provider != "supabase" {
				provider = "neon"
			}
		case "azure", "rds":
			if provider == "plain-postgres" {
				provider = database.Provider
			}
		}
	}

	clerk := has(report.Repo.AuthSystems, "clerk")
	dumpSafeAuth := has(report.Repo.AuthSystems, "better-auth") || has(report.Repo.AuthSystems, "authjs") || has(report.Repo.AuthSystems, "lucia")
	supabaseAuthCoupled := provider == "supabase" && sites.SupabaseAuth > 3 && !clerk
	supabaseDataCoupled := sites.SupabaseData > 5

	switch {
	case supabaseAuthCoupled:
		scenario.Name = "supabase + supabase-auth (auth migration first)"
		scenario.Effort = "XL"
		scenario.Plan = []string{
			fmt.Sprintf("HARD BLOCKER: ~%d supabase.auth call sites and no Clerk - Supabase Auth is the identity system.", sites.SupabaseAuth),
			"Migrate auth first (e.g. to Clerk); a DB-only move buys nothing and breaks login.",
			"Re-run this scan after the auth migration; the remaining work becomes the supabase+clerk scenario.",
			"Run `capydb import preflight` now anyway: it reports FKs into auth.users and auth-bound RLS, which size the auth migration.",
		}
	case provider == "supabase" && supabaseDataCoupled:
		scenario.Name = "supabase + " + authLabel(clerk, dumpSafeAuth) + " + supabase-js data layer (rewrite, then import)"
		scenario.Effort = "L"
		if sites.SupabaseData <= 40 {
			scenario.Effort = "M"
		}
		scenario.Plan = []string{
			fmt.Sprintf("Rewrite ~%d supabase.from() call sites to drizzle/postgres-js AGAINST THE CURRENT SUPABASE DB first (it is plain Postgres) - this decouples the code rewrite from the infra cutover.", sites.SupabaseData),
			"Map every RLS policy to an app-layer guard in the new data modules before dropping policies (run `capydb import preflight` for the authoritative policy list).",
			serviceReplacementStep(sites),
			"Then: create project, preflight, import (dump public + app schemas via the DIRECT endpoint), swap DATABASE_URL, redeploy every consumer at once.",
		}
	case provider == "supabase":
		scenario.Name = "supabase + " + authLabel(clerk, dumpSafeAuth) + " + direct data layer (env swap + services)"
		scenario.Effort = "M"
		scenario.Plan = []string{
			"Data layer already speaks plain Postgres - the DB move is an env swap.",
			serviceReplacementStep(sites),
			"Create project, `capydb import preflight` (check the auth-FK / RLS / schema warnings), import via the direct endpoint, swap DATABASE_URL (pooled :6432 for app traffic, direct :5432 for DDL), redeploy all consumers.",
		}
	case provider == "neon":
		scenario.Name = "neon + " + authLabel(clerk, dumpSafeAuth) + " (driver codemod + import)"
		scenario.Effort = "S"
		scenario.Plan = []string{
			fmt.Sprintf("Run `capydb migrate codemod neon --write` to swap @neondatabase/serverless -> postgres-js in %d file(s): %s (dry-run first without --write).", len(sites.NeonDriverFiles), strings.Join(firstN(sites.NeonDriverFiles, 4), ", ")),
			"The codemod applies `postgres(url, { max: 1, prepare: false })` for CapyDB's pooled :6432 endpoint and points drizzle-kit at DATABASE_DIRECT_URL (:5432); review its needs-manual-attention list.",
			neonBatchStep(sites),
			"Strip Neon params (`channel_binding=require` - the codemod handles .env files), then: create project, preflight, import from the DIRECT (non `-pooler`) endpoint, `capydb link --overwrite-env`, redeploy all consumers.",
		}
	default:
		scenario.Name = provider + " + " + authLabel(clerk, dumpSafeAuth) + " (standard import + env swap)"
		scenario.Effort = "S"
		scenario.Plan = []string{
			"Driver already speaks plain Postgres: create project, `capydb import preflight`, import, swap DATABASE_URL, redeploy all consumers.",
		}
	}

	if dumpSafeAuth {
		scenario.Warnings = append(scenario.Warnings, "auth tables (better-auth/auth.js/lucia) live in the app database and ride the dump - verify their schema is included, nothing else to do")
	}
	if report.Repo.SupabaseAssets.EdgeFunctions > 0 {
		scenario.Warnings = append(scenario.Warnings, fmt.Sprintf("%d supabase edge function(s) need a new home (route handlers / workers)", report.Repo.SupabaseAssets.EdgeFunctions))
	}
	if len(report.Repo.DistTagPins) > 0 {
		scenario.Warnings = append(scenario.Warnings, "dist-tag dependency pins ("+strings.Join(firstN(report.Repo.DistTagPins, 5), ", ")+"): regenerating the lockfile re-resolves these and can import unrelated API drift - capture a typecheck baseline first and audit the lockfile diff after")
	}
	for _, database := range report.Databases {
		if database.Pooled && database.Provider != "capydb" {
			scenario.Warnings = append(scenario.Warnings, database.Hostname+" is a pooler endpoint - dumps/imports must use the provider's DIRECT endpoint")
		}
		if len(database.Consumers) > 0 {
			scenario.Warnings = append(scenario.Warnings, database.Hostname+" is also referenced by: "+strings.Join(database.Consumers, ", ")+" - the cutover must swap ALL consumers in one step, then run `capydb migrate verify` against the old source")
		}
	}
	if len(report.Repo.Lockfiles) == 0 && len(report.Repo.PackageDirs) > 0 {
		scenario.Warnings = append(scenario.Warnings, "no lockfile found - dependency state is not reproducible; resolve before migrating")
	}
	if has(report.Repo.DataLayers, "drizzle") {
		// drizzle-kit v1 hygiene: push/pull manage every schema by default, and
		// migration verification moved into the kit.
		scenario.Warnings = append(scenario.Warnings,
			"drizzle-kit v1 manages ALL schemas by default - set schemaFilter: [\"public\"] in drizzle.config.ts so extension schemas (e.g. cron from pg_cron) are never offered for DROP",
			"after the move, verify migrations with `drizzle-kit check` (branch-conflict detection) and preview DDL with `drizzle-kit push --explain` before applying",
		)
	}
	return scenario
}

func authLabel(clerk, dumpSafe bool) string {
	switch {
	case clerk:
		return "clerk"
	case dumpSafe:
		return "db-native auth"
	default:
		return "no detected auth"
	}
}

func serviceReplacementStep(sites CallSites) string {
	var parts []string
	if sites.SupabaseStorage > 0 {
		parts = append(parts, fmt.Sprintf("storage: %d storage.from() call site(s) -> S3-compatible bucket (R2) + object copy", sites.SupabaseStorage))
	}
	if sites.SupabaseRealtime > 0 {
		parts = append(parts, fmt.Sprintf("realtime: %d channel/postgres_changes site(s) -> SSE or polling", sites.SupabaseRealtime))
	}
	if len(parts) == 0 {
		return "No storage/realtime coupling detected - nothing beyond the database moves."
	}
	return "Replace provider services BEFORE the DB cutover (each ships independently): " + strings.Join(parts, "; ") + "."
}

func neonBatchStep(sites CallSites) string {
	if sites.NeonBatchCalls == 0 {
		return "No db.batch()/sql.transaction([]) call sites detected - the driver swap is mechanical."
	}
	return fmt.Sprintf("Rewrite %d db.batch()/sql.transaction([]) call site(s) to db.transaction()/sql.begin() - rebind inner statements to the tx client (a db-bound query inside a max:1 transaction deadlocks).", sites.NeonBatchCalls)
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return append(append([]string{}, values[:n]...), fmt.Sprintf("+%d more", len(values)-n))
}
