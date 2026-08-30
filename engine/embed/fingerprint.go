package embed

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
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
//
// The variable is retained for documentation and the conformance test
// (which asserts every field has a canonical encoding) but is no longer
// the source of truth: encodeCanonical emits the fields in this fixed
// order. Removing or renaming a field here without changing
// encodeCanonical would silently produce a different canonical string.
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

// Canonical returns the canonical string form. Field order is fixed; the
// encoding is length-prefixed so a `|`-bearing value cannot collide with
// the field separator (a previous revision used `strings.Join(..., "|")`
// which made `("a|b","c")` collide with `("a","b|c")` — AC-2 forbids).
// The result is byte-stable across runs and architectures.
//
// Encoding per field: a decimal ASCII byte count, the literal ASCII `:`,
// then the field bytes. The byte count is the raw length of the field
// value after normalisation (lowercase + trim for the SHA fields). The
// resulting canonical string is provably injective: a different (length,
// value) pair in any field produces a different byte sequence and hence
// a different ID.
func (f Fingerprint) Canonical() string {
	parts := []string{
		f.ModelID,
		f.Revision,
		strings.ToLower(strings.TrimSpace(f.ModelSHA256)),
		strings.ToLower(strings.TrimSpace(f.TokenizerSHA256)),
		fmt.Sprintf("%d", f.Dim),
		f.DocumentSchema,
		f.ChunkerConfig,
		f.GraphGeneration,
	}
	return encodeCanonical(parts)
}

// encodeCanonical serialises an ordered list of field values using a
// length-prefixed encoding. Each field becomes `<len>:<value>`; the
// fields are then joined with `\n` so the whole canonical string is one
// line per field. The encoding is total: any two distinct inputs (in
// value, length, or field count) produce distinct outputs.
//
// The length is written as base-10 ASCII so a human can read it. A
// field value containing `\n` would defeat the encoding — the documented
// contract is that field values are ASCII ids or hex digests, neither of
// which contain newlines; the conformance test asserts this and the
// generation_id suffix prevents confusion when a human inspects the
// canonical.
func encodeCanonical(parts []string) string {
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strconv.Itoa(len(p)))
		b.WriteByte(':')
		b.WriteString(p)
	}
	return b.String()
}

// decodeCanonical is the inverse of encodeCanonical, used by
// fingerprintFromCanonical in the SQLite adapter to recover the typed
// Fingerprint from a stored canonical string. It splits on `\n` and
// verifies the per-field length prefix; an inconsistent input yields a
// Fingerprint with the truncated/empty fields set to "" (the SQLite
// path never reads the typed values back for equality — equality is a
// direct canonical-string compare — but the typed view is convenient
// for diagnostics).
func decodeCanonical(canonical string) []string {
	if canonical == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(canonical, "\n") {
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			// Malformed: append the raw line so the caller can see it.
			out = append(out, line)
			continue
		}
		n, err := strconv.Atoi(line[:colon])
		if err != nil || n < 0 || colon+1+n > len(line) {
			out = append(out, line[colon+1:])
			continue
		}
		out = append(out, line[colon+1:colon+1+n])
	}
	return out
}

// ID derives a stable generation id from the canonical fingerprint via
// sha256. The id is the FULL sha256 hex digest (no truncation), prefixed
// with the document schema's major version. Two fingerprints that differ
// in any field produce different ids and never share a generation; a
// 64-bit truncation (the previous revision) would still be collision-safe
// at the scale of a real repository, but a full digest makes the contract
// trivial to verify (sha256 is collision-resistant in practice).
//
// The "v<n>-" prefix carries the document schema's major version, so a human
// inspecting a store can tell at a glance which schema a generation was
// built for ("v1-deadbeef…" vs "v2-cafebabe…").
func (f Fingerprint) ID() GenerationID {
	canonical := f.Canonical()
	sum := sha256.Sum256([]byte(canonical))
	hexSum := hex.EncodeToString(sum[:]) // full 32-byte / 64-hex digest
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
