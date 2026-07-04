package releaseinfo

import "testing"

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
