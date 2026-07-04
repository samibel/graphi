package main

import (
	"fmt"
	"io"
	"sort"
)

// subHelp is the per-subcommand help entry: a one-line synopsis, the usage
// line, and one copy-pasteable example. A test asserts every dispatch case in
// main() has an entry, so a new subcommand cannot ship help-less.
type subHelp struct {
	synopsis string
	usage    string
	example  string
}

// subcommandHelp maps every dispatch-switch subcommand to its help entry.
var subcommandHelp = map[string]subHelp{
	"query": {
		"structural query over the graph (callers|callees|references|definition|neighborhood)",
		"graphi query <op> -symbol <node-id> [-depth N] [-db path] [-daemon socket]",
		"graphi query callers -symbol a1b2c3d4 -db ~/.graphi/graph.db",
	},
	"compound": {
		"compound / Cypher-style graph query (SEED/HOP/WHERE/MAXDEPTH)",
		"graphi compound [-db path] [-daemon socket] -q \"SEED ...\" (or query text on stdin)",
		"graphi compound -db graph.db -q 'SEED kind=function\\nHOP calls'",
	},
	"search": {
		"lexical / symbol search; -semantic runs the optional embedding search",
		"graphi search [-limit N] [-semantic] <query> [-db path] [-daemon socket] [-meta dir]",
		"graphi search -db graph.db -limit 10 parseFile",
	},
	"search-ast": {
		"structural AST pattern query",
		"graphi search-ast [-limit N] <json-pattern> [-db path]",
		`graphi search-ast -db graph.db '{"kind":"function","name":{"regex":"^handle"}}'`,
	},
	"find-clones": {
		"edge-profile clone detection",
		"graphi find-clones [<json-config>] [-db path]",
		`graphi find-clones -db graph.db '{"threshold":0.9}'`,
	},
	"index": {
		"ingest a repo into a durable store (warm-starts on an unchanged repo; --full forces a cold pass; optional --semantic embedding pass)",
		"graphi index -root <repo> [-db path] [-meta dir] [--full] [--semantic]",
		"graphi index -root . -db ~/.graphi/graph.db -meta ~/.graphi/meta",
	},
	"savings": {
		"session token-savings readout from a ledger a prior MCP/daemon session wrote",
		"graphi savings -ledger <path> [-db path] [-daemon socket]",
		"graphi savings -ledger ~/.graphi/ledger.db",
	},
	"memory": {
		"agent memory operations",
		"graphi memory store|recall|forget [-scope s] [-notebook n] [-payload text] [-tags a,b] [-id id] [-ledger path]",
		"graphi memory store -scope repo -notebook decisions -payload \"we chose sqlite\"",
	},
	"agent-brief": {
		"bounded, cited task-start context packet for agents",
		"graphi agent-brief [-topic <topic>] [-ledger path]",
		"graphi agent-brief -topic \"engine/agenttools/brief\"",
	},
	"distill": {
		"distill a session into a compact decision record",
		"graphi distill -session <id> [-decisions d1,d2] [-risks r1] [-questions q1] [-files a.go,b.go] [-ledger path]",
		"graphi distill -session s1 -decisions \"use sqlite\" -files core/graphstore/sqlite.go",
	},
	"skillgen": {
		"deterministic skill generation from a procedure description",
		"graphi skillgen -name <n> -trigger <t> [-description <d>] [-ledger path]",
		"graphi skillgen -name reindex -trigger \"after big refactor\" -description \"run graphi index\"",
	},
	"analyze": {
		"run a registered analyzer over the graph",
		"graphi analyze <analyzer> -symbol <node-id> [-direction forward|reverse] [-max-nodes N] [-target id] [-concept term] [-diff d|-diff-path f] [-db path]",
		"graphi analyze impact -symbol a1b2c3d4 -direction reverse -db graph.db",
	},
	"pr-comment": {
		"sticky PR review comment + optional risk-threshold merge gate",
		"graphi pr-comment -diff <unified-diff>|-diff-path <file> [-pr ref] [-provenance summary|full] [-gate] [-gate-threshold N] [-publish]",
		"graphi pr-comment -diff-path change.diff -gate",
	},
	"list-prs": {
		"read-only forge enumeration of open PRs (GITHUB_TOKEN/GITHUB_REPOSITORY env)",
		"graphi list-prs [-db path] [-daemon socket]",
		"GITHUB_TOKEN=... GITHUB_REPOSITORY=owner/repo graphi list-prs",
	},
	"triage-prs": {
		"graph-derived multi-PR triage ranking",
		"graphi triage-prs [-db path] [-daemon socket]",
		"GITHUB_TOKEN=... GITHUB_REPOSITORY=owner/repo graphi triage-prs -db graph.db",
	},
	"conflicts-prs": {
		"inter-PR conflict detection",
		"graphi conflicts-prs [-db path] [-daemon socket]",
		"GITHUB_TOKEN=... GITHUB_REPOSITORY=owner/repo graphi conflicts-prs -db graph.db",
	},
	"suggest-reviewers": {
		"ranked candidate-reviewer recommender for a touched set",
		"graphi suggest-reviewers [-db path] -diff <unified-diff|refs> | -diff-path <file>",
		"graphi suggest-reviewers -db graph.db -diff-path change.diff",
	},
	"compare-branches": {
		"graph-level diff of two graphi SQLite snapshots (paths, never git refs)",
		"graphi compare-branches -base <db-path> -head <db-path>",
		"graphi compare-branches -base base-graph.db -head head-graph.db",
	},
	"critique-review": {
		"graph-evidence critique of an existing PR review",
		"graphi critique-review [-db path] -diff <d>|-diff-path <f> [-pr N] [-review <json>|-review-path <f>]",
		"graphi critique-review -db graph.db -diff-path change.diff -review-path review.json",
	},
	"refactor-preview": {
		"impact-set preview of a refactor, no mutation",
		"graphi refactor-preview -root <repo> [-db path] [-meta dir] -kind <k> -target <id> -old-name X -new-name Y",
		"graphi refactor-preview -root . -kind rename -target a1b2 -old-name Foo -new-name Bar",
	},
	"refactor": {
		"commit a refactor through the atomic edit saga",
		"graphi refactor -root <repo> [-db path] [-meta dir] -kind <k> -target <id> -old-name X -new-name Y [-actor who]",
		"graphi refactor -root . -kind rename -target a1b2 -old-name Foo -new-name Bar",
	},
	"undo": {
		"reverse an applied edit by its undo token",
		"graphi undo -root <repo> [-db path] [-meta dir] -token <undo-token> [-actor who]",
		"graphi undo -root . -token 01HXYZ...",
	},
	"diagnose": {
		"graph-derived diagnostics + suggested code-actions",
		"graphi diagnose [-db path] [-daemon socket] [<kind>...]",
		"graphi diagnose -db graph.db dead_symbol",
	},
	"inline": {
		"inline refactor over the edit saga (single-line initializer targets)",
		"graphi inline -root <repo> [-db path] [-meta dir] [-dry-run] <target-node-id>",
		"graphi inline -root . -dry-run a1b2c3d4",
	},
	"safe-delete": {
		"reference-safety-gated delete (removes the declaration line only)",
		"graphi safe-delete -root <repo> [-db path] [-meta dir] [-dry-run] <target-node-id>",
		"graphi safe-delete -root . -dry-run a1b2c3d4",
	},
	"mcp": {
		"MCP stdio server (the agent-first surface)",
		"graphi mcp [-db path] [-daemon socket]",
		"graphi mcp -db ~/.graphi/graph.db",
	},
	"daemon": {
		"hot-index Unix-socket daemon lifecycle",
		"graphi daemon start|stop|status [-socket path] [-db path]",
		"graphi daemon start -socket /tmp/graphi.sock -db graph.db",
	},
	"http": {
		"read-only loopback HTTP REST + SSE surface",
		"graphi http [-addr 127.0.0.1:8080] [-db path] [-root repo] [-meta dir]",
		"graphi http -db graph.db -addr 127.0.0.1:8080",
	},
	"doctor": {
		"read-only diagnostic checkup for binary, PATH, MCP clients, DB, privacy, and local-first invariants",
		"graphi doctor [-db path] [--json]",
		"graphi doctor --json",
	},
	"setup": {
		"register graphi's MCP stdio server into local MCP clients' configs",
		"graphi setup [--client claude|copilot|cursor|windsurf|claude-desktop|all] [--dry-run] [--binary path] [--config path]",
		"graphi setup --dry-run",
	},
	"setup-embedder": {
		"print how to opt in to the optional semantic search (offline)",
		"graphi setup-embedder [<selector>]",
		"graphi setup-embedder ollama",
	},
	"tui": {
		"interactive terminal surface (requires a -tags tui build)",
		"graphi tui [-db path] [-daemon socket]",
		"graphi tui -db graph.db",
	},
	"privacy-audit": {
		"print the local-first proof (CGo scan + canary egress guard); non-zero on violation",
		"graphi privacy-audit [--target ./...]",
		"graphi privacy-audit",
	},
	"upgrade": {
		"user-initiated self-update to the latest release (never automatic)",
		"graphi upgrade [-print]",
		"graphi upgrade -print",
	},
	"version": {
		"print version / commit / build date stamped into the binary",
		"graphi version",
		"graphi version",
	},
	"help": {
		"print general help, or detailed help for one subcommand",
		"graphi help [<subcommand>]",
		"graphi help query",
	},
	"parse": {
		"parse a single file through the registry (the original default)",
		"graphi parse <file>",
		"graphi parse main.go",
	},
	"ui": {
		"index the current repo and open the local graph UI (alias for bare `graphi`)",
		"graphi ui",
		"graphi ui",
	},
	"claude": {
		"register graphi's MCP server in Claude Code (alias for `setup`)",
		"graphi claude [--dry-run]",
		"graphi claude",
	},
}

// isHelpFlag reports whether s is one of the conventional help flags.
func isHelpFlag(s string) bool {
	return s == "-h" || s == "-help" || s == "--help"
}

// printSubcommandHelp writes the help entry for name to w and reports whether
// name was known. Short query/analyze verbs resolve to their long form's entry.
func printSubcommandHelp(name string, w io.Writer) bool {
	entry, ok := subcommandHelp[name]
	if !ok {
		// Short verbs are thin aliases; show the long form they rewrite onto.
		switch {
		case queryVerbSet[name]:
			entry, ok = subcommandHelp["query"], true
		case analyzeVerbSet()[name]:
			entry, ok = subcommandHelp["analyze"], true
		}
	}
	if !ok {
		return false
	}
	fmt.Fprintf(w, "graphi %s — %s\n", name, entry.synopsis)
	fmt.Fprintf(w, "usage:   %s\n", entry.usage)
	fmt.Fprintf(w, "example: %s\n", entry.example)
	return true
}

// runHelp implements `graphi help [<subcommand>]`.
func runHelp(args []string, w io.Writer) int {
	if len(args) == 0 {
		printHelp()
		return 0
	}
	if printSubcommandHelp(args[0], w) {
		return 0
	}
	fmt.Fprintf(w, "graphi: unknown subcommand %q\n", args[0])
	names := make([]string, 0, len(subcommandHelp))
	for n := range subcommandHelp {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "known subcommands:")
	for _, n := range names {
		fmt.Fprintf(w, "  %-18s %s\n", n, subcommandHelp[n].synopsis)
	}
	return 1
}
