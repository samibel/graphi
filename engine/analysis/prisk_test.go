package analysis

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// --- test fixtures -----------------------------------------------------------

// mkNode builds a node and stores it, returning its id.
func mkNode(t *testing.T, store *graphstore.MemStore, kind, name, path string, line int) model.NodeId {
	t.Helper()
	n, err := model.NewNode(kind, name, path, line, 1)
	if err != nil {
		t.Fatalf("NewNode(%s): %v", name, err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatalf("PutNode(%s): %v", name, err)
	}
	return n.ID()
}

// stubProvider is the injectable signalProvider seam used to feed FIXED impact
// and taint results to the combiner, so the scorer is tested without re-running
// EP-004/EP-005 (and so "equal impact" is enforced exactly in ranking tests).
type stubProvider struct {
	impact map[model.NodeId]Analysis
	taint  taint.TaintResult
	calls  int
}

func (s *stubProvider) Impact(_ context.Context, _ query.Reader, region model.NodeId) (Analysis, error) {
	s.calls++
	if a, ok := s.impact[region]; ok {
		return a, nil
	}
	return Analysis{Analyzer: "impact", Outcome: query.OutcomeEmpty, Symbol: region}, nil
}

func (s *stubProvider) Taint(_ context.Context, _ query.Reader) (taint.TaintResult, error) {
	return s.taint, nil
}

// impactWith builds an impact Analysis carrying n reached nodes (blast radius)
// at the given reaching-edge tier, optionally truncated.
func impactWith(n int, tier model.ConfidenceTier, truncated bool) Analysis {
	nodes := make([]ReachedNode, 0, n)
	for i := 0; i < n; i++ {
		nodes = append(nodes, ReachedNode{
			Node:       query.ResultNode{ID: model.NodeId(strings.Repeat("0", 15) + string(rune('a'+i%6)))},
			ReachedVia: query.ResultEdge{ID: "e", Tier: tier, Reason: "calls"},
			Depth:      1,
		})
	}
	out := query.OutcomeFound
	if n == 0 {
		out = query.OutcomeEmpty
	}
	return Analysis{Analyzer: "impact", Outcome: out, Nodes: nodes, Truncated: truncated}
}

// --- AC1: golden-fixture determinism -----------------------------------------

func TestPriskGoldenDeterministic(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.A", "pkg/a.go", 10)

	prov := &stubProvider{
		impact: map[model.NodeId]Analysis{idA: impactWith(3, model.TierConfirmed, false)},
	}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}

	diff := "pkg/a.go:A"
	res, err := a.Analyze(context.Background(), store, Params{Diff: diff})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	first, err := Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Stable, known content for a known diff.
	if res.RiskReport == nil || len(res.RiskReport.Regions) != 1 {
		t.Fatalf("want 1 region, got %+v", res.RiskReport)
	}
	rec := res.RiskReport.Regions[0]
	if rec.Region != idA {
		t.Fatalf("region = %s, want %s", rec.Region, idA)
	}
	// bucket(3) = 2 -> 2*100 = 200 -> "0.200" (impact-only, no taint).
	if rec.Score != "0.200" {
		t.Fatalf("score = %s, want 0.200", rec.Score)
	}
	if rec.Degraded {
		t.Fatalf("region unexpectedly degraded")
	}
	var haveBlast bool
	for _, e := range rec.Evidence {
		if e.Kind == EvidenceImpactBlastRadius {
			haveBlast = true
		}
	}
	if !haveBlast {
		t.Fatalf("missing impact-blast-radius evidence: %+v", rec.Evidence)
	}
	if res.RiskReport.ScorerVersion != ScorerVersion {
		t.Fatalf("scorer_version = %s, want %s", res.RiskReport.ScorerVersion, ScorerVersion)
	}
	if res.RiskReport.WeightsHash == "" {
		t.Fatalf("weights_hash empty")
	}

	// Byte-identical on a second run (map-iteration nondeterminism guard).
	for i := 0; i < 25; i++ {
		r2, err := a.Analyze(context.Background(), store, Params{Diff: diff})
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		b2, err := Marshal(r2)
		if err != nil {
			t.Fatalf("iter %d marshal: %v", i, err)
		}
		if !bytes.Equal(first, b2) {
			t.Fatalf("iteration %d not byte-identical (determinism violated)\nfirst=%s\ngot  =%s", i, first, b2)
		}
	}
}

// --- AC2: taint-exposed region ranks strictly higher at equal impact ---------

func TestPriskTaintRanksStrictlyHigher(t *testing.T) {
	store := graphstore.NewMemStore()
	idClean := mkNode(t, store, "function", "pkg.Clean", "pkg/clean.go", 5)
	idTaint := mkNode(t, store, "function", "pkg.Tainted", "pkg/tainted.go", 5)

	// EQUAL impact for both regions (same bucket); the ONLY difference is taint.
	prov := &stubProvider{
		impact: map[model.NodeId]Analysis{
			idClean: impactWith(3, model.TierConfirmed, false),
			idTaint: impactWith(3, model.TierConfirmed, false),
		},
		taint: taint.TaintResult{Findings: []taint.Finding{{
			SourceID:   idTaint,
			SourceName: "userInput",
			SinkID:     idTaint,
			SinkName:   "exec",
			PathLength: 1,
			Path: []taint.PathStep{
				{NodeID: idTaint, Tier: model.TierConfirmed, QualifiedName: "pkg.Tainted"},
			},
		}}},
	}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}

	diff := "pkg/clean.go:Clean\npkg/tainted.go:Tainted"
	res, err := a.Analyze(context.Background(), store, Params{Diff: diff})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	byRegion := map[model.NodeId]RiskRecord{}
	for _, r := range res.RiskReport.Regions {
		byRegion[r.Region] = r
	}
	clean := byRegion[idClean]
	tainted := byRegion[idTaint]

	if !(tainted.Score > clean.Score) {
		t.Fatalf("taint score %s must be strictly > clean score %s (string-comparable fixed-point)", tainted.Score, clean.Score)
	}
	// Evidence must cite the taint signal.
	var citesTaint bool
	for _, e := range tainted.Evidence {
		if e.Kind == EvidenceTaintPath {
			citesTaint = true
		}
	}
	if !citesTaint {
		t.Fatalf("taint region evidence does not cite taint-path: %+v", tainted.Evidence)
	}
	// The clean region must NOT cite taint.
	for _, e := range clean.Evidence {
		if e.Kind == EvidenceTaintPath {
			t.Fatalf("clean region falsely cites taint-path")
		}
	}
}

// --- Refined-AC3: combiner monotonicity + impact-only floor ------------------

func TestPriskCombinerMonotonicAndFloor(t *testing.T) {
	w := defaultWeights
	// Strict tie-break at equal bucket: taint term is strictly positive.
	cleanFloor := impactFloor(2, 0, w)
	taintScore := impactFloor(2, 0, w) + w.TaintTerm
	if !(taintScore > cleanFloor) {
		t.Fatalf("taint term not strictly positive: %d !> %d", taintScore, cleanFloor)
	}
	// Impact-only floor: missing taint never drops below the floor.
	if impactFloor(3, 0, w) < impactFloor(2, 0, w) {
		t.Fatalf("impact floor not monotonic in bucket")
	}
	// Monotonic in bucket.
	prev := -1
	for b := 0; b <= w.MaxBucket; b++ {
		s := impactFloor(b, 0, w)
		if s < prev {
			t.Fatalf("impactFloor not monotonic at bucket %d", b)
		}
		prev = s
	}
	// Max-risk (max bucket + max centrality + taint) stays within [0,1] fixed-point.
	max := impactFloor(w.MaxBucket, 3, w) + w.TaintTerm
	if max > scoreScale {
		t.Fatalf("max-risk score %d overflows scoreScale %d", max, scoreScale)
	}
}

// --- Refined-AC4: truncated impact -> reduced confidence + evidence ----------

func TestPriskTruncatedLowersConfidence(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	prov := &stubProvider{
		impact: map[model.NodeId]Analysis{idA: impactWith(40, model.TierConfirmed, true)},
	}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/a.go:A"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rec := res.RiskReport.Regions[0]
	if rec.Confidence != ConfidenceMedium {
		t.Fatalf("confidence = %s, want %s on truncation", rec.Confidence, ConfidenceMedium)
	}
	var named bool
	for _, e := range rec.Evidence {
		if e.Kind == EvidenceTruncation {
			named = true
		}
	}
	if !named {
		t.Fatalf("truncation not named in evidence: %+v", rec.Evidence)
	}
}

// --- AC3 / Refined-AC7: degraded record for an unresolved node ---------------

func TestPriskDegradedUnresolvedRegion(t *testing.T) {
	store := graphstore.NewMemStore()
	mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	prov := &stubProvider{impact: map[model.NodeId]Analysis{}}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}

	// Diff references a file/symbol absent from the graph.
	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/ghost.go:Ghost"})
	if err != nil {
		t.Fatalf("Analyze must not fail on unresolved node: %v", err)
	}
	if len(res.RiskReport.Regions) != 1 {
		t.Fatalf("want 1 degraded region, got %d", len(res.RiskReport.Regions))
	}
	rec := res.RiskReport.Regions[0]
	if !rec.Degraded {
		t.Fatalf("unresolved region not flagged degraded: %+v", rec)
	}
	if rec.UnresolvedID == "" {
		t.Fatalf("degraded record missing unresolved_id")
	}
}

// --- AC3 / Refined-AC5: offline, zero outbound network (source scan) ---------

// TestPriskNoNetworkSourceScan is the package-local deny-network proxy: the
// pr-risk source files must contain no outbound-network symbols (the whole-binary
// egress canary enforces it at CI). Combined with the degraded test above, this
// proves the offline contract: a diff referencing an absent node completes with
// a flagged degraded record and ZERO network code on the path.
func TestPriskNoNetworkSourceScan(t *testing.T) {
	files := []string{"prisk.go", "prisk_diff.go"}
	banned := []string{"\"net\"", "\"net/http\"", "\"net/url\"", "http.Get", "http.Post", "net.Dial", "exec.Command", "os/exec"}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		s := string(body)
		for _, b := range banned {
			if strings.Contains(s, b) {
				t.Fatalf("%s contains banned symbol %q (local-first / no-exec violation)", f, b)
			}
		}
	}
}

// TestPriskOfflineDegradedThroughDefaultProvider exercises the FULL default
// provider (real impact + taint) under no network, with a diff referencing an
// absent node — it must complete and flag the region degraded.
func TestPriskOfflineDegradedThroughDefaultProvider(t *testing.T) {
	store := graphstore.NewMemStore()
	mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	svc := NewDefaultService(store)

	res, err := svc.Dispatch(context.Background(), PriskAnalyzerName, Params{Diff: "pkg/ghost.go:Ghost"})
	if err != nil {
		t.Fatalf("Dispatch pr-risk: %v", err)
	}
	if res.RiskReport == nil || len(res.RiskReport.Regions) != 1 {
		t.Fatalf("want 1 region, got %+v", res.RiskReport)
	}
	if !res.RiskReport.Regions[0].Degraded {
		t.Fatalf("absent node not flagged degraded via default provider")
	}
}

// --- Refined-AC8 edge cases --------------------------------------------------

func TestPriskEmptyDiff(t *testing.T) {
	store := graphstore.NewMemStore()
	a := priskAnalyzer{provider: &stubProvider{}, weights: defaultWeights}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "   \n  "})
	if err != nil {
		t.Fatalf("Analyze empty diff: %v", err)
	}
	if len(res.RiskReport.Regions) != 0 {
		t.Fatalf("empty diff must yield 0 regions, got %d", len(res.RiskReport.Regions))
	}
	if res.RiskReport.Outcome != string(query.OutcomeEmpty) {
		t.Fatalf("empty diff outcome = %s, want empty", res.RiskReport.Outcome)
	}
}

func TestPriskAllUnresolved(t *testing.T) {
	store := graphstore.NewMemStore()
	a := priskAnalyzer{provider: &stubProvider{}, weights: defaultWeights}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "x/a.go:A\ny/b.go:B"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(res.RiskReport.Regions) != 2 {
		t.Fatalf("want 2 degraded regions, got %d", len(res.RiskReport.Regions))
	}
	for _, r := range res.RiskReport.Regions {
		if !r.Degraded {
			t.Fatalf("region not degraded: %+v", r)
		}
	}
}

func TestPriskDuplicateNodesCollapse(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.A", "pkg/a.go", 10)
	prov := &stubProvider{impact: map[model.NodeId]Analysis{idA: impactWith(3, model.TierConfirmed, false)}}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}

	// Same node referenced three ways (name, line, bare id) collapses to one region.
	diff := "pkg/a.go:A\npkg/a.go#L10\n" + string(idA)
	res, err := a.Analyze(context.Background(), store, Params{Diff: diff})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(res.RiskReport.Regions) != 1 {
		t.Fatalf("duplicate nodes must collapse to 1 region, got %d", len(res.RiskReport.Regions))
	}
}

func TestPriskMaxRiskImpactPlusTaint(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	prov := &stubProvider{
		impact: map[model.NodeId]Analysis{idA: impactWith(100, model.TierConfirmed, false)},
		taint: taint.TaintResult{Findings: []taint.Finding{{
			SourceID: idA, SinkID: idA, PathLength: 1,
			Path: []taint.PathStep{{NodeID: idA, Tier: model.TierConfirmed}},
		}}},
	}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}
	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/a.go:A"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	rec := res.RiskReport.Regions[0]
	// max bucket (6) -> 600 + taint 200 = 800 -> "0.800"; must not overflow.
	if rec.Score != "0.800" {
		t.Fatalf("max-risk score = %s, want 0.800", rec.Score)
	}
}

// --- Refined-AC7: redaction level --------------------------------------------

func TestPriskProvenanceRedaction(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.A", "pkg/a.go", 1)
	prov := &stubProvider{
		impact: map[model.NodeId]Analysis{idA: impactWith(1, model.TierConfirmed, false)},
		taint: taint.TaintResult{Findings: []taint.Finding{{
			SourceID: idA, SourceName: "secretSource", SinkID: idA, SinkName: "secretSink", PathLength: 1,
			Path: []taint.PathStep{{NodeID: idA, Tier: model.TierConfirmed}},
		}}},
	}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}

	// Summary level: taint source/sink names must NOT appear.
	res, err := a.Analyze(context.Background(), store, Params{Diff: "pkg/a.go:A", Provenance: "summary"})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	b, _ := Marshal(res)
	if strings.Contains(string(b), "secretSource") || strings.Contains(string(b), "secretSink") {
		t.Fatalf("summary provenance leaked taint source/sink names: %s", b)
	}
	if res.RiskReport.Regions[0].ProvenanceLevel != ProvenanceSummary {
		t.Fatalf("provenance level not summary")
	}

	// Full level: names ARE present (verbatim provenance).
	resFull, _ := a.Analyze(context.Background(), store, Params{Diff: "pkg/a.go:A"})
	bFull, _ := Marshal(resFull)
	if !strings.Contains(string(bFull), "secretSource") {
		t.Fatalf("full provenance missing verbatim taint source: %s", bFull)
	}
}

// --- Refined-AC5: diff hardening (size bound) --------------------------------

func TestPriskDiffSizeBound(t *testing.T) {
	store := graphstore.NewMemStore()
	a := priskAnalyzer{provider: &stubProvider{}, weights: defaultWeights}
	huge := strings.Repeat("a", MaxDiffBytes+1)
	_, err := a.Analyze(context.Background(), store, Params{Diff: huge})
	if err == nil {
		t.Fatalf("oversized diff must be rejected (untrusted input bound)")
	}
}

// --- Refined-AC1: registered analyzer + Refined-AC6: MCP/CLI parity shape ----

func TestPriskRegisteredInDefaultService(t *testing.T) {
	store := graphstore.NewMemStore()
	svc := NewDefaultService(store)
	var found bool
	for _, n := range svc.Names() {
		if n == PriskAnalyzerName {
			found = true
		}
	}
	if !found {
		t.Fatalf("pr-risk not registered in default service: %v", svc.Names())
	}
}

// TestPriskUnifiedDiffParsing exercises the real unified-diff parser path.
func TestPriskUnifiedDiffParsing(t *testing.T) {
	store := graphstore.NewMemStore()
	idA := mkNode(t, store, "function", "pkg.Foo", "pkg/foo.go", 12)
	prov := &stubProvider{impact: map[model.NodeId]Analysis{idA: impactWith(2, model.TierConfirmed, false)}}
	a := priskAnalyzer{provider: prov, weights: defaultWeights}

	diff := "--- a/pkg/foo.go\n+++ b/pkg/foo.go\n@@ -10,3 +12,4 @@ func Foo() {\n+\tx := 1\n"
	res, err := a.Analyze(context.Background(), store, Params{Diff: diff})
	if err != nil {
		t.Fatalf("Analyze unified diff: %v", err)
	}
	if len(res.RiskReport.Regions) != 1 || res.RiskReport.Regions[0].Region != idA {
		t.Fatalf("unified diff did not resolve to pkg.Foo: %+v", res.RiskReport.Regions)
	}
}

// ensure the testdata dir reference compiles away (kept for parity with the
// other analyzers' inline-fixture convention; no external testdata is used).
var _ = filepath.Join
