package search

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

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
// mapping is total — every State has a defined reason.
func ReasonForState(state embed.State) string {
	switch state {
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
// hit traces back to its source.
type SemanticHit struct {
	NodeID        string  `json:"node_id"`
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
//   - If a semantic state has been plumbed through WithSemanticState and
//     the state is non-ready (SW-261 AC-10), it returns the typed
//     unavailable response with Reason naming the state. The configured
//     embedder is intentionally NOT consulted — a non-ready generation
//     must not be served.
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
	if s.semanticState.State != 0 && s.semanticState.State != embed.StateReady {
		// Configured embedder, but the generation store is non-ready
		// (missing / stale / corrupt). The configured path is NOT
		// consulted: the user-visible reason names the state so an
		// agent can act on it. The byte shape (query, available=false,
		// reason, hits=[]) is identical to the no-embedder graceful
		// skip — only the Reason differs.
		return SemanticResponse{Query: query, Available: false, Reason: s.semanticState.Reason, Hits: []SemanticHit{}}, nil
	}
	emb, ok := s.embedReg.Active()
	if !ok {
		return SemanticResponse{Query: query, Available: false, Reason: UnavailableReason, Hits: []SemanticHit{}}, nil
	}
	if limit <= 0 {
		limit = DefaultResultLimit
	}
	if query == "" {
		return SemanticResponse{Query: query, Available: true, Hits: []SemanticHit{}}, nil
	}
	vecs, err := emb.Embed(ctx, []string{query})
	if err != nil {
		return SemanticResponse{}, err
	}
	if len(vecs) == 0 {
		return SemanticResponse{Query: query, Available: true, Hits: []SemanticHit{}}, nil
	}
	raw := s.index.Search(vecs[0], limit)

	hits := make([]SemanticHit, 0, len(raw))
	for _, h := range raw {
		hit := SemanticHit{NodeID: string(h.NodeID), Score: h.Score}
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
	// Defensive: re-establish deterministic order at the service boundary.
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].NodeID < hits[j].NodeID
	})
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
