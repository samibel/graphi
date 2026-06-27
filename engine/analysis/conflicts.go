package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// ConflictsAnalyzerName is the dispatch key for the inter-PR conflict detector
// (EP-018 story 2/4, SW-106). It is a composite, read-only, DETERMINISTIC batch
// driver that consumes an already-enumerated open-PR set (handed in via
// Params.ConflictPRs by the surface-boundary forge client — the engine itself
// NEVER touches the network) and reports which PR PAIRS will collide over the
// LOCAL graph. It reuses the EP-007 change-set resolution (parseDiff/resolveRef)
// and the graph primitives (metrics centrality, the impact.go reverse-dependency
// adjacency) rather than re-implementing any of them; the net-new logic is the
// pairwise change-set / dependency-closure intersection, the asymmetric
// contract-dependency edge check, and the entity→PRs inverted index that keeps
// the pair materialization well below the naive O(N²).
const ConflictsAnalyzerName = "conflicts-prs"

// ConflictsSchemaVersion versions the ConflictReport JSON shape.
const ConflictsSchemaVersion = 1

// ConflictsAnalyzerVersion identifies the detection LOGIC version, echoed in the
// report so a stored/audited conflict set can be tied to the algorithm.
const ConflictsAnalyzerVersion = "conflicts-prs/1"

// Conflict kinds — a stable, enumerated, versioned vocabulary. A pair may carry
// one or more. Consumers switch on these rather than string-parse.
const (
	// ConflictTextualOverlap: two PRs touch overlapping line ranges in the SAME
	// file (classic merge-collision risk). Derived from EP-007 per-PR diff
	// resolution (changedRef.line), with a fixed merge-context proximity window.
	ConflictTextualOverlap = "textual-overlap"
	// ConflictSharedFile: two PRs touch the same file node (same normalized path;
	// line ranges need not overlap).
	ConflictSharedFile = "shared-file"
	// ConflictSharedSymbol: two PRs touch the same symbol NodeId (precise,
	// diff-resolved symbol identity — distinct from merely co-locating in a file).
	ConflictSharedSymbol = "shared-symbol"
	// ConflictSharedHighCentralityNode: two PRs share a touched node whose
	// centrality is at or above a fixed bucket threshold (metrics.go degree
	// centrality); evidence carries the integer centrality bucket.
	ConflictSharedHighCentralityNode = "shared-high-centrality-node"
	// ConflictContractDependency: ASYMMETRIC — PR-A mutates a contract node
	// (signature / exported type / interface) that an entity changed in PR-B
	// depends on via a graph edge (calls/references/defines). Flagged even with NO
	// textual file overlap — the git-invisible case. Evidence names the specific
	// contract node, the dependent entity in the depending PR, and which PR mutated
	// the contract.
	ConflictContractDependency = "contract-dependency"
)

// conflictContextLines is the fixed merge-context proximity window for the
// textual-overlap kind. Two changed lines in the same file are treated as
// textually overlapping when their line numbers are within this many lines of
// each other — mirroring how a diff tool's default context makes adjacent hunks
// collide on merge. Integer-only (no floats), documented, and version-pinned so
// the determination is byte-stable.
const conflictContextLines = 3

// conflictCentralityBucketThreshold is the inclusive lower bound on the metrics
// degree-centrality bucket (0..3, see centralityBucket) for a shared touched node
// to be classified high-centrality. Bucket 2 corresponds to a node with degree
// centrality >= 0.25 — a clearly above-leaf node. Fixed and documented so the
// classification is reproducible.
const conflictCentralityBucketThreshold = 2

// conflictMaxEvidencePerKind bounds the number of evidence items emitted per
// (pair, kind) so a pair record stays token-bounded and byte-stable even for a
// massively overlapping pair. Evidence is sorted on its canonical key before the
// bound is applied, so the retained subset is deterministic.
const conflictMaxEvidencePerKind = 32

// ConflictPRInput is the engine-side input for one enumerated PR. The surface
// maps the forge-sourced metadata onto this type and hands the set in via
// Params.ConflictPRs; the engine never fetches it. Diff is OPTIONAL: when present
// it is parsed through the EP-007 parseDiff/resolveRef kernel to recover precise
// symbol identities and changed line ranges; when absent, the analyzer resolves
// the PR's change set at file granularity from ChangedFiles only (still enough
// for shared-file / shared-high-centrality / contract-dependency detection).
type ConflictPRInput struct {
	Number       int      `json:"number"`
	ChangedFiles []string `json:"changed_files"`
	Diff         string   `json:"diff,omitempty"`
}

// ConflictEvidence is one self-describing, enumerated piece of evidence for a
// conflicting pair. The Kind discriminates which fields are populated; the rest
// stay omitted so the JSON is compact and stable (mirrors the SignalFlag /
// EvidenceItem idiom).
type ConflictEvidence struct {
	Kind string `json:"kind"` // enumerated conflict kind
	// shared-file / textual-overlap.
	File string `json:"file,omitempty"`
	// shared-symbol / shared-high-centrality-node.
	Symbol string `json:"symbol,omitempty"` // node id
	// textual-overlap: the two colliding line numbers (PR with the lower number
	// first), within the merge-context window.
	LineLow  int `json:"line_low,omitempty"`
	LineHigh int `json:"line_high,omitempty"`
	// shared-high-centrality-node: the integer centrality bucket (0..3).
	Centrality int `json:"centrality,omitempty"`
	// contract-dependency: the mutated contract node, the dependent entity in the
	// depending PR, and the PR numbers on each side of the asymmetric relationship.
	Contract    string `json:"contract,omitempty"`      // mutated contract node id
	Dependent   string `json:"dependent,omitempty"`     // dependent entity node id (in DependentPR)
	MutatedByPR int    `json:"mutated_by_pr,omitempty"` // PR number that mutated the contract
	DependentPR int    `json:"dependent_pr,omitempty"`  // PR number holding the dependent entity
}

// ConflictPair is the deterministic per-pair record: the two PR numbers (A<B by
// stable PR identity), the sorted set of conflict kind(s) present, and the
// canonically-ordered concrete conflicting entities that justify the pairing.
type ConflictPair struct {
	A        int                `json:"a"` // lower PR number
	B        int                `json:"b"` // higher PR number
	Kinds    []string           `json:"kinds"`
	Entities []ConflictEvidence `json:"entities"`
}

// ConflictReport is the full versioned payload emitted over every surface. Pairs
// are emitted in a TOTAL ORDER — (A,B) ascending on stable PR number — and the
// within-pair entities in a canonical order, so an identical PR set + graph state
// yields byte-identical output (determinism / full-vs-incremental parity).
type ConflictReport struct {
	SchemaVersion         int            `json:"schema_version"`
	AnalyzerVersion       string         `json:"analyzer_version"`
	IdentitySchemaVersion uint32         `json:"identity_schema_version"`
	Outcome               string         `json:"outcome"` // found | empty
	Pairs                 []ConflictPair `json:"pairs"`
}

// conflictsAnalyzer is the registered inter-PR conflict detector. It is stateless
// per call and performs ZERO outbound network activity (pure graph reads over the
// read-only Reader; PR enumeration egress stays at the surface boundary).
type conflictsAnalyzer struct{}

// newConflictsAnalyzer builds the production detector.
func newConflictsAnalyzer() conflictsAnalyzer { return conflictsAnalyzer{} }

func (conflictsAnalyzer) Name() string { return ConflictsAnalyzerName }

// Analyze intersects the enumerated PR set (Params.ConflictPRs) pairwise over the
// local graph and returns a versioned ConflictReport on the generic Analysis
// envelope. It never fetches anything.
func (a conflictsAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	report, err := a.detect(ctx, r, p)
	if err != nil {
		return Analysis{}, err
	}
	return Analysis{
		Analyzer:  ConflictsAnalyzerName,
		Outcome:   query.Outcome(report.Outcome),
		Symbol:    p.Symbol,
		Conflicts: &report,
	}, nil
}

// resolvedConflictPR is one PR's change set resolved ONCE to graph entities.
type resolvedConflictPR struct {
	number      int
	files       map[string]struct{}   // normalized file paths touched
	linesByFile map[string][]int      // changed line points per file (from the diff)
	symbols     map[model.NodeId]bool // precise diff-resolved symbol identities
	touched     map[model.NodeId]bool // all touched nodes (symbols ∪ file-level nodes)
}

// detect is the single-pass core. It precomputes the shared graph maps ONCE
// (nodes-by-path, centrality buckets, reverse-dependency adjacency), resolves each
// PR's change set ONCE, builds the entity→PRs inverted index ONCE, then
// materializes only the non-empty pairwise intersections + contract-dependency
// links. Disjoint PRs never form a candidate pair (the O(N²) mitigation AND the
// no-false-positive guarantee).
func (a conflictsAnalyzer) detect(ctx context.Context, r query.Reader, p Params) (ConflictReport, error) {
	report := ConflictReport{
		SchemaVersion:         ConflictsSchemaVersion,
		AnalyzerVersion:       ConflictsAnalyzerVersion,
		IdentitySchemaVersion: model.IdentitySchemaVersion,
		Outcome:               string(query.OutcomeEmpty),
		Pairs:                 []ConflictPair{},
	}
	if len(p.ConflictPRs) == 0 {
		return report, nil
	}

	// (1) Enumerate nodes ONCE; group ids by normalized source path and keep the
	// node metadata for the contract classifier and centrality lookup.
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return ConflictReport{}, err
	}
	nodeOf := make(map[model.NodeId]query.ResultNode, len(nodes))
	nodesByPath := make(map[string][]model.NodeId)
	for _, n := range nodes {
		nodeOf[n.ID()] = nodeToResult(n)
		path := model.NormalizePath(n.SourcePath())
		nodesByPath[path] = append(nodesByPath[path], n.ID())
	}

	// (2) Centrality buckets ONCE (reuse the metrics analyzer; never recompute).
	metricRes, err := (metricsAnalyzer{}).Analyze(ctx, r, Params{})
	if err != nil {
		return ConflictReport{}, err
	}
	centByNode := buildCentralityBuckets(metricRes.Metrics)

	// (3) Reverse-dependency adjacency ONCE (reuse the SW-105 builder over the
	// EP-004 dependency kinds): impactAdj[c] lists the entities that depend on c.
	impactAdj, err := buildImpactAdjacency(ctx, r)
	if err != nil {
		return ConflictReport{}, err
	}

	// (4) Resolve each PR's change set ONCE, in stable PR-number order.
	prs := make([]resolvedConflictPR, 0, len(p.ConflictPRs))
	for _, in := range p.ConflictPRs {
		rp, err := resolveConflictPR(ctx, r, in, nodesByPath)
		if err != nil {
			return ConflictReport{}, err
		}
		prs = append(prs, rp)
	}
	sort.Slice(prs, func(i, j int) bool { return prs[i].number < prs[j].number })

	// (5) Build the entity→PRs inverted indexes ONCE. Only entities actually shared
	// (or linked by a contract-dependency edge) ever produce a candidate pair.
	fileIdx := map[string][]int{}
	symIdx := map[model.NodeId][]int{}
	hcIdx := map[model.NodeId][]int{}
	touchedBy := map[model.NodeId][]int{}
	for i := range prs {
		for f := range prs[i].files {
			fileIdx[f] = append(fileIdx[f], i)
		}
		for s := range prs[i].symbols {
			symIdx[s] = append(symIdx[s], i)
		}
		for t := range prs[i].touched {
			touchedBy[t] = append(touchedBy[t], i)
			if centByNode[t] >= conflictCentralityBucketThreshold {
				hcIdx[t] = append(hcIdx[t], i)
			}
		}
	}

	acc := newConflictAccumulator()

	// (6a) shared-file + textual-overlap: enumerate candidate pairs from the file
	// inverted index (only files touched by >=2 PRs materialize a pair).
	for f, idxs := range fileIdx {
		if len(idxs) < 2 {
			continue
		}
		sort.Ints(idxs)
		for ii := 0; ii < len(idxs); ii++ {
			for jj := ii + 1; jj < len(idxs); jj++ {
				i, j := idxs[ii], idxs[jj]
				lo, hi := prs[i].number, prs[j].number
				acc.add(lo, hi, ConflictEvidence{Kind: ConflictSharedFile, File: f})
				// textual-overlap: colliding changed lines within the context window.
				for _, ov := range overlappingLines(prs[i].linesByFile[f], prs[j].linesByFile[f]) {
					acc.add(lo, hi, ConflictEvidence{Kind: ConflictTextualOverlap, File: f, LineLow: ov[0], LineHigh: ov[1]})
				}
			}
		}
	}

	// (6b) shared-symbol: precise diff-resolved symbol identity touched by >=2 PRs.
	for s, idxs := range symIdx {
		if len(idxs) < 2 {
			continue
		}
		sort.Ints(idxs)
		for ii := 0; ii < len(idxs); ii++ {
			for jj := ii + 1; jj < len(idxs); jj++ {
				acc.add(prs[idxs[ii]].number, prs[idxs[jj]].number,
					ConflictEvidence{Kind: ConflictSharedSymbol, Symbol: string(s)})
			}
		}
	}

	// (6c) shared-high-centrality-node: a shared touched node above the centrality
	// bucket threshold (a shared hot spot).
	for n, idxs := range hcIdx {
		if len(idxs) < 2 {
			continue
		}
		sort.Ints(idxs)
		for ii := 0; ii < len(idxs); ii++ {
			for jj := ii + 1; jj < len(idxs); jj++ {
				acc.add(prs[idxs[ii]].number, prs[idxs[jj]].number,
					ConflictEvidence{Kind: ConflictSharedHighCentralityNode, Symbol: string(n), Centrality: centByNode[n]})
			}
		}
	}

	// (6d) contract-dependency (asymmetric): for each contract node a PR mutates,
	// walk dependents over the reverse-dependency adjacency; if a dependent is
	// touched by a DIFFERENT PR, that is a contract-dependency conflict — flagged
	// even when the two PRs share no file (the git-invisible case).
	addContractDependencies(prs, nodeOf, impactAdj, touchedBy, acc)

	report.Pairs = acc.materialize()
	if len(report.Pairs) > 0 {
		report.Outcome = string(query.OutcomeFound)
	}
	return report, nil
}

// resolveConflictPR resolves one PR's change set ONCE. It reuses the EP-007
// kernel verbatim: parseDiff for the changed refs (with line hints) and resolveRef
// for the precise path→symbol resolution over model.NormalizePath. File-level
// entities come from ChangedFiles (the forge metadata) resolved through the
// shared nodesByPath index, so a PR carrying only a file list still resolves to a
// touched-node set. NEITHER diff parsing NOR path resolution is re-implemented.
func resolveConflictPR(ctx context.Context, r query.Reader, in ConflictPRInput, nodesByPath map[string][]model.NodeId) (resolvedConflictPR, error) {
	rp := resolvedConflictPR{
		number:      in.Number,
		files:       map[string]struct{}{},
		linesByFile: map[string][]int{},
		symbols:     map[model.NodeId]bool{},
		touched:     map[model.NodeId]bool{},
	}

	addFileNodes := func(path string) {
		for _, id := range nodesByPath[path] {
			rp.touched[id] = true
		}
	}

	// File-level entities from the forge-sourced changed-file list.
	for _, f := range in.ChangedFiles {
		path := model.NormalizePath(strings.TrimSpace(f))
		if path == "" {
			continue
		}
		rp.files[path] = struct{}{}
		addFileNodes(path)
	}

	// Symbol-precise + line-range entities from the optional diff (EP-007 reuse).
	refs, err := parseDiff(in.Diff)
	if err != nil {
		return resolvedConflictPR{}, err
	}
	for _, ref := range refs {
		if ref.file != "" {
			path := model.NormalizePath(ref.file)
			rp.files[path] = struct{}{}
			addFileNodes(path)
			if ref.line > 0 {
				rp.linesByFile[path] = append(rp.linesByFile[path], ref.line)
			}
		}
		id, ok, err := resolveRef(ctx, r, ref)
		if err != nil {
			return resolvedConflictPR{}, err
		}
		if ok {
			rp.symbols[id] = true
			rp.touched[id] = true
		}
	}

	// Stable, deduped line lists per file.
	for f := range rp.linesByFile {
		rp.linesByFile[f] = dedupeInts(rp.linesByFile[f])
	}
	return rp, nil
}

// addContractDependencies materializes the asymmetric contract-dependency
// conflicts into the accumulator. For each PR and each contract node it touches,
// it walks the transitive dependents over the reverse-dependency adjacency and
// records a conflict against every OTHER PR that touches one of those dependents.
// Direction (which PR mutated the contract vs which depends on it) is preserved
// verbatim in the evidence regardless of the canonical (A,B) pair order.
func addContractDependencies(
	prs []resolvedConflictPR,
	nodeOf map[model.NodeId]query.ResultNode,
	impactAdj map[model.NodeId][]impactNbr,
	touchedBy map[model.NodeId][]int,
	acc *conflictAccumulator,
) {
	for i := range prs {
		// Deterministic iteration over this PR's contract nodes.
		contracts := make([]model.NodeId, 0, len(prs[i].touched))
		for id := range prs[i].touched {
			if isContractNode(nodeOf[id]) {
				contracts = append(contracts, id)
			}
		}
		sort.Slice(contracts, func(a, b int) bool { return contracts[a] < contracts[b] })

		for _, c := range contracts {
			deps := conflictDependents(impactAdj, c)
			for _, d := range deps {
				holders := touchedBy[d]
				sort.Ints(holders)
				for _, j := range holders {
					if j == i {
						continue // same PR mutates and "depends" — not an inter-PR conflict
					}
					mutator := prs[i].number
					dependentPR := prs[j].number
					lo, hi := mutator, dependentPR
					if lo > hi {
						lo, hi = hi, lo
					}
					acc.add(lo, hi, ConflictEvidence{
						Kind:        ConflictContractDependency,
						Contract:    string(c),
						Dependent:   string(d),
						MutatedByPR: mutator,
						DependentPR: dependentPR,
					})
				}
			}
		}
	}
}

// conflictDependents returns the transitive set of entities that depend on the
// contract node c, by BFS over the reverse-dependency adjacency (impactAdj[x]
// lists the entities pointing AT x). Cycle-guarded and bounded by DefaultMaxNodes,
// returned in canonical node-id order. The contract node itself is excluded.
func conflictDependents(adj map[model.NodeId][]impactNbr, c model.NodeId) []model.NodeId {
	seen := map[model.NodeId]struct{}{c: {}}
	var out []model.NodeId
	frontier := []model.NodeId{c}
	for len(frontier) > 0 && len(out) < DefaultMaxNodes {
		var next []model.NodeId
		for _, cur := range frontier {
			for _, nb := range adj[cur] {
				if _, ok := seen[nb.id]; ok {
					continue
				}
				seen[nb.id] = struct{}{}
				out = append(out, nb.id)
				next = append(next, nb.id)
			}
		}
		frontier = next
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// contractNodeKinds is the DOCUMENTED, deterministic set of node kinds that are
// treated as contract carriers regardless of export visibility: a type / class /
// interface / struct / enum is a structural contract its dependents are bound to.
var contractNodeKinds = map[string]struct{}{
	"type":      {},
	"class":     {},
	"interface": {},
	"struct":    {},
	"enum":      {},
}

// isContractNode reports whether n is a contract node — a signature / exported
// type / interface whose mutation can break dependents. The classifier is a pure,
// deterministic, language-agnostic heuristic: a node is a contract node if its
// Kind is a structural-contract kind, OR it is an exported function/method (the
// simple name begins with an uppercase letter — the cross-language export
// convention). No I/O, no graph mutation.
func isContractNode(n query.ResultNode) bool {
	kind := strings.ToLower(strings.TrimSpace(n.Kind))
	if _, ok := contractNodeKinds[kind]; ok {
		return true
	}
	if kind == "function" || kind == "method" {
		return isExportedName(n.QualifiedName)
	}
	return false
}

// isExportedName reports whether the simple (trailing) identifier of a possibly
// qualified name begins with an uppercase ASCII letter — the cross-language
// "exported / public" convention. Pure string.
func isExportedName(qualified string) bool {
	name := lastIdent(strings.TrimSpace(qualified))
	// lastIdent splits on '.'; also handle other common separators.
	for _, sep := range []string{"::", "#", "/", "\\"} {
		if idx := strings.LastIndex(name, sep); idx >= 0 {
			name = name[idx+len(sep):]
		}
	}
	if name == "" {
		return false
	}
	c := name[0]
	return c >= 'A' && c <= 'Z'
}

// overlappingLines returns the colliding (lower-PR line, higher-PR line) pairs
// between two PRs' changed-line sets in a shared file: every pair within the
// fixed merge-context window. Inputs are sorted line points; the output is sorted
// and bounded so the textual-overlap evidence is byte-stable.
func overlappingLines(a, b []int) [][2]int {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	as := dedupeInts(a)
	bs := dedupeInts(b)
	var out [][2]int
	for _, la := range as {
		for _, lb := range bs {
			d := la - lb
			if d < 0 {
				d = -d
			}
			if d <= conflictContextLines {
				out = append(out, [2]int{la, lb})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// buildCentralityBuckets maps each node to its integer degree-centrality bucket
// (0..3) from the consumed metrics NodeScores (only the centrality kind is read),
// reusing the EP-007 centralityBucket thresholds so "high centrality" means the
// same thing across the PR-review suite. Integer-only (no float leakage).
func buildCentralityBuckets(scores []NodeScore) map[model.NodeId]int {
	out := make(map[model.NodeId]int, len(scores))
	for _, s := range scores {
		if s.Kind != MetricCentrality {
			continue
		}
		out[s.Node.ID] = centralityBucket([]NodeScore{s}, s.Node.ID)
	}
	return out
}

// dedupeInts returns a sorted, duplicate-free copy of xs (never nil-sensitive:
// an empty input yields an empty slice).
func dedupeInts(xs []int) []int {
	if len(xs) == 0 {
		return xs
	}
	cp := make([]int, len(xs))
	copy(cp, xs)
	sort.Ints(cp)
	out := cp[:1]
	for i := 1; i < len(cp); i++ {
		if cp[i] != out[len(out)-1] {
			out = append(out, cp[i])
		}
	}
	return out
}

// conflictPairKey is the canonical key for a conflicting pair (A<B PR numbers).
type conflictPairKey struct{ a, b int }

// pairAccum accumulates the kinds + deduped evidence for one pair as it is
// discovered across the several detection passes.
type pairAccum struct {
	kinds    map[string]struct{}
	evidence map[string]ConflictEvidence // canonical key → evidence (dedupe)
}

// conflictAccumulator collects pair records across all detection passes, deduping
// evidence so the same shared entity discovered twice is recorded once.
type conflictAccumulator struct {
	pairs map[conflictPairKey]*pairAccum
}

func newConflictAccumulator() *conflictAccumulator {
	return &conflictAccumulator{pairs: map[conflictPairKey]*pairAccum{}}
}

// add records one evidence item on the (a,b) pair (a<b enforced by the caller).
func (c *conflictAccumulator) add(a, b int, ev ConflictEvidence) {
	if a == b {
		return
	}
	if a > b {
		a, b = b, a
	}
	key := conflictPairKey{a: a, b: b}
	pa := c.pairs[key]
	if pa == nil {
		pa = &pairAccum{kinds: map[string]struct{}{}, evidence: map[string]ConflictEvidence{}}
		c.pairs[key] = pa
	}
	pa.kinds[ev.Kind] = struct{}{}
	pa.evidence[conflictEvidenceKey(ev)] = ev
}

// materialize renders the accumulated pairs into the deterministic report order:
// pairs by (A,B) ascending; within a pair, kinds in fixed enumeration order and
// entities by the canonical evidence key, bounded per kind.
func (c *conflictAccumulator) materialize() []ConflictPair {
	out := make([]ConflictPair, 0, len(c.pairs))
	for key, pa := range c.pairs {
		kinds := make([]string, 0, len(pa.kinds))
		for k := range pa.kinds {
			kinds = append(kinds, k)
		}
		sort.Slice(kinds, func(i, j int) bool { return conflictKindRank(kinds[i]) < conflictKindRank(kinds[j]) })

		ents := make([]ConflictEvidence, 0, len(pa.evidence))
		for _, ev := range pa.evidence {
			ents = append(ents, ev)
		}
		sortConflictEvidence(ents)
		ents = boundEvidencePerKind(ents)

		out = append(out, ConflictPair{A: key.a, B: key.b, Kinds: kinds, Entities: ents})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// boundEvidencePerKind caps the evidence to conflictMaxEvidencePerKind items per
// kind (input must already be sorted), keeping the record token-bounded and the
// retained subset deterministic.
func boundEvidencePerKind(ents []ConflictEvidence) []ConflictEvidence {
	seen := map[string]int{}
	out := make([]ConflictEvidence, 0, len(ents))
	for _, ev := range ents {
		if seen[ev.Kind] >= conflictMaxEvidencePerKind {
			continue
		}
		seen[ev.Kind]++
		out = append(out, ev)
	}
	return out
}

// conflictKindRank fixes the stable ordering of conflict kinds (textual-overlap,
// shared-file, shared-symbol, shared-high-centrality-node, contract-dependency).
func conflictKindRank(kind string) int {
	switch kind {
	case ConflictTextualOverlap:
		return 0
	case ConflictSharedFile:
		return 1
	case ConflictSharedSymbol:
		return 2
	case ConflictSharedHighCentralityNode:
		return 3
	case ConflictContractDependency:
		return 4
	default:
		return 5
	}
}

// conflictEvidenceKey is the canonical identity key for an evidence item, used
// both to dedupe and (via sortConflictEvidence) to order within a pair.
func conflictEvidenceKey(ev ConflictEvidence) string {
	return fmt.Sprintf("%d|%s|%s|%d|%d|%d|%s|%s|%d|%d",
		conflictKindRank(ev.Kind), ev.File, ev.Symbol, ev.LineLow, ev.LineHigh,
		ev.Centrality, ev.Contract, ev.Dependent, ev.MutatedByPR, ev.DependentPR)
}

// sortConflictEvidence orders evidence by the canonical key: kind rank, then file
// path, then symbol/contract/dependent node ids, then the line/centrality fields —
// a deterministic total order (no map-iteration, float, or wall-clock leakage).
func sortConflictEvidence(ents []ConflictEvidence) {
	sort.Slice(ents, func(i, j int) bool {
		a, b := ents[i], ents[j]
		if ra, rb := conflictKindRank(a.Kind), conflictKindRank(b.Kind); ra != rb {
			return ra < rb
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		if a.Contract != b.Contract {
			return a.Contract < b.Contract
		}
		if a.Dependent != b.Dependent {
			return a.Dependent < b.Dependent
		}
		if a.LineLow != b.LineLow {
			return a.LineLow < b.LineLow
		}
		if a.LineHigh != b.LineHigh {
			return a.LineHigh < b.LineHigh
		}
		if a.MutatedByPR != b.MutatedByPR {
			return a.MutatedByPR < b.MutatedByPR
		}
		if a.DependentPR != b.DependentPR {
			return a.DependentPR < b.DependentPR
		}
		return a.Centrality < b.Centrality
	})
}

// MarshalConflicts is the single canonical serializer for a ConflictReport, shared
// by every surface. It re-sorts defensively (total pair order + canonical
// within-pair entity order), disables HTML escaping, and trims the trailing
// newline — byte-for-byte stable across runs and surfaces (mirrors MarshalRisk /
// MarshalTriage). Empty slices are materialized (never null) so the shape is
// stable. No timestamp / wall-clock / float / map-iteration leakage.
func MarshalConflicts(rep ConflictReport) ([]byte, error) {
	out := rep
	pairs := make([]ConflictPair, len(rep.Pairs))
	copy(pairs, rep.Pairs)
	for i := range pairs {
		kinds := make([]string, len(pairs[i].Kinds))
		copy(kinds, pairs[i].Kinds)
		sort.Slice(kinds, func(a, b int) bool { return conflictKindRank(kinds[a]) < conflictKindRank(kinds[b]) })
		if kinds == nil {
			kinds = []string{}
		}
		pairs[i].Kinds = kinds

		ents := make([]ConflictEvidence, len(pairs[i].Entities))
		copy(ents, pairs[i].Entities)
		sortConflictEvidence(ents)
		if ents == nil {
			ents = []ConflictEvidence{}
		}
		pairs[i].Entities = ents
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].A != pairs[j].A {
			return pairs[i].A < pairs[j].A
		}
		return pairs[i].B < pairs[j].B
	})
	if pairs == nil {
		pairs = []ConflictPair{}
	}
	out.Pairs = pairs

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("analysis: marshal conflict report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
