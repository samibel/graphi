// Package interproctaint completes the SW-030 Sharir-Pnueli interprocedural
// dataflow seam for taint analysis (SW-102). It solves per-procedure taint
// summaries to a global fixpoint over the call graph, answers cross-procedure
// source→sink flow queries from the solved relation, persists the fixpoint as a
// canonical content-hash-keyed artifact (so post-restart queries are served with
// zero recomputation), and surfaces an explicit deterministic capped/incomplete
// verdict when a cost cap is hit.
//
// Layering: this is an engine-layer package. It reuses the lattice
// (taint.LabelSet), the worklist fixpoint engine (interproc.FixpointSolver), and
// the SCC machinery (interproc.TarjanSCC) as-is. Surfaces never import it
// directly; they reach the capability through analysis.Service.Dispatch and
// serialize the returned envelope.
package interproctaint

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis/interproc"
	"github.com/samibel/graphi/engine/analysis/taint"
	"github.com/samibel/graphi/engine/query"
)

// AnalyzerName is the capability key for the interprocedural taint solver.
const AnalyzerName = "interproc-taint"

// SchemaVersion is the on-disk artifact schema version. A loaded artifact with a
// different schema version is treated as a miss (recompute), never served.
const SchemaVersion = 1

// Verdict is the explicit, deterministic completeness verdict of a solve. A
// capped verdict means a configured cost cap was hit and the solved relation is
// a conservative over-approximation — it is NEVER silently downgraded to a "no
// flow" answer.
type Verdict string

const (
	// VerdictComplete means the fixpoint converged within all configured caps.
	VerdictComplete Verdict = "complete"
	// VerdictCapped means a cost cap was hit; the result is conservative and
	// explicitly marked incomplete.
	VerdictCapped Verdict = "capped"
)

// Flow is a cross-procedure source→sink taint flow with its call path. The
// CallPath lists procedure qualified names from the sink down to the source
// (the return/propagation direction the labels travel).
type Flow struct {
	SourceID     string   `json:"source_id"`
	SourceName   string   `json:"source_name"`
	SinkID       string   `json:"sink_id"`
	SinkName     string   `json:"sink_name"`
	SinkCategory string   `json:"sink_category,omitempty"`
	Labels       []string `json:"labels"`
	CallPath     []string `json:"call_path"`
}

// ProcSummary is the persisted per-procedure taint summary. OutputLabels are the
// taint labels that may be observed flowing out of the procedure (the labels it
// itself sources plus those its callees expose, minus the labels it sanitizes).
// KillLabels / KillAll capture the procedure's own sanitizer effect so the
// solved-summary provider can apply the procedure transfer at a call site.
type ProcSummary struct {
	ProcID       string   `json:"proc_id"`
	ProcName     string   `json:"proc_name"`
	OutputLabels []string `json:"output_labels"`
	KillLabels   []string `json:"kill_labels,omitempty"`
	KillAll      bool     `json:"kill_all,omitempty"`
	IsSource     bool     `json:"is_source,omitempty"`
	IsSink       bool     `json:"is_sink,omitempty"`
	Approximate  bool     `json:"approximate,omitempty"`
}

// Solution is the solved interprocedural taint relation plus its provenance.
// Field ordering is the canonical serialization order; slices are sorted by the
// solver so the serialized bytes are byte-stable regardless of the path taken to
// reach the graph state. Loaded/Solved are runtime-only observability flags and
// are excluded from the canonical artifact bytes.
type Solution struct {
	SchemaVersion int             `json:"schema_version"`
	ContentHash   string          `json:"content_hash"`
	ConfigHash    string          `json:"config_hash,omitempty"`
	Verdict       Verdict         `json:"verdict"`
	CapKind       string          `json:"cap_kind,omitempty"`
	Summaries     []ProcSummary   `json:"summaries"`
	SCCs          []interproc.SCC `json:"sccs"`
	Flows         []Flow          `json:"flows"`

	// Loaded reports the answer was served from the persisted artifact with no
	// recomputation. Solved reports the fixpoint was computed this call. Exactly
	// one is true. Both are runtime-only (json:"-") so they never leak into the
	// canonical bytes.
	Loaded bool `json:"-"`
	Solved bool `json:"-"`
}

// killSpec captures a procedure's own sanitizer effect.
type killSpec struct {
	all    bool
	labels []string
}

// knownCaps is the deterministic, lexicographically ordered set of cap-kind
// tokens used to derive a stable CapKind from solver diagnostics.
var knownCaps = []string{
	"max_iterations",
	"max_procedures",
	"max_scc_size",
	"max_summary_entries",
	"max_total_work",
}

// Solve computes the global interprocedural taint fixpoint over the graph
// accessible via the read-only Reader. It is deterministic and order-independent:
// the worklist is seeded in canonical topological-by-SCC order (TarjanSCC reverse
// topo) with a stable procedure-ID tiebreak, never by map enumeration.
func Solve(ctx context.Context, r query.Reader, cfg taint.Config, caps interproc.Caps, wideningThreshold int) (Solution, error) {
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return Solution{}, fmt.Errorf("interproctaint: load nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return Solution{}, fmt.Errorf("interproctaint: load edges: %w", err)
	}
	return solveFrom(ctx, nodes, edges, cfg, caps, wideningThreshold)
}

// solveFrom is the core solver over already-loaded nodes/edges. Splitting it out
// lets the persistence layer reuse the same loaded data for the content hash.
func solveFrom(ctx context.Context, nodes []model.Node, edges []model.Edge, cfg taint.Config, caps interproc.Caps, wideningThreshold int) (Solution, error) {
	contentHash := computeContentHash(nodes, edges, cfg)
	sol := Solution{
		SchemaVersion: SchemaVersion,
		ContentHash:   contentHash,
		ConfigHash:    cfg.ContentHash,
		Verdict:       VerdictComplete,
		Summaries:     []ProcSummary{},
		Flows:         []Flow{},
		Solved:        true,
	}

	// Classify nodes against the taint config (deterministic, pure string match).
	nameByID := make(map[string]string, len(nodes))
	genByID := make(map[string][]string)
	killByID := make(map[string]killSpec)
	sinkCatByID := make(map[string]string)
	for _, n := range nodes {
		id := string(n.ID())
		nameByID[id] = n.QualifiedName()
		if label, _ := cfg.MatchSource(n.Kind(), n.QualifiedName()); label != "" {
			genByID[id] = taint.NewLabelSet(label)
		}
		if sinkID, cat := cfg.MatchSink(n.Kind(), n.QualifiedName()); sinkID != "" {
			sinkCatByID[id] = cat
		}
		if san, ok := cfg.MatchSanitizer(n.Kind(), n.QualifiedName()); ok {
			if len(san.RemoveLabels) == 0 {
				killByID[id] = killSpec{all: true}
			} else {
				killByID[id] = killSpec{labels: taint.NewLabelSet(san.RemoveLabels...)}
			}
		}
	}

	// Build the call graph from "calls" edges (caller id -> sorted callee ids).
	callGraph := make(interproc.CallGraph)
	for _, e := range edges {
		if e.Kind() == query.EdgeKindCalls {
			from := string(e.From())
			callGraph[from] = append(callGraph[from], string(e.To()))
		}
	}

	// The procedure universe is every node that participates in the call graph
	// OR is itself classified (an isolated source/sink/sanitizer still gets a
	// summary). Ensure every procedure has a (possibly empty) call-graph entry so
	// TarjanSCC emits it as at least a singleton SCC.
	procSet := make(map[string]struct{})
	for caller, callees := range callGraph {
		procSet[caller] = struct{}{}
		for _, c := range callees {
			procSet[c] = struct{}{}
		}
	}
	for id := range genByID {
		procSet[id] = struct{}{}
	}
	for id := range sinkCatByID {
		procSet[id] = struct{}{}
	}
	for id := range killByID {
		procSet[id] = struct{}{}
	}
	for id := range procSet {
		if _, ok := callGraph[id]; !ok {
			callGraph[id] = nil
		}
	}
	// Sort adjacency lists for determinism.
	for k := range callGraph {
		sort.Strings(callGraph[k])
	}

	if len(procSet) == 0 {
		return sol, nil
	}

	// Build the per-procedure transfer functions. OutputLabels(P) =
	// (gen(P) ∪ ⋃_{callee} OutputLabels(callee)) \ kill(P). Monotone Union
	// composition over the finite-height LabelSet lattice guarantees termination
	// for direct and mutual recursion; the caps are only a safety net.
	procs := make(map[string]interproc.ProcBody, len(procSet))
	for id := range procSet {
		callees := callGraph[id]
		ks := killByID[id]
		procs[id] = interproc.ProcBody{
			ID:          id,
			Callees:     callees,
			InputLabels: append([]string(nil), genByID[id]...),
			Transfer:    makeTransfer(callees, ks),
		}
	}

	sccs := interproc.TarjanSCC(callGraph)

	solver := interproc.NewFixpointSolver(caps, wideningThreshold, interproc.NewSummaryCache(caps.MaxSummaryEntries))
	res, err := solver.Solve(ctx, procs, sccs)
	if err != nil {
		return Solution{}, fmt.Errorf("interproctaint: solve: %w", err)
	}
	sol.SCCs = res.SCCs

	// Derive the explicit, deterministic verdict from the solver outcome.
	outputByID := make(map[string]taint.LabelSet, len(res.Summaries))
	capped := len(res.Diagnostics) > 0
	for id, s := range res.Summaries {
		outputByID[id] = taint.NewLabelSet(s.OutputLabels...)
		if s.Approximate {
			capped = true
		}
	}
	if capped {
		sol.Verdict = VerdictCapped
		sol.CapKind = deriveCapKind(res.Diagnostics)
	}

	// Materialize summaries sorted by procedure ID (canonical, map-free order).
	ids := make([]string, 0, len(procSet))
	for id := range procSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ks := killByID[id]
		ps := ProcSummary{
			ProcID:       id,
			ProcName:     nameByID[id],
			OutputLabels: append([]string(nil), outputByID[id]...),
			KillAll:      ks.all,
			KillLabels:   append([]string(nil), ks.labels...),
			IsSource:     len(genByID[id]) > 0,
			Approximate:  res.Summaries[id].Approximate,
		}
		if _, isSink := sinkCatByID[id]; isSink {
			ps.IsSink = true
		}
		if ps.OutputLabels == nil {
			ps.OutputLabels = []string{}
		}
		sol.Summaries = append(sol.Summaries, ps)
	}

	// Answer cross-procedure source→sink queries from the solved relation.
	sol.Flows = computeFlows(ids, callGraph, genByID, killByID, sinkCatByID, nameByID, outputByID)
	return sol, nil
}

// makeTransfer builds the monotone taint transfer for a procedure: union of the
// procedure's own sourced labels with the output labels exposed by every callee,
// minus the labels the procedure sanitizes. Composition is Union (the lattice
// join), so re-entrant contributions through recursive / mutually-recursive SCCs
// only ever grow toward the bounded top, guaranteeing monotone convergence.
func makeTransfer(callees []string, ks killSpec) func([]string, func(string) interproc.Summary) []string {
	return func(input []string, calleeSummary func(string) interproc.Summary) []string {
		acc := taint.NewLabelSet(input...)
		for _, c := range callees {
			acc = acc.Union(taint.NewLabelSet(calleeSummary(c).OutputLabels...))
		}
		acc = applyKill(acc, ks)
		return []string(acc)
	}
}

// applyKill removes a procedure's sanitized labels from a label set.
func applyKill(ls taint.LabelSet, ks killSpec) taint.LabelSet {
	if ks.all {
		return nil
	}
	if len(ks.labels) == 0 {
		return ls
	}
	return ls.Remove(ks.labels)
}

// deriveCapKind returns the lexicographically-first known cap token that appears
// in any diagnostic, so the surfaced cap kind is deterministic across runs.
func deriveCapKind(diagnostics []string) string {
	for _, cap := range knownCaps {
		for _, d := range diagnostics {
			if containsToken(d, cap) {
				return cap
			}
		}
	}
	return "unknown"
}

func containsToken(s, token string) bool {
	return len(token) > 0 && indexOf(s, token) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// computeFlows enumerates cross-procedure source→sink flows from the solved
// relation. For each sink procedure whose solved output is non-empty, it finds
// the source procedures reachable along call edges whose generated labels survive
// to the sink, and records the canonical (shortest, lexicographic) call path.
func computeFlows(
	procIDs []string,
	callGraph interproc.CallGraph,
	genByID map[string][]string,
	killByID map[string]killSpec,
	sinkCatByID map[string]string,
	nameByID map[string]string,
	outputByID map[string]taint.LabelSet,
) []Flow {
	var flows []Flow
	for _, sinkID := range procIDs {
		cat, isSink := sinkCatByID[sinkID]
		if !isSink {
			continue
		}
		out := outputByID[sinkID]
		if out.Empty() {
			continue
		}
		// BFS over callees (the direction tainted return values travel up from a
		// deep source to the sink), tracking shortest-path parents.
		parent := map[string]string{sinkID: ""}
		order := []string{sinkID}
		for qi := 0; qi < len(order); qi++ {
			cur := order[qi]
			for _, next := range callGraph[cur] {
				if _, seen := parent[next]; seen {
					continue
				}
				parent[next] = cur
				order = append(order, next)
			}
		}
		// Visit reachable procedures in sorted order; emit one flow per source
		// procedure that contributes a surviving label to this sink.
		reachable := append([]string(nil), order...)
		sort.Strings(reachable)
		for _, srcID := range reachable {
			gen := taint.NewLabelSet(genByID[srcID]...)
			if gen.Empty() {
				continue
			}
			surviving := intersect(gen, out)
			if surviving.Empty() {
				continue
			}
			path := reconstructPath(sinkID, srcID, parent, nameByID)
			flows = append(flows, Flow{
				SourceID:     srcID,
				SourceName:   nameByID[srcID],
				SinkID:       sinkID,
				SinkName:     nameByID[sinkID],
				SinkCategory: cat,
				Labels:       []string(surviving),
				CallPath:     path,
			})
		}
	}
	sortFlows(flows)
	return flows
}

// reconstructPath walks the BFS parent map from sink to source, returning the
// procedure qualified names along the call path (sink first, source last).
func reconstructPath(sinkID, srcID string, parent map[string]string, nameByID map[string]string) []string {
	var rev []string
	cur := srcID
	for cur != "" {
		rev = append(rev, nameByID[cur])
		if cur == sinkID {
			break
		}
		cur = parent[cur]
	}
	// rev is source→…→sink; reverse to sink→…→source.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// intersect returns the sorted intersection of two label sets.
func intersect(a, b taint.LabelSet) taint.LabelSet {
	var out []string
	for _, l := range a {
		if b.Contains(l) {
			out = append(out, l)
		}
	}
	return taint.LabelSet(out)
}

// sortFlows orders flows canonically: by sink id, then source id, then label set.
func sortFlows(flows []Flow) {
	sort.Slice(flows, func(i, j int) bool {
		a, b := flows[i], flows[j]
		if a.SinkID != b.SinkID {
			return a.SinkID < b.SinkID
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return joinLabels(a.Labels) < joinLabels(b.Labels)
	})
}

func joinLabels(ls []string) string {
	out := ""
	for i, l := range ls {
		if i > 0 {
			out += ","
		}
		out += l
	}
	return out
}
