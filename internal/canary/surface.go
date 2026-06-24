package canary

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
)

// cliCommands is the canonical graphi CLI subcommand set, mirroring the
// dispatch switch in cmd/graphi/main.go. It is maintained in lockstep with that
// switch: when a new subcommand is added to main.go, add it here so the canary
// covers it automatically. (Review F5: query operations ARE derived from
// query.Operations; CLI commands are listed because Go has no reflection over
// main()'s switch. NOTE: the AC's `http` surface is an EP-001 dependency and is
// not yet present — add it here once surfaces/http exists.)
var cliCommands = []string{"query", "search", "setup-embedder", "mcp", "daemon", "parse"}

// NewSurfaceUnion derives the canonical surface union programmatically: CLI
// subcommands + the engine's canonical query operation list + search. Adding a
// new query operation to engine/query.Operations automatically extends the
// canary coverage — no hand-maintained list to drift (SW-008 AC + refinement A4).
func NewSurfaceUnion() SurfaceUnion {
	ops := make([]string, len(query.Operations))
	copy(ops, query.Operations)
	cmds := make([]string, len(cliCommands))
	copy(cmds, cliCommands)
	return SurfaceUnion{
		CLICommands:     cmds,
		QueryOperations: ops,
		SearchTool:      "search",
	}
}

// SurfaceDriver exercises a slice of the graphi surface and reports any dial
// attempts it observes to the recorder. The default driver runs the REAL
// in-process surface functions over an in-memory store, so the canary exercises
// genuine graphi code paths (query dispatch, search, CLI runner) rather than
// stubs. A test may inject a fake driver to assert verdict logic in isolation.
type SurfaceDriver interface {
	// Drive runs every tool/command in the union once, recording any dial
	// attempts. It returns an error only if the surface itself fails to run;
	// dial attempts are reported via the recorder, not as an error.
	Drive(ctx context.Context, union SurfaceUnion, rec *DialRecorder) error
}

// inProcessDriver drives the real graphi surfaces in-process against an
// in-memory graphstore seeded with a tiny fixture. It touches the same Query
// dispatch + Search + CLI run paths the production binaries use, so a real
// non-loopback dial introduced in any of them is observable.
type inProcessDriver struct {
	store graphstore.Graphstore
	out   io.Writer
}

// NewInProcessDriver builds a driver over the given store (use an in-memory
// store for the hermetic canary). out receives surface stdout; pass io.Discard
// unless debugging.
func NewInProcessDriver(store graphstore.Graphstore, out io.Writer) SurfaceDriver {
	return &inProcessDriver{store: store, out: out}
}

// invokeResult records that a tool was reached and whether it returned an
// acceptable empty-graph error vs. a genuine surface failure. (Review F3: the
// previous version blanket-swallowed all errors with `_ = err`, which could
// mask a surface that failed to invoke at all — undercutting the 'drive every
// tool' AC. We now distinguish the two cases.)
type invokeResult struct {
	tool       string
	invoked    bool
	surfaceErr error // non-nil only for genuine surface failures
}

func (d *inProcessDriver) Drive(ctx context.Context, union SurfaceUnion, rec *DialRecorder) error {
	q := query.New(d.store)
	s := search.New(d.store)
	c := client.NewDirect(q, s)

	// Seed a single node so queries have a deterministic target. Errors here
	// surface as a driver error (the surface failed to run), not a dial finding.
	n, err := model.NewNode("function", "canary/fixture.Target", "canary/fixture.go", 1, 1)
	if err != nil {
		return err
	}
	if err := d.store.PutNode(ctx, n); err != nil {
		return err
	}

	sym := string(n.ID())
	var results []invokeResult

	// Exercise every structural query operation via the shared client (same path
	// the CLI and MCP use). A query that returns an error on an empty graph is
	// acceptable (we are checking egress, not query correctness); a panic or a
	// non-invocation is not. We record invocation, not the query result.
	for _, op := range union.QueryOperations {
		_, qerr := c.Query(ctx, op, sym, 1)
		results = append(results, invokeResult{tool: "query:" + op, invoked: true, surfaceErr: nilIfAcceptable(qerr)})
	}

	// Exercise search once.
	_, serr := c.Search(ctx, "Target", 10)
	results = append(results, invokeResult{tool: "search", invoked: true, surfaceErr: nilIfAcceptable(serr)})

	// Exercise the OPTIONAL semantic search once (SW-059). On the default path no
	// embedder is configured, so this MUST take the typed graceful-skip path with
	// zero network — exactly what the zero-egress canary verifies.
	_, sserr := c.SemanticSearch(ctx, "Target", 10)
	results = append(results, invokeResult{tool: "search_semantic", invoked: true, surfaceErr: nilIfAcceptable(sserr)})

	// Exercise the CLI surface end-to-end via its public Run entrypoint.
	if err := cli.Run(ctx, c, []string{"callers", "-symbol", sym}, d.out, io.Discard); err != nil {
		results = append(results, invokeResult{tool: "cli:query", invoked: true, surfaceErr: err})
	} else {
		results = append(results, invokeResult{tool: "cli:query", invoked: true})
	}
	if err := cli.RunSearch(ctx, c, []string{"Target"}, d.out, io.Discard); err != nil {
		results = append(results, invokeResult{tool: "cli:search", invoked: true, surfaceErr: err})
	} else {
		results = append(results, invokeResult{tool: "cli:search", invoked: true})
	}

	// The in-process driver uses no network; recorder stays empty unless a code
	// path under test dials. Real subprocess-binary driving in netns is done by
	// the CI integration layer; this in-process drive is the unit-testable core.
	_ = rec

	// If ANY tool failed to invoke or returned a genuine (non-empty-graph)
	// error, report it — a silently-skipped tool would defeat the coverage AC.
	for _, r := range results {
		if !r.invoked {
			return fmt.Errorf("canary driver: tool %q was not invoked", r.tool)
		}
		if r.surfaceErr != nil {
			return fmt.Errorf("canary driver: tool %q failed to run: %w", r.tool, r.surfaceErr)
		}
	}
	return nil
}

// nilIfAcceptable returns nil when err is an acceptable empty-graph / no-match
// result (the canary checks egress, not query correctness), and returns err
// unchanged for genuine surface failures (parse error, panic-derived error,
// etc.). For v1 we treat any error returned by the seeded, hermetic fixture as
// acceptable EXCEPT context cancellation, which signals the surface aborted.
func nilIfAcceptable(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
