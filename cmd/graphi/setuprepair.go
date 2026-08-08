package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/samibel/graphi/internal/mcpconfig"
)

// repairPins collects the repeatable `--pin <server-name>=<repository path>`
// flag: the ONE way a user tells `graphi setup --repair` which repository a
// hand-added MCP entry was for. The mapping is keyed by server name and applies
// to every targeted client holding that name; narrow it with --client when two
// clients happen to use the same name for different repositories.
//
// There is deliberately no other source. A contending entry is contending
// BECAUSE it states no repository, so nothing in the config, the store layout,
// the client's workspace or the process environment can be read as one — every
// such reading would be a guess wearing a fact's clothes.
type repairPins map[string]string

func (r repairPins) String() string {
	pairs := make([]string, 0, len(r))
	for name, path := range r {
		pairs = append(pairs, name+"="+path)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}

func (r repairPins) Set(v string) error {
	name, path, ok := strings.Cut(v, "=")
	if !ok || name == "" || path == "" {
		return fmt.Errorf("want <server-name>=<repository path>, got %q", v)
	}
	if _, dup := r[name]; dup {
		return fmt.Errorf("--pin %s given more than once", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("--pin %s: %w", name, err)
	}
	r[name] = abs
	return nil
}

// runSetupRepair resolves MCP double-registration for the given clients and
// writes nothing else (SW-164).
//
// A client holding two or more contending ZERO-CONFIG graphi entries has every
// one of them resolve the SAME repository and contend on its cross-process
// ingest lock: one indexes, the rest answer "repository is not bound" until the
// winner finishes. Repair makes the extras say what they serve, by appending
// `-root <path>` to their args — the flag `graphi mcp` itself accepts:
//
//   - The setup-managed entry (mcpconfig.ManagedServerName) stays ZERO-CONFIG.
//     `graphi setup` owns it and keeps it current, and it is the entry that is
//     supposed to follow whatever repository the client is working in.
//   - Every OTHER contending entry is pinned. Repair NEVER deletes an entry: a
//     hand-added "graphi-myrepo" encodes an intent graphi cannot reconstruct, so
//     removing it would silently destroy that intent.
//   - The repository comes from an explicit `--pin <name>=<path>` and from
//     NOWHERE else. Where the user supplied none, the entry is reported by
//     client and server name, left untouched, and the command exits non-zero.
//   - A supplied root is VALIDATED before it is written (mcpconfig.ValidateRoot:
//     the same "exists and is a directory" conditions `graphi mcp -root`
//     enforces at bind time). A root that fails is reported, nothing is written
//     for that entry, and the command exits non-zero — repairing contention into
//     an entry that cannot bind is not a repair.
//   - An entry repair cannot APPEND to honestly — one whose args is not a JSON
//     array of strings led by the `mcp` subcommand
//     (mcpconfig.Client.UnpinnableReasons) — is reported and left exactly as it
//     is. Repair will not reshape a hand-written value into what graphi guesses
//     it meant; that is the same inference AC3 rules out, applied to args
//     instead of to roots.
//
// The three per-entry refusals above behave identically: report by client and
// server name, change nothing for that entry, carry on with its neighbours, and
// exit non-zero. Only a client whose config cannot be READ aborts that client.
//
// dryRun prints the exact planned per-entry change and writes nothing. Its exit
// code reports whether the PREVIEW ran, not what the preview found: an entry
// with no root, or with a bad one, is a finding to act on, not a failed preview,
// so a dry run that reaches the end still exits 0. Only a client whose config
// cannot be read fails the preview itself.
func runSetupRepair(targets []mcpconfig.Client, pins map[string]string, dryRun bool, stdout, stderr io.Writer) int {
	rc, contended := 0, 0
	// fail records a per-entry refusal. Under --dry-run it reports without
	// changing the exit code (AC6); otherwise it is the fail-closed non-zero.
	fail := func() {
		if !dryRun {
			rc = 1
		}
	}
	// seen records the entry names repair actually considered, so a --pin the
	// user supplied that matched nothing can be reported instead of vanishing.
	seen := map[string]bool{}
	for _, c := range targets {
		path, _ := c.ConfigPath()
		entries, err := c.ContendingEntries()
		if err != nil {
			// The preview itself could not run: non-zero even under --dry-run.
			fmt.Fprintf(stderr, "graphi: setup --repair: cannot read %s config (%s): %v\n", c.Display, path, err)
			rc = 1
			continue
		}
		if len(entries) < 2 {
			continue // no contention: one zero-config entry (or none) is the healthy state
		}
		contended++

		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name)
		}
		fmt.Fprintf(stdout, "%s (%s): %d zero-config graphi entries resolve the same repository — %s\n",
			c.Display, path, len(entries), strings.Join(names, ", "))

		// Why an entry cannot be pinned at all, read once per client. Checked
		// BEFORE the root, because telling someone to supply --pin for an entry
		// repair would refuse anyway is bad advice.
		unpinnable, err := c.UnpinnableReasons(names)
		if err != nil {
			fmt.Fprintf(stderr, "graphi: setup --repair: cannot inspect %s config (%s): %v\n", c.Display, path, err)
			rc = 1
			continue
		}

		plan := map[string]string{}
		for _, e := range entries {
			if e.Managed {
				fmt.Fprintf(stdout, "  keep %s zero-config (managed by `graphi setup`)\n", e.Name)
				continue
			}
			seen[e.Name] = true
			if why := unpinnable[e.Name]; why != "" {
				fmt.Fprintf(stderr, "graphi: setup --repair: %s: cannot pin server %q: %s — left unchanged\n",
					c.Display, e.Name, why)
				fmt.Fprintln(stderr, "  repair only APPENDS `-root <path>`; it will not reshape a value you wrote by hand. Fix the entry, then re-run.")
				fail()
				continue
			}
			supplied, ok := pins[e.Name]
			if !ok || supplied == "" {
				fmt.Fprintf(stderr, "graphi: setup --repair: %s: no repository supplied for server %q — left unchanged\n",
					c.Display, e.Name)
				fmt.Fprintf(stderr, "  name it explicitly: graphi setup --repair --pin %s=<repository path>\n", e.Name)
				fail()
				continue
			}
			root, err := mcpconfig.ValidateRoot(supplied)
			if err != nil {
				fmt.Fprintf(stderr, "graphi: setup --repair: %s: server %q: %v — left unchanged\n",
					c.Display, e.Name, err)
				fmt.Fprintln(stderr, "  a pinned root must be an existing directory, exactly as `graphi mcp -root` requires at bind time")
				fail()
				continue
			}
			fmt.Fprintf(stdout, "  pin  %s -root %s\n", e.Name, root)
			plan[e.Name] = root
		}
		if len(plan) == 0 {
			continue
		}

		res, err := c.PinRoots(plan, dryRun)
		if err != nil {
			fmt.Fprintf(stderr, "graphi: setup --repair failed for %s (%s): %v\n", c.Display, path, err)
			fmt.Fprintln(stderr, "  - your existing config was left unchanged (atomic write + fail-closed backup)")
			rc = 1
			continue
		}
		if dryRun {
			fmt.Fprintf(stdout, "[dry-run] %s: no changes written\n", c.Display)
		}
		fmt.Fprint(stdout, res.Diff)
		if dryRun {
			continue
		}
		fmt.Fprintf(stdout, "%s: %d graphi MCP %s pinned in %s\n",
			c.Display, len(plan), plural(len(plan), "entry", "entries"), path)
		if res.BackupPath != "" {
			fmt.Fprintf(stdout, "  backup of the original config written to %s\n", res.BackupPath)
		}
		fmt.Fprintf(stdout, "  restart/reload %s to pick up the change.\n", c.Display)
	}

	reportUnusedPins(pins, seen, stdout, stderr)

	if contended == 0 && rc == 0 {
		fmt.Fprintln(stdout, "no MCP client has contending zero-config graphi entries — nothing to repair.")
	}
	return rc
}

// reportUnusedPins says so when a --pin matched no entry repair considered. The
// flag is loud about malformed syntax and about duplicates, so silence here was
// inconsistent with its own contract — and the value is one the user typed
// explicitly, which makes a typo the likely cause and silence the worst answer.
//
// Two shapes, deliberately distinguished:
//   - --pin graphi=<path> is a STATED refusal, not a typo: the setup-managed
//     entry stays zero-config (AC2), which was previously true only as a side
//     effect of the loop skipping it before pins were consulted.
//   - any other unmatched name names no contending entry in any targeted
//     client.
//
// Both are warnings and neither touches the exit code. AC9 requires rc 0 when
// no client is contending, and a run whose only complaint is an unused --pin
// wrote nothing and found nothing to repair; where there IS contention, the
// entry the user meant to fix is still unpinned and AC4 has already made the
// run non-zero. The warning explains that exit code rather than inventing one.
func reportUnusedPins(pins map[string]string, seen map[string]bool, stdout, stderr io.Writer) {
	unused := make([]string, 0, len(pins))
	for name := range pins {
		if !seen[name] {
			unused = append(unused, name)
		}
	}
	sort.Strings(unused)
	for _, name := range unused {
		if name == mcpconfig.ManagedServerName {
			fmt.Fprintf(stdout, "  note: ignoring --pin %s=%s — the setup-managed %q entry deliberately stays zero-config.\n",
				name, pins[name], name)
			continue
		}
		fmt.Fprintf(stderr, "graphi: setup --repair: warning: --pin %s=%s matched no contending entry in any targeted client (check the server name, or narrow with --client)\n",
			name, pins[name])
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
