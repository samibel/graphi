// Package embed's GenerationStore: a fingerprinted, atomically published
// persistence seam for vector generations. SW-261 replaces the
// `(Upsert, Load, DeleteExcept)` VectorTable seam with a generation API whose
// contracts are:
//   - every Build validates THEN publishes — a half-written generation is
//     never observed;
//   - the active generation's fingerprint is checked against the requested
//     one, and the typed state `missing | stale | corrupt | ready` is reported
//     so a caller can fail closed instead of mixing embedding spaces;
//   - rows carry the provenance a later hit-citation needs (path, lines,
//     span method) plus the text_hash that lets a reindex carry forward
//     identical documents without re-embedding;
//   - the legacy v1 `vectors` table migrates idempotently into a v1
//     generation marked stale, so a first --semantic on the new build
//     re-embeds rather than mixing v1 name-only with v2 body+doc vectors.
//
// Two adapters satisfy the seam identically: MemGenerationStore (tests) and
// SQLiteGenerationStore (the durable sidecar). Both are pinned by one shared
// conformance suite, mirroring the two-backend pattern in core/graphstore.
//
// Layering: embed is an engine leaf; this file adds new public types but no
// new imports.
package embed

import (
	"context"
	"errors"
	"fmt"

	"github.com/samibel/graphi/core/model"
)

// State is the typed result of GenerationStore.Active. It mirrors AC-7
// verbatim — the closed vocabulary the search service keys off to decide
// whether to serve vectors or return the typed Unavailable response.
type State int

const (
	// StateMissing: no active generation exists. The store has either never
	// been written or has had every generation pruned. A caller MUST NOT
	// serve vectors from a missing generation.
	StateMissing State = iota
	// StateStale: an active generation exists, but its fingerprint does NOT
	// match the requested one. The active generation was built under a
	// different model / schema / chunker / graph generation, so its vectors
	// live in a different embedding space. A caller MUST NOT serve them
	// against the requested fingerprint — that is the silent mix the story
	// exists to prevent.
	StateStale
	// StateCorrupt: an active generation exists and its fingerprint MATCHES,
	// but validation failed: row count vs persisted rows mismatch, dim
	// mismatch, or a referenced node no longer exists in the graph. A
	// caller MUST NOT serve vectors — they are present, but untrustworthy.
	StateCorrupt
	// StateReady: an active generation exists, its fingerprint matches the
	// requested one, and every validation check passed. The caller MAY
	// serve its vectors.
	StateReady
)

// String renders a closed, machine-stable name (the surfaces render this
// verbatim in the typed unavailable response).
func (s State) String() string {
	switch s {
	case StateMissing:
		return "missing"
	case StateStale:
		return "stale"
	case StateCorrupt:
		return "corrupt"
	case StateReady:
		return "ready"
	default:
		return fmt.Sprintf("State(%d)", int(s))
	}
}

// GenerationID identifies one built generation. It is the GenerationStore's
// foreign key into the rows table and is derived from a Fingerprint via
// Fingerprint.ID(). The string is human-readable (a schema prefix plus a
// short hash) so a debug print is informative.
type GenerationID string

// Generation is the metadata the Active call returns alongside its state.
// The row count and dim are the values the build recorded on Commit; the
// fingerprint is what the build was constructed under (Active reconciles it
// against the requested fingerprint to compute the state).
type Generation struct {
	ID          GenerationID
	Fingerprint Fingerprint
	RowCount    int
	Dim         int
}

// Row is one persisted (generation_id, document) row. The fields are the wire
// names (AC-3): generation_id, document_id, node_id, text_hash, path,
// start_line, end_line, span_method, vector. Load returns rows in canonical
// (node_id, document_id) order so callers iterate deterministically.
type Row struct {
	GenerationID GenerationID `json:"generation_id"`
	DocumentID   string       `json:"document_id"`
	NodeID       model.NodeId `json:"node_id"`
	TextHash     string       `json:"text_hash"`
	Path         string       `json:"path"`
	StartLine    int          `json:"start_line"`
	EndLine      int          `json:"end_line"`
	SpanMethod   string       `json:"span_method"`
	Vector       []float32    `json:"vector"`
}

// ErrBuildInProgress is returned by Begin when another Build is already
// open on this store (AC-6). A concurrent build is a programming error
// (the runtime serialises generations on its own); the typed error makes
// it diagnosable rather than silent.
var ErrBuildInProgress = errors.New("embed: a build is already in progress on this store")

// ValidationFailedError is returned by Build.Commit when post-commit
// validation fails. The build's rows are still persisted (so a manual
// inspection can find the cause), but the active pointer is NOT moved —
// the previous active generation remains the served one, and the next
// Begin() will discard the failed staging row. The Reason names the
// specific failure mode so a caller can render an actionable message.
type ValidationFailedError struct {
	Reason string
}

func (e *ValidationFailedError) Error() string {
	return "embed: generation validation failed: " + e.Reason
}

// StagingGenerationDiscardedError is returned when Begin discards a
// stale staging generation (AC-5): a previous Begin's Upsert phase
// crashed (or was aborted by the operator) before Commit. The previous
// active generation is untouched and continues to be served.
type StagingGenerationDiscardedError struct {
	StagingID GenerationID
}

func (e *StagingGenerationDiscardedError) Error() string {
	return "embed: discarded stale staging generation " + string(e.StagingID)
}

// NodeReferencer is the narrow interface Active consults during the
// "referenced node exists" validation step (AC-7 `corrupt` case). When
// nil, Active cannot perform that validation and treats a missing
// reader as "skip that check" rather than "fail" — the in-memory test
// adapter does not need a graph; the production wiring always supplies
// one. Errors other than ErrNotFound are surfaced verbatim.
type NodeReferencer interface {
	NodeExists(ctx context.Context, id model.NodeId) (bool, error)
}

// Build is one open generation pass: Upsert rows, then either Commit
// (validate + atomically publish) or Abort (drop the staging rows).
// Implementations are NOT safe for concurrent use — Begin serialises.
type Build interface {
	// Upsert inserts or replaces a row in the staging generation. The same
	// (GenerationID, NodeID) MAY be upserted more than once (the latest
	// write wins); a generation typically holds one row per NodeId.
	Upsert(ctx context.Context, r Row) error
	// Commit validates the staging generation THEN moves the active
	// pointer atomically. On success, the staging id becomes the active
	// id and any prior active generation is marked inactive (its rows are
	// kept until the next successful Commit, so a re-load can still
	// inspect the prior state; the schema may prune it via a separate
	// path). On validation failure the staging rows are kept (for
	// inspection) but the active pointer is unchanged; the next Begin
	// discards them. A validation failure returns *ValidationFailedError.
	Commit(ctx context.Context) error
	// Abort drops the staging rows and the staging id without touching
	// the active pointer. Safe to call after Commit (no-op) and after a
	// crash (next Begin does the same discard).
	Abort(ctx context.Context) error
}

// GenerationStore is the durable seam a vector index persists into. It is
// the SW-261 replacement for VectorTable: the replace-set property SW-260
// established survives as a property of Commit (it drops every prior row
// not carried forward into the new generation), NOT as a separate
// DeleteExcept method on the seam.
type GenerationStore interface {
	// Begin opens a new staging generation under the requested fingerprint.
	// If a stale staging row exists from a prior Begin that never reached
	// Commit/Abort, Begin drops it FIRST so the new pass starts clean. The
	// returned Build's rows belong to the new staging generation; the
	// active pointer is unchanged until the caller calls Commit.
	//
	// A second Begin on the same store before Commit/Abort of the first
	// returns ErrBuildInProgress — concurrent builds are a programming
	// error.
	Begin(ctx context.Context, fp Fingerprint) (Build, error)

	// Active reports the active generation's metadata and its state against
	// the requested fingerprint. When no active generation exists the id
	// is "" and the state is StateMissing; the fingerprint is still echoed
	// back so a caller can render a state-specific reason. When an active
	// generation exists, Active:
	//   1. compares the active fingerprint to fp — mismatch ⇒ StateStale;
	//   2. counts rows and dims in the persisted sidecar — mismatch vs
	//      the build's recorded counts ⇒ StateCorrupt;
	//   3. when nodes is non-nil, verifies every row's NodeId resolves —
	//      any miss ⇒ StateCorrupt;
	//   4. otherwise ⇒ StateReady.
	//
	// The generation's persisted rows can be loaded with Load(id).
	Active(ctx context.Context, fp Fingerprint, nodes NodeReferencer) (Generation, State, error)

	// Load returns every row of the given generation, in canonical
	// (node_id, document_id) order. An unknown id returns an empty slice
	// and no error.
	Load(ctx context.Context, id GenerationID) ([]Row, error)
}
