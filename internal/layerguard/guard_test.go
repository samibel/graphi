package layerguard

import (
	"context"
	"testing"
)

func TestLayerOf(t *testing.T) {
	cases := []struct {
		pkg  string
		rank int
		ok   bool
	}{
		{"github.com/samibel/graphi/cmd/graphi", LayerCmd, true},
		{"github.com/samibel/graphi/surfaces/cli", LayerSurfaces, true},
		{"github.com/samibel/graphi/engine/query", LayerEngine, true},
		{"github.com/samibel/graphi/core/parse", LayerCore, true},
		{"github.com/samibel/graphi/internal/bench", 0, false}, // unranked tooling
		{"fmt", 0, false},                                       // stdlib
		{"github.com/cespare/xxhash/v2", 0, false},              // external
	}
	for _, c := range cases {
		rank, ok := LayerOf(c.pkg)
		if rank != c.rank || ok != c.ok {
			t.Errorf("LayerOf(%q) = (%d,%v), want (%d,%v)", c.pkg, rank, ok, c.rank, c.ok)
		}
	}
}

func TestCheck_CompliantTreePassesAndReportsAllowedEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live go-list layer scan in -short mode")
	}
	rep, err := Check(context.Background())
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !rep.Pass {
		t.Fatalf("expected compliant tree to pass; violations:\n%s", rep.Format())
	}
	// A compliant graphi tree must observe at least the core downward edges.
	wantPresent := map[string]bool{
		"engine→core":   false,
		"surfaces→core": false,
		"cmd→surfaces":  false,
	}
	for _, e := range rep.AllowedEdges {
		if _, ok := wantPresent[e]; ok {
			wantPresent[e] = true
		}
	}
	for e, seen := range wantPresent {
		if !seen {
			t.Errorf("expected allowed edge %q in observed set, got %v", e, rep.AllowedEdges)
		}
	}
}

func TestViolation_DetectsUpwardEdge(t *testing.T) {
	// Synthetic: a core package importing an engine package is an upward edge.
	importerRank, _ := LayerOf(ModulePath + "/core/parse")
	importedRank, _ := LayerOf(ModulePath + "/engine/query")
	if importedRank <= importerRank {
		t.Fatalf("engine (%d) must outrank core (%d) for an upward edge", importedRank, importerRank)
	}
	v := Violation{Importer: ModulePath + "/core/parse", Imported: ModulePath + "/engine/query",
		ImporterLayer: importerRank, ImportedLayer: importedRank}
	if !contains(v.String(), "upward edge") || !contains(v.String(), "core/parse") || !contains(v.String(), "engine/query") {
		t.Errorf("violation string should name the upward edge + both packages: %q", v.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
