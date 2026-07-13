package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/coverage"
)

// TestSubcommandHelpCoversEveryDispatchCase asserts that every subcommand in
// main()'s dispatch switch has a help entry (and no entry is stale). It reuses
// the same static AST scan the coverage-matrix gate runs, so the two can never
// disagree about what "every subcommand" means.
func TestSubcommandHelpCoversEveryDispatchCase(t *testing.T) {
	subs, err := coverage.EnumerateCLISubcommands()
	if err != nil {
		t.Fatalf("enumerate dispatch cases: %v", err)
	}
	for _, name := range subs {
		if _, ok := subcommandHelp[name]; !ok {
			t.Errorf("dispatch case %q has no subcommandHelp entry — add one in cmd/graphi/help.go", name)
		}
	}
	known := map[string]bool{}
	for _, name := range subs {
		known[name] = true
	}
	for name := range subcommandHelp {
		if !known[name] {
			t.Errorf("subcommandHelp entry %q has no dispatch case — stale entry or missing wiring", name)
		}
	}
}

// TestPrintSubcommandHelp asserts the rendered shape (name, synopsis, usage,
// example) and the short-verb aliasing onto the long forms.
func TestPrintSubcommandHelp(t *testing.T) {
	var b bytes.Buffer
	if !printSubcommandHelp("query", &b) {
		t.Fatal("query should be known")
	}
	out := b.String()
	for _, want := range []string{"graphi query —", "usage:", "example:"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q:\n%s", want, out)
		}
	}

	// Short query verb aliases to the query entry.
	b.Reset()
	if !printSubcommandHelp("callers", &b) {
		t.Fatal("short verb callers should resolve to query help")
	}
	if !strings.Contains(b.String(), "structural query") {
		t.Errorf("callers help should render the query synopsis:\n%s", b.String())
	}

	// Short analyze verb aliases to the analyze entry.
	b.Reset()
	if !printSubcommandHelp("impact", &b) {
		t.Fatal("short verb impact should resolve to analyze help")
	}

	if printSubcommandHelp("definitely-not-a-subcommand", &b) {
		t.Error("unknown name must report not-found")
	}
}

// TestStabilityMarker asserts the SCOPE-01 Stable/Labs tier is visible and
// correct in CLI help: stable operations render WITHOUT the [labs] marker; Labs
// subcommands render WITH it. Stability is derived from mcp.StableOperations.
func TestStabilityMarker(t *testing.T) {
	stable := []string{"index", "search", "explain-symbol", "related-files", "change-risk", "agent-brief", "query", "callers", "definition", "impact"}
	for _, n := range stable {
		if !subcommandIsStable(n) {
			t.Errorf("%q should be a stable operation (SCOPE-01)", n)
		}
		if stabilityMarker(n) != "" {
			t.Errorf("stable %q must not carry the labs marker", n)
		}
	}
	labs := []string{"analyze", "taint", "refactor", "memory", "distill", "skillgen", "triage-prs", "http", "daemon", "tui", "implementers", "supertypes"}
	for _, n := range labs {
		if subcommandIsStable(n) {
			t.Errorf("%q should be Labs, not stable (SCOPE-01)", n)
		}
		if stabilityMarker(n) != labsHelpMarker {
			t.Errorf("labs %q must carry the %q marker", n, labsHelpMarker)
		}
	}

	// End-to-end through the rendered help line.
	var b bytes.Buffer
	if !printSubcommandHelp("analyze", &b) {
		t.Fatal("analyze should be known")
	}
	if !strings.Contains(b.String(), labsHelpMarker) {
		t.Errorf("analyze help should carry the labs marker:\n%s", b.String())
	}
	b.Reset()
	if !printSubcommandHelp("search", &b) {
		t.Fatal("search should be known")
	}
	if strings.Contains(b.String(), labsHelpMarker) {
		t.Errorf("stable search help should NOT carry the labs marker:\n%s", b.String())
	}
}

// TestRunHelp asserts `graphi help <sub>` exit codes and the unknown-name
// listing.
func TestRunHelp(t *testing.T) {
	var b bytes.Buffer
	if code := runHelp([]string{"search"}, &b); code != 0 {
		t.Fatalf("help search: exit %d, want 0", code)
	}
	if !strings.Contains(b.String(), "graphi search") {
		t.Errorf("help search output:\n%s", b.String())
	}

	b.Reset()
	if code := runHelp([]string{"nope"}, &b); code != 1 {
		t.Fatalf("help nope: exit %d, want 1", code)
	}
	if !strings.Contains(b.String(), "known subcommands:") {
		t.Errorf("unknown-subcommand output should list known names:\n%s", b.String())
	}
}
