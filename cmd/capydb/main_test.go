package main

import "testing"

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
