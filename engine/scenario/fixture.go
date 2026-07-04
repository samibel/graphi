package scenario

import (
	"context"
	"fmt"
	"strconv"

	"github.com/samibel/graphi/engine/agenttools/brief"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/explain"
	"github.com/samibel/graphi/engine/agenttools/related"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
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
	case OpDefinition, OpReferences, OpCallers:
		return e.invokeStructural(ctx, operation, args)
	default:
		if IsAgentToolOp(operation) {
			return nil, nil, fmt.Errorf("scenario: agent-tool operation %q requires the contract seam", operation)
		}
		return nil, nil, fmt.Errorf("scenario: unknown operation %q", operation)
	}
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
	if !e.Deps.Available() {
		return nil, nil, fmt.Errorf("scenario: query service unavailable")
	}
	ref := firstArg(args, "symbol", "ref", "query")
	if ref == "" {
		return nil, nil, fmt.Errorf("scenario: %s needs a symbol argument", operation)
	}
	res, err := resolve.Strict(ctx, e.Deps, ref)
	if err != nil {
		return nil, nil, err
	}
	if res.Ambiguous() {
		lines := []string{outcomeMarker + string(contract.OutcomeAmbiguous)}
		for _, c := range res.Candidates {
			lines = append(lines, fmt.Sprintf("candidate %s %s %s:%d", c.Node.Kind(), c.Node.QualifiedName(), c.Node.SourcePath(), c.Node.Line()))
		}
		return lines, nil, nil
	}
	if !res.Resolved() {
		return []string{outcomeMarker + "not_found"}, nil, nil
	}
	id := res.Nodes[0].ID()

	var qr query.Result
	switch operation {
	case OpDefinition:
		qr, err = e.Deps.Query.Definition(ctx, id)
	case OpReferences:
		qr, err = e.Deps.Query.References(ctx, id)
	case OpCallers:
		qr, err = e.Deps.Query.Callers(ctx, id)
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
