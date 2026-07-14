package scenario

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/brief"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/explain"
	"github.com/samibel/graphi/engine/agenttools/related"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/diagnostic"
	"github.com/samibel/graphi/engine/query"
)

// FixtureEngine executes scenario operations against the shared engine
// services built over a fixture graph store. It implements both Engine (the
// legacy structural/search operations) and ContractInvoker (the EP-020
// agent-tool operations), so one engine value serves every scenario.
//
// It is read-only by construction: it consumes only the query service's
// Reader and the lexical search service.
type FixtureEngine struct {
	// Deps carries the query and search services over the fixture store.
	Deps resolve.Deps
	// RepoRoot is reported to agent_brief as the project root (optional).
	RepoRoot string
	// ProjectName is reported to agent_brief as the project name (optional).
	ProjectName string

	// analysisOnce/analysisSvc lazily build the shared analysis dispatch
	// service (impact) over the same read-only reader.
	analysisOnce sync.Once
	analysisSvc  *analysis.Service
}

// NewFixtureEngine builds a FixtureEngine over the given engine services.
func NewFixtureEngine(deps resolve.Deps) *FixtureEngine {
	return &FixtureEngine{Deps: deps}
}

var _ Engine = (*FixtureEngine)(nil)
var _ ContractInvoker = (*FixtureEngine)(nil)

// Invoke runs a legacy operation (definition/references/callers/search) and
// returns evidence lines. The first line may carry an "outcome:<value>"
// marker consumed by the runner as the tool-level outcome.
func (e *FixtureEngine) Invoke(operation string, args map[string]string) ([]string, *float64, error) {
	ctx := context.Background()
	switch operation {
	case OpSearch:
		return e.invokeSearch(ctx, args)
	case OpDefinition, OpReferences, OpCallers, OpCallees, OpNeighborhood:
		return e.invokeStructural(ctx, operation, args)
	case OpImpact:
		return e.invokeImpact(ctx, args)
	case OpIndex:
		return e.invokeIndex(ctx)
	case OpDiagnose:
		return e.invokeDiagnose(ctx, args)
	default:
		if IsAgentToolOp(operation) {
			return nil, nil, fmt.Errorf("scenario: agent-tool operation %q requires the contract seam", operation)
		}
		return nil, nil, fmt.Errorf("scenario: unknown operation %q", operation)
	}
}

// invokeDiagnose runs the engine diagnostics over the fixture store and
// renders the outcome, findings, and summary counters as evidence lines so
// scenarios can anchor on suppression behavior:
//
//	outcome:reported
//	diag <code> <file>:<line> confidence=<c> suppression=<cat> occ=<n> actions=<a,b>
//	summary shown=<n> analyzed=<n> dedup=<n>
//	suppressed <category>=<n>
//
// Args: all=true disables the default gates (--all), explain_suppressed=true
// keeps suppressed findings visible, kinds=a,b selects analyzers.
func (e *FixtureEngine) invokeDiagnose(ctx context.Context, args map[string]string) ([]string, *float64, error) {
	if !e.Deps.Available() {
		return nil, nil, fmt.Errorf("scenario: query service unavailable")
	}
	var kinds []string
	if raw := args["kinds"]; raw != "" {
		for _, k := range strings.Split(raw, ",") {
			if k = strings.TrimSpace(k); k != "" {
				kinds = append(kinds, k)
			}
		}
	}
	opts := diagnostic.DiagnoseOptions{
		All:               args["all"] == "true",
		ExplainSuppressed: args["explain_suppressed"] == "true",
	}
	res, err := diagnostic.DiagnoseWithOptions(ctx, e.Deps.Query.Reader(), kinds, opts)
	if err != nil {
		return nil, nil, err
	}

	lines := []string{outcomeMarker + string(res.Outcome)}
	for _, d := range res.Diagnostics {
		actions := make([]string, 0, len(d.Actions))
		for _, a := range d.Actions {
			actions = append(actions, string(a.Kind))
		}
		sort.Strings(actions)
		occ := d.OccurrenceCount
		if occ < 1 {
			occ = 1
		}
		lines = append(lines, fmt.Sprintf("diag %s %s:%d confidence=%s suppression=%s occ=%d actions=%s",
			d.Code, d.File, d.Line, d.Confidence, d.Suppression, occ, strings.Join(actions, ",")))
	}
	lines = append(lines, fmt.Sprintf("summary shown=%d analyzed=%d dedup=%d",
		res.Summary.Shown, res.Summary.TotalAnalyzed, res.Summary.DedupCollapsed))
	cats := make([]string, 0, len(res.Summary.SuppressedByCategory))
	for cat := range res.Summary.SuppressedByCategory {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		lines = append(lines, fmt.Sprintf("suppressed %s=%d", cat, res.Summary.SuppressedByCategory[cat]))
	}
	return lines, nil, nil
}

func (e *FixtureEngine) invokeSearch(ctx context.Context, args map[string]string) ([]string, *float64, error) {
	if e.Deps.Search == nil {
		return nil, nil, fmt.Errorf("scenario: search service unavailable")
	}
	limit := intArg(args, "limit", 25)
	resp, err := e.Deps.Search.Search(ctx, args["query"], limit)
	if err != nil {
		return nil, nil, err
	}
	outcome := contract.OutcomeEmpty
	if len(resp.Matches) > 0 {
		outcome = contract.OutcomeFound
	}
	lines := []string{outcomeMarker + string(outcome)}
	for _, m := range resp.Matches {
		lines = append(lines, fmt.Sprintf("%s %s %s:%d", m.Kind, m.QualifiedName, m.SourcePath, m.Line))
	}
	return lines, nil, nil
}

func (e *FixtureEngine) invokeStructural(ctx context.Context, operation string, args map[string]string) ([]string, *float64, error) {
	id, early, err := e.resolveTarget(ctx, operation, args)
	if early != nil || err != nil {
		return early, nil, err
	}

	var qr query.Result
	switch operation {
	case OpDefinition:
		qr, err = e.Deps.Query.Definition(ctx, id)
	case OpReferences:
		qr, err = e.Deps.Query.References(ctx, id)
	case OpCallers:
		qr, err = e.Deps.Query.Callers(ctx, id)
	case OpCallees:
		qr, err = e.Deps.Query.Callees(ctx, id)
	case OpNeighborhood:
		qr, err = e.Deps.Query.Neighborhood(ctx, id, intArg(args, "depth", 1))
	}
	if err != nil {
		return nil, nil, err
	}
	lines := []string{outcomeMarker + string(qr.Outcome)}
	for _, n := range qr.Nodes {
		lines = append(lines, fmt.Sprintf("%s %s %s:%d", n.Kind, n.QualifiedName, n.SourcePath, n.Line))
	}
	for _, ed := range qr.Edges {
		lines = append(lines, fmt.Sprintf("edge %s %s->%s [%s]", ed.Kind, ed.From, ed.To, ed.Tier))
	}
	return lines, nil, nil
}

// resolveTarget resolves the scenario's symbol argument to a single node id.
// When resolution ends early (ambiguous or not-found), the rendered evidence
// lines are returned as early and the caller passes them through unchanged.
func (e *FixtureEngine) resolveTarget(ctx context.Context, operation string, args map[string]string) (id model.NodeId, early []string, err error) {
	if !e.Deps.Available() {
		return "", nil, fmt.Errorf("scenario: query service unavailable")
	}
	ref := firstArg(args, "symbol", "ref", "query")
	if ref == "" {
		return "", nil, fmt.Errorf("scenario: %s needs a symbol argument", operation)
	}
	res, err := resolve.Strict(ctx, e.Deps, ref)
	if err != nil {
		return "", nil, err
	}
	if res.Ambiguous() {
		lines := []string{outcomeMarker + string(contract.OutcomeAmbiguous)}
		for _, c := range res.Candidates {
			lines = append(lines, fmt.Sprintf("candidate %s %s %s:%d", c.Node.Kind(), c.Node.QualifiedName(), c.Node.SourcePath(), c.Node.Line()))
		}
		return "", lines, nil
	}
	if !res.Resolved() {
		return "", []string{outcomeMarker + "not_found"}, nil
	}
	return res.Nodes[0].ID(), nil, nil
}

// invokeImpact runs the blast-radius analysis through the single
// analysis.Dispatch entry point (the same path every surface uses) and renders
// the reached nodes with depth and reaching-edge provenance.
func (e *FixtureEngine) invokeImpact(ctx context.Context, args map[string]string) ([]string, *float64, error) {
	id, early, err := e.resolveTarget(ctx, OpImpact, args)
	if early != nil || err != nil {
		return early, nil, err
	}
	e.analysisOnce.Do(func() {
		e.analysisSvc = analysis.NewDefaultService(e.Deps.Query.Reader())
	})
	p := analysis.Params{Symbol: id, MaxNodes: intArg(args, "max_nodes", 0)}
	if d := args["direction"]; d != "" {
		p.Direction = analysis.Direction(d)
	}
	an, err := e.analysisSvc.Dispatch(ctx, "impact", p)
	if err != nil {
		return nil, nil, err
	}
	lines := []string{outcomeMarker + string(an.Outcome)}
	for _, rn := range an.Nodes {
		lines = append(lines, fmt.Sprintf("%s %s %s:%d depth=%d via=%s [%s]",
			rn.Node.Kind, rn.Node.QualifiedName, rn.Node.SourcePath, rn.Node.Line,
			rn.Depth, rn.ReachedVia.Kind, rn.ReachedVia.Tier))
	}
	return lines, nil, nil
}

// invokeIndex reports whether the fixture ingest produced a non-trivial,
// queryable graph: node/edge/file counts plus one "file <path>" line per
// distinct indexed source file, so scenarios can anchor on expected files.
func (e *FixtureEngine) invokeIndex(ctx context.Context) ([]string, *float64, error) {
	if !e.Deps.Available() {
		return nil, nil, fmt.Errorf("scenario: query service unavailable")
	}
	reader := e.Deps.Query.Reader()
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, nil, err
	}
	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, nil, err
	}
	fileSet := map[string]struct{}{}
	for _, n := range nodes {
		if p := n.SourcePath(); p != "" {
			fileSet[p] = struct{}{}
		}
	}
	files := make([]string, 0, len(fileSet))
	for p := range fileSet {
		files = append(files, p)
	}
	sort.Strings(files)

	outcome := contract.OutcomeEmpty
	if len(nodes) > 0 {
		outcome = contract.OutcomeFound
	}
	lines := []string{
		outcomeMarker + string(outcome),
		fmt.Sprintf("indexed nodes=%d edges=%d files=%d", len(nodes), len(edges), len(files)),
	}
	for _, p := range files {
		lines = append(lines, "file "+p)
	}
	return lines, nil, nil
}

// InvokeContract runs one of the four EP-020 agent-tool operations against the
// fixture store and returns the full C1 contract envelope.
func (e *FixtureEngine) InvokeContract(ctx context.Context, operation string, args map[string]string) (*contract.Result, error) {
	switch operation {
	case OpExplainSymbol:
		ref := firstArg(args, "symbol", "ref")
		return explain.Explain(ctx, e.Deps, ref, intArg(args, "max_items", 10))
	case OpRelatedFiles:
		anchor := firstArg(args, "anchor", "symbol")
		return related.Files(ctx, e.Deps, anchor, args["direction"], intArg(args, "max_files", 10))
	case OpChangeRisk:
		return risk.Assess(ctx, e.Deps, firstArg(args, "target", "symbol"), args["diff"], intArg(args, "max_items", 10))
	case OpAgentBrief:
		return brief.Assemble(ctx, brief.Params{
			Topic:       args["topic"],
			ProjectName: firstNonEmpty(args["project"], e.ProjectName),
			RepoRoot:    e.RepoRoot,
			MaxItems:    intArg(args, "max_items", 0),
			Deps:        e.Deps,
		})
	default:
		return nil, fmt.Errorf("scenario: %q is not an agent-tool operation", operation)
	}
}

func intArg(args map[string]string, key string, def int) int {
	if v, ok := args[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func firstArg(args map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := args[k]; v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
