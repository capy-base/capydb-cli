package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decodeJSON(r *http.Request, dest any) error {
	return json.NewDecoder(r.Body).Decode(dest)
}

func TestResolveRestoreTargetKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		targetKind  string
		previewRef  string
		previewName string
		want        string
		wantErr     bool
	}{
		{name: "explicit project", targetKind: "project", want: "project"},
		{name: "explicit new_preview", targetKind: "NEW_PREVIEW", want: "new_preview"},
		{name: "infer preview from ref", previewRef: "prv_1", want: "preview"},
		{name: "infer new_preview from name", previewName: "qa", want: "new_preview"},
		{name: "default safe new_preview", want: "new_preview"},
		{name: "invalid kind", targetKind: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRestoreTargetKind(tt.targetKind, tt.previewRef, tt.previewName)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveRestoreTargetKind() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPreviewExtendCallsEndpoint(t *testing.T) {
	t.Setenv("CI", "true")

	var gotBody map[string]int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer capy_test_key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/preview-databases/prv_extend/extend" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := decodeJSON(r, &gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeJSON(t, w, map[string]any{
			"preview": map[string]any{
				"id":             "prv_extend",
				"name":           "qa-db",
				"project_id":     "project_1",
				"ttl_expires_at": "2026-06-01T12:00:00Z",
			},
		})
	}))
	defer server.Close()

	application := &app{cwd: t.TempDir()}
	command := newRootCommand(application, "test")
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"preview",
		"extend",
		"prv_extend",
		"--api-url", server.URL,
		"--api-key", "capy_test_key",
		"--ttl-hours", "24",
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute preview extend: %v", err)
	}

	if gotBody["ttl_hours"] != 24 {
		t.Fatalf("unexpected ttl_hours in body: %#v", gotBody)
	}

	text := output.String()
	if !strings.Contains(text, "Extended preview qa-db (prv_extend) by 24h") {
		t.Fatalf("unexpected output:\n%s", text)
	}
	if !strings.Contains(text, "ttl_expires_at: 2026-06-01T12:00:00Z") {
		t.Fatalf("expected new ttl in output:\n%s", text)
	}
}

func TestPreviewExtendRejectsOutOfRangeTTL(t *testing.T) {
	t.Setenv("CI", "true")

	application := &app{cwd: t.TempDir()}
	command := newRootCommand(application, "test")
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"preview",
		"extend",
		"prv_1",
		"--api-key", "capy_test_key",
		"--ttl-hours", "0",
	})

	if err := command.Execute(); err == nil {
		t.Fatalf("expected error for out-of-range ttl-hours")
	}
}
