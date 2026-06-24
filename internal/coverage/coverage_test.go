package coverage

import (
	"reflect"
	"sort"
	"testing"
)

func mustEnumerate(t *testing.T) LiveSet {
	t.Helper()
	live, err := Enumerate()
	if err != nil {
		t.Fatalf("Enumerate: %v", err)
	}
	return live
}

// TestEnumerate_Deterministic asserts the live set is stable and sorted across
// runs so the drift guard is never flaky (AC-1).
func TestEnumerate_Deterministic(t *testing.T) {
	a := mustEnumerate(t)
	b := mustEnumerate(t)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Enumerate not deterministic:\n a=%+v\n b=%+v", a, b)
	}
	for name, ids := range map[string][]string{
		"parsers":   a.Parsers,
		"analyzers": a.Analyzers,
		"mcp-tools": a.MCPTools,
		"surfaces":  a.Surfaces,
	} {
		if !sort.StringsAreSorted(ids) {
			t.Errorf("%s not sorted: %v", name, ids)
		}
	}
}

// TestEnumerate_Completeness asserts known live capabilities are present, guarding
// against an enumerator that silently drops a registry (AC-1).
func TestEnumerate_Completeness(t *testing.T) {
	live := mustEnumerate(t)

	wantParsers := []string{"go", "json", "python", "typescript"}
	assertContainsAll(t, "parsers", live.Parsers, wantParsers)

	// `concept` only registers when the reader implements analysis.Searcher —
	// its presence proves the stub reader wiring works.
	wantAnalyzers := []string{"concept", "impact", "pr-risk", "taint"}
	assertContainsAll(t, "analyzers", live.Analyzers, wantAnalyzers)

	wantTools := []string{"analyze", "analyze_taint", "callers", "pr_comment", "search_semantic"}
	assertContainsAll(t, "mcp-tools", live.MCPTools, wantTools)

	wantSurfaces := []string{"cli", "daemon", "github-action", "http", "mcp", "tui", "vscode", "web"}
	if !reflect.DeepEqual(live.Surfaces, sortedCopy(wantSurfaces)) {
		t.Errorf("surfaces = %v, want exactly %v", live.Surfaces, sortedCopy(wantSurfaces))
	}

	// Visibility when run with -v: the exact live ids used to author the matrix.
	t.Logf("LIVE parsers (%d): %v", len(live.Parsers), live.Parsers)
	t.Logf("LIVE analyzers (%d): %v", len(live.Analyzers), live.Analyzers)
	t.Logf("LIVE mcp-tools (%d): %v", len(live.MCPTools), live.MCPTools)
	t.Logf("LIVE surfaces (%d): %v", len(live.Surfaces), live.Surfaces)
}

func assertContainsAll(t *testing.T, label string, got, want []string) {
	t.Helper()
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("%s missing expected %q (got %v)", label, w, got)
		}
	}
}
