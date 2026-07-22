package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/distill"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ledger"
	"github.com/samibel/graphi/engine/memory"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/skillgen"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
)

// runQuery launches the CLI surface. Usage:
//
//	graphi query [-db path] [-daemon socket] <operation> -symbol <id> [-depth N]
func runQuery(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.Run(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: query: %v\n", err)
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
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()
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
	c, cleanup := makeClientOrOpenMeta(dbPath, socket, metaDir)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.RunSearch(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: search: %v\n", err)
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
	c, cleanup := makeClientOrOpenMeta(dbPath, socket, metaDir)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.RunSearchAST(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: search-ast: %v\n", err)
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
	c, cleanup := makeClientOrOpenMeta(dbPath, socket, metaDir)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.RunFindClones(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: find-clones: %v\n", err)
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
//
// startCPUProfile begins a CPU profile into path (empty = no-op) and returns
// the stop function. Best-effort: a profile that cannot start must never fail
// the command it is measuring. Local file write only — no egress.
func startCPUProfile(path string) func() {
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path) //nolint:gosec // operator-supplied local profile path
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: cpuprofile: %v\n", err)
		return func() {}
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: cpuprofile: %v\n", err)
		_ = f.Close()
		return func() {}
	}
	return func() {
		pprof.StopCPUProfile()
		_ = f.Close()
	}
}

func runIndex(args []string) int {
	return runIndexAt(getwd(), args)
}

// runIndexAt is runIndex with an injectable cwd (the anchor for the omitted
// -root default), so tests can pin the no-repo behavior without chdir.
func runIndexAt(cwd string, args []string) int {
	// Order-independent flag parsing: --semantic is a bool toggle; -root/-db/-meta
	// each take a value (space- or =-separated). Unknown tokens are ignored.
	semantic := false
	full := false
	root, dbPath, metaDir := "", "", ""
	var profileFlag *string
	cpuProfile := os.Getenv("GRAPHI_CPUPROFILE")
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
		case a == "--full" || a == "-full":
			full = true
		default:
			if v, ok := takeVal("-root"); ok {
				root = v
			} else if v, ok := takeVal("-db"); ok {
				dbPath = v
			} else if v, ok := takeVal("-meta"); ok {
				metaDir = v
			} else if v, ok := takeVal("-profile"); ok {
				profileFlag = &v
			} else if v, ok := takeVal("-cpuprofile"); ok {
				cpuProfile = v
			}
		}
	}
	// An omitted -root detects the cwd repo and (when -db/-meta are also
	// omitted) targets the auto-managed per-repo store — i.e. bare
	// `graphi index` now behaves exactly like `graphi sync`. An explicit -root
	// keeps the historical contract byte-for-byte, including the in-memory
	// store default when no -db is given.
	explicitRoot := root != ""
	target, terr := resolveIngestTarget(cwd, root, dbPath, metaDir, true)
	if errors.Is(terr, errNotARepo) {
		fmt.Fprintln(os.Stderr, "graphi: -root <repo> is required for index (or cd into a repo — see 'graphi sync')")
		return 1
	}
	if terr != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", terr)
		return 1
	}

	prof, err := profile.ResolveProfile(profileFlag, os.Getenv(profile.EnvName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	stopProfile := startCPUProfile(cpuProfile)
	defer stopProfile()

	ctx := context.Background()
	store, ing, prog, cleanup, err := openIngestSession(target.dbPath, target.metaDir, prof)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	defer cleanup()
	// Warm start by default (same cheap drift-check path bare `graphi` uses):
	// on an unchanged repo the second `graphi index` is a hash walk, not a full
	// re-parse. --full forces the cold pass (e.g. after a graphi upgrade whose
	// semantics stamp did not change, or to re-certify a store). Both paths
	// stamp the sync metadata `graphi status` reads.
	var ierr error
	if full {
		ierr = rtime.RebuildRepo(ctx, ing, store, target.root)
	} else {
		_, ierr = rtime.SyncRepo(ctx, ing, store, target.root, prog.Handle)
	}
	prog.Finish(ierr)
	if ierr != nil {
		fmt.Fprintf(os.Stderr, "graphi: index: %v\n", ierr)
		return 1
	}
	fmt.Printf("graphi index: ingested %s\n", target.root)
	if hint := indexHintLine(explicitRoot, isTerminal(os.Stderr)); hint != "" {
		fmt.Fprintln(os.Stderr, hint)
	}

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
	if dbPath == "" && socket == "" {
		// Same default discovery as `query`/`search`: without it, a bare
		// `graphi analyze` after a zero-config index would silently run
		// against an empty in-memory store.
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.RunAnalysis(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: analyze: %v\n", err)
		return 1
	}
	return 0
}

// runDiagnose launches the CLI diagnostics surface (SW-091/SW-094). Usage:
//
//	graphi diagnose [-db path] [-daemon socket] [<kind>...]
//
// Positional args select analyzer kinds; none ⇒ all built-ins. Read-only: it
// runs over the same Reader the queries use and mutates nothing.
func runDiagnose(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.RunDiagnose(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: diagnose: %v\n", err)
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
	c, storeCleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer storeCleanup()
	// If an in-process client, attach a local ledger so the readout is available.
	if socket == "" {
		if ledgerPath == "" {
			fmt.Fprintln(os.Stderr, "graphi: savings: no ledger to read — pass -ledger <path> (the ledger a prior MCP/daemon session wrote)")
			return 1
		}
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
	c, storeCleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer storeCleanup()
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

// runAgentBrief launches the CLI agent_brief surface (EP-024 SW-134). Usage:
//
//	graphi agent-brief [-topic <topic>] [-ledger path]
func runAgentBrief(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	ledgerPath := extractLedgerFlag(&rest)
	c, storeCleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer storeCleanup()
	if socket != "" {
		fmt.Fprintln(os.Stderr, "graphi: agent-brief: not available via daemon in this build")
		return 1
	}
	c, cleanup, err := withLedgerAndAgentServices(c, ledgerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: agent-brief: %v\n", err)
		return 1
	}
	defer cleanup()
	if err := cli.RunAgentBrief(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: agent-brief: %v\n", err)
		return 1
	}
	return 0
}

// runAgentTool launches one of the EP-020 CLI agent-tool surfaces
// (explain-symbol, related-files, change-risk). All three ride the shared
// client seam, so their bytes match MCP tools/call exactly. Usage:
//
//	graphi explain-symbol [-db path] [-max-items n] <symbol|path|node-id>
//	graphi related-files  [-db path] [-direction d] [-max-files n] <target>
//	graphi change-risk    [-db path] [-max-items n] (<target> | -diff <file|->)
func runAgentTool(args []string, verb string) int {
	dbPath, socket, rest := extractFlags(args)
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(getwd(), "", "")
	}
	c, storeCleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer storeCleanup()
	if socket != "" {
		fmt.Fprintf(os.Stderr, "graphi: %s: not available via daemon in this build\n", verb)
		return 1
	}
	var err error
	switch verb {
	case "explain-symbol":
		err = cli.RunExplainSymbol(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "related-files":
		err = cli.RunRelatedFiles(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "change-risk":
		err = cli.RunChangeRisk(context.Background(), c, rest, os.Stdin, os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %s: %v\n", verb, err)
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
	c, storeCleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer storeCleanup()
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
	c, storeCleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer storeCleanup()
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
