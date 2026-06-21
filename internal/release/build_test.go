package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBuild_ProducesCGoFreeVersionStampedBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping release build in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "graphi")
	if err := Build(context.Background(), BuildConfig{Version: "0.0.0-test"}, bin); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("binary not produced: %v", err)
	}
	bi, err := ReadBuildInfo(context.Background(), bin)
	if err != nil {
		t.Fatalf("ReadBuildInfo: %v", err)
	}
	if bi.CGOEnabled != "0" {
		t.Errorf("CGO_ENABLED = %q, want 0 (static CGo-free binary)", bi.CGOEnabled)
	}
	if bi.Version != "0.0.0-test" {
		t.Errorf("Version = %q, want 0.0.0-test (ldflags stamping)", bi.Version)
	}
}

func TestVerifyReproducible_ByteIdentical(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping reproducible double-build in -short mode")
	}
	if os.Getenv("RELEASE_SKIP_REPRODUCIBLE") == "1" {
		t.Skip("RELEASE_SKIP_REPRODUCIBLE=1")
	}
	sha, ok, err := VerifyReproducible(context.Background(), BuildConfig{Version: "0.0.0-test"})
	if err != nil {
		t.Fatalf("VerifyReproducible: %v", err)
	}
	if !ok {
		t.Fatalf("release build NOT reproducible: two builds of the same revision differ (sha=%s)", sha)
	}
	if sha == "" {
		t.Error("empty sha256")
	}
}

func TestParseVersionOutput(t *testing.T) {
	bi := parseVersionOutput("graphi version=1.2.3 commit=abc123def date=2026-06-20T00:00:00Z\n")
	if bi.Version != "1.2.3" {
		t.Errorf("Version=%q", bi.Version)
	}
	if bi.VCSRevision != "abc123def" {
		t.Errorf("VCSRevision=%q", bi.VCSRevision)
	}
	if bi.VCSTime != "2026-06-20T00:00:00Z" {
		t.Errorf("VCSTime=%q", bi.VCSTime)
	}
}
