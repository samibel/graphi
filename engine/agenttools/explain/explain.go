// Package explain implements the explain_symbol agent tool: a compact,
// cited, confidence-aware identity summary for one symbol, backed by the
// shared query and search services. It never returns source bodies and never
// guesses: an ambiguous reference yields candidates, an unresolved one yields
// an empty result with next-step hints.
package explain

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/query"
)

const tool = "explain_symbol"

// relationSpec drives the uniform caller/callee/reference collection so the
// three relations shape identically.
type relationSpec struct {
	name  string // item reason prefix and summary noun
	fetch func(ctx context.Context, svc *query.Service) (query.Result, error)
}

// Explain resolves ref and returns its definition site plus a bounded,
// cited summary of callers, callees, and references in the C1 contract shape.
func Explain(ctx context.Context, deps resolve.Deps, ref string, maxItems int) (*contract.Result, error) {
	if ref == "" {
		return nil, fmt.Errorf("missing symbol reference")
	}
	if !deps.Available() {
		return shape.Unavailable(tool), nil
	}

	res, err := resolve.Strict(ctx, deps, ref)
	if err != nil {
		return nil, err
	}
	if res.Ambiguous() {
		return shape.Finish(shape.Ambiguous(tool, ref, res.Candidates), maxItems)
	}
	if !res.Resolved() {
		return shape.Empty(tool, ref), nil
	}

	// A file reference resolves to many nodes; explain_symbol is about ONE
	// symbol, so several file-level nodes are candidates, not a guess.
	if len(res.Nodes) > 1 {
		cands := make([]resolve.Candidate, 0, len(res.Nodes))
		for _, n := range res.Nodes {
			cands = append(cands, resolve.Candidate{Node: n})
		}
		return shape.Finish(shape.Ambiguous(tool, ref, cands), maxItems)
	}
	node := res.Nodes[0]

	specs := []relationSpec{
		{"caller", func(ctx context.Context, svc *query.Service) (query.Result, error) {
			return svc.Callers(ctx, node.ID())
		}},
		{"callee", func(ctx context.Context, svc *query.Service) (query.Result, error) {
			return svc.Callees(ctx, node.ID())
		}},
		{"reference", func(ctx context.Context, svc *query.Service) (query.Result, error) {
			return svc.References(ctx, node.ID())
		}},
	}

	ev := shape.NewEvidenceSet()
	defEv := ev.Add(node.SourcePath(), node.Line(), "definition")
	items := []contract.Item{{
		RefID:          string(node.ID()),
		Rank:           1 << 20, // definition always outranks relations
		Reason:         fmt.Sprintf("definition: %s %s (%s:%d)", node.Kind(), node.QualifiedName(), node.SourcePath(), node.Line()),
		EvidenceRefIDs: []string{defEv},
	}}

	tally := shape.TierTally{}
	counts := map[string]int{}
	for _, spec := range specs {
		result, err := spec.fetch(ctx, deps.Query)
		if err != nil {
			return nil, err
		}
		counts[spec.name] = len(result.Edges)
		tally.CountResultEdges(result.Edges)
		items = append(items, relationItems(spec.name, result, ev)...)
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("%s %s defined at %s:%d — %d callers, %d callees, %d references (resolved via %s)",
			node.Kind(), node.QualifiedName(), node.SourcePath(), node.Line(),
			counts["caller"], counts["callee"], counts["reference"], res.Method),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("confirmed", "definition_only"),
	}
	return shape.Finish(r, maxItems)
}

// relationItems shapes one relation's result rows. Node rows are ranked by
// the incident edge confidence so higher-signal relations surface first under
// the item cap.
func relationItems(name string, result query.Result, ev *shape.EvidenceSet) []contract.Item {
	nodesByID := make(map[string]query.ResultNode, len(result.Nodes))
	for _, n := range result.Nodes {
		nodesByID[string(n.ID)] = n
	}
	items := make([]contract.Item, 0, len(result.Edges))
	for _, e := range result.Edges {
		otherID := string(e.From)
		if name == "callee" {
			otherID = string(e.To)
		}
		other, ok := nodesByID[otherID]
		if !ok {
			continue
		}
		var evIDs []string
		for _, ref := range e.Evidence {
			evIDs = append(evIDs, ev.AddRef(ref, name))
		}
		items = append(items, contract.Item{
			RefID:          otherID,
			Rank:           int(e.Confidence * 1000),
			Reason:         fmt.Sprintf("%s: %s %s (%s:%d) [%s]", name, other.Kind, other.QualifiedName, other.SourcePath, other.Line, e.Tier),
			EvidenceRefIDs: evIDs,
		})
	}
	return items
}
