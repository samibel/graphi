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
type Service struct {
	reader Reader
}

// New constructs a Service over the given read-only search reader.
func New(reader Reader) *Service {
	return &Service{reader: reader}
}

// DefaultResultLimit is the maximum number of matches returned when the caller
// passes a non-positive limit.
const DefaultResultLimit = 100

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
