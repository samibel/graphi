package taint

import (
	"container/heap"
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// AnalyzerName is the dispatch key for the taint analyzer in the registry.
const AnalyzerName = "taint"

// Analyzer is the flow-sensitive taint analyzer. It is exported so the parent
// analysis package can wrap it with a thin adapter for registry dispatch,
// avoiding an import cycle (taint cannot import analysis).
type Analyzer struct {
	config    Config
	caps      Caps
	summaries SummaryProvider
}

// New creates a taint Analyzer with the given configuration, caps, and summary
// provider. Pass nil for summaries to use the NoOpSummaryProvider stub.
func New(cfg Config, caps Caps, summaries SummaryProvider) *Analyzer {
	if summaries == nil {
		summaries = NoOpSummaryProvider{}
	}
	return &Analyzer{config: cfg, caps: caps, summaries: summaries}
}

// Name returns the analyzer dispatch key.
func (t *Analyzer) Name() string { return AnalyzerName }

// Run executes the full taint analysis and returns the detailed TaintResult
// with all findings, provenance, and diagnostics.
func (t *Analyzer) Run(ctx context.Context, r query.Reader) (TaintResult, error) {
	return t.run(ctx, r)
}

// run is the core implementation.
func (t *Analyzer) run(ctx context.Context, r query.Reader) (TaintResult, error) {
	// Load all nodes and edges from the graph.
	nodes, err := r.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return TaintResult{}, fmt.Errorf("taint: load nodes: %w", err)
	}
	edges, err := r.Edges(ctx, graphstore.Query{})
	if err != nil {
		return TaintResult{}, fmt.Errorf("taint: load edges: %w", err)
	}

	// Index nodes by ID for fast lookup.
	nodeByID := make(map[model.NodeId]model.Node, len(nodes))
	for _, n := range nodes {
		nodeByID[n.ID()] = n
	}

	// Build forward adjacency (from → to) for def-use propagation.
	// Edge kinds relevant to taint: "calls", "references", "defines", "data_dep".
	type adjEntry struct {
		to   model.NodeId
		edge model.Edge
	}
	adj := make(map[model.NodeId][]adjEntry)
	for _, e := range edges {
		adj[e.From()] = append(adj[e.From()], adjEntry{to: e.To(), edge: e})
	}
	// Sort adjacency lists for deterministic traversal.
	for k := range adj {
		entries := adj[k]
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].to != entries[j].to {
				return entries[i].to < entries[j].to
			}
			return entries[i].edge.ID() < entries[j].edge.ID()
		})
		adj[k] = entries
	}

	// Classify nodes as sources, sinks, and sanitizers.
	type sourceInfo struct {
		label    string
		sourceID string
	}
	type sinkInfo struct {
		sinkID   string
		category string
	}
	sources := make(map[model.NodeId]sourceInfo)
	sinks := make(map[model.NodeId]sinkInfo)
	sanitizers := make(map[model.NodeId]SanitizerDef)

	for _, n := range nodes {
		if label, srcID := t.config.matchSource(n.Kind(), n.QualifiedName()); label != "" {
			sources[n.ID()] = sourceInfo{label: label, sourceID: srcID}
		}
		if sinkID, cat := t.config.matchSink(n.Kind(), n.QualifiedName()); sinkID != "" {
			sinks[n.ID()] = sinkInfo{sinkID: sinkID, category: cat}
		}
		if san, ok := t.config.matchSanitizer(n.Kind(), n.QualifiedName()); ok {
			sanitizers[n.ID()] = san
		}
	}

	// If no sources or sinks, return early. The candidate counts travel with the
	// result so the caller can distinguish "no sink symbols in the graph at all"
	// (a graph that cannot possibly match — honest no_sink_candidates) from a
	// genuine no-flow result (WP-04).
	if len(sources) == 0 || len(sinks) == 0 {
		return TaintResult{
			Findings:         []Finding{},
			ConfigHash:       t.config.ContentHash,
			SinkCandidates:   len(sinks),
			SourceCandidates: len(sources),
		}, nil
	}

	// Worklist-based forward propagation from all sources.
	// State: for each node, the label set that has reached it.
	// A node is re-visited only if new labels are discovered.
	type nodeState struct {
		labels LabelSet
		depth  int
		// parent tracks the predecessor for path reconstruction.
		parent model.NodeId
		edge   *model.Edge
	}

	// Per-source propagation to find all source→sink paths.
	var allFindings []Finding
	var diagnostics []string
	totalWork := 0

	for srcID, srcInfo := range sources {
		select {
		case <-ctx.Done():
			return TaintResult{}, ctx.Err()
		default:
		}

		// Initialize worklist with the source node.
		initialLabels := NewLabelSet(srcInfo.label)
		wl := &workList{}
		heap.Init(wl)
		heap.Push(wl, workItem{
			nodeID: srcID,
			labels: initialLabels,
			depth:  0,
		})

		// State map: nodeID → best label set and path info.
		visited := make(map[model.NodeId]LabelSet)
		parentMap := make(map[model.NodeId]model.NodeId)
		parentEdge := make(map[model.NodeId]model.Edge)
		depthMap := make(map[model.NodeId]int)
		visited[srcID] = initialLabels
		depthMap[srcID] = 0
		nodesVisited := 1

		for wl.Len() > 0 {
			select {
			case <-ctx.Done():
				return TaintResult{}, ctx.Err()
			default:
			}

			item := heap.Pop(wl).(workItem)
			totalWork++

			// Check caps.
			if hit, exceeded := t.caps.exceeded(nodesVisited, totalWork, item.depth); exceeded {
				diagnostics = append(diagnostics, fmt.Sprintf(
					"cap exceeded during propagation from source %s: %s=%d (limit %d)",
					srcID, hit.Cap, hit.Value, hit.Limit,
				))
				// Mark any findings from this source as incomplete.
				for i := range allFindings {
					if allFindings[i].SourceID == srcID {
						allFindings[i].Incomplete = true
						hitCopy := hit
						allFindings[i].CapHit = &hitCopy
					}
				}
				break
			}

			currentLabels := item.labels

			// Check if this node is a sink.
			if si, isSink := sinks[item.nodeID]; isSink && !currentLabels.Empty() {
				// Reconstruct path from source to this sink.
				path := t.reconstructPath(item.nodeID, srcID, parentMap, parentEdge, depthMap, visited, nodeByID)
				srcNode := nodeByID[srcID]
				sinkNode := nodeByID[item.nodeID]

				finding := Finding{
					SourceID:     srcID,
					SourceName:   srcNode.QualifiedName(),
					SourceDefID:  srcInfo.sourceID,
					SinkID:       item.nodeID,
					SinkName:     sinkNode.QualifiedName(),
					SinkDefID:    si.sinkID,
					SinkCategory: si.category,
					Labels:       currentLabels,
					Path:         path,
					PathLength:   len(path),
					ConfigHash:   t.config.ContentHash,
				}
				allFindings = append(allFindings, finding)
				// Continue propagation — there may be other sinks downstream.
			}

			// Propagate to successors.
			for _, succ := range adj[item.nodeID] {
				nextLabels := currentLabels

				// Apply sanitizer if the successor is a sanitizer node.
				if san, isSanitizer := sanitizers[succ.to]; isSanitizer {
					nextLabels = nextLabels.Remove(san.RemoveLabels)
					if nextLabels.Empty() {
						continue // all taint removed; stop this path
					}
				}

				// Apply interprocedural summary if available.
				succNode, exists := nodeByID[succ.to]
				if exists && t.summaries.HasSummary(succNode.QualifiedName()) {
					nextLabels = t.summaries.TransferLabels(succNode.QualifiedName(), nextLabels)
					if nextLabels.Empty() {
						continue
					}
				}

				// Check if we've seen this node with these exact labels.
				if prev, seen := visited[succ.to]; seen && isSubset(nextLabels, prev) {
					continue // no new information
				}

				// Merge labels: union of existing + new.
				if prev, seen := visited[succ.to]; seen {
					nextLabels = prev.Union(nextLabels)
				}

				visited[succ.to] = nextLabels
				if _, seen := depthMap[succ.to]; !seen {
					nodesVisited++
				}
				depthMap[succ.to] = item.depth + 1
				parentMap[succ.to] = item.nodeID
				parentEdge[succ.to] = succ.edge

				heap.Push(wl, workItem{
					nodeID: succ.to,
					labels: nextLabels,
					depth:  item.depth + 1,
				})
			}
		}
	}

	// Deduplicate findings (same source→sink pair, keep shortest path).
	allFindings = deduplicateFindings(allFindings)
	sortFindings(allFindings)

	return TaintResult{
		Findings:         allFindings,
		Diagnostics:      diagnostics,
		ConfigHash:       t.config.ContentHash,
		SinkCandidates:   len(sinks),
		SourceCandidates: len(sources),
	}, nil
}

// reconstructPath builds the PathStep slice from source to the given node
// by walking the parent map backwards.
func (t *Analyzer) reconstructPath(
	target, source model.NodeId,
	parentMap map[model.NodeId]model.NodeId,
	parentEdge map[model.NodeId]model.Edge,
	depthMap map[model.NodeId]int,
	visited map[model.NodeId]LabelSet,
	nodeByID map[model.NodeId]model.Node,
) []PathStep {
	// Walk backwards from target to source.
	var reversePath []model.NodeId
	cur := target
	seen := make(map[model.NodeId]struct{}) // cycle guard
	for cur != source {
		if _, loop := seen[cur]; loop {
			break
		}
		seen[cur] = struct{}{}
		reversePath = append(reversePath, cur)
		parent, ok := parentMap[cur]
		if !ok {
			break
		}
		cur = parent
	}
	reversePath = append(reversePath, source)

	// Reverse to get source→target order.
	path := make([]PathStep, len(reversePath))
	for i, nid := range reversePath {
		idx := len(reversePath) - 1 - i
		n := nodeByID[nid]
		step := PathStep{
			NodeID:        nid,
			Kind:          n.Kind(),
			QualifiedName: n.QualifiedName(),
			SourcePath:    n.SourcePath(),
			Line:          n.Line(),
			Column:        n.Column(),
			Labels:        visited[nid],
		}
		// Attach edge provenance for non-source steps.
		if edge, ok := parentEdge[nid]; ok {
			step.EdgeKind = edge.Kind()
			step.Tier = edge.Tier()
			step.Confidence = edge.Confidence()
			step.Reason = edge.Reason()
			step.DerivationRule = "taint_propagation"
		} else if nid == source {
			step.DerivationRule = "taint_source"
		}
		path[idx] = step
	}
	return path
}

// isSubset reports whether all labels in sub are contained in super.
func isSubset(sub, super LabelSet) bool {
	for _, l := range sub {
		if !super.Contains(l) {
			return false
		}
	}
	return true
}

// deduplicateFindings keeps only the shortest path for each unique
// (sourceID, sinkID, labels) combination.
func deduplicateFindings(findings []Finding) []Finding {
	type key struct {
		src    model.NodeId
		sink   model.NodeId
		labels string
	}
	best := make(map[key]Finding)
	for _, f := range findings {
		k := key{src: f.SourceID, sink: f.SinkID, labels: f.Labels.String()}
		if existing, ok := best[k]; !ok || f.PathLength < existing.PathLength {
			best[k] = f
		}
	}
	// Extract values in deterministic order.
	keys := make([]key, 0, len(best))
	for k := range best {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].src != keys[j].src {
			return keys[i].src < keys[j].src
		}
		if keys[i].sink != keys[j].sink {
			return keys[i].sink < keys[j].sink
		}
		return keys[i].labels < keys[j].labels
	})
	out := make([]Finding, 0, len(keys))
	for _, k := range keys {
		out = append(out, best[k])
	}
	return out
}

// workItem is a single entry in the priority-queue worklist.
type workItem struct {
	nodeID model.NodeId
	labels LabelSet
	depth  int
	index  int // heap index
}

// workList is a min-heap priority queue ordered by (depth ASC, nodeID ASC)
// for deterministic traversal order.
type workList []workItem

func (w workList) Len() int { return len(w) }

func (w workList) Less(i, j int) bool {
	if w[i].depth != w[j].depth {
		return w[i].depth < w[j].depth
	}
	return w[i].nodeID < w[j].nodeID
}

func (w workList) Swap(i, j int) {
	w[i], w[j] = w[j], w[i]
	w[i].index = i
	w[j].index = j
}

func (w *workList) Push(x any) {
	item := x.(workItem)
	item.index = len(*w)
	*w = append(*w, item)
}

func (w *workList) Pop() any {
	old := *w
	n := len(old)
	item := old[n-1]
	old[n-1] = workItem{} // avoid memory leak
	item.index = -1
	*w = old[:n-1]
	return item
}
