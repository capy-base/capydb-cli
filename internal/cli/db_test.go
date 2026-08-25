package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newConnectionsServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer capy_test_key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
			writeJSON(t, w, map[string]any{
				"projects": []map[string]any{{
					"id":              "project_1",
					"organization_id": "org_1",
					"region":          "swedencentral",
					"name":            "test-app",
					"slug":            "test-app",
					"state":           "ready",
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project_1/connections":
			writeJSON(t, w, map[string]any{
				"connections": map[string]any{
					"direct_url": "postgresql://user:secret@capydb.dev:5432/app?sslmode=require",
					"pooled_url": "postgresql://user:secret@capydb.dev:6432/app?sslmode=require",
					"username":   "user",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/preview-databases/prv_1/connections":
			writeJSON(t, w, map[string]any{
				"connections": map[string]any{
					"direct_url": "postgresql://preview:secret@capydb.dev:5432/preview_db?sslmode=require",
					"pooled_url": "postgresql://preview:secret@capydb.dev:6432/preview_db?sslmode=require",
					"username":   "preview",
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
}

func TestConnectionStringPrintsOnlyURL(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "direct project url by default",
			args: []string{"--project", "test-app"},
			want: "postgresql://user:secret@capydb.dev:5432/app?sslmode=require",
		},
		{
			name: "pooled project url",
			args: []string{"--project", "test-app", "--pooled"},
			want: "postgresql://user:secret@capydb.dev:6432/app?sslmode=require",
		},
		{
			name: "preview direct url",
			args: []string{"--preview", "prv_1"},
			want: "postgresql://preview:secret@capydb.dev:5432/preview_db?sslmode=require",
		},
		{
			name: "preview pooled url",
			args: []string{"--preview", "prv_1", "--pooled"},
			want: "postgresql://preview:secret@capydb.dev:6432/preview_db?sslmode=require",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CI", "true")
			server := newConnectionsServer(t)
			defer server.Close()

			application := &app{cwd: t.TempDir()}
			command := newRootCommand(application, "test")
			stdout := new(bytes.Buffer)
			stderr := new(bytes.Buffer)
			command.SetOut(stdout)
			command.SetErr(stderr)
			command.SetArgs(append([]string{
				"connection-string",
				"--api-url", server.URL,
				"--api-key", "capy_test_key",
			}, tt.args...))

			if err := command.Execute(); err != nil {
				t.Fatalf("execute connection-string: %v", err)
			}

			if got := strings.TrimSpace(stdout.String()); got != tt.want {
				t.Fatalf("stdout = %q, want only %q", got, tt.want)
			}
		})
	}
}

func TestSQLCommandRendersTable(t *testing.T) {
	t.Setenv("CI", "true")

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
			writeJSON(t, w, map[string]any{
				"projects": []map[string]any{{
					"id":    "project_1",
					"name":  "test-app",
					"slug":  "test-app",
					"state": "ready",
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/project_1/sql":
			if err := decodeJSON(r, &gotBody); err != nil {
				t.Fatalf("decode sql body: %v", err)
			}
			writeJSON(t, w, map[string]any{
				"result": map[string]any{
					"columns": []string{"id", "email"},
					"rows": []map[string]any{
						{"id": 1, "email": "ada@example.com"},
						{"id": 2, "email": nil},
					},
					"row_count":   2,
					"duration_ms": 4,
					"truncated":   false,
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	application := &app{cwd: t.TempDir()}
	command := newRootCommand(application, "test")
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"sql",
		"select id, email from users",
		"--api-url", server.URL,
		"--api-key", "capy_test_key",
		"--project", "test-app",
		"--max-rows", "50",
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute sql: %v", err)
	}

	if gotBody["query"] != "select id, email from users" {
		t.Fatalf("unexpected query in body: %#v", gotBody)
	}
	if gotBody["max_rows"] != float64(50) {
		t.Fatalf("unexpected max_rows in body: %#v", gotBody)
	}

	text := output.String()
	for _, expected := range []string{
		"id", "email",
		"ada@example.com",
		"NULL",
		"(2 rows, 4ms)",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("sql output missing %q\n%s", expected, text)
		}
	}
}

func TestMetricsCommandRendersObservability(t *testing.T) {
	t.Setenv("CI", "true")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
			writeJSON(t, w, map[string]any{
				"projects": []map[string]any{{
					"id":    "project_1",
					"name":  "test-app",
					"slug":  "test-app",
					"state": "ready",
				}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects/project_1/observability":
			writeJSON(t, w, map[string]any{
				"observability": map[string]any{
					"connection_count":         2,
					"connection_limit":         15,
					"connection_usage_percent": 13.3,
					"database_size_bytes":      1048576,
					"storage_limit_bytes":      1073741824,
					"storage_usage_percent":    0.1,
					"alerts":                   []string{"storage usage above 80%"},
					"pg_stat_statements":       true,
					"slow_queries": []map[string]any{{
						"calls":         120,
						"rows":          2400,
						"total_time_ms": 9001.5,
						"mean_time_ms":  75.0,
						"query":         "select * from " + strings.Repeat("very_long_table_name_", 8),
					}},
					"active_queries": []map[string]any{{
						"pid":             4242,
						"username":        "app",
						"state":           "active",
						"duration_ms":     1234.0,
						"query":           "update users set last_seen = now()",
						"wait_event":      "WALWriteLock",
						"wait_event_type": "LWLock",
					}},
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	application := &app{cwd: t.TempDir()}
	command := newRootCommand(application, "test")
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"metrics",
		"--api-url", server.URL,
		"--api-key", "capy_test_key",
		"--project", "test-app",
	})

	if err := command.Execute(); err != nil {
		t.Fatalf("execute metrics: %v", err)
	}

	text := output.String()
	for _, expected := range []string{
		"storage: 1.0 MB / 1.0 GB (0.1%)",
		"connections: 2/15 (13.3%)",
		"alert: storage usage above 80%",
		"Top slow queries:",
		"75.0",
		"...", // slow query truncated to 80 chars
		"Active queries:",
		"4242",
		"LWLock:WALWriteLock",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metrics output missing %q\n%s", expected, text)
		}
	}

	// The 80-char query truncation must apply to slow queries.
	for line := range strings.SplitSeq(text, "\n") {
		if strings.Contains(line, "very_long_table_name_") && len(line) > 200 {
			t.Fatalf("slow query line not truncated: %q", line)
		}
	}
}

func TestImportPreflightRendersChecksAndFailsWhenNotOK(t *testing.T) {
	t.Setenv("CI", "true")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/projects":
			writeJSON(t, w, map[string]any{
				"projects": []map[string]any{{
					"id":    "project_1",
					"name":  "test-app",
					"slug":  "test-app",
					"state": "ready",
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/projects/project_1/imports/preflight":
			var body map[string]any
			if err := decodeJSON(r, &body); err != nil {
				t.Fatalf("decode preflight body: %v", err)
			}
			if body["source_url"] != "postgresql://src@host/db" {
				t.Fatalf("unexpected source_url: %#v", body)
			}
			writeJSON(t, w, map[string]any{
				"preflight": map[string]any{
					"ok": false,
					"checks": []map[string]any{
						{"name": "connectivity", "status": "pass"},
						{"name": "extensions", "status": "warn", "detail": "postgis version differs"},
						{"name": "size", "status": "fail", "detail": "source exceeds storage limit"},
					},
					"source": map[string]any{
						"server_version":      "16.4",
						"database_size_bytes": 2147483648,
						"extensions":          []map[string]any{{"name": "postgis", "version": "3.4"}},
					},
					"storage_limit_bytes": 1073741824,
					"target_version":      "17.2",
				},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	application := &app{cwd: t.TempDir()}
	command := newRootCommand(application, "test")
	output := new(bytes.Buffer)
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"import",
		"preflight",
		"--api-url", server.URL,
		"--api-key", "capy_test_key",
		"--project", "test-app",
		"--source-url", "postgresql://src@host/db",
	})

	err := command.Execute()
	if err == nil {
		t.Fatalf("expected non-nil error when preflight ok=false")
	}
	if !strings.Contains(err.Error(), "import preflight failed") {
		t.Fatalf("unexpected error: %v", err)
	}

	text := output.String()
	for _, expected := range []string{
		"[pass] connectivity",
		"[warn] extensions: postgis version differs",
		"[fail] size: source exceeds storage limit",
		"source_version: 16.4",
		"target_version: 17.2",
		"preflight: failed",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("preflight output missing %q\n%s", expected, text)
		}
	}
}
