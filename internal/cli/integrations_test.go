package cli

import "testing"

func TestBuildVercelEnvPayloadUsesBranchScopedPreviewTarget(t *testing.T) {
	t.Parallel()

	payload := buildVercelEnvPayload(map[string]string{
		"DATABASE_URL":        "postgres://pooled",
		"DATABASE_DIRECT_URL": "postgres://direct",
	}, "feature-one")

	if len(payload) != 2 {
		t.Fatalf("payload length = %d, want 2", len(payload))
	}
	for _, item := range payload {
		if item.Type != "sensitive" {
			t.Fatalf("item type = %q, want sensitive", item.Type)
		}
		if item.GitBranch != "feature-one" {
			t.Fatalf("git branch = %q, want feature-one", item.GitBranch)
		}
		if len(item.Target) != 1 || item.Target[0] != "preview" {
			t.Fatalf("target = %#v, want preview only", item.Target)
		}
	}
}

func TestBuildNetlifyEnvPayloadUsesSecretBuildAndFunctionScopes(t *testing.T) {
	t.Parallel()

	payload := buildNetlifyEnvPayload(map[string]string{
		"DATABASE_URL": "postgres://pooled",
	}, "deploy-preview")

	if len(payload) != 1 {
		t.Fatalf("payload length = %d, want 1", len(payload))
	}
	if !payload[0].IsSecret {
		t.Fatal("payload is_secret = false, want true")
	}
	if len(payload[0].Scopes) != 2 || payload[0].Scopes[0] != "builds" || payload[0].Scopes[1] != "functions" {
		t.Fatalf("scopes = %#v, want builds/functions", payload[0].Scopes)
	}
	if len(payload[0].Values) != 1 || payload[0].Values[0].Context != "deploy-preview" {
		t.Fatalf("values = %#v, want deploy-preview context", payload[0].Values)
	}
}
