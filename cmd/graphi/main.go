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
	"fmt"
	"os"
	"strings"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	_ "github.com/samibel/graphi/engine/embed/ollama" // opt-in loopback embedder: registers the "ollama" scheme; never constructed on the default path
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/releaseinfo"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/internal/version"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
)

func main() {
	_ = version.Version // linked so -ldflags -X can stamp it; see internal/version
	if len(os.Args) < 2 {
		// Zero-config default (SW-067): bare `graphi` detects the cwd repo,
		// indexes it, and serves the embedded UI on a loopback port. The old
		// help blurb now lives under `graphi help`.
		os.Exit(runZeroConfig())
	}

	// Uniform per-subcommand help: `graphi <sub> -h|-help|--help` (help flag as
	// the FIRST argument) prints the subcommand's usage + example and exits 0.
	// Unknown names fall through to normal dispatch (a real filename still parses).
	if len(os.Args) > 2 && isHelpFlag(os.Args[2]) && printSubcommandHelp(os.Args[1], os.Stdout) {
		os.Exit(0)
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
	case "agent-brief":
		os.Exit(runAgentBrief(os.Args[2:]))
	case "explain-symbol":
		os.Exit(runAgentTool(os.Args[2:], "explain-symbol"))
	case "related-files":
		os.Exit(runAgentTool(os.Args[2:], "related-files"))
	case "change-risk":
		os.Exit(runAgentTool(os.Args[2:], "change-risk"))
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
	case "diagnose":
		os.Exit(runDiagnose(os.Args[2:]))
	case "inline":
		os.Exit(runRefactor(os.Args[2:], "inline"))
	case "safe-delete":
		os.Exit(runRefactor(os.Args[2:], "safe-delete"))
	case "mcp":
		os.Exit(runMCP(os.Args[2:]))
	case "daemon":
		os.Exit(runDaemon(os.Args[2:]))
	case "http":
		os.Exit(runHTTP(os.Args[2:]))
	case "setup":
		os.Exit(runSetup(os.Args[2:]))
	case "doctor":
		os.Exit(runDoctor(os.Args[2:]))
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
		os.Exit(0)
	case "help":
		os.Exit(runHelp(os.Args[2:], os.Stdout))
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

// extractFlags pulls the global -db and -daemon options out of args (any
// position, space- or =-separated). -meta names the ingest sidecar dir; for the
// search path it is where the durable `vectors` table (SW-061) is reloaded from
// on startup so `search -semantic` returns hits without re-embedding.
func extractFlags(args []string) (dbPath, socket string, rest []string) {
	dbPath, socket, _, rest = extractFlagsMeta(args)
	return
}

// extractFlagsMeta is extractFlags plus the -meta sidecar dir. The global flags
// are accepted anywhere in the argument list — every documented example places
// them after the operation (`graphi query callers -symbol X -db graph.db`), so
// front-only extraction would silently ignore them.
func extractFlagsMeta(args []string) (dbPath, socket, metaDir string, rest []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		take := func(name string) (string, bool) {
			if a == name && i+1 < len(args) {
				i++
				return args[i], true
			}
			if strings.HasPrefix(a, name+"=") {
				return a[len(name)+1:], true
			}
			return "", false
		}
		if v, ok := take("-db"); ok {
			dbPath = v
			continue
		}
		if v, ok := take("-daemon"); ok {
			socket = v
			continue
		}
		if v, ok := take("-meta"); ok {
			metaDir = v
			continue
		}
		rest = append(rest, a)
	}
	return
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
// It prints errors and returns a nil client on failure. The returned cleanup
// closes the underlying store and must be deferred by the caller AFTER the
// command has run — closing earlier hands the caller a client over a closed
// store (every query then fails).
func makeClientOrOpen(dbPath, socket string) (client.Client, func()) {
	return makeClientOrOpenMeta(dbPath, socket, "")
}

// makeClientOrOpenMeta is makeClientOrOpen plus an optional meta sidecar dir.
// Since RUN-01 it is a thin adapter over the composition root's Attach mode
// (cmd/internal/runtime), which owns the store/client wiring exactly once; the
// print-and-nil error shape is preserved for the existing CLI call sites.
func makeClientOrOpenMeta(dbPath, socket, metaDir string) (client.Client, func()) {
	rt, err := rtime.Attach(dbPath, socket, metaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return nil, func() {}
	}
	return rt.Client, rt.Close
}

// runVersion prints the release version + VCS metadata embedded by SW-013's
// packaging (ldflags-stamped version.Version + debug.ReadBuildInfo VCS stamps).
// It is how the release checker verifies the embedded version/commit/date.
func runVersion() {
	fmt.Println(releaseinfo.New().VersionString())
}

// runTUI is provided by tui_enabled.go (//go:build tui) and tui_disabled.go
// (//go:build !tui). The interactive terminal surface (SW-047) pulls in the
// Bubble Tea dependency tree, which roughly doubles the binary; keeping it
// behind the `tui` build tag holds the default, local-first binary lean (the
// budget-gated benchmark enforces the size ceiling). Build with -tags tui to
// include it: `go build -tags tui ./cmd/graphi`.
