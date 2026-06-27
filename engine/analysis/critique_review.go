package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// CritiqueReviewAnalyzerName is the dispatch key for the SW-108 review critique
// (EP-018 story 4/4, the capstone). It is a composite, read-only, DETERMINISTIC
// analyzer that critiques an EXISTING PR review (comments + an overall verdict,
// fetched at the SURFACE boundary or supplied inline and handed in via
// Params.Review) against the local graph. It REPLAYS the EP-007 single-PR
// risk/blast-radius/centrality/taint signals (scoreRegion + the graph primitives)
// as the GROUND-TRUTH oracle and runs a three-way diff of that oracle against the
// supplied review, emitting a STRUCTURED, machine-readable critique — NEVER LLM
// prose. The engine NEVER touches the network: the review fetch is the surface's
// concern; the engine receives the structured ReviewInput as Params (mirroring the
// SW-105/106/107 "structured input as Params" boundary discipline).
const CritiqueReviewAnalyzerName = "critique-review"

// CritiqueSchemaVersion versions the CritiqueReport JSON shape.
const CritiqueSchemaVersion = 1

// CritiqueAnalyzerVersion identifies the critique LOGIC version, echoed in the
// report so a stored/audited critique ties to the algorithm that produced it.
const CritiqueAnalyzerVersion = "critique-review/1"

// Critique item types — a stable, enumerated, versioned vocabulary, grouped in a
// FIXED enumeration order in the serialized output. Consumers switch on these
// rather than string-parse.
const (
	// CritiqueGap: a touched entity ABOVE the EP-007 high-risk gate (large blast
	// radius / high centrality / taint-reachable) that NO review comment anchors to.
	CritiqueGap = "gap"
	// CritiqueOverFlag: a review-anchored entity whose EP-007 signals are BELOW the
	// high-risk gate (low blast radius / leaf / low centrality).
	CritiqueOverFlag = "over_flag"
	// CritiqueUnsupportedClaim: a review comment asserting an impact relation to an
	// ANCHORABLE target X where NO graph edge connects the anchored touched entity
	// to X. A prose-only / unresolvable target is NEVER emitted here — it degrades to
	// the unanchored tally (the anti-fabrication boundary).
	CritiqueUnsupportedClaim = "unsupported_claim"
)

// ClaimRef is a STRUCTURED, anchorable impact-target reference extracted at the
// surface from a review comment's asserted impact ("this breaks X"). The engine
// resolves it deterministically via the EP-007 resolveRef kernel; it is never
// parsed from prose by the engine. An unresolvable ClaimRef degrades to the
// unanchored tally, never a guessed entity.
type ClaimRef struct {
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
	Symbol string `json:"symbol,omitempty"`
	Raw    string `json:"raw,omitempty"`
}

// ReviewComment is one comment from the existing review, with its deterministic
// anchor ({path,line,symbol}) and the structured impact-target refs it asserts.
// ID is the stable per-comment identity (e.g. the forge comment id) used as the
// review-anchor sort key. The engine resolves the anchor via resolveRef; an
// unresolvable anchor is counted in the unanchored tally, never guessed.
type ReviewComment struct {
	ID           string     `json:"id"`
	Path         string     `json:"path,omitempty"`
	Line         int        `json:"line,omitempty"`
	Symbol       string     `json:"symbol,omitempty"`
	ClaimTargets []ClaimRef `json:"claim_targets,omitempty"`
}

// ReviewInput is the STRUCTURED existing-review payload the surface produces (by
// fetching the GitHub review + comments, or accepting an inline value) and hands
// to the engine as Params.Review. It is never a raw blob the engine parses — the
// surface structures it so no I/O / parsing heuristics leak into the engine.
type ReviewInput struct {
	Verdict  string          `json:"verdict,omitempty"`
	Comments []ReviewComment `json:"comments,omitempty"`
}

// CritiqueTaint is the verbatim taint-path provenance copied into a gap item when
// the touched entity lies on a source→sink path (never re-derived).
type CritiqueTaint struct {
	Source string   `json:"source,omitempty"`
	Sink   string   `json:"sink,omitempty"`
	Steps  []string `json:"steps,omitempty"`
}

// CritiqueItem is one STRUCTURED, typed critique record. Every field is
// machine-readable evidence (type enum, entity NodeId(s), contributing edge kinds,
// integer-bucketed scores, blast-radius count, taint provenance, review-anchor) —
// there is NO free-text / natural-language verdict field anywhere. Optional fields
// stay omitted per type so the JSON is compact and stable.
type CritiqueItem struct {
	Type string `json:"type"` // gap | over_flag | unsupported_claim
	// Entity is the touched entity under critique. For gap it is the high-risk
	// touched entity the review missed; for over_flag / unsupported_claim it is the
	// review-anchored entity.
	Entity string `json:"entity"`
	// ReviewAnchor is the source review-comment id (over_flag / unsupported_claim).
	// Empty for gap (no comment anchors it — that is the point).
	ReviewAnchor string `json:"review_anchor,omitempty"`
	// EP-007 oracle evidence (gap / over_flag). Score is the fixed-point composite
	// risk; BlastRadius is the dependent-node count; Blast/Centrality are the integer
	// buckets; EdgeKinds are the contributing dependency-edge kinds (sorted).
	Score       string   `json:"score"`
	BlastRadius int      `json:"blast_radius"`
	BlastBucket int      `json:"blast_bucket"`
	Centrality  int      `json:"centrality"`
	EdgeKinds   []string `json:"edge_kinds"`
	// Taint provenance, present only when the entity lies on a taint path (gap).
	Taint *CritiqueTaint `json:"taint,omitempty"`
	// unsupported_claim evidence: the resolved impact-target entity X and the fact
	// that no graph edge connects the anchored entity to X.
	ClaimTarget string `json:"claim_target,omitempty"`
	AbsentEdge  bool   `json:"absent_edge,omitempty"`
}

// CritiqueReport is the full versioned payload emitted over every surface. Items
// are grouped by Type in a FIXED enumeration order (gap, over_flag,
// unsupported_claim) then ordered by entity NodeId ascending then review-anchor
// ascending, so an identical PR + review + graph state yields byte-identical
// output (determinism / full-vs-incremental parity). The envelope reports the
// unanchored tallies so a reviewer sees coverage honestly. The HighRiskGate is the
// EP-007 fixed-point threshold the critique was computed against (echoed for
// auditability). No timestamp / wall-clock fields.
type CritiqueReport struct {
	SchemaVersion         int            `json:"schema_version"`
	AnalyzerVersion       string         `json:"analyzer_version"`
	IdentitySchemaVersion uint32         `json:"identity_schema_version"`
	HighRiskGate          int            `json:"high_risk_gate"`
	Verdict               string         `json:"verdict,omitempty"`
	Outcome               string         `json:"outcome"` // found | empty
	Unanchored            int            `json:"unanchored"`
	UnanchoredClaims      int            `json:"unanchored_claims"`
	Items                 []CritiqueItem `json:"items"`
}

// critiqueReviewAnalyzer is the registered review critique. It is stateless per
// call and performs ZERO outbound network activity (pure graph reads over the
// read-only Reader; the review-fetch egress stays at the surface boundary).
type critiqueReviewAnalyzer struct{}

func newCritiqueReviewAnalyzer() critiqueReviewAnalyzer { return critiqueReviewAnalyzer{} }

func (critiqueReviewAnalyzer) Name() string { return CritiqueReviewAnalyzerName }

// Analyze critiques the supplied review (Params.Review) against the touched-entity
// set (resolved from Params.Diff via the reused EP-007 kernel) and the local graph,
// returning a versioned CritiqueReport on the generic Analysis envelope. It never
// fetches anything.
func (a critiqueReviewAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	report, err := a.critique(ctx, r, p)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer: CritiqueReviewAnalyzerName,
		Outcome:  query.Outcome(report.Outcome),
		Symbol:   p.Symbol,
		Critique: &report,
	}, nil
}

// anchoredComment pairs a resolved review-comment anchor with its comment id and
// its resolved (anchorable) claim targets. Built once during deterministic anchoring.
type anchoredComment struct {
	id      string
	entity  model.NodeId
	targets []model.NodeId // resolved claim-target entities (anchorable only)
}

// critique is the single-pass core, split out for direct unit-testing. It builds
// the shared graph maps ONCE (nodes, reverse-dep adjacency, centrality, taint),
// resolves the touched set + review anchors deterministically, replays the EP-007
// oracle over the union, and runs the three-way diff.
func (a critiqueReviewAnalyzer) critique(ctx context.Context, r query.Reader, p Params) (CritiqueReport, error) {
	gate := defaultQuestionConfig.HighRiskThreshold // EP-007 high-risk gate (500 == 0.500)
	report := CritiqueReport{
		SchemaVersion:         CritiqueSchemaVersion,
		AnalyzerVersion:       CritiqueAnalyzerVersion,
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		HighRiskGate:          gate,
		Outcome:               string(query.OutcomeEmpty),
		Items:                 []CritiqueItem{},
	}
	if p.Review != nil {
		report.Verdict = p.Review.Verdict
	}

	// (1) Resolve the PR's touched-entity set ONCE via the reused EP-007 kernel
	// (parseDiff → resolveRef over model.NormalizePath). ok=false refs are simply
	// not part of the touched set (an unresolved CHANGED ref is the pr-risk scorer's
	// degraded-record concern, not the critique's unanchored-comment concern).
	refs, err := parseDiff(p.Diff)
	if err != nil {
		return CritiqueReport{}, err
	}
	touched := map[model.NodeId]struct{}{}
	for _, ref := range refs {
		id, ok, err := resolveRef(ctx, r, ref)
		if err != nil {
			return CritiqueReport{}, err
		}
		if ok {
			touched[id] = struct{}{}
		}
	}

	// (2) Deterministic comment→entity anchoring. Each comment's {path,line,symbol}
	// is wrapped in a changedRef and resolved via resolveRef; ok=false ⇒ counted in
	// the unanchored tally, NEVER guessed. Claim targets are anchored the same way;
	// an unresolvable target degrades to the unanchored-claims tally (the
	// anti-fabrication boundary — no unsupported_claim against a guessed entity).
	var anchored []anchoredComment
	anchoredEntities := map[model.NodeId]struct{}{}
	if p.Review != nil {
		for _, c := range p.Review.Comments {
			ref := changedRef{
				raw:  c.commentRaw(),
				file: c.Path,
				name: c.Symbol,
				line: c.Line,
			}
			id, ok, err := resolveRef(ctx, r, ref)
			if err != nil {
				return CritiqueReport{}, err
			}
			if !ok {
				report.Unanchored++
				continue
			}
			ac := anchoredComment{id: c.ID, entity: id}
			for _, ct := range c.ClaimTargets {
				tref := changedRef{raw: ct.Raw, file: ct.Path, name: ct.Symbol, line: ct.Line}
				tid, tok, err := resolveRef(ctx, r, tref)
				if err != nil {
					return CritiqueReport{}, err
				}
				if !tok {
					report.UnanchoredClaims++
					continue
				}
				ac.targets = append(ac.targets, tid)
			}
			anchored = append(anchored, ac)
			anchoredEntities[id] = struct{}{}
		}
	}

	// (3) Build the shared graph maps ONCE (mirror the SW-105/106/107 single-pass
	// discipline): nodes-by-id, reverse-dependency adjacency, centrality metrics,
	// the taint result + per-node index, and a direct dependency-edge adjacency for
	// the unsupported-claim connectivity check.
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return CritiqueReport{}, err
	}
	nodeOf := make(map[model.NodeId]query.ResultNode, len(nodes))
	for _, n := range nodes {
		nodeOf[n.ID()] = nodeToResult(n)
	}

	impactAdj, err := buildImpactAdjacency(ctx, r)
	if err != nil {
		return CritiqueReport{}, err
	}

	metricRes, err := (metricsAnalyzer{}).Analyze(ctx, r, Params{})
	if err != nil {
		return CritiqueReport{}, err
	}
	metricScores := metricRes.Metrics

	taintRes, err := newDefaultSignalProvider().Taint(ctx, r)
	if err != nil {
		return CritiqueReport{}, err
	}
	taintByNode := indexTaint(taintRes)

	directAdj, err := buildDirectDependencyAdjacency(ctx, r)
	if err != nil {
		return CritiqueReport{}, err
	}

	// (4) Replay the EP-007 oracle ONCE over the union of touched + anchored
	// entities. Build the precomputed impact map and score each entity through the
	// reused prisk.scoreRegion kernel (same blast/centrality/taint semantics).
	oracleSet := map[model.NodeId]struct{}{}
	for id := range touched {
		oracleSet[id] = struct{}{}
	}
	for id := range anchoredEntities {
		oracleSet[id] = struct{}{}
	}
	oracleIDs := sortedIDSetMap(oracleSet)

	impactByNode := make(map[model.NodeId]Analysis, len(oracleIDs))
	for _, id := range oracleIDs {
		impactByNode[id] = bfsImpact(impactAdj, id, nodeOf, metricScores)
	}
	prov := &triageProvider{impactByNode: impactByNode, taintRes: taintRes}
	kernel := priskAnalyzer{}

	type oracle struct {
		scoreUnits  int
		blastRadius int
		blastBucket int
		centrality  int
		edgeKinds   []string
		taint       *CritiqueTaint
	}
	oracleByNode := make(map[model.NodeId]oracle, len(oracleIDs))
	for _, id := range oracleIDs {
		rec, err := kernel.scoreRegion(ctx, r, id, prov, defaultWeights, taintByNode, ProvenanceFull)
		if err != nil {
			return CritiqueReport{}, err
		}
		units, _ := parseFixed(rec.Score)
		imp := impactByNode[id]
		o := oracle{
			scoreUnits:  units,
			blastRadius: len(imp.Nodes),
			blastBucket: blastRadiusBucket(len(imp.Nodes), defaultWeights.MaxBucket),
			centrality:  centralityBucket(metricScores, id),
			edgeKinds:   contributingEdgeKinds(imp.Nodes),
		}
		if f, ok := taintByNode[id]; ok {
			o.taint = critiqueTaintFrom(f)
		}
		oracleByNode[id] = o
	}

	// (5) Three-way diff against the oracle.
	var items []CritiqueItem

	// gap: a touched entity ABOVE the high-risk gate that NO comment anchors to.
	for _, id := range sortedIDSetMap(touched) {
		if _, mentioned := anchoredEntities[id]; mentioned {
			continue
		}
		o := oracleByNode[id]
		if o.scoreUnits <= gate {
			continue
		}
		items = append(items, CritiqueItem{
			Type:        CritiqueGap,
			Entity:      string(id),
			Score:       renderFixed(o.scoreUnits),
			BlastRadius: o.blastRadius,
			BlastBucket: o.blastBucket,
			Centrality:  o.centrality,
			EdgeKinds:   o.edgeKinds,
			Taint:       o.taint,
		})
	}

	// over_flag + unsupported_claim: per anchored comment (in deterministic order).
	sort.Slice(anchored, func(i, j int) bool {
		if anchored[i].entity != anchored[j].entity {
			return anchored[i].entity < anchored[j].entity
		}
		return anchored[i].id < anchored[j].id
	})
	for _, ac := range anchored {
		o := oracleByNode[ac.entity]
		// over_flag: the review flagged an entity the oracle shows is below the gate
		// (low blast radius / leaf / low centrality).
		if o.scoreUnits <= gate {
			items = append(items, CritiqueItem{
				Type:         CritiqueOverFlag,
				Entity:       string(ac.entity),
				ReviewAnchor: ac.id,
				Score:        renderFixed(o.scoreUnits),
				BlastRadius:  o.blastRadius,
				BlastBucket:  o.blastBucket,
				Centrality:   o.centrality,
				EdgeKinds:    o.edgeKinds,
			})
		}
		// unsupported_claim: an asserted impact to an anchorable target X with no
		// graph edge connecting the anchored entity to X.
		targets := append([]model.NodeId(nil), ac.targets...)
		sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
		seen := map[model.NodeId]struct{}{}
		for _, tgt := range targets {
			if _, dup := seen[tgt]; dup {
				continue
			}
			seen[tgt] = struct{}{}
			if directlyConnected(directAdj, ac.entity, tgt) {
				continue // the claim IS supported by a connecting edge
			}
			items = append(items, CritiqueItem{
				Type:         CritiqueUnsupportedClaim,
				Entity:       string(ac.entity),
				ReviewAnchor: ac.id,
				ClaimTarget:  string(tgt),
				AbsentEdge:   true,
				Score:        renderFixed(o.scoreUnits),
				EdgeKinds:    []string{},
			})
		}
	}

	sortCritiqueItems(items)
	report.Items = items
	if len(items) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// commentRaw builds a stable verbatim reference for a review comment's anchor,
// reusing the EP-007 diffRaw convention.
func (c ReviewComment) commentRaw() string {
	return diffRaw(c.Path, c.Symbol, c.Line)
}

// contributingEdgeKinds returns the sorted, deduped set of dependency-edge kinds
// by which the blast-radius nodes are reached — the "contributing edges" evidence.
func contributingEdgeKinds(reached []ReachedNode) []string {
	set := map[string]struct{}{}
	for _, rn := range reached {
		if k := rn.ReachedVia.Kind; k != "" {
			set[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// critiqueTaintFrom copies a taint Finding's provenance VERBATIM into the critique
// taint evidence (never re-derived), mirroring the EP-007 EvidenceTaintPath rule.
func critiqueTaintFrom(f taint.Finding) *CritiqueTaint {
	steps := make([]string, 0, len(f.Path))
	for _, st := range f.Path {
		steps = append(steps, string(st.NodeID))
	}
	return &CritiqueTaint{Source: f.SourceName, Sink: f.SinkName, Steps: steps}
}

// buildDirectDependencyAdjacency builds an UNDIRECTED direct-edge adjacency over
// the EP-004 dependency kinds (calls/references/defines): connected[x][y] is true
// iff a dependency edge directly links x and y in either direction. It is the
// connectivity oracle for the unsupported_claim check ("no graph edge connects the
// touched entity to X"). Pure local reads, zero egress.
func buildDirectDependencyAdjacency(ctx context.Context, r query.Reader) (map[model.NodeId]map[model.NodeId]struct{}, error) {
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	want := make(map[string]struct{}, len(dependencyKinds))
	for _, k := range dependencyKinds {
		want[k] = struct{}{}
	}
	adj := map[model.NodeId]map[model.NodeId]struct{}{}
	add := func(x, y model.NodeId) {
		if adj[x] == nil {
			adj[x] = map[model.NodeId]struct{}{}
		}
		adj[x][y] = struct{}{}
	}
	for _, e := range edges {
		if _, ok := want[e.Kind()]; !ok {
			continue
		}
		add(e.From(), e.To())
		add(e.To(), e.From())
	}
	return adj, nil
}

// directlyConnected reports whether a and b are linked by a direct dependency edge
// (either direction). Self-reference is treated as connected (a claim about the
// entity itself is trivially supported).
func directlyConnected(adj map[model.NodeId]map[model.NodeId]struct{}, a, b model.NodeId) bool {
	if a == b {
		return true
	}
	if nbrs, ok := adj[a]; ok {
		if _, ok := nbrs[b]; ok {
			return true
		}
	}
	return false
}

// critiqueTypeRank fixes the stable, FIXED enumeration order of critique item types
// (gap, over_flag, unsupported_claim) for grouping in the serialized output.
func critiqueTypeRank(t string) int {
	switch t {
	case CritiqueGap:
		return 0
	case CritiqueOverFlag:
		return 1
	case CritiqueUnsupportedClaim:
		return 2
	default:
		return 3
	}
}

// sortCritiqueItems enforces the TOTAL ORDER: type (fixed enumeration order) →
// entity NodeId ascending → review-anchor ascending → claim-target ascending. This
// is the ONLY place item ordering is decided; combined with MarshalCritique it
// makes the report byte-identical regardless of map-iteration order, and the
// claim-target backstop guarantees a unique total order even for an entity with
// several unsupported claims.
func sortCritiqueItems(items []CritiqueItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if ra, rb := critiqueTypeRank(a.Type), critiqueTypeRank(b.Type); ra != rb {
			return ra < rb
		}
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		if a.ReviewAnchor != b.ReviewAnchor {
			return a.ReviewAnchor < b.ReviewAnchor
		}
		return a.ClaimTarget < b.ClaimTarget
	})
}

// MarshalCritique is the single canonical serializer for a CritiqueReport, shared
// by every surface. It re-sorts defensively (total order), disables HTML escaping,
// and trims the trailing newline — byte-for-byte stable across runs and surfaces
// (mirrors MarshalRisk / MarshalTriage / MarshalConflicts / MarshalReviewers).
// Empty slices are materialized (never null) so the shape is stable. No timestamp /
// wall-clock / float / map-iteration leakage (scores are integer-bucketed /
// fixed-precision strings).
func MarshalCritique(rep CritiqueReport) ([]byte, error) {
	out := rep
	items := make([]CritiqueItem, len(rep.Items))
	copy(items, rep.Items)
	sortCritiqueItems(items)
	for i := range items {
		if items[i].EdgeKinds == nil {
			items[i].EdgeKinds = []string{}
		}
	}
	if items == nil {
		items = []CritiqueItem{}
	}
	out.Items = items

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal critique report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
