package embed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Fingerprint identifies a single embedding space: every vector persisted under
// this fingerprint was produced by the same (model, tokenizer, document schema,
// chunker, graph generation) tuple, so reloading rows under the SAME
// fingerprint is safe while reading rows under a DIFFERENT fingerprint would
// silently mix embedding spaces — what SW-261 exists to prevent.
//
// Field order in Canonical() is FIXED: a different order produces a different
// canonical string, hence a different generation id, hence no sharing. The
// eight fields together cover the inputs that can change the meaning of an
// embedding without changing a NodeId:
//
//   - ModelID: the registered embedder id (e.g. "mock", "ollama:nomic-embed-text").
//   - Revision: an opaque, embedder-defined build/revision tag ("" when none).
//   - ModelSHA256: the pinned model artifact digest (hex, lowercase). Empty when
//     no artifact (e.g. the deterministic mock).
//   - TokenizerSHA256: the pinned tokenizer digest (hex, lowercase). Empty when
//     no tokenizer (mock) or when the embedder exposes none.
//   - Dim: the dimensionality the embedder emits; a model that changes its
//     dimension invalidates every prior vector.
//   - DocumentSchema: "v1" (name-only NodeText) or "v2" (SemanticDocument body
//   - doc + path); two schemas mixed in one generation produce nonsense hits.
//   - ChunkerConfig: an opaque chunker description (e.g. "window:2048" or "").
//     Empty when the embedder embeds the whole document.
//   - GraphGeneration: a stable identifier for the graph the vectors were
//     produced against. graphstore exposes one through
//     `Metadata(ctx, "index.full_ingest_generation")`; when absent we use the
//     placeholder "unknown" and the orchestrator reports this as an open
//     finding (the call site must surface that decision to its operator — see
//     `runtime.NewSearchService`).
//
// The canonical string is pipe-separated, NOT map-iterated, so it is
// byte-stable and can serve as an equality / hash key without normalisation.
type Fingerprint struct {
	ModelID         string
	Revision        string
	ModelSHA256     string
	TokenizerSHA256 string
	Dim             int
	DocumentSchema  string
	ChunkerConfig   string
	GraphGeneration string
}

// fingerprintFields is the FIXED order Canonical emits. Adding a field here is a
// generation-breaking change: every prior store becomes stale and re-embeds.
// Renaming or reordering is also breaking — same reason.
var fingerprintFields = []string{
	"model_id",
	"revision",
	"model_sha256",
	"tokenizer_sha256",
	"dim",
	"document_schema",
	"chunker_config",
	"graph_generation",
}

// Canonical returns the canonical string form. Field order is fixed; values
// are unescaped (the fields are documented to be hex / ASCII ids, not free
// text); the result is byte-stable across runs and architectures.
func (f Fingerprint) Canonical() string {
	return strings.Join([]string{
		f.ModelID,
		f.Revision,
		strings.ToLower(strings.TrimSpace(f.ModelSHA256)),
		strings.ToLower(strings.TrimSpace(f.TokenizerSHA256)),
		fmt.Sprintf("%d", f.Dim),
		f.DocumentSchema,
		f.ChunkerConfig,
		f.GraphGeneration,
	}, "|")
}

// ID derives a stable generation id from the canonical fingerprint via
// sha256. The id is hex, fixed-width, and depends on EVERY field, so two
// fingerprints that differ in any field produce different ids and never
// share a generation.
//
// The "v<n>-" prefix carries the document schema's major version, so a human
// inspecting a store can tell at a glance which schema a generation was
// built for ("v1-deadbeef…" vs "v2-cafebabe…"). The numeric part is the
// truncated sha256 of the canonical string.
func (f Fingerprint) ID() GenerationID {
	canonical := f.Canonical()
	sum := sha256.Sum256([]byte(canonical))
	hexSum := hex.EncodeToString(sum[:8]) // 16 hex chars; collision-safe for our scale
	schema := strings.TrimSpace(f.DocumentSchema)
	if schema == "" {
		schema = "v?"
	}
	return GenerationID(schema + "-" + hexSum)
}

// GraphGenerationPlaceholder is the value used for the `graph_generation`
// fingerprint field when the graphstore does not expose one. The placeholder
// is deliberately distinct from any real generation id so a migration or
// first-run store is visibly flagged.
const GraphGenerationPlaceholder = "unknown"
