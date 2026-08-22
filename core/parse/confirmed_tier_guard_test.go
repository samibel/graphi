package parse_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// THE GAP THIS GUARDS — ADR 0008 ruling D9, `Owns` is not TOTAL (SW-170 review
// round 1, finding 3).
// ---------------------------------------------------------------------------
//
// D9 made the stale-confirmed sweep key (directory, LANGUAGE): engine/ingest
// sweeps a stored confirmed calls/references/implements edge only when the
// edge's FROM-node sits in a file some registrant OWNS. The registrants own
// exactly `*.go`, `*.java` and `*.kt` (engine/typeresolve/registry.go,
// engine/semantic/semantic.go), and that set is a PARTITION, not a COVER:
//
//	every path that is not .go/.java/.kt is owned by NOBODY.
//
// So a stored confirmed calls/references/implements edge whose from-node is
// sourced in, say, a .py or .ts file could never be swept by anyone — it would
// be IMMORTAL, surviving every incremental sync while the full pass never has
// it. That is a wrong surviving edge, the exact failure class D9 exists to
// close. Before D9 the Go pass reached those edges incidentally (the key was
// the directory alone), so the narrowing is what opens the hole.
//
// THE GAP IS UNREACHABLE TODAY, AND THIS TEST IS WHY IT STAYS THAT WAY. The
// unreachability argument has exactly one load-bearing step: NO PARSER MINTS A
// CONFIRMED-TIER EDGE OF A SWEEPABLE KIND. Concretely —
//
//   - engine/link/link.go never returns model.TierConfirmed (heuristic tier
//     only), so cross-file name resolution cannot produce one.
//   - Every tree-sitter walker hardcodes model.TierDerived
//     (parser_tswalk.go, parser_ts.go), as does extract_go.go for calls and
//     references.
//   - The only confirmed-tier producers in this package are the two `defines`
//     edges allow-listed below, and `defines` is NOT in engine/ingest's
//     typeresolveKind set (calls|references|implements), so it never enters
//     the sweep at all.
//   - The remaining confirmed-tier producers in the tree are
//     engine/ingest/notebook.go's `notebook_cell` (also outside
//     typeresolveKind) and the two semantic resolvers themselves, whose
//     from-nodes are .go/.java/.kt by construction.
//
// BUT NOTHING ENFORCED THAT STEP. core/parse/mapping.go honours a
// parser-supplied `spec.Tier` verbatim (`tier := spec.Tier`, defaulting only
// when invalid), so a future grammar worker that sets model.TierConfirmed on a
// `calls` spec would silently open the gap, and the symptom would be a
// permanently wrong edge that no sweep can reach. This test is that
// enforcement, and it is deliberately CHEAP and TEST-ONLY: it adds no product
// bytes, so it costs no candidate move.
//
// SCOPE, stated so it is not over-read: this guards core/parse, the package
// that owns every shipped parser and the mapping.go seam that honours a
// supplied tier. It does not, and cannot statically, prove `Owns` totality —
// the durable fix is either a residual-owner sweep rule (an edge owned by
// nobody is swept by whoever checked its directory) or a product-wide
// assertion that no confirmed typeresolveKind edge has an unowned from-node.
// Both are recorded for SW-178's D9 ratification; this test holds the line
// until one of them lands.

// confirmedTierAllowlist maps a source file in this package to the token that
// MUST appear on any line minting model.TierConfirmed there. Both entries are
// the per-symbol `defines` edge — a file declaring the symbols it declares, a
// fact the parser genuinely proves and which no sweep touches.
//
// To add an entry you must first answer: is the new confirmed edge's kind in
// engine/ingest's typeresolveKind (calls|references|implements)? If it is, do
// NOT allow-list it — a confirmed edge of that kind from a file no registrant
// owns is immortal. See the header.
var confirmedTierAllowlist = map[string]string{
	"extract_go.go": "goEdgeDefines",
	"mapping.go":    "EdgeDefines",
}

// TestNoConfirmedTierOutsideDefines statically asserts that every
// model.TierConfirmed in this package's non-test sources sits on an
// allow-listed `defines` edge. It fails closed: a new confirmed-tier site in a
// new grammar worker is an error until someone states, in the allow-list, which
// edge kind it mints and why the sweep can reach it.
func TestNoConfirmedTierOutsideDefines(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked, found := 0, 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		checked++
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			if j := strings.Index(code, "//"); j >= 0 {
				code = code[:j] // comments discuss the tier; only code mints it
			}
			if !strings.Contains(code, "TierConfirmed") {
				continue
			}
			found++
			want, ok := confirmedTierAllowlist[name]
			if !ok {
				t.Errorf("%s:%d mints model.TierConfirmed, but %s is not in confirmedTierAllowlist.\n"+
					"ADR 0008 D9: a confirmed calls/references/implements edge from a file no\n"+
					"typeresolve registrant OWNS (anything but .go/.java/.kt) can never be swept —\n"+
					"it is immortal. If this edge is a `defines`-shaped fact, add the file and its\n"+
					"edge-kind token to the allow-list; otherwise do not mint it at this tier.\n"+
					"  line: %s", name, i+1, name, strings.TrimSpace(line))
				continue
			}
			if !strings.Contains(code, want) {
				t.Errorf("%s:%d mints model.TierConfirmed on a line that does not name %q.\n"+
					"The allow-list admits this file ONLY for that edge kind (see the header).\n"+
					"  line: %s", name, i+1, want, strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files found to check")
	}
	if found != len(confirmedTierAllowlist) {
		t.Errorf("found %d confirmed-tier site(s), allow-list declares %d: the allow-list is stale, "+
			"and a stale allow-list is how this guard stops guarding", found, len(confirmedTierAllowlist))
	}
}
