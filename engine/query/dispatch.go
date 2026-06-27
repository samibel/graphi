package query

import (
	"context"
	"fmt"

	"github.com/samibel/graphi/core/model"
)

// Operation names accepted by Dispatch. Surfaces map their own command/tool
// names onto these so there is exactly one dispatch table for the whole engine.
const (
	OpCallers      = "callers"
	OpCallees      = "callees"
	OpReferences   = "references"
	OpDefinition   = "definition"
	OpNeighborhood = "neighborhood"

	// Hierarchy operations (EP-011 G2). They navigate the implements/inherits/
	// overrides edge vocabulary populated at ingest.
	OpImplementers = "implementers" // types that implement/embed symbolID (inbound implements)
	OpImplements   = "implements"   // interfaces/types symbolID implements (outbound implements)
	OpOverrides    = "overrides"    // methods that override symbolID (inbound overrides)
	OpSubtypes     = "subtypes"     // subtypes of symbolID (inbound inherits + implements)
	OpSupertypes   = "supertypes"   // supertypes of symbolID (outbound inherits + implements)
)

// Operations is the canonical, ordered list of supported operations. Surfaces
// use it to advertise their commands/tools without re-listing them locally.
var Operations = []string{OpCallers, OpCallees, OpReferences, OpDefinition, OpNeighborhood, OpImplementers, OpImplements, OpOverrides, OpSubtypes, OpSupertypes}

// Dispatch routes a named operation to the corresponding Service method. It is
// the SINGLE entry point both the CLI and MCP surfaces call, so neither surface
// can introduce divergent query, traversal, ordering, or outcome logic: parity
// is guaranteed by construction. depth is honored only by the neighborhood
// operation (and is clamped by the service); it is ignored by the others.
//
// An unknown operation is a programmer/caller error (returned as an error), as
// distinct from an unresolved symbol (a typed not-found Result, never an error).
func (s *Service) Dispatch(ctx context.Context, operation string, symbolID model.NodeId, depth int) (Result, error) {
	switch operation {
	case OpCallers:
		return s.Callers(ctx, symbolID)
	case OpCallees:
		return s.Callees(ctx, symbolID)
	case OpReferences:
		return s.References(ctx, symbolID)
	case OpDefinition:
		return s.Definition(ctx, symbolID)
	case OpNeighborhood:
		return s.Neighborhood(ctx, symbolID, depth)
	case OpImplementers:
		return s.Implementers(ctx, symbolID)
	case OpImplements:
		return s.Implements(ctx, symbolID)
	case OpOverrides:
		return s.Overrides(ctx, symbolID)
	case OpSubtypes:
		return s.Subtypes(ctx, symbolID)
	case OpSupertypes:
		return s.Supertypes(ctx, symbolID)
	default:
		return Result{}, fmt.Errorf("query: unknown operation %q (want one of %v)", operation, Operations)
	}
}
