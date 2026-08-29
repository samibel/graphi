package releaseinfo

import (
	"runtime/debug"
	"testing"
)

func TestNewNoNetwork(t *testing.T) {
	info := New()
	if info.Version() == "" {
		t.Fatal("version should not be empty")
	}
	if info.Arch() == "" {
		t.Fatal("arch should not be empty")
	}
	// New performs no I/O and no network calls by construction.
}

func TestReleaseMarkerDev(t *testing.T) {
	info := Info{version: "dev"}
	if info.IsRelease() {
		t.Fatal("dev build should not be a release")
	}
	if got := info.ReleaseMarker(); got != "dev / not a packaged release" {
		t.Fatalf("unexpected marker: %q", got)
	}
}

func TestReleaseMarkerRelease(t *testing.T) {
	info := Info{version: "1.0.0"}
	if !info.IsRelease() {
		t.Fatal("versioned build should be a release")
	}
	if got := info.ReleaseMarker(); got != "packaged release" {
		t.Fatalf("unexpected marker: %q", got)
	}
}

func TestVersionStringContainsAllFields(t *testing.T) {
	info := Info{version: "1.0.0", commit: "abc", date: "2024-01-01", arch: "darwin/arm64", isRelease: true}
	s := info.VersionString()
	for _, want := range []string{"version=1.0.0", "commit=abc", "date=2024-01-01", "arch=darwin/arm64", "release_marker=packaged release"} {
		if !contains(s, want) {
			t.Fatalf("VersionString missing %q: %s", want, s)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// CGOEnabled must mirror the CGO_ENABLED build setting the Go toolchain
// recorded into this binary — it is the ground truth internal/audit compares a
// build attestation's cgo_enabled claim against.
func TestCGOEnabledMirrorsBuildSetting(t *testing.T) {
	want := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "CGO_ENABLED" {
				want = s.Value
			}
		}
	}
	if got := New().CGOEnabled(); got != want {
		t.Fatalf("CGOEnabled() = %q, want the recorded build setting %q", got, want)
	}
	if want == "" {
		t.Fatal("this test binary carries no CGO_ENABLED build setting; the ground truth this asserts on is missing")
	}
}

// BuildTags must mirror the raw `-tags` build setting — the ground truth
// internal/audit compares a build attestation's build_tags claim against.
func TestBuildTagsMirrorsBuildSetting(t *testing.T) {
	want := ""
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "-tags" {
				want = s.Value
			}
		}
	}
	if got := New().BuildTags(); got != want {
		t.Fatalf("BuildTags() = %q, want the recorded build setting %q", got, want)
	}
}
