package search

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// UnavailableReason is the single, canonical graceful-skip reason string emitted
// when no embedder is configured. It is engine-owned so every surface
// (CLI/MCP/HTTP) serializes byte-identically (SW-059 parity).
const UnavailableReason = "no embedder configured; run `graphi setup-embedder ...`"

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
