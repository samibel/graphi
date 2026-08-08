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
//	graphi setup --repair [--pin <server-name>=<repo>]... [--client id] [--config path] [--dry-run]
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
//
// --repair (SW-164) registers NOTHING. It repairs the GLOBAL client configs that
// already hold two or more contending zero-config graphi entries, by pinning the
// extras with `-root <path>`; see runSetupRepair. It is a different operation
// from --project, which writes a repository's own project-scoped .mcp.json.
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
	repair := fs.Bool("repair", false, "registers nothing: resolve MCP double-registration by pinning the extra zero-config graphi entries with -root")
	pins := repairPins{}
	fs.Var(pins, "pin", "with --repair: the repository for one contending entry, as <server-name>=<path> (repeatable)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: setup: %v\n", err)
		return 1
	}
	if len(pins) > 0 && !*repair {
		fmt.Fprintln(os.Stderr, "graphi: setup: --pin only applies to --repair (it names the repository of a contending MCP entry)")
		return 1
	}
	if *repair && *project {
		fmt.Fprintln(os.Stderr, "graphi: setup: --repair and --project are different operations: --project writes <root>/.mcp.json; --repair fixes global client configs that already contend")
		return 1
	}
	bin := *binary
	if bin == "" && !*repair { // --repair rewrites existing entries; it registers no binary
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
		if *repair {
			return runSetupRepair([]mcpconfig.Client{c.WithConfigPath(*cfgPath)}, pins, *dryRun, os.Stdout, os.Stderr)
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

	if *repair {
		return runSetupRepair(targets, pins, *dryRun, os.Stdout, os.Stderr)
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
