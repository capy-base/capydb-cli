package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	client, err := NewClient(baseURL, "capy_test_key", "1.2.3 (commit: abc, built: now)")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	// Shrink the retry backoff so tests stay fast while keeping three attempts.
	client.doer.RetryBackoff = []time.Duration{0, time.Millisecond, time.Millisecond}
	return client
}

func TestGetRetriesOnServerErrorsAndNetworkFailures(t *testing.T) {
	tests := []struct {
		name         string
		failures     int
		wantAttempts int32
		wantErr      bool
	}{
		{name: "succeeds first try", failures: 0, wantAttempts: 1},
		{name: "recovers after one 5xx", failures: 1, wantAttempts: 2},
		{name: "recovers after two 5xx", failures: 2, wantAttempts: 3},
		{name: "gives up after three 5xx", failures: 3, wantAttempts: 3, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/projects" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if int(attempts.Add(1)) <= tt.failures {
					http.Error(w, `{"error":"upstream blew up"}`, http.StatusBadGateway)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"projects":[]}`))
			}))
			defer server.Close()

			client := newTestClient(t, server.URL)
			_, err := client.ListProjects(context.Background(), "")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error after exhausting retries")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := attempts.Load(); got != tt.wantAttempts {
				t.Fatalf("attempts = %d, want %d", got, tt.wantAttempts)
			}
		})
	}
}

func TestGetDoesNotRetryOnClientErrors(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if _, err := client.ListProjects(context.Background(), ""); err == nil {
		t.Fatalf("expected error for 404")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (4xx must not be retried)", got)
	}
}

func TestNonGetIsNeverRetried(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		attempts.Add(1)
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if _, err := client.CreateBackup(context.Background(), "project_1", "nightly"); err == nil {
		t.Fatalf("expected error for 500")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1 (POST must not be retried)", got)
	}
}

func TestUserAgentHeaderCarriesCLIVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "capydb-cli/1.2.3" {
			t.Fatalf("unexpected user-agent: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"projects":[]}`))
	}))
	defer server.Close()

	client := newTestClient(t, server.URL)
	if _, err := client.ListProjects(context.Background(), ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHTTPTimeoutEnv(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr string
	}{
		{name: "default", value: "", want: 30 * time.Second},
		{name: "valid duration", value: "45s", want: 45 * time.Second},
		{name: "minutes", value: "2m", want: 2 * time.Minute},
		{name: "garbage", value: "bogus", wantErr: "invalid CAPYDB_HTTP_TIMEOUT"},
		{name: "negative", value: "-5s", wantErr: "must be positive"},
		{name: "zero", value: "0s", wantErr: "must be positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CAPYDB_HTTP_TIMEOUT", tt.value)
			client, err := NewClient("https://capydb.dev/api/capydb", "key", "test")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client.doer.HTTPClient.Timeout != tt.want {
				t.Fatalf("timeout = %s, want %s", client.doer.HTTPClient.Timeout, tt.want)
			}
		})
	}
}
