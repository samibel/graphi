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
	"github.com/samibel/graphi/engine/embed"
	_ "github.com/samibel/graphi/engine/embed/ollama" // opt-in loopback embedder: registers the "ollama" scheme; never constructed on the default path
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/audit"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/version"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

func main() {
	_ = version.Version // linked so -ldflags -X can stamp it; see internal/version
	if len(os.Args) < 2 {
		// Zero-config default (SW-067): bare `graphi` detects the cwd repo,
		// indexes it, and serves the embedded UI on a loopback port. The old
		// help blurb now lives under `graphi help`.
		os.Exit(runZeroConfig())
	}

	switch os.Args[1] {
	case "query":
		os.Exit(runQuery(os.Args[2:]))
	case "search":
		os.Exit(runSearch(os.Args[2:]))
	case "index":
		os.Exit(runIndex(os.Args[2:]))
	case "savings":
		os.Exit(runSavings(os.Args[2:]))
	case "analyze":
		os.Exit(runAnalyze(os.Args[2:]))
	case "pr-comment":
		os.Exit(runPrComment(os.Args[2:]))
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
	case "http":
		os.Exit(runHTTP(os.Args[2:]))
	case "setup":
		os.Exit(runSetup(os.Args[2:]))
	case "setup-embedder":
		os.Exit(runSetupEmbedder(os.Args[2:]))
	case "tui":
		os.Exit(runTUI(os.Args[2:]))
	case "privacy-audit":
		os.Exit(runPrivacyAudit(os.Args[2:]))
	case "upgrade":
		os.Exit(runUpgrade(os.Args[2:]))
	case "version":
		runVersion()
	case "help":
		printHelp()
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

// extractFlags pulls -db, -daemon, and -meta options off the front of args. -meta
// names the ingest sidecar dir; for the search path it is where the durable
// `vectors` table (SW-061) is reloaded from on startup so `search -semantic`
// returns hits without re-embedding.
func extractFlags(args []string) (dbPath, socket string, rest []string) {
	dbPath, socket, _, rest = extractFlagsMeta(args)
	return
}

// extractFlagsMeta is extractFlags plus the -meta sidecar dir.
func extractFlagsMeta(args []string) (dbPath, socket, metaDir string, rest []string) {
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
		case rest[0] == "-meta" && len(rest) >= 2:
			metaDir = rest[1]
			rest = rest[2:]
		case len(rest[0]) > 6 && rest[0][:6] == "-meta=":
			metaDir = rest[0][6:]
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
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
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
	dbPath, socket, metaDir, rest := extractFlagsMeta(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c := makeClientOrOpenMeta(dbPath, socket, metaDir)
	if c == nil {
		return 1
	}
	if err := cli.RunSearch(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

// runIndex is the `graphi index [--semantic]` subcommand (SW-061). It ingests the
// repo at -root into the store at -db with its ingest-meta sidecar under -meta,
// reusing the existing engine/ingest path. With --semantic AND a configured
// embedder it additionally runs the embedding-GENERATION pass: it embeds every
// node (keyed by NodeId) and Upserts the vectors into the durable `vectors` sidecar
// table, so a later `graphi search -semantic` returns ranked hits without
// re-embedding.
//
// Trust posture (story hard constraints):
//
//   - The DEFAULT path (`graphi index`, no --semantic) NEVER embeds and NEVER dials.
//
//   - With --semantic but NO embedder configured, it GRACEFULLY SKIPS embedding —
//     reporting the typed "unavailable — no embedder configured" line, no error,
//     zero network — while lexical indexing completes normally.
//
//     graphi index [--semantic] -root <repo> [-db path] [-meta dir]
func runIndex(args []string) int {
	// Order-independent flag parsing: --semantic is a bool toggle; -root/-db/-meta
	// each take a value (space- or =-separated). Unknown tokens are ignored.
	semantic := false
	root, dbPath, metaDir := "", "", ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		takeVal := func(name string) (string, bool) {
			if a == name && i+1 < len(args) {
				i++
				return args[i], true
			}
			if len(a) > len(name)+1 && a[:len(name)+1] == name+"=" {
				return a[len(name)+1:], true
			}
			return "", false
		}
		switch {
		case a == "--semantic" || a == "-semantic":
			semantic = true
		default:
			if v, ok := takeVal("-root"); ok {
				root = v
			} else if v, ok := takeVal("-db"); ok {
				dbPath = v
			} else if v, ok := takeVal("-meta"); ok {
				metaDir = v
			}
		}
	}
	if root == "" {
		fmt.Fprintln(os.Stderr, "graphi: -root <repo> is required for index")
		return 1
	}

	ctx := context.Background()
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	ing, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: ingest: %v\n", err)
		return 1
	}
	defer func() { _ = ing.Close() }()
	if err := ing.IngestAll(ctx, root); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: index: %v\n", err)
		return 1
	}
	fmt.Printf("graphi index: ingested %s\n", root)

	if !semantic {
		return 0 // default path: no embed, no dial
	}

	// Semantic generation pass. Construct the configured embedder ONLY here, on the
	// explicit --semantic opt-in; an empty/unknown selector ⇒ graceful skip.
	emb, err := embed.Constructor(os.Getenv(embed.EnvSelector), embed.DefaultConstructors())
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		fmt.Printf("graphi index --semantic: unavailable — %s\n", search.UnavailableReason)
		return 0 // graceful: lexical index already committed; no error
	}
	if emb == nil {
		fmt.Printf("graphi index --semantic: unavailable — %s\n", search.UnavailableReason)
		return 0 // graceful skip: no embedder, no network
	}
	reg := embed.NewRegistry()
	reg.Register(emb)

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: index --semantic: read nodes: %v\n", err)
		return 1
	}
	table, err := embed.NewSQLiteVectorTableDB(ctx, ing.MetaDB(), emb.ID(), emb.Dim())
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: index --semantic: open vectors table: %v\n", err)
		return 1
	}
	res, err := embed.GenerateAndPersist(ctx, reg, nodes, embed.NewIndex(), table)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: index --semantic: %v\n", err)
		return 1
	}
	fmt.Printf("graphi index --semantic: embedded %d nodes via %s\n", res.Embedded, res.EmbedderID)
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

// runPrComment launches the SW-042 sticky PR-comment + merge-gate surface. Usage:
//
//	graphi pr-comment [-db path] [-daemon socket] -diff <unified-diff> | -diff-path <file>
//	  [-pr ref] [-provenance summary|full] [-gate] [-gate-threshold N] [-publish]
func runPrComment(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunPrComment(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
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

// resolveSession is the additive default-discovery seam (SW-068). Given the cwd
// and any explicit overrides, it derives the per-repo durable store + daemon
// socket from internal/state. The DB is returned ONLY if a state store already
// exists for the cwd repo (else ""), so callers fall back to today's in-memory
// behavior with zero regression. The socket is returned ONLY if a daemon is
// already listening on it (a UNIX-only liveness probe via daemon.IsAlive); a
// dead/absent socket yields "" so the default path never auto-starts a daemon
// or dials TCP. Discovery errors are swallowed → overrides/empty, keeping the
// default path resilient and offline.
func resolveSession(cwd, dbOverride, socketOverride string) (db, socket string) {
	db, err := state.DiscoverDB(cwd, dbOverride)
	if err != nil {
		db = dbOverride
	}
	socket, err = state.DiscoverSocket(cwd, socketOverride)
	if err != nil {
		socket = socketOverride
	}
	// Only route to a daemon that is actually alive; otherwise leave socket empty
	// so makeClientOrOpen builds an in-process client (no auto-start, no dial).
	if socket != "" && socketOverride == "" && !daemon.IsAlive(socket) {
		socket = ""
	}
	return db, socket
}

// getwd returns the current working directory, or "." on error, so default
// discovery degrades gracefully rather than failing.
func getwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// makeClientOrOpen creates a client, opening an in-process store if needed.
// It prints errors and returns nil on failure.
func makeClientOrOpen(dbPath, socket string) client.Client {
	return makeClientOrOpenMeta(dbPath, socket, "")
}

// makeClientOrOpenMeta is makeClientOrOpen plus an optional meta sidecar dir. When
// a semantic embedder is configured AND metaDir is set, the search service reloads
// its durable vectors from the meta sidecar so `search -semantic` returns hits
// without re-embedding (SW-061 reload-on-startup; a pure local read).
func makeClientOrOpenMeta(dbPath, socket, metaDir string) client.Client {
	if socket != "" {
		return daemon.NewClient(socket, "")
	}
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return nil
	}
	defer func() { _ = store.Close() }()
	analysisSvc := analysis.NewDefaultService(store)
	return client.NewDirect(query.New(store), newSearchService(store, metaDir)).
		WithAnalysis(analysisSvc).
		WithReview(review.NewService(analysisSvc))
}

// newSearchService builds the shared search service. Lexical search is always
// available. Semantic search is OPTIONAL and OFF by default: it is enabled ONLY
// when GRAPHI_EMBEDDER explicitly selects a (recognized) embedder, which is
// constructed here through the embed.Constructor seam. An empty/unknown selector
// leaves the service in the graceful-skip state (no embedder, no network), so the
// default binary never constructs or dials an embedder (SW-059 / OQ6).
func newSearchService(store graphstore.Graphstore, metaDir string) *search.Service {
	svc := search.New(store)
	emb, err := embed.Constructor(os.Getenv(embed.EnvSelector), embed.DefaultConstructors())
	if err != nil {
		// Fail-closed (e.g. a non-loopback Ollama host): report and keep semantic
		// search OFF rather than constructing an unsafe embedder.
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		return svc
	}
	if emb == nil {
		return svc // graceful skip: nothing configured
	}
	reg := embed.NewRegistry()
	reg.Register(emb)

	// RELOAD (SW-061): rebuild the in-memory index from the durable `vectors`
	// sidecar so `search -semantic` returns the vectors a prior `index --semantic`
	// generated — WITHOUT re-embedding. Rebuild reads only local SQLite rows scoped
	// to the active embedder's (id, dim), so a changed/absent embedder loads zero
	// stale vectors. This is a PURE LOCAL READ: zero embedder dials, zero network.
	// When no metaDir is given (e.g. an in-memory ad-hoc run) the index starts
	// empty; nothing is dialed.
	index := embed.NewIndex()
	if metaDir != "" {
		table, terr := embed.OpenSQLiteVectorTable(context.Background(), metaDir, emb.ID(), emb.Dim())
		if terr != nil {
			fmt.Fprintf(os.Stderr, "graphi: vectors reload disabled: %v\n", terr)
		} else {
			if rerr := index.Rebuild(context.Background(), table); rerr != nil {
				fmt.Fprintf(os.Stderr, "graphi: vectors reload failed: %v\n", rerr)
			}
			_ = table.Close()
		}
	}
	return svc.WithSemantic(reg, index, nodeReader(store))
}

// nodeReader adapts the graphstore to the narrow search.NodeReader seam used to
// enrich semantic hits with node provenance.
func nodeReader(store graphstore.Graphstore) search.NodeReader { return store }

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

// runHTTP runs the read-only HTTP REST + SSE surface (SW-039). It serves the
// shared query/search/analysis client over loopback HTTP with a versioned
// envelope, and (optionally) indexes a repo with the broker attached so SSE
// clients receive ingest-completed freshness events. Local-first: the listen
// address MUST be loopback; the surface makes zero outbound connections.
//
//	graphi http [-addr 127.0.0.1:8080] [-db path] [-root repo]
func runHTTP(args []string) int {
	fs := flag.NewFlagSet("http", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "loopback listen address (host must be 127.0.0.1/localhost/::1)")
	dbPath := fs.String("db", "", "SQLite graphstore path (empty = in-memory)")
	root := fs.String("root", "", "optional repo root to ingest on startup (attaches the ingest-event producer for SSE)")
	metaDir := fs.String("meta", "", "ingest meta sidecar dir; defaults to an OS temp dir when -root is set")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: http: %v\n", err)
		return 1
	}

	store, err := openStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	broker := observe.New()
	cleanupIngest := func() {}
	if *root != "" {
		// ingest meta needs a real dir: the ":memory:" path opens a private DB
		// per pooled connection (tables vanish), so default to a temp dir.
		meta := *metaDir
		if meta == "" {
			td, err := os.MkdirTemp("", "graphi-http-meta-*")
			if err != nil {
				fmt.Fprintf(os.Stderr, "graphi: meta temp dir: %v\n", err)
				return 1
			}
			meta = td
			cleanupIngest = func() { _ = os.RemoveAll(td) }
		}
		ing, ierr := ingest.New(store, parse.NewDefaultRegistry(), meta)
		if ierr != nil {
			fmt.Fprintf(os.Stderr, "graphi: ingest: %v\n", ierr)
			return 1
		}
		ing.WithBroker(broker)
		cleanupIngest = func() { _ = ing.Close(); _ = os.RemoveAll(meta) }
		if ierr := ing.IngestAll(context.Background(), *root); ierr != nil {
			fmt.Fprintf(os.Stderr, "graphi: initial ingest: %v\n", ierr)
			return 1
		}
	}
	defer cleanupIngest()

	asvc := analysis.NewDefaultService(store)
	c := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
	ln, err := httpsrv.ListenLoopback(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	fmt.Printf("graphi http listening on %s (schema_version=%d)\n", ln.Addr(), httpsrv.SchemaVersion)
	// Inject the analyzer names so /contract can advertise them for client
	// capability negotiation without the http package importing engine/analysis.
	srv := httpsrv.New(c, broker).WithWiki(store).WithDescriptors(asvc.Names())
	if err := srv.Serve(ln); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: http serve: %v\n", err)
		return 1
	}
	return 0
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

// printHelp prints the help blurb. Bare `graphi` now runs the zero-config
// index+serve flow (SW-067); the original SW-001 help text is preserved here
// under `graphi help`, prefixed with a line documenting the new default.
func printHelp() {
	reg := parse.NewDefaultRegistry()
	fmt.Printf("graphi: run with no arguments to index the current repo and open the local UI in your browser.\nregistered languages: %v\nsubcommands: query, search, index, savings, analyze, refactor-preview, refactor, undo, mcp, daemon, http, tui, setup, setup-embedder, privacy-audit, version, help, parse <file>\n", reg.Languages())
}

// runParseDefault preserves the original SW-001 parser-registry behavior.
func runParseDefault(args []string) {
	reg := parse.NewDefaultRegistry()

	if len(args) < 1 {
		printHelp()
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

// runSetup registers graphi's MCP stdio server into the Claude Code client
// config in one command (SW-044). Idempotent, non-destructive, atomic; --dry-run
// previews without writing. Offline.
//
//	graphi setup [--dry-run] [--binary path] [--config path]
func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the planned config change without writing")
	binary := fs.String("binary", "", "graphi binary to register (default: this executable)")
	cfgPath := fs.String("config", "", "config file path (default: ~/.claude.json, or $CLAUDE_CONFIG_PATH)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup: %v\n", err)
		return 1
	}
	bin := *binary
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: resolve executable: %v\n", err)
			return 1
		}
		bin = exe
	}
	path := *cfgPath
	if path == "" {
		p, err := mcpconfig.ConfigPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
			return 1
		}
		path = p
	}
	entry := mcpconfig.GraphiEntry(bin, nil)
	res, err := mcpconfig.Apply(path, "graphi", entry, *dryRun)
	if err != nil {
		// Actionable remediation: name the config path and the most likely fixes.
		// The original config is byte-identical (atomic write + fail-closed backup),
		// so this is safe to retry after addressing the cause.
		fmt.Fprintf(os.Stderr, "graphi: setup failed for %s: %v\n", path, err)
		fmt.Fprintln(os.Stderr, "  - check the file/directory is writable (permissions), or pass --config <path>")
		fmt.Fprintln(os.Stderr, "  - if Claude Code is not installed, install it first (the config is created on first setup)")
		fmt.Fprintln(os.Stderr, "  - your existing config was left unchanged (atomic write + fail-closed backup)")
		return 1
	}
	if *dryRun {
		fmt.Print("[dry-run] no changes written\n")
	}
	fmt.Print(res.Diff)
	if res.Action == mcpconfig.ActionUnchanged {
		fmt.Printf("graphi already configured in %s — no changes.\n", path)
		return 0
	}
	fmt.Printf("graphi MCP server %s in %s (command=%s args=%v)\n", res.Action, path, entry.Command, entry.Args)
	if res.BackupPath != "" {
		fmt.Printf("backup of the original config written to %s\n", res.BackupPath)
	}
	if res.Action == mcpconfig.ActionCreated || res.Action == mcpconfig.ActionUpdated {
		fmt.Println("restart/reload Claude Code to expose graphi's tools.")
	}
	return 0
}

// runSetupEmbedder is the opt-in `graphi setup-embedder` command (SW-059). It
// prints the explicit GRAPHI_EMBEDDER config a user sets to enable the OPTIONAL
// semantic search. It is OFFLINE (no construction, no dial) and there is no
// hidden default — semantic search stays OFF until the user opts in.
//
//	graphi setup-embedder [<selector>]
func runSetupEmbedder(args []string) int {
	if err := cli.RunSetupEmbedder(context.Background(), args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup-embedder: %v\n", err)
		return 1
	}
	return 0
}

// runPrivacyAudit prints the local-first proof from real facts and exits non-zero
// on any violation (SW-044). Offline; reuses internal/cgoconformance +
// internal/canary.
//
//	graphi privacy-audit [--target ./...]
func runPrivacyAudit(args []string) int {
	fs := flag.NewFlagSet("privacy-audit", flag.ContinueOnError)
	target := fs.String("target", "./...", "build target to scan for CGo imports")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: privacy-audit: %v\n", err)
		return 1
	}
	rep := audit.Run(context.Background(), *target)
	rep.Render(os.Stdout)
	return rep.ExitCode()
}

// runTUI is provided by tui_enabled.go (//go:build tui) and tui_disabled.go
// (//go:build !tui). The interactive terminal surface (SW-047) pulls in the
// Bubble Tea dependency tree, which roughly doubles the binary; keeping it
// behind the `tui` build tag holds the default, local-first binary lean (the
// budget-gated benchmark enforces the size ceiling). Build with -tags tui to
// include it: `go build -tags tui ./cmd/graphi`.
