package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/query"
)

// --- helpers ---------------------------------------------------------------

// runCritique dispatches the critique-review analyzer over a store with a touched
// set (diff) and an existing review, returning the canonical bytes + the report.
func runCritique(t *testing.T, store *graphstore.MemStore, diff string, review ReviewInput) ([]byte, CritiqueReport) {
	t.Helper()
	svc := NewDefaultService(store)
	res, err := svc.Dispatch(context.Background(), CritiqueReviewAnalyzerName, Params{Diff: diff, Review: &review})
	if err != nil {
		t.Fatalf("dispatch critique-review: %v", err)
	}
	b, err := Marshal(res)
	if err != nil {
		t.Fatalf("marshal critique: %v", err)
	}
	if res.Critique == nil {
		t.Fatalf("critique report nil")
	}
	return b, *res.Critique
}

func itemsOfType(rep CritiqueReport, typ string) []CritiqueItem {
	var out []CritiqueItem
	for _, it := range rep.Items {
		if it.Type == typ {
			out = append(out, it)
		}
	}
	return out
}

// starFixture builds a hub-and-leaves graph: `count` leaves each CALL Hub, so Hub's
// blast radius (dependents) and degree-centrality are high. Returns store + ids.
func starFixture(t *testing.T, count int) (*graphstore.MemStore, map[string]string) {
	t.Helper()
	nodes := []cfNode{{name: "Hub", kind: "function", path: "hub.go"}}
	var edges [][3]string
	leaves := make([]string, 0, count)
	for i := 0; i < count; i++ {
		name := "L" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		nodes = append(nodes, cfNode{name: name, kind: "function", path: name + ".go"})
		edges = append(edges, [3]string{name, "Hub", string(query.EdgeKindCalls)})
		leaves = append(leaves, name)
	}
	store, ids := buildConflictStore(t, nodes, edges)
	out := map[string]string{"Hub": string(ids["Hub"])}
	for _, l := range leaves {
		out[l] = string(ids[l])
	}
	return store, out
}

// --- AC-1: gap emission with blast/centrality/edge evidence ----------------

func TestCritique_GapEmitsHighRiskTouchedEntity(t *testing.T) {
	store, ids := starFixture(t, 20)
	// PR touches Hub; the review has NO comment anchoring to Hub → gap.
	_, rep := runCritique(t, store, "hub.go:Hub", ReviewInput{Verdict: "APPROVED"})

	gaps := itemsOfType(rep, CritiqueGap)
	if len(gaps) != 1 {
		t.Fatalf("expected exactly one gap; got %+v", rep.Items)
	}
	g := gaps[0]
	if g.Entity != ids["Hub"] {
		t.Fatalf("gap must name Hub %q; got %q", ids["Hub"], g.Entity)
	}
	if g.BlastRadius != 20 {
		t.Errorf("gap blast radius should be 20 (dependents); got %d", g.BlastRadius)
	}
	if g.Centrality <= 0 {
		t.Errorf("gap should carry a positive centrality bucket; got %d", g.Centrality)
	}
	if len(g.EdgeKinds) == 0 {
		t.Errorf("gap must carry contributing edge kinds; got none")
	}
	foundCalls := false
	for _, k := range g.EdgeKinds {
		if k == string(query.EdgeKindCalls) {
			foundCalls = true
		}
	}
	if !foundCalls {
		t.Errorf("gap edge kinds should include calls; got %v", g.EdgeKinds)
	}
	scoreUnits, ok := parseFixed(g.Score)
	if !ok || scoreUnits <= rep.HighRiskGate {
		t.Errorf("gap score %q must exceed the high-risk gate %d", g.Score, rep.HighRiskGate)
	}
}

// --- AC-2: over_flag links the review-comment anchor to low-risk evidence --

func TestCritique_OverFlagForLowRiskAnchoredLeaf(t *testing.T) {
	// A single leaf calling nothing: blast 0, low centrality → below the gate.
	store, ids := buildConflictStore(t, []cfNode{
		{name: "Leaf", kind: "function", path: "leaf.go"},
		{name: "Other", kind: "function", path: "other.go"},
	}, [][3]string{{"Leaf", "Other", string(query.EdgeKindCalls)}})

	review := ReviewInput{Comments: []ReviewComment{
		{ID: "c1", Path: "leaf.go", Symbol: "Leaf"},
	}}
	_, rep := runCritique(t, store, "leaf.go:Leaf", review)

	ofs := itemsOfType(rep, CritiqueOverFlag)
	if len(ofs) != 1 {
		t.Fatalf("expected one over_flag; got items %+v", rep.Items)
	}
	o := ofs[0]
	if o.Entity != string(ids["Leaf"]) {
		t.Errorf("over_flag must name the flagged Leaf; got %q", o.Entity)
	}
	if o.ReviewAnchor != "c1" {
		t.Errorf("over_flag must link the review-comment anchor c1; got %q", o.ReviewAnchor)
	}
	scoreUnits, ok := parseFixed(o.Score)
	if !ok || scoreUnits > rep.HighRiskGate {
		t.Errorf("over_flag score %q must be at/below the gate %d (low-risk evidence)", o.Score, rep.HighRiskGate)
	}
	if len(itemsOfType(rep, CritiqueGap)) != 0 {
		t.Errorf("a low-risk leaf must not also produce a gap: %+v", rep.Items)
	}
}

// --- AC-3: unsupported_claim cites the absent edge --------------------------

func TestCritique_UnsupportedClaimForAbsentEdge(t *testing.T) {
	// A references Y (edge exists); X is unrelated (no edge A–X).
	store, ids := buildConflictStore(t, []cfNode{
		{name: "A", kind: "function", path: "a.go"},
		{name: "X", kind: "function", path: "x.go"},
		{name: "Y", kind: "function", path: "y.go"},
	}, [][3]string{{"A", "Y", string(query.EdgeKindReferences)}})

	review := ReviewInput{Comments: []ReviewComment{
		// claims impact on X (no connecting edge) AND on Y (edge exists).
		{ID: "c1", Path: "a.go", Symbol: "A", ClaimTargets: []ClaimRef{
			{Path: "x.go", Symbol: "X"},
			{Path: "y.go", Symbol: "Y"},
		}},
	}}
	_, rep := runCritique(t, store, "a.go:A", review)

	uns := itemsOfType(rep, CritiqueUnsupportedClaim)
	if len(uns) != 1 {
		t.Fatalf("expected exactly one unsupported_claim (X only; Y is supported); got %+v", uns)
	}
	u := uns[0]
	if u.Entity != string(ids["A"]) {
		t.Errorf("unsupported_claim entity must be A; got %q", u.Entity)
	}
	if u.ClaimTarget != string(ids["X"]) {
		t.Errorf("unsupported_claim target must be X (the absent edge); got %q", u.ClaimTarget)
	}
	if !u.AbsentEdge {
		t.Errorf("unsupported_claim must flag the absent edge")
	}
	if u.ReviewAnchor != "c1" {
		t.Errorf("unsupported_claim must cite the review-comment anchor c1; got %q", u.ReviewAnchor)
	}
}

// --- AC-3 support: unanchored degradation (never guessed) -------------------

func TestCritique_UnanchoredDegradation(t *testing.T) {
	store, _ := buildConflictStore(t, []cfNode{
		{name: "A", kind: "function", path: "a.go"},
	}, nil)

	review := ReviewInput{Comments: []ReviewComment{
		// vague/prose-only anchor — resolves to nothing.
		{ID: "c1", Path: "does-not-exist.go", Symbol: "Ghost"},
		// resolvable anchor, but a prose-only claim target that resolves to nothing.
		{ID: "c2", Path: "a.go", Symbol: "A", ClaimTargets: []ClaimRef{{Path: "phantom.go", Symbol: "Nope"}}},
	}}
	_, rep := runCritique(t, store, "a.go:A", review)

	if rep.Unanchored != 1 {
		t.Errorf("expected 1 unanchored comment (Ghost); got %d", rep.Unanchored)
	}
	if rep.UnanchoredClaims != 1 {
		t.Errorf("expected 1 unanchored claim target (Nope); got %d", rep.UnanchoredClaims)
	}
	// The unresolvable refs must NEVER be guessed into an item.
	for _, it := range rep.Items {
		if it.Type == CritiqueUnsupportedClaim {
			t.Fatalf("a prose-only claim target must not produce an unsupported_claim: %+v", it)
		}
		if it.ReviewAnchor == "c1" {
			t.Fatalf("an unanchored comment must not produce any item: %+v", it)
		}
	}
}

// --- AC-4: structured typed records, NO LLM prose ---------------------------

func TestCritique_StructuredNoProse(t *testing.T) {
	store, _ := starFixture(t, 20)
	b, _ := runCritique(t, store, "hub.go:Hub", ReviewInput{})

	// Decode generically and assert the envelope + items carry ONLY known,
	// machine-readable, typed fields — no free-text/verdict-synthesis field.
	var env map[string]json.RawMessage
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("decode critique envelope: %v", err)
	}
	allowedEnvelope := map[string]struct{}{
		"schema_version": {}, "analyzer_version": {}, "identity_schema_version": {},
		"high_risk_gate": {}, "verdict": {}, "outcome": {}, "unanchored": {},
		"unanchored_claims": {}, "items": {},
	}
	for k := range env {
		if _, ok := allowedEnvelope[k]; !ok {
			t.Errorf("unexpected envelope field %q (possible prose leakage)", k)
		}
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(env["items"], &items); err != nil {
		t.Fatalf("decode items: %v", err)
	}
	allowedItem := map[string]struct{}{
		"type": {}, "entity": {}, "review_anchor": {}, "score": {}, "blast_radius": {},
		"blast_bucket": {}, "centrality": {}, "edge_kinds": {}, "taint": {},
		"claim_target": {}, "absent_edge": {},
	}
	if len(items) == 0 {
		t.Fatalf("expected at least one item")
	}
	for _, it := range items {
		for k := range it {
			if _, ok := allowedItem[k]; !ok {
				t.Errorf("unexpected item field %q (possible prose leakage)", k)
			}
		}
		if _, ok := it["type"]; !ok {
			t.Errorf("item missing the required typed `type` field")
		}
		if _, ok := it["entity"]; !ok {
			t.Errorf("item missing the required `entity` evidence field")
		}
	}
}

// --- AC-5: determinism — repeat-run byte-identical + stable ordering --------

func TestCritique_DeterminismRepeatRuns(t *testing.T) {
	store, review := multiTypeFixture(t)
	const diff = "hub.go:Hub\nleaf.go:Leaf\na.go:A"
	first, _ := runCritique(t, store, diff, review)
	for i := 0; i < 8; i++ {
		got, _ := runCritique(t, store, diff, review)
		if !bytes.Equal(first, got) {
			t.Fatalf("run %d not byte-identical:\nfirst: %s\ngot:   %s", i, first, got)
		}
	}
	// Items must be in (typeRank, entity, anchor) ascending order.
	_, rep := runCritique(t, store, diff, review)
	for i := 1; i < len(rep.Items); i++ {
		a, b := rep.Items[i-1], rep.Items[i]
		if critiqueTypeRank(a.Type) > critiqueTypeRank(b.Type) {
			t.Fatalf("items not grouped by type order: %+v", rep.Items)
		}
		if critiqueTypeRank(a.Type) == critiqueTypeRank(b.Type) {
			if a.Entity > b.Entity || (a.Entity == b.Entity && a.ReviewAnchor > b.ReviewAnchor) {
				t.Fatalf("items not in (entity, anchor) order within type: %+v", rep.Items)
			}
		}
	}
}

// --- AC-6: byte-identical full vs incremental index -------------------------

func TestCritique_FullVsIncrementalByteIdentical(t *testing.T) {
	nodes := []cfNode{
		{name: "Hub", kind: "function", path: "hub.go"},
		{name: "A", kind: "function", path: "a.go"},
		{name: "X", kind: "function", path: "x.go"},
	}
	var edges [][3]string
	for i := 0; i < 18; i++ {
		nm := "C" + string(rune('a'+i))
		nodes = append(nodes, cfNode{name: nm, kind: "function", path: nm + ".go"})
		edges = append(edges, [3]string{nm, "Hub", string(query.EdgeKindCalls)})
	}
	review := ReviewInput{Comments: []ReviewComment{
		{ID: "c1", Path: "a.go", Symbol: "A", ClaimTargets: []ClaimRef{{Path: "x.go", Symbol: "X"}}},
	}}
	const diff = "hub.go:Hub\na.go:A"

	full, _ := buildConflictStore(t, nodes, edges)
	rev := make([]cfNode, len(nodes))
	for i := range nodes {
		rev[len(nodes)-1-i] = nodes[i]
	}
	incr, _ := buildConflictStore(t, rev, edges)

	bFull, _ := runCritique(t, full, diff, review)
	bIncr, _ := runCritique(t, incr, diff, review)
	if !bytes.Equal(bFull, bIncr) {
		t.Fatalf("full vs incremental not byte-identical:\nfull: %s\nincr: %s", bFull, bIncr)
	}
}

// --- AC-7: zero engine egress (static import scan) --------------------------

func TestCritique_ZeroEgressImports(t *testing.T) {
	src, err := os.ReadFile("critique_review.go")
	if err != nil {
		t.Fatalf("read critique_review.go: %v", err)
	}
	for _, banned := range []string{`"net"`, `"net/http"`, `"os"`, `"os/exec"`, `"net/url"`} {
		if bytes.Contains(src, []byte(banned)) {
			t.Fatalf("engine critique-review analyzer must not import %s (zero engine egress)", banned)
		}
	}
	if strings.Contains(string(src), "surfaces/forge") {
		t.Fatalf("engine critique-review analyzer must not import the forge client (egress stays at the surface)")
	}
}

// multiTypeFixture builds one store + review exercising all three item types so
// determinism covers multi-type / multi-entity ordering: a high-risk Hub (gap),
// a flagged low-risk Leaf (over_flag), and a comment on A claiming unrelated X
// (unsupported_claim).
func multiTypeFixture(t *testing.T) (*graphstore.MemStore, ReviewInput) {
	t.Helper()
	nodes := []cfNode{
		{name: "Hub", kind: "function", path: "hub.go"},
		{name: "Leaf", kind: "function", path: "leaf.go"},
		{name: "A", kind: "function", path: "a.go"},
		{name: "X", kind: "function", path: "x.go"},
	}
	edges := [][3]string{{"Leaf", "Other", string(query.EdgeKindCalls)}}
	for i := 0; i < 20; i++ {
		nm := "D" + string(rune('a'+i))
		nodes = append(nodes, cfNode{name: nm, kind: "function", path: nm + ".go"})
		edges = append(edges, [3]string{nm, "Hub", string(query.EdgeKindCalls)})
	}
	nodes = append(nodes, cfNode{name: "Other", kind: "function", path: "other.go"})
	store, _ := buildConflictStore(t, nodes, edges)
	review := ReviewInput{
		Verdict: "CHANGES_REQUESTED",
		Comments: []ReviewComment{
			{ID: "c-leaf", Path: "leaf.go", Symbol: "Leaf"},
			{ID: "c-a", Path: "a.go", Symbol: "A", ClaimTargets: []ClaimRef{{Path: "x.go", Symbol: "X"}}},
		},
	}
	return store, review
}
