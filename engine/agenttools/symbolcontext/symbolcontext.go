// Package symbolcontext implements the symbol_context agent tool (labs): the
// unified single-call symbol view. One invocation returns the definition site
// (optionally with a token-budgeted source snippet), the type-hierarchy
// relations, callers/callees/references, the test files that exercise the
// symbol (bounded reverse walk — never a whole-graph scan), and a change-risk
// level consistent with change_risk. It never guesses: ambiguous references
// yield candidates, unresolved ones an empty result.
//
// Results are sectioned by rank bands (rank = band<<20 + score) so the
// contract's canonical item order (rank desc, ref id asc) renders sections
// top-down and the item cap truncates the least important band first.
//
// Git history ("recent changes") is deliberately out of scope; it belongs to
// the git-intelligence epic.
package symbolcontext

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/agenttools/shape"
	pathclass "github.com/samibel/graphi/engine/classify"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/query"
)

const tool = "symbol_context"

// MethodVersion stamps the assembly logic version into the summary so stored
// outputs can be tied to the algorithm that produced them.
const MethodVersion = "symbol_context/1"

// Rank bands. rank = band<<20 + score with score < 1<<20, so bands render in
// order under the contract's rank-desc sort and the item cap drops the
// lowest-value sections first.
const (
	bandDefinition = 9
	bandSnippet    = 8
	bandHierarchy  = 7
	bandCallers    = 6
	bandCallees    = 5
	bandReferences = 4
	bandTests      = 3
	bandRisk       = 2
)

// Defaults and walk bounds. The reverse test walk is bounded three ways —
// per-node edge limit, global visited-node budget, and depth clamp — so a
// high-fan-in symbol can never trigger an unbounded read (CORE-02).
const (
	DefaultDepth       = 2
	MaxDepth           = 3
	DefaultTokenBudget = 700
	snippetContext     = 8 // context lines around the definition (captures doc comments)
	edgesPerNode       = 64
	walkNodeBudget     = 512
)

// Params carries the symbol_context inputs.
type Params struct {
	// Ref is the symbol reference: node id, repo-relative path, qualified
	// name, or search text (strict resolution — no guessing).
	Ref string
	// Depth bounds the reverse test walk; 0 selects DefaultDepth, values are
	// clamped to [1, MaxDepth].
	Depth int
	// MaxItems caps the item list (0 selects shape.DefaultMaxItems).
	MaxItems int
	// TokenBudget bounds the definition snippet; 0 selects
	// DefaultTokenBudget, negative disables the snippet.
	TokenBudget int
	// Deps are the shared engine services.
	Deps resolve.Deps
	// Reader supplies source bytes for the definition snippet. Nil disables
	// snippets (reported in limits.next, never an error).
	Reader enginecontext.Reader
}

func (p Params) depth() int {
	d := p.Depth
	if d == 0 {
		d = DefaultDepth
	}
	if d < 1 {
		d = 1
	}
	if d > MaxDepth {
		d = MaxDepth
	}
	return d
}

func (p Params) tokenBudget() int {
	if p.TokenBudget == 0 {
		return DefaultTokenBudget
	}
	if p.TokenBudget < 0 {
		return 0
	}
	return p.TokenBudget
}

// relationSpec drives the uniform relation collection so every relation shapes
// identically. useTo selects which endpoint of the edge is "the other symbol".
type relationSpec struct {
	band  int
	name  string
	useTo bool
	fetch func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error)
}

var relationSpecs = []relationSpec{
	{bandHierarchy, "implementer", false, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Implementers(ctx, id)
	}},
	{bandHierarchy, "implements", true, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Implements(ctx, id)
	}},
	{bandHierarchy, "override", false, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Overrides(ctx, id)
	}},
	{bandHierarchy, "subtype", false, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Subtypes(ctx, id)
	}},
	{bandHierarchy, "supertype", true, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Supertypes(ctx, id)
	}},
	{bandCallers, "caller", false, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Callers(ctx, id)
	}},
	{bandCallees, "callee", true, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.Callees(ctx, id)
	}},
	{bandReferences, "reference", false, func(ctx context.Context, svc *query.Service, id model.NodeId) (query.Result, error) {
		return svc.References(ctx, id)
	}},
}

// Context resolves ref and assembles the unified symbol view in the C1
// contract shape.
func Context(ctx context.Context, p Params) (*contract.Result, error) {
	if p.Ref == "" {
		return nil, fmt.Errorf("missing symbol reference")
	}
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}

	res, err := resolve.Strict(ctx, p.Deps, p.Ref)
	if err != nil {
		return nil, err
	}
	if res.Ambiguous() {
		return shape.Finish(shape.Ambiguous(tool, p.Ref, res.Candidates), p.MaxItems)
	}
	if !res.Resolved() {
		return shape.Empty(tool, p.Ref), nil
	}
	if len(res.Nodes) > 1 {
		// A file reference resolves to many nodes; symbol_context is about ONE
		// symbol, so file-level nodes become candidates, not a guess.
		cands := make([]resolve.Candidate, 0, len(res.Nodes))
		for _, n := range res.Nodes {
			cands = append(cands, resolve.Candidate{Node: n})
		}
		return shape.Finish(shape.Ambiguous(tool, p.Ref, cands), p.MaxItems)
	}
	node := res.Nodes[0]

	ev := shape.NewEvidenceSet()
	tally := shape.TierTally{}
	var nextHint string

	// Band 9: definition.
	defEv := ev.Add(node.SourcePath(), node.Line(), "definition")
	items := []contract.Item{{
		RefID:          string(node.ID()),
		Rank:           bandDefinition << 20,
		Reason:         fmt.Sprintf("definition: %s %s (%s:%d)", node.Kind(), node.QualifiedName(), node.SourcePath(), node.Line()),
		EvidenceRefIDs: []string{defEv},
	}}

	// Band 8: token-budgeted definition snippet (with leading context lines so
	// a doc comment above the declaration is captured).
	if snippetItem, hint := snippetSection(node, p, ev); snippetItem != nil {
		items = append(items, *snippetItem)
	} else if hint != "" {
		nextHint = hint
	}

	// Bands 7..4: hierarchy and call/reference relations.
	counts := map[string]int{}
	for _, spec := range relationSpecs {
		result, err := spec.fetch(ctx, p.Deps.Query, node.ID())
		if err != nil {
			return nil, err
		}
		counts[spec.name] = len(result.Edges)
		tally.CountResultEdges(result.Edges)
		items = append(items, relationItems(spec, result, ev)...)
	}

	// Bands 3 and 2: bounded reverse walk for covering tests, first hop reused
	// for the risk classification.
	walk, err := reverseWalk(ctx, p.Deps.Query, node, p.depth())
	if err != nil {
		return nil, err
	}
	for _, e := range walk.consumedEdges {
		tally.Count(e.Tier())
	}
	items = append(items, testItems(walk, ev)...)

	level := risk.LevelFor(walk.fanIn, len(walk.dependentFiles))
	items = append(items, contract.Item{
		RefID:          "risk",
		Rank:           bandRisk << 20,
		Reason:         fmt.Sprintf("risk: %s — fan-in %d, %d dependent file(s)", level, walk.fanIn, len(walk.dependentFiles)),
		EvidenceRefIDs: []string{defEv},
	})

	hierarchyCount := counts["implementer"] + counts["implements"] + counts["override"] + counts["subtype"] + counts["supertype"]
	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("%s %s defined at %s:%d — %d callers, %d callees, %d references, %d hierarchy relation(s), %d test file(s), risk %s (resolved via %s; %s)",
			node.Kind(), node.QualifiedName(), node.SourcePath(), node.Line(),
			counts["caller"], counts["callee"], counts["reference"], hierarchyCount,
			len(walk.testFiles), level, res.Method, MethodVersion),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("confirmed", "definition_only"),
	}
	out, err := shape.Finish(r, p.MaxItems)
	if err != nil {
		return nil, err
	}
	if walk.truncated {
		out.Limits.Truncated = true
		if out.Outcome == contract.OutcomeFound {
			out.Outcome = contract.OutcomePartial
		}
	}
	if out.Limits.Next == "" {
		out.Limits.Next = nextHint
	}
	return out, nil
}

// snippetSection assembles the definition snippet. It returns the item to
// append (nil when no snippet was included) and a limits.next hint explaining
// an omission.
func snippetSection(node model.Node, p Params, ev *shape.EvidenceSet) (*contract.Item, string) {
	budget := p.tokenBudget()
	if budget <= 0 {
		return nil, ""
	}
	if p.Reader == nil {
		return nil, "no source reader on this surface; run from the repository root to include the definition snippet"
	}
	cand := enginecontext.Candidate{
		Path:      node.SourcePath(),
		StartLine: node.Line(),
		EndLine:   node.Line(),
		Symbol:    node.QualifiedName(),
		Kind:      node.Kind(),
	}
	readable := enginecontext.FilterReadable(p.Reader, []enginecontext.Candidate{cand})
	if len(readable) == 0 {
		return nil, fmt.Sprintf("definition source %s not readable from this working directory; run from the repository root to include the snippet", node.SourcePath())
	}
	bundle, err := enginecontext.Assemble(tool+":"+node.QualifiedName(), readable,
		enginecontext.Options{Budget: budget, ContextLines: snippetContext}, p.Reader)
	if err != nil || len(bundle.Snippets) == 0 {
		return nil, fmt.Sprintf("raise token_budget (>%d) to include the definition snippet", budget)
	}
	snip := bundle.Snippets[0]
	span := fmt.Sprintf("%d-%d", snip.Citation.StartLine, snip.Citation.EndLine)
	snipEv := ev.AddSnippet(snip.Citation.Path, snip.Citation.StartLine, "snippet", span, snip.Text)
	return &contract.Item{
		RefID:          "snippet",
		Rank:           bandSnippet << 20,
		Reason:         fmt.Sprintf("snippet: %s lines %s (%d tokens of %d budget)", snip.Citation.Path, span, snip.Tokens, budget),
		EvidenceRefIDs: []string{snipEv},
	}, ""
}

// relationItems shapes one relation's result rows into its band, scored by the
// incident edge confidence so higher-signal rows survive the item cap first.
func relationItems(spec relationSpec, result query.Result, ev *shape.EvidenceSet) []contract.Item {
	nodesByID := make(map[string]query.ResultNode, len(result.Nodes))
	for _, n := range result.Nodes {
		nodesByID[string(n.ID)] = n
	}
	items := make([]contract.Item, 0, len(result.Edges))
	for _, e := range result.Edges {
		otherID := string(e.From)
		if spec.useTo {
			otherID = string(e.To)
		}
		other, ok := nodesByID[otherID]
		if !ok {
			continue
		}
		var evIDs []string
		for _, ref := range e.Evidence {
			evIDs = append(evIDs, ev.AddRef(ref, spec.name))
		}
		items = append(items, contract.Item{
			RefID:          otherID,
			Rank:           spec.band<<20 + int(e.Confidence*1000),
			Reason:         fmt.Sprintf("%s: %s %s (%s:%d) [%s]", spec.name, other.Kind, other.QualifiedName, other.SourcePath, other.Line, e.Tier),
			EvidenceRefIDs: evIDs,
		})
	}
	return items
}

// walkResult carries what the bounded reverse walk found.
type walkResult struct {
	// testFiles maps test source path → shallowest hop depth it was found at.
	testFiles map[string]int
	// testNodes are the test symbols, sorted by node id.
	testNodes []query.ResultNode
	// testDepth maps test node id → hop depth.
	testDepth map[model.NodeId]int
	// fanIn and dependentFiles mirror change_risk's hop-1 evidence.
	fanIn          int
	dependentFiles map[string]struct{}
	consumedEdges  []model.Edge
	truncated      bool
}

// reverseWalk performs the bounded inbound walk: hop 1 reads ALL inbound edge
// kinds (risk parity with change_risk's fan-in), deeper hops follow only
// calls/references. Every read is bounded per node, the visited set is bounded
// globally, and iteration orders are canonical, so identical graphs yield
// identical results.
func reverseWalk(ctx context.Context, svc *query.Service, node model.Node, depth int) (walkResult, error) {
	out := walkResult{
		testFiles:      map[string]int{},
		testDepth:      map[model.NodeId]int{},
		dependentFiles: map[string]struct{}{},
	}
	bounded, ok := svc.Reader().(graphstore.BoundedGraphLookup)
	lookup, lok := svc.Reader().(graphstore.GraphLookup)
	if !ok || !lok {
		// Both shipped backends implement the bounded ports; a reader that does
		// not simply yields no test/risk evidence rather than a full scan.
		return out, nil
	}

	visited := map[model.NodeId]struct{}{node.ID(): {}}
	frontier := []model.NodeId{node.ID()}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		// Collect this hop's inbound edges in canonical frontier order.
		var hopEdges []model.Edge
		for _, id := range frontier {
			var (
				edges []model.Edge
				trunc bool
				err   error
			)
			if d == 1 {
				edges, trunc, err = bounded.IncomingBounded(ctx, id, edgesPerNode)
			} else {
				edges, trunc, err = bounded.IncomingBounded(ctx, id, edgesPerNode, query.EdgeKindCalls, query.EdgeKindReferences)
			}
			if err != nil {
				return walkResult{}, err
			}
			out.truncated = out.truncated || trunc
			hopEdges = append(hopEdges, edges...)
		}
		sort.Slice(hopEdges, func(i, j int) bool { return hopEdges[i].ID() < hopEdges[j].ID() })

		// Hydrate the hop's neighbor nodes in one batch.
		var neighborIDs []model.NodeId
		seen := map[model.NodeId]struct{}{}
		for _, e := range hopEdges {
			from := e.From()
			if _, dup := seen[from]; dup {
				continue
			}
			seen[from] = struct{}{}
			neighborIDs = append(neighborIDs, from)
		}
		neighbors, err := lookup.NodesByID(ctx, neighborIDs)
		if err != nil {
			return walkResult{}, err
		}
		nodeByID := make(map[model.NodeId]model.Node, len(neighbors))
		for _, n := range neighbors {
			nodeByID[n.ID()] = n
		}

		var next []model.NodeId
		for _, e := range hopEdges {
			from := e.From()
			if from == node.ID() {
				continue // self-recursion counts neither for risk nor the walk
			}
			if d == 1 {
				out.fanIn++
			}
			neighbor, hydrated := nodeByID[from]
			if !hydrated || neighbor.SourcePath() == "" {
				continue // referential drift or external node: not walkable
			}
			if d == 1 && neighbor.SourcePath() != node.SourcePath() {
				out.dependentFiles[neighbor.SourcePath()] = struct{}{}
			}
			if d > 1 || e.Kind() == query.EdgeKindCalls || e.Kind() == query.EdgeKindReferences {
				out.consumedEdges = append(out.consumedEdges, e)
			}
			if _, done := visited[from]; done {
				continue
			}
			if len(visited) >= walkNodeBudget {
				out.truncated = true
				continue
			}
			visited[from] = struct{}{}
			if pathclass.IsTestPath(neighbor.SourcePath()) {
				if _, known := out.testFiles[neighbor.SourcePath()]; !known {
					out.testFiles[neighbor.SourcePath()] = d
				}
				out.testDepth[neighbor.ID()] = d
				out.testNodes = append(out.testNodes, query.ResultNode{
					ID:            neighbor.ID(),
					Kind:          neighbor.Kind(),
					QualifiedName: neighbor.QualifiedName(),
					SourcePath:    neighbor.SourcePath(),
					Line:          neighbor.Line(),
				})
				continue // a test node terminates its branch of the walk
			}
			next = append(next, from)
		}
		frontier = next
	}
	sort.Slice(out.testNodes, func(i, j int) bool { return out.testNodes[i].ID < out.testNodes[j].ID })
	return out, nil
}

// testItems shapes the walk's test symbols, scored so shallower (more direct)
// tests outrank deeper ones.
func testItems(walk walkResult, ev *shape.EvidenceSet) []contract.Item {
	items := make([]contract.Item, 0, len(walk.testNodes))
	for _, n := range walk.testNodes {
		d := walk.testDepth[n.ID]
		evID := ev.Add(n.SourcePath, n.Line, "test")
		items = append(items, contract.Item{
			RefID:          string(n.ID),
			Rank:           bandTests<<20 + (MaxDepth-d+1)*100,
			Reason:         fmt.Sprintf("test: %s %s (%s:%d) [depth %d]", n.Kind, n.QualifiedName, n.SourcePath, n.Line, d),
			EvidenceRefIDs: []string{evID},
		})
	}
	return items
}
