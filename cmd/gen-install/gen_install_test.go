package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/release"
)

// renderSh returns just the rendered install.sh content.
func renderSh(t *testing.T) string {
	t.Helper()
	files, err := render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, f := range files {
		if f.name == "install.sh" {
			return f.content
		}
	}
	t.Fatal("install.sh not produced by render()")
	return ""
}

// TestAssetParity proves the rendered install.sh lists EXACTLY the asset names
// release.AssetName produces for every release.ReleaseTargets entry — the
// installer's accepted target set is provably the release source of truth.
func TestAssetParity(t *testing.T) {
	sh := renderSh(t)

	// Parse the one-per-line VALID_ASSETS="..." block.
	start := strings.Index(sh, `VALID_ASSETS="`)
	if start < 0 {
		t.Fatal("install.sh has no VALID_ASSETS block")
	}
	rest := sh[start+len(`VALID_ASSETS="`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatal("VALID_ASSETS block is not terminated")
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(rest[:end], "\n") {
		if s := strings.TrimSpace(line); s != "" {
			listed[s] = true
		}
	}

	// Every ReleaseTargets asset must be present...
	for _, p := range release.ReleaseTargets {
		name := release.AssetName(p)
		if !listed[name] {
			t.Errorf("install.sh missing release asset %q from the valid list", name)
		}
	}
	// ...and the list must not contain MORE than the source of truth.
	if len(listed) != len(release.ReleaseTargets) {
		t.Errorf("install.sh lists %d assets, want %d (ReleaseTargets)", len(listed), len(release.ReleaseTargets))
	}
}

// TestRenderDeterministic proves two renders are byte-identical (no wall-clock,
// no rand) — required for the drift gate to be stable.
func TestRenderDeterministic(t *testing.T) {
	a, err := render()
	if err != nil {
		t.Fatalf("render A: %v", err)
	}
	b, err := render()
	if err != nil {
		t.Fatalf("render B: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("render produced different file counts: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].content != b[i].content {
			t.Fatalf("render %q is not deterministic across two runs", a[i].name)
		}
	}
}

// TestCheckDetectsDrift mutates a committed copy in a temp module root and
// asserts the unifiedDiff path reports a difference (the -check semantics).
func TestCheckDetectsDrift(t *testing.T) {
	files := []renderedFile{}
	var err error
	files, err = render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	dir := t.TempDir()
	// Write a deliberately stale install.sh.
	stale := "stale content that does not match\n"
	if werr := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(stale), 0o644); werr != nil {
		t.Fatalf("write stale: %v", werr)
	}
	for _, f := range files {
		if f.name != "install.sh" {
			continue
		}
		got, _ := os.ReadFile(filepath.Join(dir, "install.sh"))
		if string(got) == f.content {
			t.Fatal("temp copy unexpectedly matched the fresh render")
		}
		diff := unifiedDiff(f.name, string(got), f.content)
		if diff == "" || !strings.Contains(diff, "stale content") {
			t.Errorf("unifiedDiff did not report the drift; got:\n%s", diff)
		}
	}
}

// TestNoDriftAfterGenerate proves the committed install.sh/install.ps1 at the
// module root match a fresh render — i.e. `-check` would pass right now. This is
// the local mirror of the CI install-script-drift gate.
func TestNoDriftAfterGenerate(t *testing.T) {
	root := moduleRoot()
	files, err := render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, f := range files {
		got, rerr := os.ReadFile(filepath.Join(root, f.name))
		if rerr != nil {
			t.Fatalf("read committed %s: %v", f.name, rerr)
		}
		if string(got) != f.content {
			t.Errorf("committed %s is stale — run `go run ./cmd/gen-install`", f.name)
		}
	}
}
