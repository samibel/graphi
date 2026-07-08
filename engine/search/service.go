// Package search is graphi's lexical and symbol search service.
//
// Layering: search is an engine package. It consumes the read-only graphstore
// SearchNodes API and owns ranking interpretation, result shaping, and the
// canonical response serializer. Surfaces (CLI, MCP, daemon) must not hold
// search logic of their own.
package search

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/embed"
)

// Match is one ranked search result. It carries the node's identity, provenance,
// and FTS5 rank so callers can trace the symbol back to its source.
type Match struct {
	NodeID        string  `json:"node_id"`
	Kind          string  `json:"kind"`
	QualifiedName string  `json:"qualified_name"`
	SourcePath    string  `json:"source_path"`
	Line          int     `json:"line"`
	Column        int     `json:"column"`
	Rank          float64 `json:"rank"`
}

// Response is the canonical search response returned by the service and
// serialized by Marshal. It is identical for every surface.
type Response struct {
	Query   string  `json:"query"`
	Matches []Match `json:"matches"`
}

// Reader is the narrow dependency the search service needs from graphstore.
// It is satisfied by graphstore.Graphstore.
type Reader interface {
	SearchNodes(ctx context.Context, text string, limit int) ([]graphstore.RankedNode, error)
}

// Service is the shared search service. It is safe for concurrent use when the
// underlying Reader is.
//
// Lexical search (Search) is always available. Semantic search (SemanticSearch)
// is OPTIONAL and OFF by default: a Service constructed with New has a nil embed
// registry, so SemanticSearch returns the typed Unavailable response (graceful
// skip). WithSemantic opts the service into the configured embedder + vector
// index.
type Service struct {
	reader Reader

	// Optional semantic-search collaborators. All nil on the default path ⇒
	// SemanticSearch gracefully skips (no embedder, no network).
	embedReg   *embed.Registry
	index      embed.VectorIndex
	nodeReader NodeReader
}

// New constructs a Service over the given read-only search reader. Semantic
// search is OFF (graceful skip) until WithSemantic is called.
func New(reader Reader) *Service {
	return &Service{reader: reader}
}

// WithSemantic opts the service into OPTIONAL semantic search (SW-059). reg is
// the embedder registry (zero/unconfigured ⇒ still graceful-skip), index is the
// vector index backend (any embed.VectorIndex — brute-force or HNSW; SW-084), and
// nodeReader resolves NodeId → Node for hit provenance (may be nil). It returns the
// receiver for chaining. Passing a nil or unconfigured registry preserves the
// graceful-skip behavior; a nil index defaults to the brute-force backend.
func (s *Service) WithSemantic(reg *embed.Registry, index embed.VectorIndex, nodeReader NodeReader) *Service {
	s.embedReg = reg
	if index == nil {
		index = embed.NewIndex()
	}
	s.index = index
	s.nodeReader = nodeReader
	return s
}

// DefaultResultLimit is the maximum number of matches returned when the caller
// passes a non-positive limit.
const DefaultResultLimit = 100

// kindPackage is the interned package-node kind (WP-01), excluded from search
// results; mirrors core/parse.KindPackage without importing the parse layer.
const kindPackage = "package"

// kindExternal is the interned external-symbol node kind (WP-03), excluded from
// search results: external nodes are heuristic linker artifacts (stdlib /
// 3rd-party call/ref targets, empty source path, no line) minted so name-keyed
// analyses have a node to match — they are not navigable symbols. Mirrors
// core/parse.KindExternal without importing the parse layer.
const kindExternal = "external"

// Search runs a ranked symbol/text search. An empty query returns an empty
// result set with no error. Matches are ordered by rank ascending (better
// matches first), then by qualified_name and node_id for deterministic
// tie-breaking.
func (s *Service) Search(ctx context.Context, query string, limit int) (Response, error) {
	if limit <= 0 {
		limit = DefaultResultLimit
	}
	ranked, err := s.reader.SearchNodes(ctx, query, limit)
	if err != nil {
		return Response{}, err
	}
	matches := make([]Match, 0, len(ranked))
	for _, rn := range ranked {
		n := rn.Node
		// WP-01 query hygiene: interned `package` nodes are structural linking
		// artifacts (empty source path, no line) minted to collapse the import
		// fan-out — they are not navigable symbols, so they never surface in search.
		if n.Kind() == kindPackage || n.Kind() == kindExternal {
			continue
		}
		matches = append(matches, Match{
			NodeID:        string(n.ID()),
			Kind:          n.Kind(),
			QualifiedName: n.QualifiedName(),
			SourcePath:    n.SourcePath(),
			Line:          n.Line(),
			Column:        n.Column(),
			Rank:          rn.Rank,
		})
	}
	// Defensive sort: the backend is responsible for ordering, but the service
	// re-establishes the contract here so surfaces cannot observe drift.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Rank != matches[j].Rank {
			return matches[i].Rank < matches[j].Rank
		}
		if matches[i].QualifiedName != matches[j].QualifiedName {
			return matches[i].QualifiedName < matches[j].QualifiedName
		}
		return matches[i].NodeID < matches[j].NodeID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return Response{Query: query, Matches: matches}, nil
}

// Marshal serializes the response to stable, compact JSON with deterministic
// key order. It is the single canonical serializer used by every surface.
func Marshal(r Response) ([]byte, error) {
	if r.Matches == nil {
		r.Matches = []Match{}
	}
	return json.Marshal(r)
}
