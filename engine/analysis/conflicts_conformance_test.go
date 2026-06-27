package analysis

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// --- fixture helpers -------------------------------------------------------

type cfNode struct {
	name string
	kind string
	path string
	line int
}

// buildConflictStore builds a MemStore from a node spec + edge spec. Edges are
// (fromName, toName, kind). It returns the store and a name→NodeId map so tests
// can assert on concrete identities.
func buildConflictStore(t *testing.T, nodes []cfNode, edges [][3]string) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}
	mk := func(n cfNode) {
		line := n.line
		if line == 0 {
			line = 1
		}
		nd, err := model.NewNode(n.kind, "pkg."+n.name, n.path, line, 1)
		if err != nil {
			t.Fatalf("NewNode %s: %v", n.name, err)
		}
		if err := store.PutNode(ctx, nd); err != nil {
			t.Fatalf("PutNode %s: %v", n.name, err)
		}
		ids[n.name] = nd.ID()
	}
	for _, n := range nodes {
		mk(n)
	}
	for _, e := range edges {
		ed, err := model.NewEdge(ids[e[0]], ids[e[1]], e[2], model.TierConfirmed, 1, e[0]+e[1], []string{e[0] + e[1]})
		if err != nil {
			t.Fatalf("NewEdge %v: %v", e, err)
		}
		if err := store.PutEdge(ctx, ed); err != nil {
			t.Fatalf("PutEdge %v: %v", e, err)
		}
	}
	return store, ids
}

func runConflicts(t *testing.T, store *graphstore.MemStore, prs []ConflictPRInput) ([]byte, ConflictReport) {
	t.Helper()
	svc := NewDefaultService(store)
	res, err := svc.Dispatch(context.Background(), ConflictsAnalyzerName, Params{ConflictPRs: prs})
	if err != nil {
		t.Fatalf("dispatch conflicts-prs: %v", err)
	}
	b, err := Marshal(res)
	if err != nil {
		t.Fatalf("marshal conflicts: %v", err)
	}
	if res.Conflicts == nil {
		t.Fatalf("conflicts report nil")
	}
	return b, *res.Conflicts
}

func pairFor(rep ConflictReport, a, b int) (ConflictPair, bool) {
	if a > b {
		a, b = b, a
	}
	for _, p := range rep.Pairs {
		if p.A == a && p.B == b {
			return p, true
		}
	}
	return ConflictPair{}, false
}

func hasKind(p ConflictPair, kind string) bool {
	for _, k := range p.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// --- AC-1: textual-overlap + shared-file + shared-symbol -------------------

func TestConflicts_TextualOverlapAndSharedFileSymbol(t *testing.T) {
	store, _ := buildConflictStore(t, []cfNode{
		{name: "Foo", kind: "function", path: "shared.go", line: 1},
	}, nil)
	prs := []ConflictPRInput{
		{Number: 1, ChangedFiles: []string{"shared.go"}, Diff: "shared.go#L10"},
		{Number: 2, ChangedFiles: []string{"shared.go"}, Diff: "shared.go#L12"},
	}
	_, rep := runConflicts(t, store, prs)
	p, ok := pairFor(rep, 1, 2)
	if !ok {
		t.Fatalf("expected pair (1,2); got %+v", rep.Pairs)
	}
	if !hasKind(p, ConflictSharedFile) {
		t.Errorf("missing shared-file kind: %v", p.Kinds)
	}
	if !hasKind(p, ConflictTextualOverlap) {
		t.Errorf("missing textual-overlap kind (lines 10 vs 12 within window): %v", p.Kinds)
	}
	if !hasKind(p, ConflictSharedSymbol) {
		t.Errorf("missing shared-symbol kind (both resolve to Foo): %v", p.Kinds)
	}
	// textual-overlap evidence must name the colliding lines.
	foundLines := false
	for _, e := range p.Entities {
		if e.Kind == ConflictTextualOverlap && e.File == "shared.go" && e.LineLow == 10 && e.LineHigh == 12 {
			foundLines = true
		}
	}
	if !foundLines {
		t.Errorf("textual-overlap evidence missing colliding lines 10/12: %+v", p.Entities)
	}
}

// --- AC-1: shared-high-centrality node -------------------------------------

func TestConflicts_SharedHighCentralityNode(t *testing.T) {
	// Star graph: Hub (in hub.go) with 4 leaves in separate files → centrality 1.0.
	store, ids := buildConflictStore(t, []cfNode{
		{name: "Hub", kind: "function", path: "hub.go"},
		{name: "L1", kind: "function", path: "l1.go"},
		{name: "L2", kind: "function", path: "l2.go"},
		{name: "L3", kind: "function", path: "l3.go"},
		{name: "L4", kind: "function", path: "l4.go"},
	}, [][3]string{
		{"Hub", "L1", string(query.EdgeKindCalls)},
		{"Hub", "L2", string(query.EdgeKindCalls)},
		{"Hub", "L3", string(query.EdgeKindCalls)},
		{"Hub", "L4", string(query.EdgeKindCalls)},
	})
	prs := []ConflictPRInput{
		{Number: 1, ChangedFiles: []string{"hub.go"}},
		{Number: 2, ChangedFiles: []string{"hub.go"}},
	}
	_, rep := runConflicts(t, store, prs)
	p, ok := pairFor(rep, 1, 2)
	if !ok {
		t.Fatalf("expected pair (1,2); got %+v", rep.Pairs)
	}
	if !hasKind(p, ConflictSharedHighCentralityNode) {
		t.Fatalf("missing shared-high-centrality-node kind: %v", p.Kinds)
	}
	found := false
	for _, e := range p.Entities {
		if e.Kind == ConflictSharedHighCentralityNode && e.Symbol == string(ids["Hub"]) && e.Centrality >= conflictCentralityBucketThreshold {
			found = true
		}
	}
	if !found {
		t.Errorf("high-centrality evidence missing Hub with bucket>=%d: %+v", conflictCentralityBucketThreshold, p.Entities)
	}
}

// --- AC-2: asymmetric contract-dependency with NO textual overlap ----------

func TestConflicts_ContractDependencyNoTextualOverlap(t *testing.T) {
	// PR-A mutates contract type T (t.go); PR-B changes UseT (use.go) which
	// references T via a graph edge. Disjoint files → git would never surface it.
	store, ids := buildConflictStore(t, []cfNode{
		{name: "T", kind: "type", path: "t.go"},
		{name: "UseT", kind: "function", path: "use.go"},
	}, [][3]string{
		{"UseT", "T", string(query.EdgeKindReferences)},
	})
	prs := []ConflictPRInput{
		{Number: 7, ChangedFiles: []string{"t.go"}},   // mutates the contract
		{Number: 9, ChangedFiles: []string{"use.go"}}, // depends on it
	}
	_, rep := runConflicts(t, store, prs)
	p, ok := pairFor(rep, 7, 9)
	if !ok {
		t.Fatalf("expected contract-dependency pair (7,9); got %+v", rep.Pairs)
	}
	if hasKind(p, ConflictSharedFile) {
		t.Errorf("pair must NOT carry shared-file (disjoint files): %v", p.Kinds)
	}
	if !hasKind(p, ConflictContractDependency) {
		t.Fatalf("missing contract-dependency kind: %v", p.Kinds)
	}
	found := false
	for _, e := range p.Entities {
		if e.Kind != ConflictContractDependency {
			continue
		}
		if e.Contract == string(ids["T"]) && e.Dependent == string(ids["UseT"]) &&
			e.MutatedByPR == 7 && e.DependentPR == 9 {
			found = true
		}
	}
	if !found {
		t.Fatalf("contract-dependency evidence must name contract T + dependent UseT (mutated by 7, dependent in 9): %+v", p.Entities)
	}
}

// --- AC-7: disjoint PRs → no false-positive pair ---------------------------

func TestConflicts_NoFalsePositiveForDisjointPRs(t *testing.T) {
	store, _ := buildConflictStore(t, []cfNode{
		{name: "x", kind: "function", path: "x.go"},
		{name: "y", kind: "function", path: "y.go"},
	}, nil) // no edges between them
	prs := []ConflictPRInput{
		{Number: 1, ChangedFiles: []string{"x.go"}},
		{Number: 2, ChangedFiles: []string{"y.go"}},
	}
	_, rep := runConflicts(t, store, prs)
	if len(rep.Pairs) != 0 {
		t.Fatalf("disjoint PRs must produce NO pair; got %+v", rep.Pairs)
	}
	if rep.Outcome != string(query.OutcomeEmpty) {
		t.Errorf("disjoint PRs outcome should be empty; got %q", rep.Outcome)
	}
}

// --- AC-3: determinism — repeat-run byte-identical + stable ordering -------

func TestConflicts_DeterminismRepeatRuns(t *testing.T) {
	store, prs := multiKindFixture(t)
	first, _ := runConflicts(t, store, prs)
	for i := 0; i < 8; i++ {
		got, _ := runConflicts(t, store, prs)
		if !bytes.Equal(first, got) {
			t.Fatalf("run %d not byte-identical:\nfirst: %s\ngot:   %s", i, first, got)
		}
	}
	// Pair ordering must be ascending on (A,B).
	_, rep := runConflicts(t, store, prs)
	for i := 1; i < len(rep.Pairs); i++ {
		a, b := rep.Pairs[i-1], rep.Pairs[i]
		if a.A > b.A || (a.A == b.A && a.B > b.B) {
			t.Fatalf("pairs not in ascending (A,B) order: %+v", rep.Pairs)
		}
	}
}

// --- AC-4: byte-identical full vs incremental index ------------------------

func TestConflicts_FullVsIncrementalByteIdentical(t *testing.T) {
	nodes := []cfNode{
		{name: "Foo", kind: "function", path: "shared.go", line: 1},
		{name: "T", kind: "type", path: "t.go"},
		{name: "UseT", kind: "function", path: "use.go"},
	}
	edges := [][3]string{{"UseT", "T", string(query.EdgeKindReferences)}}
	prs := []ConflictPRInput{
		{Number: 1, ChangedFiles: []string{"shared.go", "t.go"}, Diff: "shared.go#L5"},
		{Number: 2, ChangedFiles: []string{"shared.go", "use.go"}, Diff: "shared.go#L6"},
	}

	// "full": build all at once. "incremental": same logical state, inserted in a
	// different (reversed) order — a freshly-built vs incrementally-updated index of
	// the SAME state must yield byte-identical reports.
	full, _ := buildConflictStore(t, nodes, edges)
	revNodes := make([]cfNode, len(nodes))
	for i := range nodes {
		revNodes[len(nodes)-1-i] = nodes[i]
	}
	incr, _ := buildConflictStore(t, revNodes, edges)

	bFull, _ := runConflicts(t, full, prs)
	bIncr, _ := runConflicts(t, incr, prs)
	if !bytes.Equal(bFull, bIncr) {
		t.Fatalf("full vs incremental not byte-identical:\nfull: %s\nincr: %s", bFull, bIncr)
	}
}

// --- AC-5: zero engine egress (static import scan) -------------------------

func TestConflicts_ZeroEgressImports(t *testing.T) {
	src, err := os.ReadFile("conflicts.go")
	if err != nil {
		t.Fatalf("read conflicts.go: %v", err)
	}
	for _, banned := range []string{`"net"`, `"net/http"`, `"os"`, `"os/exec"`, `"net/url"`} {
		if bytes.Contains(src, []byte(banned)) {
			t.Fatalf("engine conflicts analyzer must not import %s (zero engine egress)", banned)
		}
	}
	// Also reject any forge client import (egress stays at the surface boundary).
	if strings.Contains(string(src), "surfaces/forge") {
		t.Fatalf("engine conflicts analyzer must not import the forge client")
	}
}

// multiKindFixture builds a graph + PR set exercising several kinds in one pair
// plus a contract-dependency pair, so determinism covers multi-kind / multi-entity
// within-pair ordering.
func multiKindFixture(t *testing.T) (*graphstore.MemStore, []ConflictPRInput) {
	t.Helper()
	store, _ := buildConflictStore(t, []cfNode{
		{name: "Foo", kind: "function", path: "shared.go", line: 1},
		{name: "T", kind: "type", path: "t.go"},
		{name: "UseT", kind: "function", path: "use.go"},
	}, [][3]string{
		{"UseT", "T", string(query.EdgeKindReferences)},
	})
	prs := []ConflictPRInput{
		// PRs 1 & 2 collide on shared.go (file + lines + symbol Foo).
		{Number: 1, ChangedFiles: []string{"shared.go"}, Diff: "shared.go#L10"},
		{Number: 2, ChangedFiles: []string{"shared.go"}, Diff: "shared.go#L11"},
		// PR 3 mutates contract T; PR 2 also touches use.go's dependent? No — make
		// PR 4 depend on T so (3,4) is a contract-dependency pair.
		{Number: 3, ChangedFiles: []string{"t.go"}},
		{Number: 4, ChangedFiles: []string{"use.go"}},
	}
	return store, prs
}
