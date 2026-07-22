package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/capy-base/capydb/cli/internal/api"
	"github.com/capy-base/capydb/cli/internal/exitcode"
)

func TestBuildVersionFromSource(t *testing.T) {
	previousVersion := version
	previousCommit := commit
	previousDate := date
	previousBuiltBy := builtBy
	t.Cleanup(func() {
		version = previousVersion
		commit = previousCommit
		date = previousDate
		builtBy = previousBuiltBy
	})

	version = "dev"
	commit = "none"
	date = "unknown"
	builtBy = "unknown"

	if got, want := buildVersion(), "dev (built from source)"; got != want {
		t.Fatalf("buildVersion() = %q, want %q", got, want)
	}
}

func TestBuildVersionRelease(t *testing.T) {
	previousVersion := version
	previousCommit := commit
	previousDate := date
	previousBuiltBy := builtBy
	t.Cleanup(func() {
		version = previousVersion
		commit = previousCommit
		date = previousDate
		builtBy = previousBuiltBy
	})

	version = "v0.1.0"
	commit = "abc1234"
	date = "2026-04-23T10:00:00Z"
	builtBy = "goreleaser"

	want := "v0.1.0 (commit: abc1234, built: 2026-04-23T10:00:00Z, by: goreleaser)"
	if got := buildVersion(); got != want {
		t.Fatalf("buildVersion() = %q, want %q", got, want)
	}
}

func TestExitCodeFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "generic", err: errors.New("boom"), want: 1},
		{name: "usage", err: exitcode.Errorf(exitcode.UsageError, "bad flag"), want: 2},
		{name: "wrapped exit code", err: fmt.Errorf("outer: %w", exitcode.Errorf(exitcode.Timeout, "timed out")), want: 6},
		{name: "api 401", err: fmt.Errorf("call: %w", &api.APIError{StatusCode: 401}), want: 3},
		{name: "api 403", err: &api.APIError{StatusCode: 403}, want: 3},
		{name: "api 404", err: &api.APIError{StatusCode: 404}, want: 4},
		{name: "api 409", err: &api.APIError{StatusCode: 409}, want: 5},
		{name: "api 504", err: &api.APIError{StatusCode: 504}, want: 6},
		{name: "api 500", err: &api.APIError{StatusCode: 500}, want: 1},
		{name: "deadline", err: fmt.Errorf("wait: %w", context.DeadlineExceeded), want: 6},
		{name: "exit code wins over api error", err: exitcode.New(exitcode.NotFound, &api.APIError{StatusCode: 401}), want: 4},
	}

	for _, tt := range tests {
		if got := exitCodeFor(tt.err); got != tt.want {
			t.Fatalf("%s: exitCodeFor(%v) = %d, want %d", tt.name, tt.err, got, tt.want)
		}
	}
}
