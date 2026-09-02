package scan

import (
	"slices"
	"testing"
)

// The ordering in serverRules is the whole correctness of classifyServer: an
// Aurora cluster also answers to every RDS signal, and an AlloyDB instance to
// every Cloud SQL one. Getting the order wrong produces a plausible-looking
// classification with a remediation that cannot work.
func TestClassifyServerPrecedence(t *testing.T) {
	cases := []struct {
		name    string
		signals []string
		want    string
	}{
		{
			name:    "aurora outranks rds",
			signals: []string{"role:rds_superuser", "guc:rds", "proc:aurora_version"},
			want:    ProviderAurora,
		},
		{
			name:    "plain rds",
			signals: []string{"role:rds_superuser", "guc:rds"},
			want:    ProviderRDS,
		},
		{
			name:    "alloydb outranks cloud sql",
			signals: []string{"role:cloudsqladmin", "guc:cloudsql", "guc:alloydb"},
			want:    ProviderAlloyDB,
		},
		{
			name:    "plain cloud sql",
			signals: []string{"role:cloudsqladmin", "guc:cloudsql"},
			want:    ProviderCloudSQL,
		},
		{
			// Heroku runs on RDS underneath, so both sets of markers can be
			// present. Heroku must win: it is the only provider where a
			// streaming import is impossible, and an RDS verdict would
			// recommend a path that cannot run.
			name:    "heroku outranks the aws markers underneath it",
			signals: []string{"schema:heroku_ext", "role:rds_superuser", "guc:rds"},
			want:    ProviderHeroku,
		},
		{
			name:    "azure",
			signals: []string{"role:azure_pg_admin", "guc:azure"},
			want:    ProviderAzure,
		},
		{
			name:    "neon",
			signals: []string{"ext:neon", "role:neondb_owner"},
			want:    ProviderNeon,
		},
		{
			name:    "supabase by its own role",
			signals: []string{"role:supabase_admin", "schema:auth", "schema:storage"},
			want:    ProviderSupabase,
		},
		{
			// A tenant role cannot always see supabase_admin; the three managed
			// schemas together are still conclusive.
			name:    "supabase by its managed schemas",
			signals: []string{"schema:auth", "schema:storage", "schema:realtime"},
			want:    ProviderSupabase,
		},
		{
			// One schema called `auth` is an ordinary application choice.
			name:    "an application schema called auth is not supabase",
			signals: []string{"schema:auth"},
			want:    ProviderOther,
		},
		{
			name:    "self-managed",
			signals: nil,
			want:    ProviderOther,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, matched := classifyServer(testCase.signals)
			if got != testCase.want {
				t.Fatalf("classifyServer(%v) = %q, want %q", testCase.signals, got, testCase.want)
			}
			if got != ProviderOther && len(matched) == 0 {
				t.Fatal("a positive classification must report the signals that produced it")
			}
			for _, signal := range matched {
				if !slices.Contains(testCase.signals, signal) {
					t.Fatalf("reported signal %q was not present in the input", signal)
				}
			}
		})
	}
}

// timescaledb installs on any Postgres, so it identifies a schema shape rather
// than a host. Classifying on it would label a self-managed database as
// Timescale Cloud and hand out the wrong remediation.
func TestClassifyServerIgnoresPortableExtensions(t *testing.T) {
	if got, _ := classifyServer([]string{"ext:timescaledb"}); got != ProviderOther {
		t.Fatalf("timescaledb alone classified as %q; it must not identify a provider", got)
	}
}

func TestReplicationBlockers(t *testing.T) {
	ready := SourceReplication{
		WALLevel: "logical", MaxReplicationSlots: 10, MaxWALSenders: 10,
		UsedSlots: 1, RoleCanReplicate: true,
	}
	if blockers := replicationBlockers(ready, SourceInventory{}); len(blockers) != 0 {
		t.Fatalf("a ready source reported blockers: %v", blockers)
	}

	cases := map[string]SourceReplication{
		"wal_level": {WALLevel: "replica", MaxReplicationSlots: 10, MaxWALSenders: 10, RoleCanReplicate: true},
		"no free slot": {
			WALLevel: "logical", MaxReplicationSlots: 2, UsedSlots: 2,
			MaxWALSenders: 10, RoleCanReplicate: true,
		},
		"no wal senders":         {WALLevel: "logical", MaxReplicationSlots: 10, MaxWALSenders: 0, RoleCanReplicate: true},
		"role lacks REPLICATION": {WALLevel: "logical", MaxReplicationSlots: 10, MaxWALSenders: 10},
	}
	for name, replication := range cases {
		if blockers := replicationBlockers(replication, SourceInventory{}); len(blockers) == 0 {
			t.Errorf("%s: expected a blocker, got none", name)
		}
	}

	// A table with no primary key and no replica identity breaks a streaming
	// import at the first UPDATE, not at setup - so it has to be a blocker
	// before the migration starts, not a surprise during it.
	withoutIdentity := SourceInventory{TablesWithoutReplicaIdentity: []string{"public.events"}}
	if blockers := replicationBlockers(ready, withoutIdentity); len(blockers) != 1 {
		t.Fatalf("expected the replica-identity blocker, got %v", blockers)
	}
}

func TestRefreshReplicationReadiness(t *testing.T) {
	facts := &SourceFacts{
		Replication: SourceReplication{
			WALLevel: "logical", MaxReplicationSlots: 10, MaxWALSenders: 10, RoleCanReplicate: true,
			Ready: true,
		},
		Inventory: SourceInventory{TablesWithoutReplicaIdentity: []string{"public.audit"}},
	}
	facts.refreshReplicationReadiness()
	if facts.Replication.Ready {
		t.Fatal("readiness must be re-graded once the inventory is known")
	}
}

func TestFirstNames(t *testing.T) {
	values := []string{"a", "b", "c", "d"}
	if got := firstNames(values, 10); len(got) != 4 {
		t.Fatalf("a short list must pass through unchanged, got %v", got)
	}
	got := firstNames(values, 2)
	if len(got) != 3 || got[2] != "and 2 more" {
		t.Fatalf("firstNames capped badly: %v", got)
	}
	// Capping must not scribble on the caller's slice.
	if values[2] != "c" {
		t.Fatalf("firstNames mutated its input: %v", values)
	}
}
