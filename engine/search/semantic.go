package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/embed"
)

// UnavailableReason is the single, canonical graceful-skip reason string emitted
// when no embedder is configured. It is engine-owned so every surface
// (CLI/MCP/HTTP) serializes byte-identically (SW-059 parity).
//
// The SW-261 track extends this file's "unavailable" envelope to name the
// GenerationStore state when one IS configured but not ready: the typed
// reason is the user-visible hint, and it travels byte-identically across
// CLI/MCP/HTTP because every surface renders through the same
// engine/search.SemanticResponse. The default build (no embedder) keeps
// emitting UnavailableReason verbatim — the S0 baseline golden for
// `search_semantic` is unchanged.
const UnavailableReason = "no embedder configured; run `graphi setup-embedder ...`"

// ReasonUnavailable names the user-visible state when an embedder IS
// configured but the GenerationStore has no active generation. It is the
// companion of UnavailableReason for the configured-but-unbuilt path.
const ReasonUnavailable = "semantic index unavailable: run `graphi index --semantic`"

// ReasonStale names the user-visible state when the active generation's
// fingerprint differs from the requested one (model, schema, chunker or
// graph generation changed). The active generation lives in a different
// embedding space and cannot be served; the user must re-index.
const ReasonStale = "semantic index stale: run `graphi index --semantic`"

// ReasonCorrupt names the user-visible state when the active generation
// failed validation (row count, dim, or a referenced node). The rows are
// untrustworthy and must not be served; re-indexing rebuilds them.
const ReasonCorrupt = "semantic index corrupt: run `graphi index --semantic`"

// ReasonForState renders the closed-vocabulary reason for a typed state.
// The state is the SW-261 GenerationStore state; the reason is the
// user-visible message the typed Unavailable response carries. The
// mapping is total — every State has a defined reason, including the
// StateUnset sentinel.
//
// StateUnset is NOT a safety net: SemanticSearch treats an unset state as
// "no state plumbed" and proceeds, so ReasonForState is never consulted on
// that path. Callers that hold a generation store must plumb the state
// (the runtime does, in loadSemanticState, including the no-meta-dir case
// it synthesises as missing). An earlier version of this comment claimed
// the sentinel prevented a forgotten plumbing from serving a missing
// generation as ready; it does not, and the review that caught it is the
// reason the runtime synthesises rather than relying on this.
func ReasonForState(state embed.State) string {
	switch state {
	case embed.StateUnset:
		return UnavailableReason
	case embed.StateMissing:
		return ReasonUnavailable
	case embed.StateStale:
		return ReasonStale
	case embed.StateCorrupt:
		return ReasonCorrupt
	case embed.StateReady:
		return ""
	default:
		return fmt.Sprintf("semantic index unknown state: %d", int(state))
	}
}

// SemanticHit is one ranked semantic-search result: the node identity (cited by
// NodeId) plus its cosine score. The node provenance fields mirror Match so a
// hit traces back to its source. DocumentID is the embedding-space document id
// the GenerationStore persisted with the vector (SW-260
// SemanticDocument.DocumentID); the retrieval module's hierarchical dedupe key
// (AC-2) consumes it. Empty when the indexed row had no document id (a
// legacy fixture path).
type SemanticHit struct {
	NodeID        string  `json:"node_id"`
	DocumentID    string  `json:"document_id,omitempty"`
	Kind          string  `json:"kind"`
	QualifiedName string  `json:"qualified_name"`
	SourcePath    string  `json:"source_path"`
	Line          int     `json:"line"`
	Column        int     `json:"column"`
	Score         float64 `json:"score"`
}

// SemanticResponse is the SINGLE engine-owned typed result for semantic search,
// serialized by MarshalSemantic. It is identical for every surface, including the
// graceful-skip path:
//
//   - When no embedder is configured, Available is false, Reason is
//     UnavailableReason, and Hits is empty — NO error, NO network, NO embedding.
//   - When an embedder is configured, Available is true, Reason is empty, and
//     Hits carries the ranked NodeId+score results.
type SemanticResponse struct {
	Query     string        `json:"query"`
	Available bool          `json:"available"`
	Reason    string        `json:"reason,omitempty"`
	Hits      []SemanticHit `json:"hits"`
}

// SemanticSearch runs an OPTIONAL semantic search over the configured embedder
// and vector index. It is the CORE graceful-skip path (SW-059):
//
//   - If registry is nil or !registry.Configured() (the default build), it returns
//     a typed Unavailable SemanticResponse (Available=false, Reason=
//     UnavailableReason) with NO error, makes ZERO network calls, performs NO
//     embedding, and does not touch the always-available lexical Search.
//   - If the configured embedder exposes embed.AvailabilityChecker, its local
//     artifact preflight runs before generation-state and empty-query short
//     circuits. A repairable failure returns the exact setup command. This
//     precedence is deliberate: installing the artifact is a prerequisite to
//     rebuilding or querying a stale/corrupt generation; once installed, the
//     generation-state repair becomes visible. SW-265 consumes this ordering.
//   - If a semantic state has been plumbed through WithSemanticState and the
//     artifact is available but the state is non-ready (SW-261 AC-10), it
//     returns the typed unavailable response with Reason naming the state.
//   - Otherwise it embeds the query with the active embedder, ranks indexed
//     vectors by cosine similarity, and returns scored hits citing NodeId + score
//     in deterministic order (score desc, NodeId asc).
//
// Lexical Search is unchanged and always available regardless of this path.
func (s *Service) SemanticSearch(ctx context.Context, query string, limit int) (SemanticResponse, error) {
	if s.embedReg == nil || !s.embedReg.Configured() {
		// Graceful skip: no embedder, no network, no error.
		return SemanticResponse{Query: query, Available: false, Reason: UnavailableReason, Hits: []SemanticHit{}}, nil
	}
	emb, ok := s.embedReg.Active()
	if !ok {
		return SemanticResponse{Query: query, Available: false, Reason: UnavailableReason, Hits: []SemanticHit{}}, nil
	}
	if checker, ok := emb.(embed.AvailabilityChecker); ok {
		if err := checker.CheckAvailable(ctx); err != nil {
			if repair := repairable(err); repair != "" {
				return SemanticResponse{Query: query, Available: false, Reason: repair, Hits: []SemanticHit{}}, nil
			}
			return SemanticResponse{}, err
		}
	}
	if !s.semanticState.State.IsZero() && s.semanticState.State != embed.StateReady {
		// Configured embedder, but the generation store is non-ready
		// (missing / stale / corrupt). Availability has been checked, but
		// the embedder is NOT invoked: the user-visible reason names the state
		// so an agent can act on it. The byte shape (query, available=false,
		// reason, hits=[]) is identical to the no-embedder graceful
		// skip — only the Reason differs.
		return SemanticResponse{Query: query, Available: false, Reason: s.semanticState.Reason, Hits: []SemanticHit{}}, nil
	}
	if limit <= 0 {
		limit = DefaultResultLimit
	}
	if query == "" {
		return SemanticResponse{Query: query, Available: true, Hits: []SemanticHit{}}, nil
	}
	vecs, err := emb.Embed(ctx, []string{query})
	if err != nil {
		// AC-5: an embedder that surfaces a typed UnavailableError must
		// reach SemanticSearch as the typed unavailable response with
		// reason carrying the exact repair command. A plain
		// error from the configured path is a real surface failure
		// (an off-the-shelf embedder returning a wrapped network
		// error, for example) and continues to surface as a non-nil
		// error; the typed case is opt-in.
		if u := repairable(err); u != "" {
			return SemanticResponse{Query: query, Available: false, Reason: u, Hits: []SemanticHit{}}, nil
		}
		return SemanticResponse{}, err
	}
	if len(vecs) == 0 {
		return SemanticResponse{Query: query, Available: true, Hits: []SemanticHit{}}, nil
	}
	raw := s.index.Search(vecs[0], limit)

	hits := make([]SemanticHit, 0, len(raw))
	for _, h := range raw {
		hit := SemanticHit{NodeID: string(h.NodeID), DocumentID: h.DocumentID, Score: h.Score}
		// Enrich with provenance when the node is resolvable; a missing node still
		// yields a NodeId+score citation (never blocks the path).
		if s.nodeReader != nil {
			if n, gerr := s.nodeReader.GetNode(ctx, h.NodeID); gerr == nil {
				hit.Kind = n.Kind()
				hit.QualifiedName = n.QualifiedName()
				hit.SourcePath = n.SourcePath()
				hit.Line = n.Line()
				hit.Column = n.Column()
			}
		}
		hits = append(hits, hit)
	}
	// AC-3: the in-memory index already orders by QUANTISED cosine
	// (int(round(cos*10000))) with canonical NodeId as the tie-break, and
	// the truncation to `limit` already happened after that order, so a
	// re-order here would be both redundant and unsafe: a quantised tie
	// that straddles the rank-50 cutoff in the index must not be
	// re-resolved on a difference the AC-3 contract says is not there.
	// The defensive sort that lived here previously is removed.
	return SemanticResponse{Query: query, Available: true, Hits: hits}, nil
}

// MarshalSemantic serializes a SemanticResponse to stable, compact JSON with
// deterministic key order. It is the single canonical serializer used by every
// surface, so the graceful-skip "unavailable" bytes are byte-identical across
// CLI/MCP/HTTP (SW-059 serialized-byte parity).
func MarshalSemantic(r SemanticResponse) ([]byte, error) {
	if r.Hits == nil {
		r.Hits = []SemanticHit{}
	}
	return json.Marshal(r)
}

// NodeReader is the narrow read dependency SemanticSearch uses to enrich hits
// with node provenance. It is satisfied by graphstore.Graphstore.
type NodeReader interface {
	GetNode(ctx context.Context, id model.NodeId) (model.Node, error)
}

// Repairable is the interface an embedder error must implement to be
// surfaced into the typed unavailable response with its repair command.
// The production static embedder satisfies it (see engine/embed/static
// .UnavailableError.Repair); other embedders continue to surface their
// errors as plain errors.
type Repairable interface {
	error
	Repair() string
}

// repairable unwraps err to find a typed Repairable. Returns the repair
// command on success, "" on no typed repair. The walk uses errors.As so
// a wrapped UnavailableError is recognised.
func repairable(err error) string {
	if err == nil {
		return ""
	}
	var r Repairable
	if errors.As(err, &r) {
		return r.Repair()
	}
	return ""
}
