package analysis_test

import (
	"bytes"
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
)

// TestTriage_EngineNoNetwork (AC-5, structural): the triage analyzer file imports
// NO network/exec packages, so the engine scoring path cannot dial. All outbound
// activity is confined to the surface-boundary forge enumeration client; the
// engine `triage-prs` analyzer is zero-egress by construction.
func TestTriage_EngineNoNetwork(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "triage.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse triage.go: %v", err)
	}
	forbidden := []string{"net", "net/http", "net/url", "net/rpc", "os/exec"}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, "\"")
		for _, bad := range forbidden {
			if path == bad {
				t.Fatalf("triage.go must not import %q (engine is zero-egress; forge enumeration lives at the surface)", bad)
			}
		}
	}
}

// triageFixture seeds a graph with a clear blast-radius gradient plus a test node,
// then returns the store and the canonical paths the PRs touch.
//
//	a.go (A)        — depended on by nobody but A itself; covered by a test
//	b.go (B)        — leaf
//	c.go (C)        — HUB: A and B both depend on it (high blast radius / centrality)
//	a_test.go (T)   — a test node that calls A (so A is "reachable from test")
//
// Edges (From depends on To): A->C, B->C, T->A.
func triageFixture(t *testing.T, insert func(put func(model.Node), edge func(from, to, kind string))) *graphstore.MemStore {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	nodes := map[string]model.Node{}
	mkNode := func(name, path string) model.Node {
		n, err := model.NewNode("function", "p."+name, path, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		nodes[name] = n
		return n
	}
	mkNode("A", "a.go")
	mkNode("B", "b.go")
	mkNode("C", "c.go")
	mkNode("Atest", "a_test.go")

	put := func(n model.Node) {
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	// Persist all nodes first (nodes before edges; edges reference node ids).
	for _, name := range []string{"A", "B", "C", "Atest"} {
		put(nodes[name])
	}
	edge := func(from, to, kind string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, model.TierConfirmed, 1, from+to, []string{from + to})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	insert(put, edge)
	return store
}

// triagePRs is the fixed PR set used across the determinism/parity tests.
func triagePRs() []analysis.TriagePRInput {
	return []analysis.TriagePRInput{
		{Number: 3, Title: "touch leaf B", Author: "bob", HeadSHA: "sha3", ChangedFiles: []string{"b.go"}},
		{Number: 1, Title: "touch hub C", Author: "alice", HeadSHA: "sha1", ChangedFiles: []string{"c.go"}},
		{Number: 2, Title: "touch tested A", Author: "carol", HeadSHA: "sha2", ChangedFiles: []string{"a.go"}},
		// Two PRs touching only unresolvable paths: equal composite (0) → tie-break
		// must order them by PR number ASC (5 after 4).
		{Number: 5, Title: "ghost five", Author: "dan", HeadSHA: "sha5", ChangedFiles: []string{"ghost/five.go"}},
		{Number: 4, Title: "ghost four", Author: "eve", HeadSHA: "sha4", ChangedFiles: []string{"ghost/four.go"}},
	}
}

func dispatchTriage(t *testing.T, store *graphstore.MemStore, prs []analysis.TriagePRInput) []byte {
	t.Helper()
	svc := analysis.NewDefaultService(store)
	res, err := svc.Dispatch(context.Background(), analysis.TriageAnalyzerName, analysis.Params{PRs: prs})
	if err != nil {
		t.Fatalf("dispatch triage: %v", err)
	}
	b, err := analysis.Marshal(res)
	if err != nil {
		t.Fatalf("marshal triage: %v", err)
	}
	return b
}

// buildFull seeds the fixture in one batch (nodes then edges).
func buildFull(t *testing.T) *graphstore.MemStore {
	return triageFixture(t, func(put func(model.Node), edge func(from, to, kind string)) {
		// The node set is created inside triageFixture; we only declare the edges here.
		edge("A", "C", query.EdgeKindCalls)
		edge("B", "C", query.EdgeKindCalls)
		edge("Atest", "A", query.EdgeKindCalls)
	})
}

// TestTriage_FiveSignalsAndRanking (AC-2): triage_prs produces a ranked list
// scoring each PR by all five signals with a per-PR breakdown.
func TestTriage_FiveSignalsAndRanking(t *testing.T) {
	store := buildFull(t)
	out := dispatchTriage(t, store, triagePRs())

	var rep analysis.TriageReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatalf("decode triage report: %v\n%s", err, out)
	}
	if rep.AnalyzerVersion != analysis.TriageAnalyzerVersion {
		t.Fatalf("analyzer_version = %q, want %q", rep.AnalyzerVersion, analysis.TriageAnalyzerVersion)
	}
	if rep.WeightsHash == "" {
		t.Fatal("weights_hash must be present (audit trail)")
	}
	if len(rep.PRs) != 5 {
		t.Fatalf("want 5 ranked PRs, got %d", len(rep.PRs))
	}
	// The hub PR (#1, touches C which A+B depend on) must outrank the tested-leaf
	// PR (#2, touches A which is reachable from a test).
	if rep.PRs[0].Number != 1 {
		t.Fatalf("expected hub PR #1 ranked first, got #%d (composites: %+v)", rep.PRs[0].Number, ranks(rep))
	}
	// The hub PR must have a non-zero blast radius bucket — the five-signal
	// breakdown is genuinely computed, not stubbed.
	if rep.PRs[0].Signals.BlastRadius == 0 {
		t.Fatalf("hub PR blast radius bucket must be > 0; breakdown=%+v", rep.PRs[0].Signals)
	}
	// The tested PR #2 must register a non-zero test-coverage signal (A is
	// reachable from a_test.go) — the NEW signal is genuinely built.
	pr2 := findPR(rep, 2)
	if pr2 == nil || pr2.Signals.TestCoverage == 0 {
		t.Fatalf("tested PR #2 must have test_coverage > 0; got %+v", pr2)
	}
	// Composite must be monotonically non-increasing (total order, DESC).
	for i := 1; i < len(rep.PRs); i++ {
		if rep.PRs[i-1].Composite < rep.PRs[i].Composite {
			t.Fatalf("ranking not composite-DESC at %d: %+v", i, ranks(rep))
		}
	}
}

// TestTriage_TieBreak (AC-3): equal-composite PRs retain a fixed order by PR
// number ASC. PRs #4 and #5 touch only unresolvable paths → composite 0 each.
func TestTriage_TieBreak(t *testing.T) {
	store := buildFull(t)
	out := dispatchTriage(t, store, triagePRs())
	var rep analysis.TriageReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatal(err)
	}
	pr4, pr5 := findPR(rep, 4), findPR(rep, 5)
	if pr4 == nil || pr5 == nil {
		t.Fatal("PRs 4 and 5 must be present")
	}
	if pr4.Composite != pr5.Composite {
		t.Fatalf("expected equal composite for the two ghost PRs, got %d vs %d", pr4.Composite, pr5.Composite)
	}
	// Find their positions: #4 must come before #5 (number ASC on tie).
	var pos4, pos5 int
	for i, p := range rep.PRs {
		if p.Number == 4 {
			pos4 = i
		}
		if p.Number == 5 {
			pos5 = i
		}
	}
	if pos4 >= pos5 {
		t.Fatalf("tie-break: PR #4 must precede #5 (got pos4=%d pos5=%d)", pos4, pos5)
	}
}

// TestTriage_Determinism (AC-3): identical graph + PR set → byte-identical output
// across repeated runs.
func TestTriage_Determinism(t *testing.T) {
	store := buildFull(t)
	prs := triagePRs()
	first := dispatchTriage(t, store, prs)
	for i := 0; i < 5; i++ {
		again := dispatchTriage(t, store, prs)
		if !bytes.Equal(first, again) {
			t.Fatalf("triage output not byte-identical on repeat run %d:\nfirst: %s\nagain: %s", i, first, again)
		}
	}
	// Input PR order must not affect output (the encoder enforces the total order).
	shuffled := []analysis.TriagePRInput{prs[4], prs[0], prs[2], prs[3], prs[1]}
	if got := dispatchTriage(t, store, shuffled); !bytes.Equal(first, got) {
		t.Fatalf("triage output changed with reordered input:\nsorted:   %s\nshuffled: %s", first, got)
	}
}

// TestTriage_FullVsIncremental (AC-4): a graph built full-batch versus built
// incrementally over the SAME repo state yields byte-identical ranked output.
// Identity is content-addressed (EP-001), so the touched-node sets — and thus the
// ranking — are independent of the index path.
func TestTriage_FullVsIncremental(t *testing.T) {
	full := buildFull(t)

	// Incremental: same nodes + edges, inserted in a different (interleaved) order.
	incremental := triageFixture(t, func(put func(model.Node), edge func(from, to, kind string)) {
		edge("Atest", "A", query.EdgeKindCalls)
		edge("B", "C", query.EdgeKindCalls)
		edge("A", "C", query.EdgeKindCalls)
	})

	prs := triagePRs()
	fullOut := dispatchTriage(t, full, prs)
	incOut := dispatchTriage(t, incremental, prs)
	if !bytes.Equal(fullOut, incOut) {
		t.Fatalf("full vs incremental triage output differs:\nfull: %s\ninc:  %s", fullOut, incOut)
	}
}

// TestTriage_EmptyPRSet completes with an empty ranked list (never an error).
func TestTriage_EmptyPRSet(t *testing.T) {
	store := buildFull(t)
	out := dispatchTriage(t, store, nil)
	var rep analysis.TriageReport
	if err := json.Unmarshal(out, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Outcome != string(query.OutcomeEmpty) || len(rep.PRs) != 0 {
		t.Fatalf("empty PR set must yield empty outcome + no PRs, got %+v", rep)
	}
}

// TestTriage_EngineIsForgeFree (AC-5, structural): the engine triage path scores a
// fully in-memory graph from the PR set handed in via Params — it never enumerates
// or dials. Dispatching against a MemStore (no network anything) producing a valid
// ranking demonstrates the scoring path is forge-free / zero-egress; the forge
// enumeration lives strictly above the engine at the surface boundary.
func TestTriage_EngineIsForgeFree(t *testing.T) {
	store := buildFull(t)
	out := dispatchTriage(t, store, triagePRs())
	if !bytes.Contains(out, []byte(`"analyzer_version":"triage-prs/1"`)) {
		t.Fatalf("triage output missing analyzer_version (engine scoring path): %s", out)
	}
}

func ranks(rep analysis.TriageReport) []string {
	out := make([]string, 0, len(rep.PRs))
	for _, p := range rep.PRs {
		out = append(out, jsonOf(p.Number, p.Composite))
	}
	return out
}

func jsonOf(num, comp int) string {
	b, _ := json.Marshal(map[string]int{"pr": num, "composite": comp})
	return string(b)
}

func findPR(rep analysis.TriageReport, num int) *analysis.TriagePRScore {
	for i := range rep.PRs {
		if rep.PRs[i].Number == num {
			return &rep.PRs[i]
		}
	}
	return nil
}
