package importgraph

import (
	"context"
	"testing"
)

func TestZoneOf(t *testing.T) {
	cases := map[string]string{
		"cmd/graphi":            ZoneCmd,
		"surfaces/client":       ZoneSurfaces,
		"engine/query":          ZoneEngine,
		"core/graphstore":       ZoneCore,
		"internal/layerguard":   ZoneInternal,
		"corpus":                ZoneOther,
		"extensions/github":     ZoneOther,
		"cmd":                   ZoneCmd,
		"surfacesomething/pkg":  ZoneOther,
		"enginewrapper/adapter": ZoneOther,
	}
	for path, want := range cases {
		if got := ZoneOf(path); got != want {
			t.Errorf("ZoneOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRel(t *testing.T) {
	if got, ok := rel(ModulePath + "/engine/query"); !ok || got != "engine/query" {
		t.Errorf("rel(module/engine/query) = (%q, %v), want (engine/query, true)", got, ok)
	}
	// A foreign path that merely shares a prefix must not be misread as internal.
	if _, ok := rel(ModulePath + "-fork/engine"); ok {
		t.Errorf("rel treated a same-prefix foreign module path as module-internal")
	}
	if _, ok := rel("encoding/json"); ok {
		t.Errorf("rel treated a stdlib path as module-internal")
	}
}

// TestBuild_RequiresCommit pins the deliberate design choice that a snapshot must
// name the revision it describes; silently recording "some tree" would make later
// A/B comparisons unfalsifiable.
func TestBuild_RequiresCommit(t *testing.T) {
	if _, err := Build(context.Background(), t.TempDir(), ""); err == nil {
		t.Fatal("Build accepted an empty commit; the artifact must record its baseline revision")
	}
}

// TestBuild_LiveTreeIsDeterministic scans the real module twice and requires
// byte-identical artifacts. The snapshot is only useful as a baseline if map
// iteration order cannot leak into it.
func TestBuild_LiveTreeIsDeterministic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live `go list` scan in -short mode")
	}
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	ctx := context.Background()

	first, err := Build(ctx, root, "test-commit")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	second, err := Build(ctx, root, "test-commit")
	if err != nil {
		t.Fatalf("Build (second): %v", err)
	}

	a, err := Render(first)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	b, err := Render(second)
	if err != nil {
		t.Fatalf("Render (second): %v", err)
	}
	if string(a) != string(b) {
		t.Error("import graph rendering is not deterministic across two scans of the same tree")
	}

	if len(first.Packages) == 0 {
		t.Fatal("scan found no packages in the module")
	}
	// Sanity-check the shape rather than pin counts: this is a descriptive
	// artifact, and pinning package totals here would fail on every unrelated
	// package addition without telling anyone anything useful.
	for _, p := range first.Packages {
		if p.Zone == "" {
			t.Errorf("package %q has no zone", p.Path)
		}
		for _, imp := range p.Imports {
			if imp == p.Path {
				t.Errorf("package %q imports itself", p.Path)
			}
		}
	}
	if len(first.ZoneEdges) == 0 {
		t.Error("scan found no zone edges; the module is not that flat")
	}
}
