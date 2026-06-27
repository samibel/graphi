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
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/distill"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/embed"
	_ "github.com/samibel/graphi/engine/embed/ollama" // opt-in loopback embedder: registers the "ollama" scheme; never constructed on the default path
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/skillgen"
	"github.com/samibel/graphi/engine/watch"
	"github.com/samibel/graphi/internal/audit"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/version"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	"github.com/samibel/graphi/surfaces/forge"
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
	case "compound":
		os.Exit(runCompound(os.Args[2:]))
	case "search":
		os.Exit(runSearch(os.Args[2:]))
	case "search-ast":
		os.Exit(runSearchAST(os.Args[2:]))
	case "find-clones":
		os.Exit(runFindClones(os.Args[2:]))
	case "index":
		os.Exit(runIndex(os.Args[2:]))
	case "savings":
		os.Exit(runSavings(os.Args[2:]))
	case "memory":
		os.Exit(runMemory(os.Args[2:]))
	case "distill":
		os.Exit(runDistill(os.Args[2:]))
	case "skillgen":
		os.Exit(runSkillGen(os.Args[2:]))
	case "analyze":
		os.Exit(runAnalyze(os.Args[2:]))
	case "pr-comment":
		os.Exit(runPrComment(os.Args[2:]))
	case "list-prs":
		os.Exit(runListPRs(os.Args[2:]))
	case "triage-prs":
		os.Exit(runTriagePRs(os.Args[2:]))
	case "conflicts-prs":
		os.Exit(runConflictsPRs(os.Args[2:]))
	case "suggest-reviewers":
		os.Exit(runSuggestReviewers(os.Args[2:]))
	case "compare-branches":
		os.Exit(runCompareBranches(os.Args[2:]))
	case "critique-review":
		os.Exit(runCritiqueReview(os.Args[2:]))
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
	case "ui":
		// Short verb (SW-069): alias for the zero-config index+serve flow.
		os.Exit(runZeroConfig())
	case "claude":
		// Short verb (SW-069): alias for `setup` (register the MCP server).
		os.Exit(runSetup(os.Args[2:]))
	default:
		// Short verbs (SW-069, EP-010 Task F): thin aliases that rewrite argv
		// onto the existing query/analyze dispatchers (byte-identical output).
		// These are checked BEFORE the filename fallback so they never shadow an
		// existing subcommand and a real filename still parses.
		if queryVerbSet[os.Args[1]] {
			os.Exit(runQuery(rewriteVerbArgs(os.Args[1], os.Args[2:])))
		}
		if analyzeVerbSet()[os.Args[1]] {
			os.Exit(runAnalyze(rewriteVerbArgs(os.Args[1], os.Args[2:])))
		}
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

// runCompound launches the CLI surface for a compound / Cypher-style graph query
// (EP-011 G1). Usage:
//
//	graphi compound [-db path] [-daemon socket] -q "SEED ..\nHOP .."
//	graphi compound [-db path] [-daemon socket] < query.txt   (stdin)
//
// The query text is passed to the shared client.Compound seam (byte-identical
// to the MCP/HTTP/daemon surfaces). Output is the canonical query.Result.
func runCompound(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	fs := flag.NewFlagSet("compound", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	q := fs.String("q", "", "compound query text (SEED/HOP/WHERE/MAXDEPTH); if empty, read from stdin")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	text := *q
	if text == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "compound: read stdin:", err)
			return 1
		}
		text = string(b)
	}
	b, err := c.Compound(context.Background(), text)
	if err != nil {
		fmt.Fprintln(os.Stderr, "compound:", err)
		return 1
	}
	if _, err := os.Stdout.Write(append(b, '\n')); err != nil {
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

// runSearchAST launches the CLI structural-AST-search surface (SW-082 / SW-085).
// Usage:
//
//	graphi search-ast [-db path] [-daemon socket] [-limit N] <json-pattern>
func runSearchAST(args []string) int {
	dbPath, socket, metaDir, rest := extractFlagsMeta(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c := makeClientOrOpenMeta(dbPath, socket, metaDir)
	if c == nil {
		return 1
	}
	if err := cli.RunSearchAST(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		return 1
	}
	return 0
}

// runFindClones launches the CLI clone-detection surface (SW-083 / SW-085). Usage:
//
//	graphi find-clones [-db path] [-daemon socket] [<json-config>]
func runFindClones(args []string) int {
	dbPath, socket, metaDir, rest := extractFlagsMeta(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c := makeClientOrOpenMeta(dbPath, socket, metaDir)
	if c == nil {
		return 1
	}
	if err := cli.RunFindClones(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
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

	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
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

// runListPRs launches the SW-105 read-only forge PR-enumeration surface. Usage:
//
//	graphi list-prs [-db path] [-daemon socket]
//
// The forge client is resolved from the GitHub Actions environment
// (forge.FromEnv reads GITHUB_TOKEN/GITHUB_REPOSITORY/GITHUB_API_URL — never
// argv). Absent a token the forge is unwired and the surface reports the typed
// "forge unavailable" error (local-first: no network without explicit config).
func runListPRs(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeForgeClient(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunListPRs(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: list-prs: %v\n", err)
		return 1
	}
	return 0
}

// runTriagePRs launches the SW-105 single-pass graph-derived PR triage ranking.
// Usage: graphi triage-prs [-db path] [-daemon socket]. Enumeration is the only
// egress (forge.FromEnv); ranking is a zero-egress pass over the local graph.
func runTriagePRs(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeForgeClient(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunTriagePRs(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: triage-prs: %v\n", err)
		return 1
	}
	return 0
}

// runConflictsPRs launches the SW-106 inter-PR conflict detection. Usage:
// graphi conflicts-prs [-db path] [-daemon socket]. Enumeration is the only egress
// (forge.FromEnv); conflict detection is a zero-egress pass over the local graph.
func runConflictsPRs(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeForgeClient(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunConflictsPRs(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: conflicts-prs: %v\n", err)
		return 1
	}
	return 0
}

// runSuggestReviewers launches the SW-107 reviewer recommender. Usage:
//
//	graphi suggest-reviewers [-db path] -diff <unified-diff> | -diff-path <file>
//
// The ranking is a zero-egress pass over the local graph + git history; the diff
// is local-first untrusted input resolved through the reused EP-007 kernel.
func runSuggestReviewers(args []string) int {
	dbPath, _, rest := extractFlags(args)
	fs := flag.NewFlagSet("suggest-reviewers", flag.ContinueOnError)
	diff := fs.String("diff", "", "unified diff or line-oriented refs of the change")
	diffPath := fs.String("diff-path", "", "read the diff/refs from this local file instead of -diff")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	payload := *diff
	if *diffPath != "" {
		b, err := os.ReadFile(*diffPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: suggest-reviewers: read diff: %v\n", err)
			return 1
		}
		payload = string(b)
	}
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	c := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(analysis.NewDefaultService(store))
	if err := cli.RunSuggestReviewers(context.Background(), c, payload, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: suggest-reviewers: %v\n", err)
		return 1
	}
	return 0
}

// runCompareBranches launches the SW-107 graph-level branch comparator. Usage:
//
//	graphi compare-branches -base <db-path> -head <db-path>
//
// Each ref is a path to an already-built graph state (a graphi SQLite snapshot).
// Materialization happens HERE, above the surface boundary, by opening each
// persisted state; the engine compare-branches analyzer receives the two read-only
// states and performs a pure local set-diff with ZERO egress (it never resolves a
// git ref). An unknown/empty ref materializes to an empty state (well-defined diff).
func runCompareBranches(args []string) int {
	fs := flag.NewFlagSet("compare-branches", flag.ContinueOnError)
	base := fs.String("base", "", "base branch graph-state path (graphi SQLite snapshot)")
	head := fs.String("head", "", "head branch graph-state path (graphi SQLite snapshot)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	m := newSnapshotMaterializer()
	defer m.Close()
	// The compare analyzer ignores the service reader (it diffs the two Params
	// states), so an empty in-memory store backs the service.
	store := graphstore.NewMemStore()
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithBranchStates(m)
	if err := cli.RunCompareBranches(context.Background(), c, *base, *head, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: compare-branches: %v\n", err)
		return 1
	}
	return 0
}

// runCritiqueReview launches the SW-108 review critique (the EP-018 capstone). Usage:
//
//	graphi critique-review [-db path] -diff <unified-diff>|-diff-path <file> \
//	  [-pr N] [-review <json>|-review-path <file>]
//
// The EXISTING review is supplied inline (-review/-review-path, read once HERE at the
// surface) or fetched from the forge for -pr via the net-new read-only review-fetch
// egress (resolved from the environment; unwired when no token). The critique itself
// is a ZERO-egress pass over the local graph: it replays the EP-007 oracle and runs
// the three-way gap/over_flag/unsupported_claim diff against the review.
func runCritiqueReview(args []string) int {
	dbPath, _, rest := extractFlags(args)
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	d := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(analysis.NewDefaultService(store))
	// Wire the net-new read-only review-fetch egress from the environment so a -pr
	// with no inline review can fetch the existing review. When no token is present
	// the fetcher stays unwired (local-first default) — an inline review still works.
	if gh, ferr := forge.FromEnv(); ferr == nil && gh != nil {
		d = d.WithReviewFetcher(gh)
	}
	// The CLI surface parses -diff/-diff-path/-pr/-review/-review-path and reads any
	// local files ONCE at the surface (no engine file I/O / remote fetch on the hot path).
	if err := cli.RunCritiqueReview(context.Background(), d, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: critique-review: %v\n", err)
		return 1
	}
	return 0
}

// snapshotMaterializer is the cmd-side BranchStateMaterializer: it materializes a
// branch ref → read-only graph state by opening the persisted graphi state at that
// path (reusing the existing graphstore persistence path). An empty ref yields an
// empty in-memory state (a degenerate side → well-defined empty diff, no error).
// Opened stores are tracked and closed together after the single command runs, so
// both states stay live across the dispatch. This stays ABOVE the surface boundary;
// the engine never sees a ref.
type snapshotMaterializer struct {
	opened []graphstore.Graphstore
}

func newSnapshotMaterializer() *snapshotMaterializer { return &snapshotMaterializer{} }

func (m *snapshotMaterializer) StateForRef(_ context.Context, ref string) (query.Reader, error) {
	if strings.TrimSpace(ref) == "" {
		return graphstore.NewMemStore(), nil // empty/unknown ref → empty state
	}
	store, err := graphstore.OpenSQLite(ref)
	if err != nil {
		return nil, err
	}
	m.opened = append(m.opened, store)
	return store, nil
}

func (m *snapshotMaterializer) Close() {
	for _, s := range m.opened {
		_ = s.Close()
	}
}

// makeForgeClient builds an in-process client wired with the read-only forge
// PR-enumeration boundary (SW-105). The forge is resolved from the environment;
// when no token is present the forge stays unwired and the triage surface reports
// the typed unavailable error rather than dialing anything (local-first default).
func makeForgeClient(dbPath, socket string) client.Client {
	if socket != "" {
		// The daemon path has no forge RPC yet; it reports forge-unavailable.
		return daemon.NewClient(socket, "")
	}
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return nil
	}
	defer func() { _ = store.Close() }()
	asvc := analysis.NewDefaultService(store)
	d := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
	if gh, ferr := forge.FromEnv(); ferr == nil && gh != nil {
		d = d.WithForge(gh)
	}
	return d
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
	ing, err := ingest.New(store, ingest.NewNotebookParser(reg), metaDir)
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
		fi, ierr := ingest.New(fs, ingest.NewNotebookParser(parse.NewDefaultRegistry()), "")
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

// watchStatusProvider adapts the engine/watch Manager's read-only per-root health
// to the analysis.WatchStatusProvider seam (SW-104). It lives in cmd (the only
// layer that may import both engine/watch and engine/analysis), so the
// `watcher-status` operation surfaces live, honest watcher health — including the
// SW-101 Reconcile error — over the single dispatch path, with no surface→engine
// coupling. It is read-only and adds no timestamp/wall-clock field.
type watchStatusProvider struct {
	mgr *watch.Manager
}

func (p watchStatusProvider) WatchStatus(_ context.Context) analysis.WatcherStatusReport {
	statuses := p.mgr.Statuses()
	rep := analysis.WatcherStatusReport{
		Active: len(statuses) > 0,
		Roots:  make([]analysis.WatchRootStatus, 0, len(statuses)),
	}
	for _, st := range statuses {
		rep.Roots = append(rep.Roots, analysis.WatchRootStatus{
			Root:      st.Root,
			Watching:  st.Watching,
			Healthy:   st.LastError == "",
			LastError: st.LastError,
		})
	}
	return rep
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
		// SW-101: wire a filesystem-watch Manager so `track`ing a workspace starts
		// a pure-Go watcher + bounded worker-pool that keeps THIS daemon's shared
		// store fresh (deterministic, canonical-ordered incremental apply) without
		// an explicit re-index. The factory builds a per-root ingester over the
		// shared store and performs the initial full index.
		watchMgr := watch.NewManager(func(root string) (watch.Indexer, func(), error) {
			metaDir, err := os.MkdirTemp("", "graphi-watch-meta-")
			if err != nil {
				return nil, nil, err
			}
			ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
			if err != nil {
				_ = os.RemoveAll(metaDir)
				return nil, nil, err
			}
			if err := ing.IngestAll(context.Background(), root); err != nil {
				_ = ing.Close()
				_ = os.RemoveAll(metaDir)
				return nil, nil, err
			}
			cleanup := func() { _ = ing.Close(); _ = os.RemoveAll(metaDir) }
			return ing, cleanup, nil
		}, watch.DefaultConfig())
		defer watchMgr.StopAll()
		// SW-104: give the daemon handler an analysis service so the four EP-017
		// operations (and every analyzer) route through the SAME single dispatch
		// path over the daemon RPC, and back `watcher-status` with a read-only
		// provider over the watch Manager so the daemon reports live, honest per-root
		// watcher health — including the SW-101 Reconcile error — rather than masking
		// it.
		asvc := analysis.NewDefaultServiceWithWatch(store, watchStatusProvider{mgr: watchMgr})
		handler := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
		srv := daemon.NewServerWithWatch(handler, watchMgr)
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
		ing, ierr := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), meta)
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

// withLedgerAndAgentServices attaches the EP-012 memory, distillation, and
// skill-generation services to an in-process Direct client when a ledger path
// is provided. It returns the possibly-upgraded client and a cleanup function.
func withLedgerAndAgentServices(c client.Client, ledgerPath string) (client.Client, func(), error) {
	d, ok := c.(*client.Direct)
	if !ok {
		return c, func() {}, nil
	}
	var l *ledger.Ledger
	var cleanup func()
	cleanup = func() {}
	if ledgerPath != "" {
		var err error
		l, err = ledger.Open(ledgerPath)
		if err != nil {
			return nil, nil, fmt.Errorf("open ledger: %w", err)
		}
		cleanup = func() { _ = l.Close() }
		d = d.WithLedger(l)
	}
	memStore, err := memory.NewMemStore(memory.NewLedgerHook(l, "", true))
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("memory store: %w", err)
	}
	prev := cleanup
	cleanup = func() { prev(); _ = memStore.Close() }
	d = d.WithMemory(memStore).
		WithDistill(distill.NewDistiller(distill.NewLedgerHook(l, "", true))).
		WithSkillGen(skillgen.NewGenerator(skillgen.NewLedgerHook(l, "", true)))
	return d, cleanup, nil
}

// runMemory launches the CLI memory surface (EP-012). Usage:
//
//	graphi memory store|recall|forget ... [-ledger path]
func runMemory(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	ledgerPath := extractLedgerFlag(&rest)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if socket != "" {
		fmt.Fprintln(os.Stderr, "graphi: memory: not available via daemon in this build")
		return 1
	}
	c, cleanup, err := withLedgerAndAgentServices(c, ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: memory: %v\n", err)
		return 1
	}
	defer cleanup()
	if err := cli.RunMemory(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: memory: %v\n", err)
		return 1
	}
	return 0
}

// runDistill launches the CLI session-distillation surface (EP-012). Usage:
//
//	graphi distill -session <id> ... [-ledger path]
func runDistill(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	ledgerPath := extractLedgerFlag(&rest)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if socket != "" {
		fmt.Fprintln(os.Stderr, "graphi: distill: not available via daemon in this build")
		return 1
	}
	c, cleanup, err := withLedgerAndAgentServices(c, ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: distill: %v\n", err)
		return 1
	}
	defer cleanup()
	if err := cli.RunDistill(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: distill: %v\n", err)
		return 1
	}
	return 0
}

// runSkillGen launches the CLI skill-generation surface (EP-012). Usage:
//
//	graphi skillgen -name <name> -trigger <trigger> ... [-ledger path]
func runSkillGen(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	ledgerPath := extractLedgerFlag(&rest)
	c := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	if socket != "" {
		fmt.Fprintln(os.Stderr, "graphi: skillgen: not available via daemon in this build")
		return 1
	}
	c, cleanup, err := withLedgerAndAgentServices(c, ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: skillgen: %v\n", err)
		return 1
	}
	defer cleanup()
	if err := cli.RunSkillGen(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: skillgen: %v\n", err)
		return 1
	}
	return 0
}

// extractLedgerFlag pulls -ledger <path> out of args and returns the path.
func extractLedgerFlag(args *[]string) string {
	rest := *args
	for i := 0; i < len(rest); i++ {
		if rest[i] == "-ledger" && i+1 < len(rest) {
			path := rest[i+1]
			*args = append(rest[:i], rest[i+2:]...)
			return path
		}
	}
	return ""
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
	fmt.Print("graphi: run with no arguments to index the current repo and open the local UI in your browser.\n")
	fmt.Print("\nQuick verbs:\n")
	fmt.Print("  graphi callers <symbol>     who calls a symbol (also: callees, references, definition, neighborhood)\n")
	fmt.Print("  graphi impact <symbol>      blast radius of a change (also: taint and other analyzers)\n")
	fmt.Print("  graphi ui                   index this repo and open the local UI\n")
	fmt.Print("  graphi claude               register graphi's MCP server in Claude Code\n")
	fmt.Print("\nAdvanced (long forms):\n")
	fmt.Print("  graphi query <op> -symbol <id> [-depth N]\n")
	fmt.Print("  graphi analyze <name> -symbol <id> [-direction forward|reverse] [-max-nodes N]\n")
	fmt.Printf("registered languages: %v\nsubcommands: query, search, index, savings, analyze, refactor-preview, refactor, undo, mcp, daemon, http, tui, setup, setup-embedder, privacy-audit, version, help, parse <file>\n", reg.Languages())
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

// runSetup registers graphi's MCP stdio server into one or more local MCP client
// configs in one command (SW-044, generalized). Idempotent, non-destructive,
// atomic; --dry-run previews without writing. Offline.
//
//	graphi setup [--client claude|copilot|cursor|windsurf|claude-desktop|all]
//	             [--dry-run] [--binary path] [--config path]
//
// Default (--client all): always target Claude Code (created if absent, matching
// historical behavior) plus every OTHER local client that looks installed. A
// specific --client targets just that one. --config overrides the file path for a
// single client (default claude), preserving the original single-file behavior.
func runSetup(args []string) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the planned config change without writing")
	binary := fs.String("binary", "", "graphi binary to register (default: this executable)")
	cfgPath := fs.String("config", "", "config file path override (single client; default: that client's path)")
	client := fs.String("client", "all", "client to wire: "+strings.Join(mcpconfig.ClientIDs(), "|")+"|all")
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

	// --config pins a single file; it implies a single client (the named one, or
	// claude by default) and reproduces the original single-file behavior exactly.
	if *cfgPath != "" {
		id := *client
		if id == "all" {
			id = "claude"
		}
		c, ok := mcpconfig.ClientByID(id)
		if !ok {
			fmt.Fprintf(os.Stderr, "graphi: setup: unknown --client %q\n", id)
			return 1
		}
		entry := mcpconfig.GraphiEntry(bin, nil)
		return reportSetup(c.Display, *cfgPath, entry, *dryRun, func() (mcpconfig.Result, error) {
			return mcpconfig.Apply(*cfgPath, "graphi", entry, *dryRun) // claude key; --config implies the claude shape
		})
	}

	// Resolve the set of target clients.
	var targets []mcpconfig.Client
	if *client == "all" {
		claude, _ := mcpconfig.ClientByID("claude")
		targets = append(targets, claude) // always, even if absent (created)
		for _, c := range mcpconfig.Clients() {
			if c.ID != "claude" && c.Configurable() {
				targets = append(targets, c)
			}
		}
	} else {
		c, ok := mcpconfig.ClientByID(*client)
		if !ok {
			fmt.Fprintf(os.Stderr, "graphi: setup: unknown --client %q (want one of %s|all)\n",
				*client, strings.Join(mcpconfig.ClientIDs(), "|"))
			return 1
		}
		targets = []mcpconfig.Client{c}
	}

	rc := 0
	for _, c := range targets {
		path, _ := c.ConfigPath()
		entry := mcpconfig.GraphiEntry(bin, nil)
		if reportSetup(c.Display, path, entry, *dryRun, func() (mcpconfig.Result, error) {
			return c.Apply(bin, nil, *dryRun)
		}) != 0 {
			rc = 1
		}
	}
	return rc
}

// reportSetup runs one client's apply closure and prints a consistent,
// actionable report. It returns 0 on success (including unchanged/dry-run) and 1
// on error, having left the target config byte-identical (atomic + fail-closed
// backup) so a retry is safe.
func reportSetup(display, path string, entry mcpconfig.ServerEntry, dryRun bool, apply func() (mcpconfig.Result, error)) int {
	res, err := apply()
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup failed for %s (%s): %v\n", display, path, err)
		fmt.Fprintln(os.Stderr, "  - check the file/directory is writable (permissions), or pass --config <path>")
		fmt.Fprintln(os.Stderr, "  - your existing config was left unchanged (atomic write + fail-closed backup)")
		return 1
	}
	if dryRun {
		fmt.Printf("[dry-run] %s: no changes written\n", display)
	}
	fmt.Print(res.Diff)
	if res.Action == mcpconfig.ActionUnchanged {
		fmt.Printf("%s: graphi already configured in %s — no changes.\n", display, path)
		return 0
	}
	fmt.Printf("%s: graphi MCP server %s in %s (command=%s args=%v)\n", display, res.Action, path, entry.Command, entry.Args)
	if res.BackupPath != "" {
		fmt.Printf("  backup of the original config written to %s\n", res.BackupPath)
	}
	if res.Action == mcpconfig.ActionCreated || res.Action == mcpconfig.ActionUpdated {
		fmt.Printf("  restart/reload %s to expose graphi's tools.\n", display)
	}
	return 0
}

// applyClients wires graphi into each given client using this executable as the
// registered binary. Used by the consent-gated first-run offer. Best-effort: it
// applies every client and returns the first error (if any).
func applyClients(cs []mcpconfig.Client) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	var firstErr error
	for _, c := range cs {
		if _, err := c.Apply(self, []string{"mcp"}, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
