// Command graphi is the single static binary wiring point for the graphi
// code-intelligence engine. It wires the shared read-only query service
// (engine/query) and search service (engine/search) to two surfaces — the CLI
// (`query`, `search`) and the MCP stdio server (`mcp`) — through either an
// in-process client or a hot-index daemon client, plus the original SW-001
// parser-registry behavior (default / `parse`).
//
// Layering: cmd is the top layer; it imports surfaces + engine + core and wires
// them together. It contains no query/search logic of its own.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/version"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	"github.com/samibel/graphi/surfaces/mcp"
)

func main() {
	_ = version.Version // linked so -ldflags -X can stamp it; see internal/version
	if len(os.Args) < 2 {
		runParseDefault(nil)
		return
	}

	switch os.Args[1] {
	case "query":
		os.Exit(runQuery(os.Args[2:]))
	case "search":
		os.Exit(runSearch(os.Args[2:]))
	case "savings":
		os.Exit(runSavings(os.Args[2:]))
	case "analyze":
		os.Exit(runAnalyze(os.Args[2:]))
	case "refactor-preview":
		os.Exit(runRefactor(os.Args[2:], "refactor-preview"))
	case "refactor":
		os.Exit(runRefactor(os.Args[2:], "refactor"))
	case "undo":
		os.Exit(runRefactor(os.Args[2:], "undo"))
	case "mcp":
		os.Exit(runMCP(os.Args[2:]))
	case "daemon":
		os.Exit(runDaemon(os.Args[2:]))
	case "version":
		runVersion()
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

// makeClient returns a surface client. If socket is non-empty, it connects to
// the daemon; otherwise it builds an in-process client over the store.
func makeClient(store graphstore.Graphstore, socket string) client.Client {
	if socket != "" {
		return daemon.NewClient(socket, "")
	}
	return client.NewDirect(query.New(store), search.New(store)).WithAnalysis(analysis.NewDefaultService(store))
}

// extractFlags pulls -db and -daemon options off the front of args.
func extractFlags(args []string) (dbPath, socket string, rest []string) {
	rest = args
	for len(rest) > 0 {
		switch {
		case rest[0] == "-db" && len(rest) >= 2:
			dbPath = rest[1]
			rest = rest[2:]
		case len(rest[0]) > 4 && rest[0][:4] == "-db=":
			dbPath = rest[0][4:]
			rest = rest[1:]
		case rest[0] == "-daemon" && len(rest) >= 2:
			socket = rest[1]
			rest = rest[2:]
		case len(rest[0]) > 8 && rest[0][:8] == "-daemon=":
			socket = rest[0][8:]
			rest = rest[1:]
		default:
			return
		}
	}
	return
}

// runQuery launches the CLI surface. Usage:
//
//	graphi query [-db path] [-daemon socket] <operation> -symbol <id> [-depth N]
func runQuery(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.Run(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

// runSearch launches the CLI search surface. Usage:
//
//	graphi search [-db path] [-daemon socket] [-limit N] <query>
func runSearch(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunSearch(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

// runAnalyze launches the CLI analyzer surface (SW-022). Usage:
//
//	graphi analyze [-db path] [-daemon socket] <analyzer> -symbol <id> [-direction forward|reverse] [-max-nodes N]
func runAnalyze(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunAnalysis(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

// runMCP launches the MCP stdio server. Usage: graphi mcp [-db path] [-daemon socket]
func runMCP(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	srv := mcp.NewServerWithClient(c)
	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: mcp: %v\n", err)
		return 1
	}
	return 0
}

// makeClientOrOpen creates a client, opening an in-process store if needed.
// It prints errors and returns nil on failure.
func makeClientOrOpen(dbPath, socket string) client.Client {
	if socket != "" {
		return daemon.NewClient(socket, "")
	}
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return nil
	}
	defer func() { _ = store.Close() }()
	return client.NewDirect(query.New(store), search.New(store)).WithAnalysis(analysis.NewDefaultService(store))
}

// runRefactor launches the SW-038 edit/refactor command surface (refactor-preview
// / refactor / undo). It builds an in-process client with a fully-wired edit
// applier + change recorder over the repo at -root, ingested into the store at
// -db with its ingest-meta sidecar under -meta. The CLI surface holds no engine
// logic — it parses flags and calls the shared client.
//
//	graphi <verb> -root <repo> [-db path] [-meta dir] <verb-flags>
func runRefactor(args []string, verb string) int {
	root, dbPath, metaDir, rest := extractEditFlags(args)
	if root == "" {
		fmt.Fprintln(os.Stderr, "graphi: -root <repo> is required for edit/refactor commands")
		return 1
	}
	c, cleanup, err := makeEditorClient(root, dbPath, metaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v: %v\n", verb, err)
		return 1
	}
	defer cleanup()

	switch verb {
	case "refactor-preview":
		err = cli.RunRefactorPreview(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "refactor":
		err = cli.RunRefactor(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "undo":
		err = cli.RunUndo(context.Background(), c, rest, os.Stdout, os.Stderr)
	}
	if err != nil {
		return 1
	}
	return 0
}

// extractEditFlags pulls -root, -db, -meta off the front of args (the rest are
// the verb-specific flags).
func extractEditFlags(args []string) (root, dbPath, metaDir string, rest []string) {
	rest = args
	take := func(name string) (string, bool) {
		if len(rest) >= 2 && rest[0] == name {
			v := rest[1]
			rest = rest[2:]
			return v, true
		}
		if len(rest[0]) > len(name)+1 && rest[0][:len(name)+1] == name+"=" {
			v := rest[0][len(name)+1:]
			rest = rest[1:]
			return v, true
		}
		return "", false
	}
	for len(rest) > 0 {
		if v, ok := take("-root"); ok {
			root = v
			continue
		}
		if v, ok := take("-db"); ok {
			dbPath = v
			continue
		}
		if v, ok := take("-meta"); ok {
			metaDir = v
			continue
		}
		break
	}
	return root, dbPath, metaDir, rest
}

// makeEditorClient builds an in-process client with the edit/refactor command
// surface wired (SW-038): it opens the store, constructs an Ingester over the
// default parser registry + meta sidecar, performs an initial full ingest of the
// repo, builds the Applier with a consistency checker and a ChangeRecorder, and
// attaches them via Direct.WithEditor. The returned cleanup releases all handles.
func makeEditorClient(root, dbPath, metaDir string) (client.Client, func(), error) {
	ctx := context.Background()
	store, err := openStore(dbPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open store: %w", err)
	}
	reg := parse.NewDefaultRegistry()
	ing, err := ingest.New(store, reg, metaDir)
	if err != nil {
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("ingest: %w", err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("initial ingest: %w", err)
	}
	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, ierr := ingest.New(fs, parse.NewDefaultRegistry(), "")
		if ierr != nil {
			return nil, nil, nil, ierr
		}
		return fs, fi, func() { _ = fi.Close(); _ = fs.Close() }, nil
	})
	applier, err := edit.NewApplier(store, ing, root, checker)
	if err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("applier: %w", err)
	}
	recorder, err := edit.NewChangeRecorder(ctx, ing, metaDir)
	if err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("change recorder: %w", err)
	}
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithEditor(applier, recorder)
	cleanup := func() { _ = ing.Close(); _ = store.Close() }
	return c, cleanup, nil
}

// runDaemon runs the daemon lifecycle commands: start, stop, status.
func runDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	socket := fs.String("socket", daemon.DefaultSocketPath(), "Unix socket path")
	dbPath := fs.String("db", "", "SQLite graphstore path (empty = in-memory)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: daemon: %v\n", err)
		return 1
	}
	if len(fs.Args()) < 1 {
		fmt.Fprintln(os.Stderr, "usage: graphi daemon start|stop|status [-socket path] [-db path]")
		return 1
	}
	cmd := fs.Args()[0]
	switch cmd {
	case "start":
		store, err := openStore(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
			return 1
		}
		defer func() { _ = store.Close() }()
		handler := client.NewDirect(query.New(store), search.New(store))
		srv := daemon.NewServer(handler)
		if err := srv.Start(*socket); err != nil {
			fmt.Fprintf(os.Stderr, "graphi: daemon start: %v\n", err)
			return 1
		}
		fmt.Printf("daemon listening on %s\n", *socket)
		// Block until stopped via signal or Stop.
		select {}
	case "stop":
		// Best-effort: connect and send a shutdown request is not implemented;
		// for now rely on process management.
		fmt.Fprintln(os.Stderr, "daemon stop: not implemented; stop the daemon process directly")
		return 1
	case "status":
		c := daemon.NewClient(*socket, "")
		if _, err := c.Query(context.Background(), "callers", "", 0); err != nil {
			fmt.Printf("daemon at %s: not responding (%v)\n", *socket, err)
			return 1
		}
		fmt.Printf("daemon at %s: responding\n", *socket)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "graphi: unknown daemon command %q\n", cmd)
		return 1
	}
}

// runSavings launches the CLI savings readout surface. Usage:
//
//	graphi savings [-db path] [-daemon socket] [-ledger path]
func runSavings(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	ledgerPath := ""
	for len(rest) > 0 {
		if rest[0] == "-ledger" && len(rest) >= 2 {
			ledgerPath = rest[1]
			rest = rest[2:]
			continue
		}
		break
	}
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	// If an in-process client, attach a local ledger so the readout is available.
	if socket == "" {
		if l, err := ledger.Open(ledgerPath); err == nil {
			defer func() { _ = l.Close() }()
			if d, ok := c.(*client.Direct); ok {
				c = d.WithLedger(l)
			}
		} else {
			fmt.Fprintf(os.Stderr, "graphi: open ledger: %v\n", err)
		}
	}
	if err := cli.RunSavings(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: savings: %v\n", err)
		return 1
	}
	return 0
}

// runVersion prints the release version + VCS metadata embedded by SW-013's
// packaging (ldflags-stamped version.Version + debug.ReadBuildInfo VCS stamps).
// It is how the release checker verifies the embedded version/commit/date.
func runVersion() {
	commit, date := "", ""
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				commit = s.Value
			case "vcs.time":
				date = s.Value
			}
		}
	}
	fmt.Printf("graphi version=%s commit=%s date=%s\n", version.Version, commit, date)
}

// runParseDefault preserves the original SW-001 parser-registry behavior.
func runParseDefault(args []string) {
	reg := parse.NewDefaultRegistry()

	if len(args) < 1 {
		fmt.Printf("graphi\nregistered languages: %v\nsubcommands: query, search, mcp, daemon, parse <file>\n", reg.Languages())
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
