package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectNextProjectPrefersEnvLocal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"next":"15.0.0"}}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}

	detection, err := Detect(dir, "")
	if err != nil {
		t.Fatalf("detect project: %v", err)
	}

	if detection.Framework != FrameworkNextJS {
		t.Fatalf("expected nextjs framework, got %s", detection.Framework)
	}
	if detection.EnvFile != ".env.local" {
		t.Fatalf("expected .env.local, got %s", detection.EnvFile)
	}
	if detection.Profile != FrameworkNextJS {
		t.Fatalf("expected nextjs profile, got %s", detection.Profile)
	}
}

func TestDetectMonorepoFindsNestedApp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"private":true,"workspaces":["apps/*"]}`), 0o644); err != nil {
		t.Fatalf("write root package.json: %v", err)
	}
	appDir := filepath.Join(dir, "apps", "web")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("mkdir app dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "package.json"), []byte(`{"dependencies":{"next":"15.0.0","drizzle-orm":"1.0.0"}}`), 0o644); err != nil {
		t.Fatalf("write app package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "drizzle.config.ts"), []byte(`export default {}`), 0o644); err != nil {
		t.Fatalf("write drizzle config: %v", err)
	}

	detection, err := Detect(dir, "")
	if err != nil {
		t.Fatalf("detect project: %v", err)
	}

	if detection.AppPath != filepath.Join("apps", "web") {
		t.Fatalf("expected app path apps/web, got %s", detection.AppPath)
	}
	if detection.Profile != "nextjs+drizzle" {
		t.Fatalf("expected nextjs+drizzle profile, got %s", detection.Profile)
	}
}

func TestDetectDjangoProject(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manage.py"), []byte(""), 0o644); err != nil {
		t.Fatalf("write manage.py: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("Django==5.0\n"), 0o644); err != nil {
		t.Fatalf("write requirements.txt: %v", err)
	}

	detection, err := Detect(dir, "")
	if err != nil {
		t.Fatalf("detect project: %v", err)
	}

	if detection.Framework != FrameworkDjango {
		t.Fatalf("expected django framework, got %s", detection.Framework)
	}
	if detection.EnvFile != ".env" {
		t.Fatalf("expected .env, got %s", detection.EnvFile)
	}
}

func TestBuildEnvPlanForPrismaUsesPooledPrimary(t *testing.T) {
	t.Parallel()

	plan := BuildEnvPlan(Detection{DatabaseLayer: DatabaseLayerPrisma, Framework: FrameworkNextJS}, "postgres://direct", "postgres://pooled")

	if plan.Vars["DATABASE_URL"] != "postgres://pooled" {
		t.Fatalf("expected pooled DATABASE_URL, got %q", plan.Vars["DATABASE_URL"])
	}
	if plan.Vars["DIRECT_URL"] != "postgres://direct" {
		t.Fatalf("expected DIRECT_URL, got %q", plan.Vars["DIRECT_URL"])
	}
}
