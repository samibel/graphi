package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/audit"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/cli"
)

// runSetup registers graphi's MCP stdio server into one or more local MCP client
// configs in one command (SW-044, generalized). Idempotent, non-destructive,
// atomic; --dry-run previews without writing. Offline.
//
//	graphi setup [--client claude|copilot|cursor|devin|windsurf|claude-desktop|all]
//	             [--dry-run] [--binary path] [--config path]
//	graphi setup --project [--root <repo>] [--attach] [--dry-run] [--binary path]
//
// Default (--client all): always target Claude Code (created if absent, matching
// historical behavior) plus every OTHER local client that looks installed. A
// specific --client targets just that one. --config overrides the file path for a
// single client (default claude), preserving the original single-file behavior.
//
// --project is the per-repository variant (the mcpconfig follow-up the global
// contract deliberately deferred): it upserts graphi into the project-scoped
// .mcp.json at the repository root, with the session root pinned via
// `mcp -root <abs root>` — so a client that launches the server outside the
// repo (cwd=$HOME) and supplies no MCP roots still binds this repository. The
// root is detected from the working directory, or named with --root. --attach
// pins the auto-managed per-repo store instead (`mcp -db <store> -meta
// <sidecar>`, Attach mode: no auto-ingest — pair with `graphi sync`).
func runSetup(args []string) int {
	// setup --check is a diagnostic alias for `graphi doctor`.
	for _, a := range args {
		if a == "--check" || a == "-check" {
			return runDoctor(nil)
		}
	}
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "print the planned config change without writing")
	binary := fs.String("binary", "", "graphi binary to register (default: this executable)")
	cfgPath := fs.String("config", "", "config file path override (single client; default: that client's path)")
	client := fs.String("client", "all", "client to wire: "+strings.Join(mcpconfig.ClientIDs(), "|")+"|all")
	project := fs.Bool("project", false, "write the project-scoped .mcp.json at the repository root with the session root pinned (mcp -root)")
	rootFlag := fs.String("root", "", "repository root for --project (default: detected from the working directory)")
	attach := fs.Bool("attach", false, "with --project: pin the auto-managed per-repo store instead (mcp -db/-meta; Attach mode, no auto-ingest)")
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

	if *rootFlag != "" && !*project {
		fmt.Fprintln(os.Stderr, "graphi: setup: --root requires --project (client configs are global; only the project file pins a repository)")
		return 1
	}
	if *attach && !*project {
		fmt.Fprintln(os.Stderr, "graphi: setup: --attach requires --project (the per-repo store paths only make sense in the project file)")
		return 1
	}
	if *project {
		clientSet := false
		fs.Visit(func(f *flag.Flag) {
			if f.Name == "client" || f.Name == "config" {
				clientSet = true
			}
		})
		if clientSet {
			fmt.Fprintln(os.Stderr, "graphi: setup: --project writes <root>/.mcp.json; --client/--config do not apply to it")
			return 1
		}
		return runSetupProject(getwd(), *rootFlag, bin, *dryRun, *attach)
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

// runSetupProject upserts graphi into the project-scoped .mcp.json at the
// repository root. Default: pin the MCP session root via `mcp -root <abs
// root>` (the session then auto-manages and auto-syncs its per-repo store).
// With attach, pin the auto-managed store itself via `mcp -db <store> -meta
// <sidecar>` (Attach mode: exactly that store, no ingest — the user keeps it
// fresh with `graphi sync`); the paths are derived, not hand-copied, from the
// same state layout every flagless verb uses. Same guarantees as the client
// path (idempotent, atomic, fail-closed backup, offline) through the shared
// mcpconfig.Apply. The written file carries absolute paths, so it is
// machine-specific by construction: each clone runs `graphi setup --project`
// once rather than committing it.
func runSetupProject(cwd, rootOverride, bin string, dryRun, attach bool) int {
	root := rootOverride
	if root == "" {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			printNotARepo("setup --project")
			return 1
		}
		root = detected
	}
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup: --project: resolve root %q: %v\n", root, err)
		return 1
	}
	root = abs
	info, err := os.Stat(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup: --project: root %q does not exist\n", root)
		return 1
	}
	if !info.IsDir() {
		fmt.Fprintf(os.Stderr, "graphi: setup: --project: root %q is not a directory\n", root)
		return 1
	}

	args := []string{"mcp", "-root", root}
	storeMissing := false
	if attach {
		p, err := state.Resolve(root)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: setup: --project: resolve per-repo state for %q: %v\n", root, err)
			return 1
		}
		args = []string{"mcp", "-db", p.DB, "-meta", p.Meta}
		if _, err := os.Stat(p.DB); err != nil {
			storeMissing = true
		}
	}

	path := filepath.Join(root, ".mcp.json")
	entry := mcpconfig.GraphiEntry(bin, args)
	rc := reportSetup("project (.mcp.json)", path, entry, dryRun, func() (mcpconfig.Result, error) {
		return mcpconfig.Apply(path, "graphi", entry, dryRun)
	})
	if rc == 0 && !dryRun {
		if attach {
			fmt.Println("  note: attach mode serves the store as-is (no auto-ingest); keep it fresh with 'graphi sync'.")
			if storeMissing {
				fmt.Println("  note: the pinned store does not exist yet — run 'graphi sync' in the repo before the first session,")
				fmt.Println("        or sessions will serve an empty graph.")
			}
		}
		fmt.Printf("  note: %s pins absolute paths and is machine-specific;\n", path)
		fmt.Println("        gitignore it, or have each clone run 'graphi setup --project' once.")
		fmt.Println("  note: Claude Code asks once to approve project-scoped MCP servers on first use.")
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

// runSetupEmbedder is the opt-in `graphi setup-embedder` command (SW-059,
// extended by SW-262). It dispatches the `static:<model>@<revision>`
// selector to the cmd-local download path (this file) and every other
// selector to the offline print path (surfaces/cli.RunSetupEmbedder).
// The static path is the ONLY one that reaches the network; it lives
// here — NOT in surfaces/cli or engine/embed/static — so the default
// graph does not link an outbound HTTP client (AC-5 / AC-8).
//
//	graphi setup-embedder [<selector>]
func runSetupEmbedder(args []string) int {
	selector := ""
	if len(args) > 0 {
		selector = args[0]
	}
	if strings.HasPrefix(selector, "static:") {
		return runStaticSetupEmbedder(args)
	}
	if err := cli.RunSetupEmbedder(context.Background(), args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup-embedder: %v\n", err)
		return 1
	}
	return 0
}

// runStaticSetupEmbedder is the cmd-graphi side of the static-embedder
// download path. It parses the model@revision + --local / --cache-dir
// flags and delegates to the StaticDownload / StaticInstallLocal helpers
// in setup_static.go. The network code (net/http) lives in this package
// only — surfaces/cli and engine/embed/static are net-free.
func runStaticSetupEmbedder(args []string) int {
	selector := args[0]
	if len(selector) <= len("static:") {
		fmt.Fprintln(os.Stderr, "graphi: setup-embedder: the `static:` selector requires a model@revision (e.g. `graphi setup-embedder static:potion-code-16M-v2@<revision>`)")
		return 1
	}
	modelAtRev := selector[len("static:"):]

	fs := flag.NewFlagSet("setup-embedder static", flag.ContinueOnError)
	local := fs.String("local", "", "validate and install from a local artifact directory (air-gapped path; AC-6)")
	dest := fs.String("cache-dir", defaultStaticCacheDir(), "destination directory for the artifact (default $XDG_CACHE_HOME/graphi/models/)")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *local != "" {
		if err := StaticInstallLocal(context.Background(), *local, *dest); err != nil {
			fmt.Fprintf(os.Stderr, "graphi: setup-embedder: %v\n", err)
			return 1
		}
		fmt.Printf("static: artifact installed from %s to %s (SHA-256 verified)\n", *local, *dest)
		fmt.Println("To enable semantic search, export:")
		fmt.Printf("  export GRAPHI_EMBEDDER=static:%s\n", modelAtRev)
		fmt.Println("Then re-index with embeddings:  graphi index --semantic")
		return 0
	}
	if err := StaticDownload(context.Background(), *dest); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup-embedder: %v\n", err)
		return 1
	}
	fmt.Printf("static: artifact downloaded to %s (SHA-256 verified)\n", *dest)
	fmt.Println("To enable semantic search, export:")
	fmt.Printf("  export GRAPHI_EMBEDDER=static:%s\n", modelAtRev)
	fmt.Println("Then re-index with embeddings:  graphi index --semantic")
	return 0
}

// defaultStaticCacheDir returns the default destination for setup-embedder
// (the same path the embedder reads from). Honours XDG_CACHE_HOME.
func defaultStaticCacheDir() string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			cache = filepath.Join(home, ".cache")
		}
	}
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "graphi", "models", "potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b")
}

// runPrivacyAudit prints the local-first proof from real facts and exits non-zero
// on any violation (SW-044). Offline; reuses internal/cgoconformance +
// internal/canary.
//
//	graphi privacy-audit [--source [--target ./...]]
func runPrivacyAudit(args []string) int {
	fs := flag.NewFlagSet("privacy-audit", flag.ContinueOnError)
	source := fs.Bool("source", false, "scan the current graphi source checkout instead of reporting this binary's embedded build evidence")
	target := fs.String("target", "./...", "source build target to scan for CGo imports (requires --source)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: privacy-audit: %v\n", err)
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "graphi: privacy-audit: unexpected arguments: %v\n", fs.Args())
		return 1
	}
	targetSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "target" {
			targetSet = true
		}
	})
	if targetSet && !*source {
		fmt.Fprintln(os.Stderr, "graphi: privacy-audit: --target requires --source; a source scan describes the checkout, not the running binary")
		return 1
	}
	var rep audit.Report
	if *source {
		rep = audit.RunSource(context.Background(), *target)
	} else {
		rep = audit.Run(context.Background(), *target)
	}
	rep.Render(os.Stdout)
	return rep.ExitCode()
}
