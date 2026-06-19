// Package cli is the command-line surface over the shared engine/query service.
//
// Layering: cli is a surface. It imports engine/query and core only; it holds NO
// query, traversal, ordering, or serialization logic of its own. It parses
// arguments, calls query.Service.Dispatch, and prints the bytes produced by the
// single canonical serializer (query.Marshal) — the same bytes the MCP surface
// emits for the same query (MCP↔CLI parity by construction).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/query"
)

// Run executes one CLI invocation against the shared service and writes the
// canonical serialized result to out. args are the arguments AFTER the surface
// selector (i.e. the subcommand and its flags). It returns a non-zero-style
// error only for usage/infrastructure failures; an unresolved symbol is a normal
// not-found Result printed to out (not an error), matching the MCP surface.
//
// Usage:
//
//	<op> -symbol <id> [-depth N]
//
// where <op> is one of callers|callees|references|definition|neighborhood.
func Run(ctx context.Context, svc *query.Service, args []string, out, errOut io.Writer) error {
	if len(args) < 1 {
		fmt.Fprintf(errOut, "usage: <operation> -symbol <id> [-depth N]\noperations: %v\n", query.Operations)
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

	res, err := svc.Dispatch(ctx, op, model.NodeId(*symbol), *depth)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}

	b, err := query.Marshal(res)
	if err != nil {
		return fmt.Errorf("cli: %w", err)
	}
	if _, err := out.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("cli: write output: %w", err)
	}
	return nil
}
