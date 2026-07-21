package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/samibel/graphi/internal/audit"
	"github.com/samibel/graphi/internal/mcpconfig"
	"github.com/samibel/graphi/surfaces/cli"
)

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
