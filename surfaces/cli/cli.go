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
