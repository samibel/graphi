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
//     `Metadata(ctx, "index.commit_generation")`; when absent we use the
//     placeholder "unknown" and the orchestrator reports this as an open
//     finding (the call site must surface that decision to its operator — see
//     `runtime.NewSearchService`).
//
// The canonical string is length-prefixed and line-delimited, NOT
// pipe-separated or map-iterated, so it is byte-stable and can serve
// as an equality / hash key without normalisation. See
// `encodeCanonical` for the byte-level encoding.
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

// fingerprintFields (the FIXED order Canonical emits) is documented in
// the encodeCanonical call below. The order is inlined at the call site
// so the encoding's source of truth is one place, not a separate
// variable that might drift. Renaming or reordering a field is
// generation-breaking; see Canonical for the precise mapping.

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

// encodeCanonical serialises an ordered list of field values using a true
// length-prefixed encoding. Each field becomes `<len>:<value>`; the fields
// are then joined with `\n`. The encoding is total: any two distinct
// inputs (in value, length, or field count) produce distinct outputs.
//
// SW-261 review round 2 (MAJOR 6): the previous revision also joined on
// `\n`, so a value containing a newline character would defeat the
// decoder (it would split the encoded string at the embedded newline,
// producing the wrong field count and silently corrupting the typed
// view). The encoding now uses a true length prefix — the decoder
// reads exactly the declared number of bytes per field, so any byte
// sequence is round-trippable, including values containing `|`, `\n`,
// or arbitrary bytes. The length is written as base-10 ASCII so a
// human can read it.
//
// Field values are still constrained by the Fingerprint's typed shape
// (ASCII ids, hex digests, an integer dim) — none of which carry
// embedded newlines today — but the encoder no longer relies on that
// constraint to round-trip, so a future field that does carry binary
// data (e.g. a content hash) can be added without breaking the
// decoder.
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

// decodeCanonical is the inverse of encodeCanonical. It is a true
// length-prefixed decoder: it reads the declared length of each field
// and consumes exactly that many bytes, then advances to the next
// field. Embedded `\n` characters in a field value are therefore safe
// (the decoder does not split on them). The previous revision split
// the canonical on `\n`, which silently corrupted round-trip for any
// value containing a newline character (SW-261 review round 2
// MAJOR 6).
//
// Malformed input (a missing colon, a negative length, a length that
// runs past the end of the input, or a non-numeric length) yields a
// Fingerprint with the affected field truncated to "" — the SQLite
// path never reads the typed values back for equality (equality is a
// direct canonical-string compare) but the typed view is convenient
// for diagnostics. The decoder never panics.
func decodeCanonical(canonical string) []string {
	if canonical == "" {
		return nil
	}
	var out []string
	pos := 0
	for pos < len(canonical) {
		// Find the colon that closes the length prefix.
		colon := strings.IndexByte(canonical[pos:], ':')
		if colon < 0 {
			// Malformed: no length prefix. Append the rest so the
			// caller can see it.
			out = append(out, canonical[pos:])
			return out
		}
		colon += pos
		// Parse the length. A malformed length yields an empty
		// field rather than a panic; this preserves the
		// "degraded, never panic" contract on a hand-tampered
		// sidecar.
		n, err := strconv.Atoi(canonical[pos:colon])
		if err != nil || n < 0 {
			out = append(out, "")
			// Skip past the colon to advance.
			pos = colon + 1
			continue
		}
		start := colon + 1
		end := start + n
		if end > len(canonical) {
			// Length declared more bytes than remain. Take
			// what we have and stop — the canonical is
			// truncated, the typed view reflects it.
			out = append(out, canonical[start:])
			return out
		}
		out = append(out, canonical[start:end])
		// Advance past the field's declared bytes; skip the
		// inter-field newline that follows (if any).
		pos = end
		if pos < len(canonical) && canonical[pos] == '\n' {
			pos++
		}
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
