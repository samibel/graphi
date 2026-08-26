package main

// SW-225 (AX-05) AC-3 — CLI help metadata generated from the operation catalog,
// in COMPARISON MODE ONLY.
//
// Served CLI help stays exactly where it is: help.go's hand-written
// subcommandHelp table renders every `graphi help` line, and nothing in this
// file is reachable from a command path. The story is explicit that CLI help is
// allowed to differ from the catalog and that the difference is tracked rather
// than gated — so this generator exists to MEASURE the gap, not to close it.
//
// Why the gap is expected, and why measuring it is still worth doing:
//
//   - The catalog's descriptions are written for an agent choosing a tool. They
//     carry the six-facet template (purpose / when to use / when NOT to use /
//     input shape / read-only / partial-possible) and run to several hundred
//     characters. A CLI synopsis is one line a human scans in a terminal. These
//     are different texts on purpose, and collapsing them would make one of the
//     two audiences worse off.
//   - The verb vocabularies differ too: CLI verbs are kebab-case
//     (`explain-symbol`), operation ids are snake_case (`explain_symbol`), and
//     the CLI has verbs no operation backs at all (`doctor`, `setup`, `help`)
//     plus one umbrella verb (`query`) that hosts five operations.
//
// What the comparison IS good for is the tier marker. `[labs] ` on a CLI help
// line and `tier: labs` in the catalog are the same claim about the same
// capability, told to two audiences, and those must not disagree. The generator
// therefore reports marker disagreements separately from prose differences, so
// the one signal that matters is not buried in the noise the story permits.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/opcatalog"
)

// catalogHelpEntry is one CLI help line derived from an OperationSpec.
type catalogHelpEntry struct {
	// Verb is the CLI subcommand spelling (kebab-case) of the operation.
	Verb string
	// Operation is the catalog operation id it was derived from.
	Operation string
	// Marker is the tier marker the catalog implies: labsHelpMarker or "".
	Marker string
	// Synopsis is the catalog description reduced to one scannable line.
	Synopsis string
}

// Line renders the entry the way runHelp renders a served line, so the two are
// compared in the same shape rather than through a translation nobody reads.
func (e catalogHelpEntry) Line() string {
	return fmt.Sprintf("%-18s %s%s", e.Verb, e.Marker, e.Synopsis)
}

// catalogHelpSynopsisLimit bounds a generated synopsis, in RUNES rather than
// bytes: the agent-facing descriptions are full of em dashes and arrows, and a
// byte budget would silently give those lines a shorter one. The cap exists so
// an agent-facing description cannot produce a help line that wraps a terminal.
const catalogHelpSynopsisLimit = 110

// verbForOperation maps an operation id to its CLI verb spelling. Operation ids
// are snake_case wire identifiers; CLI verbs are kebab-case (a decision recorded
// with ToolStrictQuery in surfaces/mcp/tools.go: `strict_query` on the wire,
// `query-strict` on the command line).
func verbForOperation(id string) string { return strings.ReplaceAll(id, "_", "-") }

// catalogSynopsis reduces an agent-facing description to one CLI-sized line.
//
// It is deliberately mechanical: take the text up to the first sentence break,
// drop a leading "<name>: " self-reference (the agent descriptions repeat the
// tool name, which a help line already shows in its first column), and truncate
// on a word boundary. A cleverer summariser would be a second author of the
// prose, and the whole point of comparison mode is to see what the CATALOG says.
func catalogSynopsis(id, description string) string {
	text := strings.TrimSpace(description)
	text = strings.TrimPrefix(text, id+": ")
	text = text[:firstSentenceEnd(text)]
	text = strings.TrimSuffix(strings.TrimSpace(text), ".")

	runes := []rune(text)
	if len(runes) <= catalogHelpSynopsisLimit {
		return text
	}
	cut := string(runes[:catalogHelpSynopsisLimit])
	if idx := strings.LastIndex(cut, " "); idx > 0 {
		cut = cut[:idx]
	}
	return cut + "…"
}

// firstSentenceEnd returns the index of the first real sentence break in text,
// or len(text) when there is none.
//
// "Real" excludes an abbreviation's period: several descriptions read "run a
// named graph analyzer (e.g. impact ...)", and splitting there would generate
// the synopsis "run a named graph analyzer (e.g". The rule is deliberately
// crude — a ". " whose preceding two characters already contain a period is an
// abbreviation — because a comparison generator that needed real sentence
// segmentation would be a second author of the prose it is supposed to be
// measuring.
func firstSentenceEnd(text string) int {
	for offset := 0; ; {
		idx := strings.Index(text[offset:], ". ")
		if idx < 0 {
			return len(text)
		}
		at := offset + idx
		if at > 1 && !strings.Contains(text[at-2:at], ".") {
			return at
		}
		offset = at + 2
	}
}

// catalogHelpEntries generates one help entry per catalog operation that the CLI
// exposes as a subcommand of its own, in canonical verb order.
//
// Operations with no CLI verb are not entries — they are reported separately by
// diffCatalogHelp, because "the CLI does not expose this operation" is a finding
// and not a blank line.
func catalogHelpEntries(catalog *opcatalog.Catalog) []catalogHelpEntry {
	var out []catalogHelpEntry
	for _, spec := range catalog.All() {
		verb := verbForOperation(spec.ID)
		if _, served := subcommandHelp[verb]; !served {
			continue
		}
		marker := ""
		if spec.Tier != opcatalog.TierStable {
			marker = labsHelpMarker
		}
		out = append(out, catalogHelpEntry{
			Verb:      verb,
			Operation: spec.ID,
			Marker:    marker,
			Synopsis:  catalogSynopsis(spec.ID, spec.Description),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Verb < out[j].Verb })
	return out
}

// catalogHelpDiff is the tracked, non-blocking comparison result.
type catalogHelpDiff struct {
	// MarkerDisagreements are verbs where the catalog tier and the served CLI
	// tier marker contradict each other. This is the half that would be a bug.
	MarkerDisagreements []string
	// SynopsisDifferences are verbs where both sources describe the operation
	// but in different words. Expected, and listed so the size of the gap is
	// visible rather than assumed.
	SynopsisDifferences []string
	// OperationsWithoutAVerb are catalog operations the CLI does not expose as a
	// subcommand of its own.
	OperationsWithoutAVerb []string
	// VerbsWithoutAnOperation are CLI subcommands no catalog operation backs
	// (lifecycle and tooling verbs, and the `query` umbrella).
	VerbsWithoutAnOperation []string
}

// diffCatalogHelp compares generated help metadata against the served table.
func diffCatalogHelp(catalog *opcatalog.Catalog) catalogHelpDiff {
	var diff catalogHelpDiff
	generated := make(map[string]catalogHelpEntry)
	for _, entry := range catalogHelpEntries(catalog) {
		generated[entry.Verb] = entry
		served := subcommandHelp[entry.Verb]
		if servedMarker := stabilityMarker(entry.Verb); servedMarker != entry.Marker {
			diff.MarkerDisagreements = append(diff.MarkerDisagreements, fmt.Sprintf(
				"%s: catalog implies %q, served CLI help renders %q",
				entry.Verb, entry.Marker, servedMarker))
		}
		if served.synopsis != entry.Synopsis {
			diff.SynopsisDifferences = append(diff.SynopsisDifferences, fmt.Sprintf(
				"%s:\n    served:    %s\n    generated: %s", entry.Verb, served.synopsis, entry.Synopsis))
		}
	}
	for _, spec := range catalog.All() {
		if _, ok := generated[verbForOperation(spec.ID)]; !ok {
			diff.OperationsWithoutAVerb = append(diff.OperationsWithoutAVerb, spec.ID)
		}
	}
	verbs := make([]string, 0, len(subcommandHelp))
	for verb := range subcommandHelp {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	for _, verb := range verbs {
		if _, ok := generated[verb]; !ok {
			diff.VerbsWithoutAnOperation = append(diff.VerbsWithoutAnOperation, verb)
		}
	}
	sort.Strings(diff.MarkerDisagreements)
	sort.Strings(diff.SynopsisDifferences)
	sort.Strings(diff.OperationsWithoutAVerb)
	return diff
}

// Report renders the diff as a reviewable block.
func (d catalogHelpDiff) Report() string {
	var b strings.Builder
	section := func(title string, rows []string) {
		fmt.Fprintf(&b, "%s (%d)\n", title, len(rows))
		for _, row := range rows {
			fmt.Fprintf(&b, "  %s\n", row)
		}
	}
	section("tier-marker disagreements — the half that would be a bug", d.MarkerDisagreements)
	section("catalog operations with no CLI verb of their own", d.OperationsWithoutAVerb)
	section("CLI verbs no catalog operation backs", d.VerbsWithoutAnOperation)
	section("synopsis differences — expected: two audiences, two texts", d.SynopsisDifferences)
	return b.String()
}
