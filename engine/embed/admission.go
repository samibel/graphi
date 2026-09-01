package embed

import (
	"context"
	"errors"
	"fmt"
)

// Admission is the per-document contract the embedder adapter exposes so the
// document builder can determine the EXACT bytes the model will consume
// (AC-1, AC-2, AC-7). The adapter — not the document builder, not the
// server — owns input admission: the adapter holds the exact tokenizer
// (or the server-side authoritative contract for one), the usable token
// limit, the special-token reserve and the preparation policy. The
// builder keeps semantic assembly and source-span provenance; the
// server is the final authority the adapter calls, but the adapter is
// what decides what bytes to send.
//
// Admission is FAIL-CLOSED. An adapter that cannot prove a document
// admissible returns a typed *AdmissionError naming the document and
// the limit that closed the gap; the calling build then aborts (AC-4)
// and never publishes a partial generation as ready (AC-5). Silent
// truncation is not a valid outcome on any path.
//
// Admit is called once per document before its bytes are hashed into
// TextHash / DocumentID and forwarded to Embed. The returned Admitted
// bytes ARE what gets embedded (so the persisted vector represents the
// bytes TextHash and DocumentID describe — AC-7's contract that the
// stored row's text_hash names the same text the model consumed).
type Admission interface {
	Admit(ctx context.Context, text string) (Admitted, error)
}

// Admitted is the per-document result of Admission. Text is the exact
// bytes the model will consume (which become TextHash and the embedded
// payload), TokenCount is the exact count the model will see (post-
// preparation, post-unk-handling), and Bound names which bound closed
// the gap: "none" (the document fit every bound), "tokens" (the
// adapter's preparation closed it), or "bytes" (only the resource
// cap ran — the adapter exposes no tokenizer-aware count).
type Admitted struct {
	Text       string
	TokenCount int
	Bound      string
}

// AdmissionProfile is the OPTIONAL interface an Embedder may expose so
// the v3 fingerprint carries a pinned, byte-stable admission identity
// (AC-3, AC-8). The serialized spec enters Embedder.ID() and therefore
// Fingerprint.Canonical(); a profile change invalidates stored
// generations rather than silently reusing them under false provenance.
//
// An Embedder that does NOT implement AdmissionProfile returns a zero
// AdmissionSpec; the fingerprint then names the empty profile (so a
// future adapter that gains admission support cannot silently inherit
// vectors that were built without one).
type AdmissionProfile interface {
	Profile() AdmissionSpec
}

// AdmissionSpec is the pinned admission identity that participates in
// the fingerprint (AC-3, AC-8). Every field either pins an external
// resource (TokenizerSHA256, the artifact digest) or names a
// deterministic algorithm (Algorithm, AlgorithmVersion). A change to
// any field changes Fingerprint.Canonical() and invalidates stored
// generations.
//
// MaxTokens is the USABLE token limit (after Reserve has been deducted
// from the model's full context). Reserve is the special-token reserve
// (BOS / EOS / pad) the preparation algorithm must leave headroom for.
// Algorithm names the preparation policy ("first-n-tokens" — the only
// policy this codebase currently implements); AlgorithmVersion is its
// monotonic semantic version so a behavior change to the policy mints
// a different fingerprint.
type AdmissionSpec struct {
	TokenizerID      string
	TokenizerSHA256  string
	TokenizerVersion string
	MaxTokens        int
	Reserve          int
	Algorithm        string
	AlgorithmVersion string
}

// IsZero reports whether the spec is the zero value (the fingerprint
// marks generations with no admission profile as a distinct identity —
// so a future adapter that gains admission cannot silently inherit
// vectors that were built without one).
func (s AdmissionSpec) IsZero() bool {
	return s.TokenizerID == "" && s.TokenizerSHA256 == "" && s.TokenizerVersion == "" &&
		s.MaxTokens == 0 && s.Reserve == 0 && s.Algorithm == "" && s.AlgorithmVersion == ""
}

// String renders the spec as a stable, length-prefixed string suitable
// for fingerprint composition. Field order is fixed.
func (s AdmissionSpec) String() string {
	return encodeCanonical([]string{
		s.TokenizerID,
		s.TokenizerSHA256,
		s.TokenizerVersion,
		fmt.Sprintf("%d", s.MaxTokens),
		fmt.Sprintf("%d", s.Reserve),
		s.Algorithm,
		s.AlgorithmVersion,
	})
}

// AdmissionError is the typed error Admission returns when a document
// cannot be admitted within the configured limit. NodeID and Path let
// the operator locate the offending document; Limit and Actual are the
// numbers that closed the gap. The build that surfaced this error
// aborts (AC-4) so a generation with inadmissible documents never
// reaches StateReady.
type AdmissionError struct {
	NodeID  string
	Path    string
	Limit   int
	Actual  int
	Profile AdmissionSpec
}

func (e *AdmissionError) Error() string {
	return fmt.Sprintf("embed: admission: node %s (%s): token count %d exceeds limit %d (profile=%s)",
		e.NodeID, e.Path, e.Actual, e.Limit, e.Profile.TokenizerID)
}

// IsAdmissionError reports whether err is or wraps an *AdmissionError.
// Build paths use it to fail-closed (AC-4) without losing the typed
// discriminator for the operator-facing renderer.
func IsAdmissionError(err error) bool {
	var ae *AdmissionError
	return errors.As(err, &ae)
}

// admissionAlgorithmFirstN is the only preparation policy implemented
// today: cut the tokenized sequence to at most MaxTokens tokens and
// keep the FIRST N (drop the tail). A "middle" or "sliding-window"
// policy could be added later under a new AlgorithmVersion.
const admissionAlgorithmFirstN = "first-n-tokens"

// admissionAlgorithmVersion is the semantic version of the preparation
// policy. Bump it when the algorithm semantics change so a stored
// generation is invalid by fingerprint rather than by re-embedding
// drift.
const admissionAlgorithmVersion = "1"
