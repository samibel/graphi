package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/internal/state"
)

// `graphi extension` — the declarative rule-pack verbs (SW-229 / AX-09, Labs).
//
// Exit-code contract, documented in docs/cli-reference.md and asserted by test:
//
//	0  the operation succeeded / `doctor` found nothing to act on
//	1  an ACTIONABLE failure: a pack did not validate, a hash did not match, a
//	   pack is not installed, `doctor` found a problem, or the cwd is not a repo
//	2  a USAGE error: an unknown subcommand, a missing argument, a missing
//	   --sha256 on install
//
// The split matters for scripting: 2 means "you invoked it wrong", 1 means "you
// invoked it right and the answer is no". A caller that cannot tell those apart
// retries the wrong one.
const (
	extExitOK       = 0
	extExitProblem  = 1
	extExitUsageErr = 2
)

// extensionUsage is printed for a usage error and for `extension` with no
// subcommand.
const extensionUsage = `usage: graphi extension <subcommand> [flags]

  validate <manifest-file> [--sha256 <hex>]   check a pack against the schema; writes nothing
  install --sha256 <hex> <manifest-file>      verify and install a pack from a LOCAL file
  list [--json]                               show installed packs with their hashes
  doctor [--json]                             diagnose hash mismatches, orphans, disabled packs
  enable <pack-id> | disable <pack-id>        flip a pack on or off (disabled == absent)
  remove <pack-id>                            delete a pack and its lockfile entry

All subcommands accept -root <repo>. Installation is offline: the source is a
local file and graphi makes no network call for it. A pack is data — graphi
executes nothing it ships and follows no path or URL it names.`

// runExtension dispatches the `graphi extension` subcommands.
func runExtension(args []string) int {
	return runExtensionAt(getwd(), args, os.Stdout, os.Stderr)
}

func runExtensionAt(cwd string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, extensionUsage)
		return extExitUsageErr
	}
	sub := args[0]
	rest := args[1:]

	var root, sha string
	asJSON := false
	var positional []string
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		takeVal := func(name string) (string, bool) {
			if a == name && i+1 < len(rest) {
				i++
				return rest[i], true
			}
			if strings.HasPrefix(a, name+"=") {
				return a[len(name)+1:], true
			}
			return "", false
		}
		switch {
		case a == "--json" || a == "-json":
			asJSON = true
		default:
			if v, ok := takeVal("-root"); ok {
				root = v
			} else if v, ok := takeVal("--root"); ok {
				root = v
			} else if v, ok := takeVal("--sha256"); ok {
				sha = v
			} else if v, ok := takeVal("-sha256"); ok {
				sha = v
			} else if strings.HasPrefix(a, "-") {
				fmt.Fprintf(stderr, "graphi: extension %s: unknown flag %q\n\n%s\n", sub, a, extensionUsage)
				return extExitUsageErr
			} else {
				positional = append(positional, a)
			}
		}
	}

	// `validate` is the one subcommand that needs no repository: a pack author
	// checks a file, and the file is wherever they put it.
	if sub == "validate" {
		return runExtensionValidate(positional, sha, stdout, stderr)
	}

	if root == "" {
		detected, ok := state.DetectRepo(cwd)
		if !ok {
			printNotARepo("extension")
			return extExitProblem
		}
		root = detected
	}

	switch sub {
	case "install":
		return runExtensionInstall(root, positional, sha, stdout, stderr)
	case "list":
		return runExtensionList(root, asJSON, stdout, stderr)
	case "doctor":
		return runExtensionDoctor(root, asJSON, stdout, stderr)
	case "enable":
		return runExtensionSetEnabled(root, positional, true, stdout, stderr)
	case "disable":
		return runExtensionSetEnabled(root, positional, false, stdout, stderr)
	case "remove":
		return runExtensionRemove(root, positional, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "graphi: extension: unknown subcommand %q\n\n%s\n", sub, extensionUsage)
		return extExitUsageErr
	}
}

func runExtensionValidate(positional []string, sha string, stdout, stderr io.Writer) int {
	if len(positional) != 1 {
		fmt.Fprintf(stderr, "graphi: extension validate: expected exactly one manifest file\n\n%s\n", extensionUsage)
		return extExitUsageErr
	}
	candidate, err := extpack.ValidateFile(positional[0], sha)
	if err != nil {
		fmt.Fprintf(stderr, "graphi: extension validate: %v\n", err)
		return extExitProblem
	}
	m := candidate.Manifest
	fmt.Fprintf(stdout, "ok  %s@%s (%s)\n", m.ID, m.Version, m.Kind)
	fmt.Fprintf(stdout, "    schema     %s\n", m.SchemaVersion)
	fmt.Fprintf(stdout, "    api        %s..%s (host speaks %s)\n", m.API.Min, m.API.Max, extpack.APIVersion)
	fmt.Fprintf(stdout, "    manifest   sha256:%s\n", candidate.ManifestSHA256)
	fmt.Fprintf(stdout, "    artifact   sha256:%s\n", candidate.ArtifactSHA256)
	fmt.Fprintf(stdout, "    provides   %d capability key(s)\n", len(m.Capabilities.Provides))
	for _, key := range m.Capabilities.Provides {
		fmt.Fprintf(stdout, "               %s\n", extpack.Bound(key))
	}
	fmt.Fprintf(stdout, "install with: graphi extension install --sha256 %s %s\n", candidate.ManifestSHA256, positional[0])
	return extExitOK
}

func runExtensionInstall(root string, positional []string, sha string, stdout, stderr io.Writer) int {
	if len(positional) != 1 {
		fmt.Fprintf(stderr, "graphi: extension install: expected exactly one manifest file\n\n%s\n", extensionUsage)
		return extExitUsageErr
	}
	if sha == "" {
		fmt.Fprintf(stderr, "graphi: extension install: --sha256 is required\n"+
			"graphi will not install a pack it cannot pin. Run `graphi extension validate %s` to print the hash.\n", positional[0])
		return extExitUsageErr
	}
	entry, err := extpack.Install(root, positional[0], sha)
	if err != nil {
		fmt.Fprintf(stderr, "graphi: extension install: %v\n", err)
		return extExitProblem
	}
	fmt.Fprintf(stdout, "installed %s@%s (%s)\n", entry.ID, entry.Version, entry.Kind)
	fmt.Fprintf(stdout, "  manifest sha256:%s\n", entry.ManifestSHA256)
	fmt.Fprintf(stdout, "  artifact sha256:%s\n", entry.ArtifactSHA256)
	fmt.Fprintf(stdout, "  recorded in %s\n", extpack.LockPath(root))
	return extExitOK
}

// extensionListRow is the --json row for `list`.
type extensionListRow struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Kind           string `json:"kind"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	Enabled        bool   `json:"enabled"`
}

func runExtensionList(root string, asJSON bool, stdout, stderr io.Writer) int {
	lock, err := extpack.LoadLock(root)
	if err != nil {
		fmt.Fprintf(stderr, "graphi: extension list: %v\n", err)
		return extExitProblem
	}
	rows := make([]extensionListRow, 0, len(lock.Packs))
	for _, e := range lock.Packs {
		rows = append(rows, extensionListRow{
			ID: e.ID, Version: e.Version, Kind: string(e.Kind),
			ManifestSHA256: e.ManifestSHA256, ArtifactSHA256: e.ArtifactSHA256, Enabled: e.Enabled,
		})
	}
	if asJSON {
		return writeExtensionJSON(stdout, stderr, "list", map[string]any{"packs": rows})
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "no rule packs installed (%s)\n", extpack.LockPath(root))
		return extExitOK
	}
	for _, r := range rows {
		stateWord := "enabled"
		if !r.Enabled {
			stateWord = "disabled"
		}
		fmt.Fprintf(stdout, "%-8s %s@%s  %s  manifest sha256:%s  artifact sha256:%s\n",
			stateWord, r.ID, r.Version, r.Kind, r.ManifestSHA256, r.ArtifactSHA256)
	}
	return extExitOK
}

func runExtensionDoctor(root string, asJSON bool, stdout, stderr io.Writer) int {
	rows, err := extpack.Diagnose(root)
	if err != nil {
		fmt.Fprintf(stderr, "graphi: extension doctor: %v\n", err)
		return extExitProblem
	}
	problems := 0
	for _, r := range rows {
		if !r.Kind.Healthy() {
			problems++
		}
	}
	if asJSON {
		code := extExitOK
		if problems > 0 {
			code = extExitProblem
		}
		if writeExtensionJSON(stdout, stderr, "doctor", map[string]any{
			"packs": rows, "problems": problems,
		}) != extExitOK {
			return extExitProblem
		}
		return code
	}
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "no rule packs installed (%s)\n", extpack.LockPath(root))
		return extExitOK
	}
	for _, r := range rows {
		fmt.Fprintf(stdout, "%-13s %s — %s\n", r.Kind, extpack.Bound(r.ID), r.Detail)
	}
	if problems > 0 {
		fmt.Fprintf(stdout, "%d pack(s) need attention\n", problems)
		return extExitProblem
	}
	fmt.Fprintf(stdout, "%d pack(s), none needing attention\n", len(rows))
	return extExitOK
}

func runExtensionSetEnabled(root string, positional []string, enabled bool, stdout, stderr io.Writer) int {
	verb := "enable"
	if !enabled {
		verb = "disable"
	}
	if len(positional) != 1 {
		fmt.Fprintf(stderr, "graphi: extension %s: expected exactly one pack id\n\n%s\n", verb, extensionUsage)
		return extExitUsageErr
	}
	entry, err := extpack.SetEnabled(root, positional[0], enabled)
	if err != nil {
		fmt.Fprintf(stderr, "graphi: extension %s: %v\n", verb, err)
		return extExitProblem
	}
	if enabled {
		fmt.Fprintf(stdout, "enabled %s@%s\n", entry.ID, entry.Version)
		return extExitOK
	}
	fmt.Fprintf(stdout, "disabled %s@%s — graphi now behaves exactly as it did before the pack was installed\n", entry.ID, entry.Version)
	return extExitOK
}

func runExtensionRemove(root string, positional []string, stdout, stderr io.Writer) int {
	if len(positional) != 1 {
		fmt.Fprintf(stderr, "graphi: extension remove: expected exactly one pack id\n\n%s\n", extensionUsage)
		return extExitUsageErr
	}
	if err := extpack.Remove(root, positional[0]); err != nil {
		fmt.Fprintf(stderr, "graphi: extension remove: %v\n", err)
		return extExitProblem
	}
	fmt.Fprintf(stdout, "removed %s\n", extpack.Bound(positional[0]))
	return extExitOK
}

// writeExtensionJSON renders a --json document deterministically.
func writeExtensionJSON(stdout, stderr io.Writer, verb string, doc map[string]any) int {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "graphi: extension %s: encode json: %v\n", verb, err)
		return extExitProblem
	}
	fmt.Fprintf(stdout, "%s\n", data)
	return extExitOK
}
