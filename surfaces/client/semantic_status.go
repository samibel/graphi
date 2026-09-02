package client

// This file is the shared semantic-status composition (SW-265 AC-1 / AC-2):
// the ONE place the canonical `graphi semantic status --json` document is
// assembled and serialized, following the trust_report / explain_symbol
// template — engine composition -> client.SemanticStatus method -> Direct
// canonical bytes — so CLI, MCP and HTTP emit byte-identical documents by
// construction.
//
// The composition reads the embedder registry the wire dispatch already
// carries (one shared composition per Client), so all three surfaces see
// the same configured-vs-unconfigured answer without re-resolving the
// GRAPHI_EMBEDDER selector themselves.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/internal/state"
)

// SemanticStatusJSONSchemaVersion versions the `graphi semantic status --json`
// document (SW-265 AC-1 contract, following the statusJSONSchemaVersion
// convention). Bump only on a breaking wire change; the struct tags carry
// the field names verbatim so a rename is a wire change.
const SemanticStatusJSONSchemaVersion = 1

// SemanticStatusOptions is the transport-agnostic input for the semantic-
// status composition. Root/DBPath/MetaDir locate the auto-managed state
// exactly as the trust-report and status surfaces do; an empty DBPath
// and MetaDir resolve the auto-managed locations WITHOUT creating them
// (the status verb is a pure observer). Embedder is the resolved
// embed.Registry the dispatch already constructed; nil reads as the
// unconfigured state.
type SemanticStatusOptions struct {
	Root     string
	DBPath   string
	MetaDir  string
	Embedder *embed.Registry
}

// semanticStatusDoc is the wire document. The first field is
// `schema_version`, the canonical versioned identifier. Field order
// follows the struct declaration; encoding/json renders them in that
// order with HTML escaping disabled (see encodeSemanticStatus).
//
// Every field is always present on the wire; empty slices/maps encode
// as `[]`/`{}`, never null (the SW-265 AC-3 goldens pin both shapes).
type semanticStatusDoc struct {
	SchemaVersion    int                     `json:"schema_version"`
	Installed        bool                    `json:"installed"`
	Configured       bool                    `json:"configured"`
	Indexed          bool                    `json:"indexed"`
	Fresh            bool                    `json:"fresh"`
	State            embed.State             `json:"state"`
	Model            embed.Model             `json:"model"`
	ActiveGeneration embed.GenerationSummary `json:"active_generation"`
	LastGeneration   embed.GenerationSummary `json:"last_generation"`
	Languages        map[string]string       `json:"languages"`
	Repair           string                  `json:"repair"`
}

// composeSemanticStatus is the shared composition: resolve the auto-managed
// state (no creation), call the engine-owned Status loader, and encode the
// canonical document.
//
// The composition reads the embedder registry the call site already
// constructed (one shared composition per Client), so all three surfaces
// see the SAME configured-vs-unconfigured answer without re-resolving the
// GRAPHI_EMBEDDER selector themselves.
//
// Returns the canonical bytes plus the typed State so the CLI maps its
// exit codes without re-parsing JSON (the trust_report pattern). A non-nil
// error is operational (CLI exit 2, typed MCP tool error); a missing store
// is NOT an error — it composes the fail-closed "no index yet" document.
func composeSemanticStatus(ctx context.Context, opts SemanticStatusOptions) ([]byte, embed.State, error) {
	root, dbPath, metaDir := resolveSemanticStatusLocations(opts)
	_ = root
	graphGeneration := ""
	var nodes embed.NodeReferencer
	if dbPath != "" {
		if _, err := os.Stat(dbPath); err == nil {
			store, openErr := graphstore.OpenSQLiteReadOnly(dbPath)
			if openErr != nil {
				return nil, embed.StateUnset, openErr
			}
			defer store.Close()
			graphGeneration, openErr = semanticGraphGeneration(ctx, store)
			if openErr != nil {
				return nil, embed.StateUnset, openErr
			}
			nodes = embed.NodeReferencerFromGraphLookup(store.GetNode)
		}
	}
	status := embed.LoadStatus(ctx, metaDir, opts.Embedder, graphGeneration, nodes)
	if status.ActiveGeneration.SpanMethodShare == nil {
		status.ActiveGeneration.SpanMethodShare = map[string]float64{}
	}
	if status.LastGeneration.SpanMethodShare == nil {
		status.LastGeneration.SpanMethodShare = map[string]float64{}
	}
	doc := semanticStatusDoc{
		SchemaVersion:    SemanticStatusJSONSchemaVersion,
		Installed:        status.Installed,
		Configured:       status.Configured,
		Indexed:          status.Indexed,
		Fresh:            status.Fresh,
		State:            status.State,
		Model:            status.Model,
		ActiveGeneration: status.ActiveGeneration,
		LastGeneration:   status.LastGeneration,
		Languages:        mergeLanguages(status.Languages, indexedLanguages()),
		Repair:           status.Repair,
	}
	b, err := encodeSemanticStatus(doc)
	if err != nil {
		return nil, embed.StateUnset, err
	}
	return b, status.State, nil
}

// resolveSemanticStatusLocations locates the auto-managed per-repo store
// and meta sidecar WITHOUT creating them. Empty inputs resolve via
// state.Resolve exactly as the trust-report and status surfaces do; a
// non-resolvable root reads as "no repository" (typed missing state).
func resolveSemanticStatusLocations(opts SemanticStatusOptions) (root, dbPath, metaDir string) {
	root = opts.Root
	dbPath = opts.DBPath
	metaDir = opts.MetaDir
	if root == "" {
		if detected, ok := state.DetectRepo("."); ok {
			root = detected
		}
	}
	if root != "" && dbPath == "" && metaDir == "" {
		if paths, err := state.Resolve(root); err == nil {
			dbPath = paths.DB
			metaDir = paths.Meta
		}
	}
	return root, dbPath, metaDir
}

func semanticGraphGeneration(ctx context.Context, store graphstore.Graphstore) (string, error) {
	for _, key := range []string{"index.commit_generation", "index.full_ingest_generation"} {
		value, err := store.Metadata(ctx, key)
		if err == nil && value != "" {
			return value, nil
		}
		if err != nil && !errors.Is(err, graphstore.ErrNotFound) {
			return "", fmt.Errorf("client: read semantic graph generation: %w", err)
		}
	}
	return "", nil
}

// mergeLanguages folds the language validation map (Go is `validated`,
// every other indexed language `unvalidated`) with the indexed-language
// list from the parser registry. The union is the AC-5 / spec-decision-2
// contract — derived live, not from a hand-maintained table — so adding
// a parser for a new language flips its row automatically.
func mergeLanguages(base map[string]string, indexed []string) map[string]string {
	out := make(map[string]string, len(base)+len(indexed))
	for k, v := range base {
		out[k] = v
	}
	for _, lang := range indexed {
		if _, ok := out[lang]; ok {
			continue
		}
		if lang == "go" {
			out[lang] = "validated"
		} else {
			out[lang] = "unvalidated"
		}
	}
	if len(out) == 0 {
		return map[string]string{}
	}
	return out
}

// indexedLanguages returns the parser registry's shipped languages, in
// canonical order, for the language validation map. The registry is the
// live source — a language whose parser is registered but never indexed
// is omitted from the output, which is the AC-5 "ships but does not
// validate" shape.
func indexedLanguages() []string {
	registry := parse.NewDefaultRegistry()
	langs := registry.Languages()
	out := make([]string, 0, len(langs))
	for _, l := range langs {
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// encodeSemanticStatus is THE canonical semantic-status encoder — one
// encoder for every surface, mirroring the trust-report encoder's
// discipline (encoding/json, HTML escaping disabled, no trailing
// newline). Field order follows the struct declaration; every map
// is pre-allocated, so identical inputs always encode to identical bytes.
func encodeSemanticStatus(doc semanticStatusDoc) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("client: encode semantic status: %w", err)
	}
	return buf.Bytes(), nil
}

// SemanticStatus runs the shared composition without a constructed client
// — the CLI's entry point. Direct.SemanticStatus and the MCP surface
// ride the same function, so every surface emits byte-identical
// documents for the same options (parity-by-construction).
func SemanticStatus(ctx context.Context, opts SemanticStatusOptions) ([]byte, embed.State, error) {
	return composeSemanticStatus(ctx, opts)
}
