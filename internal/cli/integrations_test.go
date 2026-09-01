package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/capy-base/capydb-cli/internal/api"
)

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

func TestWranglerPayloadFromIntegrations(t *testing.T) {
	t.Parallel()

	vars := map[string]string{
		"DATABASE_URL":        "postgres://pooled",
		"DATABASE_DIRECT_URL": "postgres://direct",
	}

	t.Run("renders the binding and the local dev hint", func(t *testing.T) {
		t.Parallel()

		payload, hint, err := wranglerPayloadFromIntegrations([]api.ProjectIntegration{
			{Provider: "vercel", State: "active"},
			{
				Config:   json.RawMessage(`{"hyperdrive":true,"hyperdrive_binding":"DB","hyperdrive_id":"hd_1","target":"workers"}`),
				Provider: "cloudflare",
				State:    "active",
			},
		}, vars)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(payload.Hyperdrive) != 1 || payload.Hyperdrive[0].ID != "hd_1" || payload.Hyperdrive[0].Binding != "DB" {
			t.Fatalf("hyperdrive = %#v, want the configured binding and id", payload.Hyperdrive)
		}
		if len(payload.CompatibilityFlags) != 1 || payload.CompatibilityFlags[0] != "nodejs_compat" {
			t.Fatalf("compatibility flags = %#v, want nodejs_compat", payload.CompatibilityFlags)
		}
		// The hint must carry the DIRECT url: wrangler dev cannot go through a pooler binding.
		if !strings.Contains(hint, "CLOUDFLARE_HYPERDRIVE_LOCAL_CONNECTION_STRING_DB") ||
			!strings.Contains(hint, "postgres://direct") {
			t.Fatalf("hint = %q, want the binding-specific local connection string", hint)
		}
	})

	t.Run("defaults the binding name", func(t *testing.T) {
		t.Parallel()

		payload, _, err := wranglerPayloadFromIntegrations([]api.ProjectIntegration{{
			Config:   json.RawMessage(`{"hyperdrive":true,"hyperdrive_id":"hd_2"}`),
			Provider: "cloudflare",
			State:    "active",
		}}, vars)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if payload.Hyperdrive[0].Binding != defaultHyperdriveBinding {
			t.Fatalf("binding = %q, want %q", payload.Hyperdrive[0].Binding, defaultHyperdriveBinding)
		}
	})

	t.Run("actionable errors instead of placeholder ids", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name         string
			integrations []api.ProjectIntegration
			wantContains string
		}{
			{
				name:         "no integration",
				integrations: []api.ProjectIntegration{{Provider: "netlify", State: "active"}},
				wantContains: "connect one in the dashboard",
			},
			{
				name: "disabled integration is not usable",
				integrations: []api.ProjectIntegration{{
					Config:   json.RawMessage(`{"hyperdrive":true,"hyperdrive_id":"hd_3"}`),
					Provider: "cloudflare",
					State:    "disabled",
				}},
				wantContains: "connect one in the dashboard",
			},
			{
				name: "connected without hyperdrive",
				integrations: []api.ProjectIntegration{{
					Config:   json.RawMessage(`{"hyperdrive":false,"target":"workers"}`),
					Provider: "cloudflare",
					State:    "active",
				}},
				wantContains: "without Hyperdrive",
			},
			{
				name: "sync has not created the config yet",
				integrations: []api.ProjectIntegration{{
					Config:   json.RawMessage(`{"hyperdrive":true}`),
					Provider: "cloudflare",
					State:    "active",
				}},
				wantContains: "still being created",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				if _, _, err := wranglerPayloadFromIntegrations(tt.integrations, vars); err == nil ||
					!strings.Contains(err.Error(), tt.wantContains) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantContains)
				}
			})
		}
	})
}
