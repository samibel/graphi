package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// TriageAnalyzerName is the dispatch key for the multi-PR triage ranker (EP-018
// story 1/4, SW-105). It is a composite, read-only, DETERMINISTIC batch driver
// over the EP-007 per-PR kernel: it consumes an already-enumerated open-PR set
// (handed in via Params.PRs by the surface-boundary forge client — the engine
// itself NEVER touches the network) and computes a ranked risk/priority ordering
// in a SINGLE pass over the local graph. It reuses prisk.scoreRegion as the
// per-PR scoring kernel rather than re-implementing per-region analysis, and folds
// five signals into one fixed-integer composite score.
const TriageAnalyzerName = "triage-prs"

// TriageSchemaVersion versions the TriageReport JSON shape.
const TriageSchemaVersion = 1

// TriageAnalyzerVersion identifies the ranking LOGIC version, echoed in every
// report so a stored/audited ranking can be tied to the algorithm that produced it.
const TriageAnalyzerVersion = "triage-prs/1"

// triageWeights is the DOCUMENTED, fixed-by-default integer weight model for the
// triage-level signals that ride ON TOP of the EP-007 RiskRecord score. Integer
// only (no floats → no non-associativity / nondeterminism). It is hashed into
// weights_hash so any change is auditable and reproducible (mirrors the EP-007
// weightTable.hash discipline).
//
//	composite = riskBase (EP-007 worst-region score: blast+centrality+taint)
//	          + Ownership*ownershipBucket
//	          + Churn*churnBucket
//	          − TestCoverage*testCoverageBucket
//
// Higher composite = higher triage priority. Test coverage REDUCES priority
// (well-tested touched code is lower-risk). All terms are integer buckets.
type triageWeights struct {
	Ownership    int `json:"ownership"`
	Churn        int `json:"churn"`
	TestCoverage int `json:"test_coverage"`
}

// defaultTriageWeights is the fixed weight model for TriageAnalyzerVersion
// "triage-prs/1". The EP-007 riskBase dominates (0..980); the triage-level terms
// are deliberately smaller, documented adjustments.
var defaultTriageWeights = triageWeights{
	Ownership:    50,
	Churn:        40,
	TestCoverage: 30,
}

func (w triageWeights) hash() string {
	b, _ := json.Marshal(w)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// TriagePRInput is the engine-side input for one enumerated PR. The surface
// maps the forge-sourced metadata onto this type and hands the set in via
// Params.PRs; the engine never fetches it. Changed-file paths are resolved to
// graph nodes via model.NormalizePath so the touched-node set is machine- and
// index-path-independent (full vs incremental → identical).
type TriagePRInput struct {
	Number       int      `json:"number"`
	Title        string   `json:"title"`
	Author       string   `json:"author"`
	BaseRef      string   `json:"base_ref"`
	HeadRef      string   `json:"head_ref"`
	HeadSHA      string   `json:"head_sha"`
	ChangedFiles []string `json:"changed_files"`
	Additions    int      `json:"additions"`
	Deletions    int      `json:"deletions"`
	Mergeable    string   `json:"mergeable"`
}

// TriageSignalBreakdown is the per-PR contribution of each of the five signals,
// emitted alongside the composite so the ranking is auditable. BlastRadius and
// Centrality are the EP-007 sub-signals already folded into RiskBase (reported
// here for transparency); Ownership, Churn and TestCoverage are the triage-level
// integer buckets the composite adds/subtracts.
type TriageSignalBreakdown struct {
	BlastRadius  int `json:"blast_radius"`  // max EP-007 blast-radius bucket over touched nodes
	Centrality   int `json:"centrality"`    // max EP-007 centrality bucket over touched nodes
	Ownership    int `json:"ownership"`     // ownership-concentration bucket (bridge + author)
	Churn        int `json:"churn"`         // recent-change-density bucket over touched files
	TestCoverage int `json:"test_coverage"` // test-reachability bucket (fraction of touched nodes reachable from tests)
	RiskBase     int `json:"risk_base"`     // EP-007 worst-region score in fixed-point units (1/1000)
}

// TriagePRScore is one ranked PR: its identity + metadata, its composite score,
// the contributing signal breakdown, and the bounded touched-node provenance.
type TriagePRScore struct {
	Number       int                   `json:"number"`
	Composite    int                   `json:"composite"`
	Title        string                `json:"title"`
	Author       string                `json:"author"`
	HeadSHA      string                `json:"head_sha"`
	Mergeable    string                `json:"mergeable,omitempty"`
	Signals      TriageSignalBreakdown `json:"signals"`
	TouchedNodes []string              `json:"touched_nodes"`
	Unresolved   []string              `json:"unresolved,omitempty"`
}

// TriageReport is the full versioned payload emitted over every surface. PRs are
// emitted in a TOTAL ORDER — composite score DESC, then PR number ASC — so
// equal-scored PRs retain a fixed position and identical graph + PR set yield
// byte-identical output.
type TriageReport struct {
	SchemaVersion         int             `json:"schema_version"`
	AnalyzerVersion       string          `json:"analyzer_version"`
	WeightsHash           string          `json:"weights_hash"`
	IdentitySchemaVersion uint32          `json:"identity_schema_version"`
	Outcome               string          `json:"outcome"` // found | empty
	PRs                   []TriagePRScore `json:"prs"`
}

// triageProvider is the precomputed signalProvider the reused prisk.scoreRegion
// kernel reads through. Impact and Taint are computed ONCE over the union of all
// touched nodes and indexed; scoreRegion then indexes into these shared maps
// rather than re-traversing per region (the single-pass guarantee).
type triageProvider struct {
	impactByNode map[model.NodeId]Analysis
	taintRes     taint.TaintResult
}

func (p *triageProvider) Impact(_ context.Context, _ query.Reader, region model.NodeId) (Analysis, error) {
	if a, ok := p.impactByNode[region]; ok {
		return a, nil
	}
	// A touched node with no precomputed reach is an empty (no-blast) region.
	return Analysis{Analyzer: "impact", Outcome: query.OutcomeEmpty, Symbol: region, Nodes: []ReachedNode{}}, nil
}

func (p *triageProvider) Taint(_ context.Context, _ query.Reader) (taint.TaintResult, error) {
	return p.taintRes, nil
}

// triageAnalyzer is the registered multi-PR triage ranker. It reuses the EP-007
// scoring kernel (prisk.scoreRegion) and graph primitives (metrics, impact,
// churn) and adds the batch driver + composite ranking + the new
// test-coverage-of-touched-code signal. It is stateless per call and performs
// ZERO outbound network activity (pure graph reads over the read-only Reader;
// PR enumeration egress stays at the surface boundary).
type triageAnalyzer struct {
	source  signalSource
	taintP  signalProvider
	weights triageWeights
}

// newTriageAnalyzer builds the production ranker wired to the real signal sources
// (metrics + churn via signalSource; taint via the prisk default provider).
func newTriageAnalyzer() triageAnalyzer {
	return triageAnalyzer{
		source:  newDefaultSignalSource(),
		taintP:  newDefaultSignalProvider(),
		weights: defaultTriageWeights,
	}
}

func (triageAnalyzer) Name() string { return TriageAnalyzerName }

// Analyze ranks the enumerated PR set (Params.PRs) and returns a versioned
// TriageReport on the generic Analysis envelope. It never fetches anything.
func (a triageAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	src := a.source
	if src == nil {
		src = newDefaultSignalSource()
	}
	tp := a.taintP
	if tp == nil {
		tp = newDefaultSignalProvider()
	}
	weights := a.weights
	if weights == (triageWeights{}) {
		weights = defaultTriageWeights
	}

	report, err := a.rank(ctx, r, p, src, tp, weights)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer: TriageAnalyzerName,
		Outcome:  query.Outcome(report.Outcome),
		Symbol:   p.Symbol,
		Triage:   &report,
	}, nil
}

// rank is the single-pass core. It precomputes the shared graph maps ONCE over the
// union of all PRs' touched nodes, then scores each PR by indexing into them.
func (a triageAnalyzer) rank(ctx context.Context, r query.Reader, p Params, src signalSource, tp signalProvider, weights triageWeights) (TriageReport, error) {
	report := TriageReport{
		SchemaVersion:         TriageSchemaVersion,
		AnalyzerVersion:       TriageAnalyzerVersion,
		WeightsHash:           weights.hash(),
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		PRs:                   []TriagePRScore{},
	}
	if len(p.PRs) == 0 {
		return report, nil
	}

	// (1) Enumerate nodes ONCE; group node ids by normalized source path.
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return TriageReport{}, err
	}
	nodeOf := make(map[model.NodeId]query.ResultNode, len(nodes))
	nodesByPath := make(map[string][]model.NodeId)
	for _, n := range nodes {
		nodeOf[n.ID()] = nodeToResult(n)
		path := model.NormalizePath(n.SourcePath())
		nodesByPath[path] = append(nodesByPath[path], n.ID())
	}
	for path := range nodesByPath {
		ids := nodesByPath[path]
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		nodesByPath[path] = ids
	}

	// (2) Resolve each PR's touched-node set; collect the UNION of touched nodes.
	type resolvedPR struct {
		in         TriagePRInput
		touched    []model.NodeId
		unresolved []string
		files      []string
	}
	resolved := make([]resolvedPR, 0, len(p.PRs))
	union := map[model.NodeId]struct{}{}
	for _, pr := range p.PRs {
		seen := map[model.NodeId]struct{}{}
		var touched []model.NodeId
		var unresolved []string
		files := make([]string, 0, len(pr.ChangedFiles))
		for _, f := range pr.ChangedFiles {
			path := model.NormalizePath(strings.TrimSpace(f))
			if path == "" {
				continue
			}
			files = append(files, path)
			ids, ok := nodesByPath[path]
			if !ok || len(ids) == 0 {
				unresolved = append(unresolved, path)
				continue
			}
			for _, id := range ids {
				if _, dup := seen[id]; dup {
					continue
				}
				seen[id] = struct{}{}
				touched = append(touched, id)
				union[id] = struct{}{}
			}
		}
		sort.Slice(touched, func(i, j int) bool { return touched[i] < touched[j] })
		sort.Strings(unresolved)
		sort.Strings(files)
		resolved = append(resolved, resolvedPR{in: pr, touched: touched, unresolved: dedupeStr(unresolved), files: dedupeStr(files)})
	}

	// (3) Precompute the shared graph maps ONCE over the union of touched nodes.
	metricScores, err := src.Metrics(ctx, r)
	if err != nil {
		return TriageReport{}, err
	}
	_, bridgeByNode := indexMetrics(metricScores)

	taintRes, err := tp.Taint(ctx, r)
	if err != nil {
		return TriageReport{}, err
	}
	taintByNode := indexTaint(taintRes)

	churn, err := src.Churn(ctx, r)
	if err != nil {
		return TriageReport{}, err
	}
	churnByPath := indexChurn(churn)
	authorByPath := indexChurnAuthor(churn)

	// Reverse-dependency adjacency (forward-impact / blast radius) built ONCE.
	impactAdj, err := buildImpactAdjacency(ctx, r)
	if err != nil {
		return TriageReport{}, err
	}
	unionIDs := make([]model.NodeId, 0, len(union))
	for id := range union {
		unionIDs = append(unionIDs, id)
	}
	sort.Slice(unionIDs, func(i, j int) bool { return unionIDs[i] < unionIDs[j] })
	impactByNode := make(map[model.NodeId]Analysis, len(unionIDs))
	for _, id := range unionIDs {
		impactByNode[id] = bfsImpact(impactAdj, id, nodeOf, metricScores)
	}

	// Test-coverage-of-touched-code: the set of nodes reachable FROM test nodes,
	// computed ONCE over the whole graph (a pure local-graph read, zero egress).
	reachedFromTest := computeTestReachability(ctx, r, nodeOf)

	prov := &triageProvider{impactByNode: impactByNode, taintRes: taintRes}
	kernel := priskAnalyzer{} // reused EP-007 per-PR scoring kernel

	// (4) Score each PR against the shared precomputed maps.
	scores := make([]TriagePRScore, 0, len(resolved))
	for _, rp := range resolved {
		var riskBase, blastBucket, centBucket int
		for _, id := range rp.touched {
			rec, err := kernel.scoreRegion(ctx, r, id, prov, defaultWeights, taintByNode, ProvenanceFull)
			if err != nil {
				return TriageReport{}, err
			}
			if units, ok := parseFixed(rec.Score); ok && units > riskBase {
				riskBase = units
			}
			imp := impactByNode[id]
			if b := blastRadiusBucket(len(imp.Nodes), defaultWeights.MaxBucket); b > blastBucket {
				blastBucket = b
			}
			if c := centralityBucket(metricScores, id); c > centBucket {
				centBucket = c
			}
		}
		ownBucket := ownershipBucket(rp.touched, bridgeByNode, rp.files, authorByPath)
		churnBkt := churnBucket(rp.files, churnByPath)
		covBucket := testCoverageBucket(rp.touched, reachedFromTest)

		composite := riskBase +
			weights.Ownership*ownBucket +
			weights.Churn*churnBkt -
			weights.TestCoverage*covBucket

		scores = append(scores, TriagePRScore{
			Number:    rp.in.Number,
			Composite: composite,
			Title:     rp.in.Title,
			Author:    rp.in.Author,
			HeadSHA:   rp.in.HeadSHA,
			Mergeable: rp.in.Mergeable,
			Signals: TriageSignalBreakdown{
				BlastRadius:  blastBucket,
				Centrality:   centBucket,
				Ownership:    ownBucket,
				Churn:        churnBkt,
				TestCoverage: covBucket,
				RiskBase:     riskBase,
			},
			TouchedNodes: boundedIDs(rp.touched),
			Unresolved:   rp.unresolved,
		})
	}

	sortTriagePRs(scores)
	report.PRs = scores
	if len(scores) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// impactNbr is one reverse-dependency adjacency neighbor (mirrors impact.go).
type impactNbr struct {
	id  model.NodeId
	via query.ResultEdge
}

// buildImpactAdjacency builds the forward-impact (blast-radius) adjacency ONCE:
// for each dependency edge (From → To), To's dependents include From (everything
// pointing AT To). This mirrors impactAnalyzer's Forward adjacency so the reused
// kernel sees the same blast-radius semantics, but is built a single time and
// shared across every PR (the single-pass guarantee).
func buildImpactAdjacency(ctx context.Context, r query.Reader) (map[model.NodeId][]impactNbr, error) {
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, len(dependencyKinds))
	for _, k := range dependencyKinds {
		want[k] = struct{}{}
	}
	adj := make(map[model.NodeId][]impactNbr)
	for _, e := range edges {
		if _, ok := want[e.Kind()]; !ok {
			continue
		}
		adj[e.To()] = append(adj[e.To()], impactNbr{id: e.From(), via: edgeToResult(e)})
	}
	return adj, nil
}

// bfsImpact runs the cycle-guarded bounded blast-radius BFS over the prebuilt
// adjacency, producing the same ReachedNode shape the impact analyzer emits (so
// the reused scoreRegion kernel reads identical evidence). The metrics slice is
// attached verbatim so scoreRegion's centralityBucket lookup works unchanged.
func bfsImpact(adj map[model.NodeId][]impactNbr, seed model.NodeId, nodeOf map[model.NodeId]query.ResultNode, metrics []NodeScore) Analysis {
	reached := make(map[model.NodeId]ReachedNode)
	depth := map[model.NodeId]int{seed: 0}
	expanded := map[model.NodeId]struct{}{}
	frontier := []model.NodeId{seed}
	for len(frontier) > 0 {
		var next []model.NodeId
		for _, cur := range frontier {
			if _, done := expanded[cur]; done {
				continue
			}
			expanded[cur] = struct{}{}
			for _, nb := range adj[cur] {
				if nb.id == seed {
					continue
				}
				if existing, seen := reached[nb.id]; seen {
					if edgeBetter(nb.via, existing.ReachedVia) {
						existing.ReachedVia = nb.via
						reached[nb.id] = existing
					}
					continue
				}
				n, ok := nodeOf[nb.id]
				if !ok {
					continue // referential drift: endpoint no longer exists
				}
				reached[nb.id] = ReachedNode{Node: n, ReachedVia: nb.via, Depth: depth[cur] + 1}
				depth[nb.id] = depth[cur] + 1
				next = append(next, nb.id)
			}
		}
		frontier = next
	}
	outcome := query.OutcomeFound
	if len(reached) == 0 {
		outcome = query.OutcomeEmpty
	}
	out := make([]ReachedNode, 0, len(reached))
	for _, rn := range reached {
		out = append(out, rn)
	}
	sortReached(out)
	truncated := false
	if len(out) > DefaultMaxNodes {
		out = out[:DefaultMaxNodes]
		truncated = true
	}
	return Analysis{Analyzer: "impact", Outcome: outcome, Symbol: seed, Truncated: truncated, Nodes: out, Metrics: metrics}
}

// testNodePathPatterns is the DOCUMENTED, deterministic, pure-string heuristic for
// classifying a node as a test node by its source path. It is conservative and
// language-agnostic (Go _test.go, JS/TS .test./.spec., Python test_/_test, and the
// common tests/spec directories). A node whose kind contains "test" also counts.
var testNodePathPatterns = []string{
	"_test.",
	".test.",
	".spec.",
	"_spec.",
	"test_",
	"/tests/",
	"/test/",
	"/spec/",
	"/__tests__/",
}

// isTestNode reports whether a node is a test node by the documented heuristic.
func isTestNode(n query.ResultNode) bool {
	if strings.Contains(strings.ToLower(n.Kind), "test") {
		return true
	}
	p := strings.ToLower(model.NormalizePath(n.SourcePath))
	for _, pat := range testNodePathPatterns {
		if strings.Contains(p, pat) {
			return true
		}
	}
	return false
}

// computeTestReachability returns the set of nodes reachable FROM any test node by
// following dependency edges (From → To: a test that calls/references a symbol can
// reach it). This is the local-graph read backing the NEW
// test-coverage-of-touched-code signal: a touched node IN this set is exercised by
// tests. Computed once over the whole graph; per-PR coverage is a membership test.
func computeTestReachability(ctx context.Context, r query.Reader, nodeOf map[model.NodeId]query.ResultNode) map[model.NodeId]struct{} {
	reached := map[model.NodeId]struct{}{}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return reached
	}
	want := make(map[string]struct{}, len(dependencyKinds))
	for _, k := range dependencyKinds {
		want[k] = struct{}{}
	}
	// Forward adjacency: From depends on To, so From can reach To.
	adj := make(map[model.NodeId][]model.NodeId)
	for _, e := range edges {
		if _, ok := want[e.Kind()]; !ok {
			continue
		}
		adj[e.From()] = append(adj[e.From()], e.To())
	}
	// Seed the BFS with every test node (a test node is itself "covered").
	var frontier []model.NodeId
	seedIDs := make([]model.NodeId, 0, len(nodeOf))
	for id := range nodeOf {
		seedIDs = append(seedIDs, id)
	}
	sort.Slice(seedIDs, func(i, j int) bool { return seedIDs[i] < seedIDs[j] })
	for _, id := range seedIDs {
		if isTestNode(nodeOf[id]) {
			reached[id] = struct{}{}
			frontier = append(frontier, id)
		}
	}
	for len(frontier) > 0 {
		var next []model.NodeId
		for _, cur := range frontier {
			for _, to := range adj[cur] {
				if _, seen := reached[to]; seen {
					continue
				}
				reached[to] = struct{}{}
				next = append(next, to)
			}
		}
		frontier = next
	}
	return reached
}

// indexChurnAuthor builds a normalized-path → last-author index from churn scores.
func indexChurnAuthor(scores []githistory.ChurnScore) map[string]string {
	out := make(map[string]string, len(scores))
	for _, c := range scores {
		out[model.NormalizePath(c.Path)] = c.LastAuthor
	}
	return out
}

// ownershipBucket measures ownership/structural concentration over a PR's touched
// code: how many touched nodes are articulation points (bridges between modules —
// concentrated structural ownership) and how concentrated authorship is (few
// distinct recent authors over the touched files = higher bus-factor risk). The
// stronger of the two structural/authorship signals wins; capped at 3.
func ownershipBucket(touched []model.NodeId, bridgeByNode map[model.NodeId]struct{}, files []string, authorByPath map[string]string) int {
	bridges := 0
	for _, id := range touched {
		if _, ok := bridgeByNode[id]; ok {
			bridges++
		}
	}
	bridgeB := 0
	switch {
	case bridges >= 2:
		bridgeB = 3
	case bridges == 1:
		bridgeB = 2
	}

	authors := map[string]struct{}{}
	known := 0
	for _, f := range files {
		if a, ok := authorByPath[f]; ok && a != "" {
			authors[a] = struct{}{}
			known++
		}
	}
	authorB := 0
	if known > 0 {
		switch len(authors) {
		case 1:
			authorB = 3
		case 2:
			authorB = 2
		case 3:
			authorB = 1
		}
	}

	if bridgeB > authorB {
		return bridgeB
	}
	return authorB
}

// churnBucket buckets the aggregate recent change density (total commits over the
// PR's touched files in the churn window). Absent git history, every file is
// unknown and the bucket is 0 (deterministic degrade, never guessed).
func churnBucket(files []string, churnByPath map[string]int) int {
	sum := 0
	for _, f := range files {
		sum += churnByPath[f]
	}
	switch {
	case sum <= 0:
		return 0
	case sum <= 2:
		return 1
	case sum <= 5:
		return 2
	case sum <= 10:
		return 3
	default:
		return 4
	}
}

// testCoverageBucket buckets the fraction of a PR's touched nodes that are
// reachable from test nodes. Higher bucket = better tested = lower triage
// priority (the composite SUBTRACTS this term). A PR touching no resolvable node
// has no measurable coverage and degrades to 0 deterministically.
func testCoverageBucket(touched []model.NodeId, reachedFromTest map[model.NodeId]struct{}) int {
	if len(touched) == 0 {
		return 0
	}
	covered := 0
	for _, id := range touched {
		if _, ok := reachedFromTest[id]; ok {
			covered++
		}
	}
	// Integer fraction in tenths to avoid float non-associativity.
	tenths := covered * 10 / len(touched)
	switch {
	case tenths <= 0:
		return 0
	case tenths < 4:
		return 1
	case tenths < 7:
		return 2
	case tenths < 10:
		return 3
	default:
		return 4
	}
}

// boundedIDs returns up to a bounded number of node ids (already sorted) as
// stable, token-bounded provenance.
func boundedIDs(ids []model.NodeId) []string {
	const maxIDs = 16
	out := make([]string, 0, maxIDs)
	for i, id := range ids {
		if i >= maxIDs {
			break
		}
		out = append(out, string(id))
	}
	return out
}

// dedupeStr removes adjacent duplicates from a sorted string slice and never
// returns nil (so the serialized shape is stable: empty slice, never null).
func dedupeStr(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := s[:1]
	for i := 1; i < len(s); i++ {
		if s[i] != out[len(out)-1] {
			out = append(out, s[i])
		}
	}
	return out
}

// sortTriagePRs enforces the TOTAL ORDER: composite score DESC, then PR number
// ASC. This is the ONLY place PR ordering is decided; combined with MarshalTriage
// it makes the report byte-identical regardless of input order. The total order
// guarantees equal-scored PRs retain a fixed position (the tie-break).
func sortTriagePRs(prs []TriagePRScore) {
	sort.SliceStable(prs, func(i, j int) bool {
		if prs[i].Composite != prs[j].Composite {
			return prs[i].Composite > prs[j].Composite // higher priority first
		}
		return prs[i].Number < prs[j].Number
	})
}

// MarshalTriage is the single canonical serializer for a TriageReport, shared by
// every surface. It re-sorts defensively (total order), disables HTML escaping,
// and trims the trailing newline — byte-for-byte stable across runs and surfaces
// (mirrors MarshalRisk / MarshalSignals / MarshalQuestions). Empty slices are
// materialized (never null) so the shape is stable. No timestamp / wall-clock /
// float / map-iteration leakage.
func MarshalTriage(rep TriageReport) ([]byte, error) {
	out := rep
	prs := make([]TriagePRScore, len(rep.PRs))
	copy(prs, rep.PRs)
	sortTriagePRs(prs)
	for i := range prs {
		if prs[i].TouchedNodes == nil {
			prs[i].TouchedNodes = []string{}
		}
		if prs[i].Unresolved == nil {
			prs[i].Unresolved = []string{}
		}
	}
	if prs == nil {
		prs = []TriagePRScore{}
	}
	out.PRs = prs

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal triage report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
