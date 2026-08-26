package main

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/opcatalog"
)

// SW-225 (AX-05) AC-3 — the CLI comparison, reported and NOT gating.
//
// The story is explicit: CLI help generation from catalog metadata exists in
// comparison mode only, served help stays legacy, and the diff is allowed to
// differ, tracked, not blocking. So this file asserts the things that would make
// the comparison worthless — a generator that produces nothing, or produces
// something different on each run — and logs everything else.
//
// The one signal it does check is that the generated tier markers do not
// contradict the served ones. That is not the story tightening its own AC: a
// disagreement there is not a "CLI prose differs from agent prose" finding, it
// is two surfaces telling a user different things about whether a capability is
// a stable promise, and graphi's standards make the taxonomy a single fact
// (mcp.StableOperations). It is reported as a failure with that reasoning
// attached; every other difference is logged.

func helpCatalog(t *testing.T) *opcatalog.Catalog {
	t.Helper()
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow(): %v", err)
	}
	return catalog
}

func TestAX05_CLIHelpFromCatalog_ComparisonIsReported(t *testing.T) {
	catalog := helpCatalog(t)

	entries := catalogHelpEntries(catalog)
	if len(entries) == 0 {
		t.Fatal("the catalog generated no CLI help entries; the comparison would be vacuous")
	}
	for _, entry := range entries {
		if strings.TrimSpace(entry.Synopsis) == "" {
			t.Errorf("%s: generated an empty synopsis from %q", entry.Verb, entry.Operation)
		}
		if runes := len([]rune(entry.Synopsis)); runes > catalogHelpSynopsisLimit+1 {
			t.Errorf("%s: generated synopsis is %d characters, over the %d-character line budget",
				entry.Verb, runes, catalogHelpSynopsisLimit)
		}
	}

	diff := diffCatalogHelp(catalog)
	t.Logf("AX-05 AC-3 — CLI help: catalog-generated vs served (%d operations map to a CLI verb)\n%s",
		len(entries), diff.Report())

	// NOT blocking, per AC-3 — but the tier half is a different claim from the
	// prose half. See the file header.
	for _, disagreement := range diff.MarkerDisagreements {
		t.Errorf("CLI/catalog stability taxonomy disagreement: %s\n"+
			"The [labs] marker on a help line and `tier` in the catalog are the same claim about "+
			"the same capability. Prose may differ; the tier may not.", disagreement)
	}
}

// A generator that is not deterministic cannot be compared against anything.
func TestAX05_CLIHelpFromCatalog_IsDeterministic(t *testing.T) {
	catalog := helpCatalog(t)
	render := func() string {
		var b strings.Builder
		for _, entry := range catalogHelpEntries(catalog) {
			b.WriteString(entry.Line())
			b.WriteString("\n")
		}
		b.WriteString(diffCatalogHelp(catalog).Report())
		return b.String()
	}
	first := render()
	for i := 0; i < 5; i++ {
		if got := render(); got != first {
			t.Fatalf("the CLI help generator is not deterministic:\n first = %s\n got   = %s", first, got)
		}
	}
}

// AC-3 — served CLI help must remain LEGACY. The generator may not have leaked
// into a command path: `graphi help <verb>` still renders the hand-written
// table, which is what the AX-00 cli-help.txt golden freezes.
func TestAX05_ServedCLIHelp_StaysLegacy(t *testing.T) {
	catalog := helpCatalog(t)
	entries := catalogHelpEntries(catalog)

	var differing int
	for _, entry := range entries {
		var buf strings.Builder
		if !printSubcommandHelp(entry.Verb, &buf) {
			t.Fatalf("%s: served help has no entry, but the generator produced one", entry.Verb)
		}
		served := buf.String()
		if !strings.Contains(served, subcommandHelp[entry.Verb].synopsis) {
			t.Errorf("%s: served help no longer renders the hand-written synopsis", entry.Verb)
		}
		if subcommandHelp[entry.Verb].synopsis != entry.Synopsis {
			differing++
			if strings.Contains(served, entry.Synopsis) {
				t.Errorf("%s: served help rendered the GENERATED synopsis — comparison mode leaked "+
					"into the served path, which AC-3 forbids", entry.Verb)
			}
		}
	}
	if differing == 0 {
		t.Log("every generated synopsis happens to equal the served one; the leak check above " +
			"cannot distinguish the two sources this run")
	}
}
