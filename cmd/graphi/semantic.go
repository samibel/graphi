package main

// `graphi semantic status [--json]` — the SW-265 status verb.
//
// One command, one shared composition (surfaces/client.SemanticStatus),
// three surfaces (CLI/MCP/HTTP). The CLI's exit code maps the typed
// lifecycle state: 0 = ready, 1 = actionable (missing/stale), 2 =
// error/corrupt, mirroring `graphi status`'s exit contract.
//
// The composition reads the auto-managed embedder registry the runtime
// already constructed (one shared composition per process), so the
// CLI never re-resolves the GRAPHI_EMBEDDER selector — it consumes
// the registry the running graphi was launched with.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
)

// runSemantic dispatches the `graphi semantic` subcommand. The verb is
// labs-tier (semantic search is OFF by default and the verb's output is
// informational); exit codes follow the closed state vocabulary:
//
//	0  state == ready
//	1  state ∈ {missing, stale}        (operator action: graphi index --semantic)
//	2  state == corrupt                 (operator action: graphi index --semantic)
//
// Without a subcommand the command prints its subcommand list.
func runSemantic(args []string) int {
	if len(args) == 0 {
		printSemanticHelp(os.Stdout)
		return 0
	}
	switch args[0] {
	case "status":
		return runSemanticStatus(args[1:])
	case "-h", "-help", "--help":
		printSemanticHelp(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "graphi: semantic: unknown subcommand %q (expected: status)\n", args[0])
		return 2
	}
}

// printSemanticHelp renders the `graphi semantic` help text. Kept in one
// place so the help catalog and the runtime path cannot disagree.
func printSemanticHelp(w io.Writer) {
	fmt.Fprintln(w, "graphi semantic — Labs: surface for the optional semantic search state")
	fmt.Fprintln(w, "usage:   graphi semantic status [--json] [-root <repo>] [-db path] [-meta dir]")
	fmt.Fprintln(w, "example: graphi semantic status --json")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Reports the canonical semantic status document (SW-265): installed,")
	fmt.Fprintln(w, "configured, indexed, fresh, the typed state, the active and prior")
	fmt.Fprintln(w, "generations, the language validation map, and the exact repair command")
	fmt.Fprintln(w, "the operator runs to leave that state. The JSON form is byte-identical")
	fmt.Fprintln(w, "to the MCP `semantic_status` tool and the `GET /semantic/status` HTTP route.")
}

// runSemanticStatus is `graphi semantic status [--json]`. It composes
// the canonical document via surfaces/client.SemanticStatus and prints
// it (human or JSON).
func runSemanticStatus(args []string) int {
	return runSemanticStatusAt(getwd(), args, os.Stdout)
}

// runSemanticStatusAt is runSemanticStatus with an injectable cwd.
func runSemanticStatusAt(cwd string, args []string, stdout io.Writer) int {
	return runSemanticStatusWithRegistryAt(cwd, args, stdout, runtimeEmbedderRegistryFromEnv(os.Getenv(embed.EnvSelector)))
}

// runSemanticStatusWithRegistryAt is the explicit dependency-injection seam
// used by tests. The registry is per call; request paths never consult
// mutable package-level state.
func runSemanticStatusWithRegistryAt(cwd string, args []string, stdout io.Writer, reg *embed.Registry) int {
	asJSON := false
	rest := args[:0:0]
	for _, a := range args {
		if a == "--json" || a == "-json" {
			asJSON = true
			continue
		}
		rest = append(rest, a)
	}
	root, dbPath, metaDir, _ := parseIngestVerbFlags(rest)
	if root == "" {
		if detected, ok := state.DetectRepo(cwd); ok {
			root = detected
		} else {
			printNotARepo("semantic status")
			return 2
		}
	}

	// Resolve the embedder registry the runtime already constructed for
	// this process — search-side reads honour GRAPHI_EMBEDDER exactly the
	// same way the index path does, so the CLI never re-resolves the
	// selector. An unset/empty selector reads as the unconfigured state.
	if reg == nil {
		// No registry was constructed (zero-value graceful skip); the
		// composition reads Configured=false. We synthesise an empty
		// registry here rather than nil so the leaf's Active() reads
		// consistently.
		reg = embed.NewRegistry()
	}

	b, state, err := client.SemanticStatus(context.Background(), client.SemanticStatusOptions{
		Root:     root,
		DBPath:   dbPath,
		MetaDir:  metaDir,
		Embedder: reg,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: semantic status: %v\n", err)
		return 2
	}
	if asJSON {
		if _, werr := stdout.Write(b); werr != nil {
			fmt.Fprintf(os.Stderr, "graphi: semantic status: %v\n", werr)
			return 2
		}
	} else {
		renderSemanticStatusHuman(stdout, b)
	}
	return semanticStatusExitCode(state)
}

// runtimeEmbedderRegistryFromEnv is the env-driven path the runtime
// accessor delegates to. Kept separate so the test seam is testable in
// isolation and so a future test that simulates the env-driven path
// does not need to mutate os.Environ.
func runtimeEmbedderRegistryFromEnv(selector string) *embed.Registry {
	emb, err := embed.Constructor(selector, embed.DefaultConstructors())
	if err != nil || emb == nil {
		return embed.NewRegistry() // graceful-skip registry: Active() returns (nil, false)
	}
	reg := embed.NewRegistry()
	if rerr := reg.Register(emb); rerr != nil {
		return embed.NewRegistry()
	}
	return reg
}

// semanticStatusExitCode maps the typed State onto the documented
// CLI exit contract. The mapping mirrors `graphi status`: 0 = ready,
// 1 = actionable (missing/stale, the operator runs `graphi index --semantic`),
// 2 = corrupt (the index has a verifiable failure). Unset reads as
// missing (actionable).
func semanticStatusExitCode(state embed.State) int {
	switch state {
	case embed.StateReady:
		return 0
	case embed.StateMissing, embed.StateStale, embed.StateUnset:
		return 1
	case embed.StateCorrupt:
		return 2
	}
	return 2
}

// renderSemanticStatusHuman renders the compact human report from the
// canonical bytes. Decoding the JSON keeps the wire document the single
// source of truth — the renderer never invents facts.
func renderSemanticStatusHuman(w io.Writer, doc []byte) {
	var d struct {
		SchemaVersion int    `json:"schema_version"`
		Installed     bool   `json:"installed"`
		Configured    bool   `json:"configured"`
		Indexed       bool   `json:"indexed"`
		Fresh         bool   `json:"fresh"`
		State         string `json:"state"`
		Model         struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
		} `json:"model"`
		ActiveGeneration struct {
			ID              string             `json:"id"`
			Fingerprint     string             `json:"fingerprint"`
			Documents       int                `json:"documents"`
			Nodes           int                `json:"nodes"`
			SpanMethodShare map[string]float64 `json:"span_method_share"`
			BuiltAt         string             `json:"built_at"`
		} `json:"active_generation"`
		Languages map[string]string `json:"languages"`
		Repair    string            `json:"repair"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		// The composition produced these bytes; a decode failure is a
		// bug, but the facts must still reach the user.
		fmt.Fprintln(w, string(doc))
		return
	}

	fmt.Fprintln(w, "Graphi semantic status")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "state:    %s\n", d.State)
	fmt.Fprintf(w, "  installed:  %v\n", d.Installed)
	fmt.Fprintf(w, "  configured: %v\n", d.Configured)
	fmt.Fprintf(w, "  indexed:    %v\n", d.Indexed)
	fmt.Fprintf(w, "  fresh:      %v\n", d.Fresh)
	if d.Model.ID != "" {
		fmt.Fprintf(w, "model:    %s\n", d.Model.ID)
		if d.Model.Revision != "" {
			fmt.Fprintf(w, "  revision: %s\n", d.Model.Revision)
		}
	} else {
		fmt.Fprintln(w, "model:    (no embedder configured)")
	}
	if d.ActiveGeneration.ID != "" {
		fmt.Fprintf(w, "active:   %s\n", d.ActiveGeneration.ID)
		fmt.Fprintf(w, "  documents: %d\n", d.ActiveGeneration.Documents)
		fmt.Fprintf(w, "  nodes:     %d\n", d.ActiveGeneration.Nodes)
		if len(d.ActiveGeneration.SpanMethodShare) > 0 {
			fmt.Fprintf(w, "  span:      ")
			first := true
			keys := make([]string, 0, len(d.ActiveGeneration.SpanMethodShare))
			for k := range d.ActiveGeneration.SpanMethodShare {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := d.ActiveGeneration.SpanMethodShare[k]
				if !first {
					fmt.Fprintf(w, ", ")
				}
				first = false
				fmt.Fprintf(w, "%s %.0f%%", k, v*100)
			}
			fmt.Fprintln(w)
		}
		if d.ActiveGeneration.BuiltAt != "" {
			fmt.Fprintf(w, "  built:     %s\n", d.ActiveGeneration.BuiltAt)
		}
	} else {
		fmt.Fprintln(w, "active:   (no active generation)")
	}
	// AC-5: the human output prints one line summarising language validation.
	if len(d.Languages) > 0 {
		validated := 0
		unvalidated := 0
		for _, v := range d.Languages {
			if v == "validated" {
				validated++
			} else {
				unvalidated++
			}
		}
		fmt.Fprintf(w, "languages: %d validated, %d unvalidated (Go is validated; every other language is unvalidated — see docs/semantic-search.md)\n", validated, unvalidated)
	}
	if d.Repair != "" {
		fmt.Fprintf(w, "repair:   %s\n", d.Repair)
	} else {
		fmt.Fprintln(w, "repair:   (none — state is ready)")
	}
	// Reference the typed unavailable reasons the search service renders,
	// so an operator reading the human report sees why no vectors are served.
	if d.State != "ready" {
		fmt.Fprintf(w, "note:     typed UnavailableReason = %q\n", humanReason(d.State))
	}
}

// humanReason returns the typed unavailable reason the search service
// renders for each non-ready state. Kept inline (rather than importing
// engine/search) so the cmd rank stays free of an engine-search coupling
// for one display string; the strings are the documented closed vocabulary.
func humanReason(state string) string {
	switch state {
	case "missing":
		return search.ReasonUnavailable
	case "stale":
		return search.ReasonStale
	case "corrupt":
		return search.ReasonCorrupt
	}
	return search.UnavailableReason
}
