// Command graphi is the single static binary wiring point for the graphi
// code-intelligence engine. It wires the shared read-only query service
// (engine/query) to two surfaces — the CLI (`query`) and the MCP stdio server
// (`mcp`) — both dispatching through the SAME service, plus the original SW-001
// parser-registry behavior (default / `parse`).
//
// Layering: cmd is the top layer; it imports surfaces + engine + core and wires
// them together. It contains no query logic of its own.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/mcp"
)

func main() {
	if len(os.Args) < 2 {
		runParseDefault(nil)
		return
	}

	switch os.Args[1] {
	case "query":
		os.Exit(runQuery(os.Args[2:]))
	case "mcp":
		os.Exit(runMCP(os.Args[2:]))
	case "parse":
		runParseDefault(os.Args[2:])
	default:
		// Backwards-compatible: treat the first arg as a filename to parse
		// (preserves the original SW-001 invocation `graphi <file>`).
		runParseDefault(os.Args[1:])
	}
}

// openStore opens the durable graphstore. dbPath empty → in-memory store
// (useful for ad-hoc/testing); otherwise the CGo-free SQLite backend.
func openStore(dbPath string) (graphstore.Graphstore, error) {
	if dbPath == "" {
		return graphstore.NewMemStore(), nil
	}
	return graphstore.OpenSQLite(dbPath)
}

// runQuery launches the CLI surface. Usage:
//
//	graphi query [-db path] <operation> -symbol <id> [-depth N]
func runQuery(args []string) int {
	dbPath, rest := extractDBFlag(args)
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	svc := query.New(store)
	if err := cli.Run(context.Background(), svc, rest, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

// runMCP launches the MCP stdio server. Usage: graphi mcp [-db path]
func runMCP(args []string) int {
	dbPath, _ := extractDBFlag(args)
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	svc := query.New(store)
	srv := mcp.NewServer(svc)
	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: mcp: %v\n", err)
		return 1
	}
	return 0
}

// extractDBFlag pulls a leading `-db <path>` (or `-db=<path>`) option off args
// without a full FlagSet, so the remaining args pass through to the surface
// verbatim. Returns the db path and the remaining args.
func extractDBFlag(args []string) (string, []string) {
	if len(args) == 0 {
		return "", args
	}
	switch {
	case args[0] == "-db" && len(args) >= 2:
		return args[1], args[2:]
	case len(args[0]) > 4 && args[0][:4] == "-db=":
		return args[0][4:], args[1:]
	default:
		return "", args
	}
}

// runParseDefault preserves the original SW-001 parser-registry behavior.
func runParseDefault(args []string) {
	reg := parse.NewDefaultRegistry()

	if len(args) < 1 {
		fmt.Printf("graphi\nregistered languages: %v\nsubcommands: query, mcp, parse <file>\n", reg.Languages())
		return
	}

	filename := args[0]
	src, err := os.ReadFile(filename) //nolint:gosec // local-first CLI reading a user-named file
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: cannot read %q: %v\n", filename, err)
		os.Exit(1)
	}

	res, err := reg.Parse(context.Background(), filename, src)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("parsed %s as %s (%d bytes, hash %s)\n",
		res.Meta.Path, res.Meta.Language, res.Meta.Size, res.Meta.ContentHash)
}
