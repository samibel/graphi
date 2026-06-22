// Package cli is the command-line surface over the shared surface client.
//
// Layering: cli is a surface. It imports surfaces/client and holds NO query,
// traversal, ordering, or serialization logic of its own. It parses arguments,
// calls client.Client.Query/Search, and prints the canonical serialized bytes.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/price"
	"github.com/samibel/graphi/surfaces/client"
)

// Run executes one CLI structural-query invocation against the shared client
// and writes the canonical serialized result to out. args are the arguments
// AFTER the surface selector (i.e. the subcommand and its flags).
//
// Usage:
//
//	<op> -symbol <id> [-depth N]
//
// where <op> is one of callers|callees|references|definition|neighborhood.
func Run(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		fmt.Fprintf(errOut, "usage: <operation> -symbol <id> [-depth N]\n")
		return fmt.Errorf("cli: missing operation")
	}
	op := args[0]

	fs := flag.NewFlagSet(op, flag.ContinueOnError)
	fs.SetOutput(errOut)
	symbol := fs.String("symbol", "", "symbol (node) id to query")
	depth := fs.Int("depth", 1, "neighborhood hop depth (clamped to the documented max; ignored by other operations)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *symbol == "" {
		fmt.Fprintln(errOut, "cli: -symbol is required")
		return fmt.Errorf("cli: -symbol is required")
	}

	b, err := c.Query(ctx, op, *symbol, *depth)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// cliActor is the default actor recorded for edits initiated via the CLI surface
// when the caller supplies none (Scope decision 6: actor is per-surface, recorded,
// excluded from the AC-4 parity comparable subset).
const cliActor = "cli"

// refactorFlags parses the shared refactor flags into a RefactorRequest. Both the
// preview and apply subcommands use the SAME parsing so the two surfaces (and the
// two CLI verbs) construct identical RefactorOps (parity by construction).
func refactorFlags(name string, args []string, errOut io.Writer) (client.RefactorRequest, string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	kind := fs.String("kind", "", "refactor kind: rename|extract|move|signature_change")
	target := fs.String("target", "", "resolved node id of the symbol to refactor")
	oldName := fs.String("old-name", "", "current spelling of the symbol")
	newName := fs.String("new-name", "", "replacement spelling")
	dest := fs.String("destination-file", "", "destination file (move only)")
	actor := fs.String("actor", cliActor, "request identity recorded on the change record")
	if err := fs.Parse(args); err != nil {
		return client.RefactorRequest{}, "", fmt.Errorf("cli: %w", err)
	}
	if *kind == "" || *target == "" || *oldName == "" || *newName == "" {
		fmt.Fprintln(errOut, "cli: -kind, -target, -old-name and -new-name are required")
		return client.RefactorRequest{}, "", fmt.Errorf("cli: missing required refactor flags")
	}
	return client.RefactorRequest{
		Kind:            *kind,
		TargetSymbol:    *target,
		OldName:         *oldName,
		NewName:         *newName,
		DestinationFile: *dest,
	}, *actor, nil
}

// RunRefactorPreview executes a refactor PREVIEW against the shared client and
// writes the canonical EP-004 impact set (blast radius + planned ops) WITHOUT
// mutating (AC-1). The CLI holds no engine logic.
//
// Usage:
//
//	refactor-preview -kind rename -target <id> -old-name X -new-name Y
func RunRefactorPreview(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	req, _, err := refactorFlags("refactor-preview", args, errOut)
	if err != nil {
		return err
	}
	b, err := c.RefactorPreview(ctx, req)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunRefactor commits a refactor through the shared client and writes the
// canonical auditable change record. The actor defaults to "cli".
//
// Usage:
//
//	refactor -kind rename -target <id> -old-name X -new-name Y [-actor who]
func RunRefactor(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	req, actor, err := refactorFlags("refactor", args, errOut)
	if err != nil {
		return err
	}
	b, err := c.Refactor(ctx, req, actor)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunUndo reverses an applied edit by its undo token and writes the canonical
// reversal change record.
//
// Usage:
//
//	undo -token <undo-token> [-actor who]
func RunUndo(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("undo", flag.ContinueOnError)
	fs.SetOutput(errOut)
	token := fs.String("token", "", "the undo token returned by a prior refactor")
	actor := fs.String("actor", cliActor, "request identity recorded on the reversal record")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *token == "" {
		fmt.Fprintln(errOut, "cli: -token is required")
		return fmt.Errorf("cli: -token is required")
	}
	b, err := c.Undo(ctx, *token, *actor)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSearch executes one CLI search invocation against the shared client and
// writes the canonical serialized result to out.
//
// Usage:
//
//	graphi search [-limit N] <query>
func RunSearch(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	limit := 100
	var queryArgs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-limit" && i+1 < len(args) {
			// Use fmt.Sscanf to avoid strconv import.
			var v int
			if _, err := fmt.Sscanf(args[i+1], "%d", &v); err != nil {
				fmt.Fprintf(errOut, "cli: invalid -limit value %q\n", args[i+1])
				return fmt.Errorf("cli: invalid -limit")
			}
			limit = v
			i++
			continue
		}
		queryArgs = append(queryArgs, args[i])
	}
	if len(queryArgs) == 0 {
		fmt.Fprintln(errOut, "usage: search [-limit N] <query>")
		return fmt.Errorf("cli: missing query")
	}
	query := queryArgs[0]

	b, err := c.Search(ctx, query, limit)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunSavings prints the savings-ledger readout (SW-020): the headline
// "Saved $X this session" line plus per-call and cumulative USD figures,
// followed by the canonical structured readout JSON (identical to the MCP tool
// result for the same ledger state, preserving MCP<->CLI parity).
//
// Usage:
//
//	savings
func RunSavings(ctx context.Context, c client.Client, out, errOut io.Writer) error {
	b, err := c.Savings(ctx)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	var r ledger.Readout
	if err := json.Unmarshal(b, &r); err != nil {
		return fmt.Errorf("cli: decode readout: %w", err)
	}
	// Headline + figures (formatted via the shared micro-USD formatter).
	fmt.Fprintf(out, "Saved %s this session\n", price.FormatUSD(r.SessionMicroUSD))
	fmt.Fprintf(out, "per-call: %s\n", price.FormatUSD(r.LastCallMicroUSD))
	fmt.Fprintf(out, "cumulative: %s\n", price.FormatUSD(r.CumulativeMicroUSD))
	if r.SessionCapped || r.LastCallCapped {
		fmt.Fprintln(out, "note: anti-gaming cap applied to one or more contributions")
	}
	// Canonical structured readout (parity with the MCP tool result).
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}

// RunAnalysis executes one CLI analyzer invocation against the shared client and
// writes the canonical serialized result to out (SW-022). The CLI holds no
// analysis logic of its own — it parses arguments, builds AnalyzeParams, calls
// client.Client.Analyze, and prints the canonical bytes (MCP<->CLI parity).
//
// Usage:
//
//	<name> -symbol <id> [-direction forward|reverse] [-max-nodes N]
//
// where <name> is a registered analyzer (e.g. impact).
func RunAnalysis(ctx context.Context, c client.Client, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		fmt.Fprintf(errOut, "usage: <analyzer> -symbol <id> [-direction forward|reverse] [-max-nodes N]\n")
		return fmt.Errorf("cli: missing analyzer name")
	}
	name := args[0]
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	symbol := fs.String("symbol", "", "symbol (node) id to analyze")
	target := fs.String("target", "", "target symbol (node) id (call-chain endpoint)")
	concept := fs.String("concept", "", "concept term (concept resolver)")
	direction := fs.String("direction", "forward", "traversal direction for directional analyzers (forward|reverse)")
	maxNodes := fs.Int("max-nodes", 0, "output budget on reached nodes (0 = analyzer default)")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if *symbol == "" {
		fmt.Fprintln(errOut, "cli: -symbol is required")
		return fmt.Errorf("cli: -symbol is required")
	}
	b, err := c.Analyze(ctx, client.AnalyzeParams{
		Name:      name,
		Symbol:    *symbol,
		Target:    *target,
		Concept:   *concept,
		Direction: *direction,
		MaxNodes:  *maxNodes,
	})
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}
