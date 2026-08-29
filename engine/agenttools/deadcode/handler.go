package deadcode

// SW-255 (AX-15): the engine-side operation handler.
//
// Before this file, every operation the executor could run was a legacy
// adapter in surfaces/client that called the same Client method the legacy
// path calls — so "executor" and "legacy" were one function behind two names,
// and the byte-parity evidence on the seam compared that function with itself.
//
// Handler is the first operation handler that lives in engine. It receives its
// runtime dependencies as typed ports (the graph-query and graph-search
// services the catalog spec declares for dead_code), decodes the wire arguments
// fail-closed into the operation's OWN params type, and ends in exactly the
// call the legacy Direct method ends in: Assemble, then contract.Serialize.
// There is no second serializer and no second defaulting site — MaxItems is
// passed through verbatim and Assemble applies DefaultMaxItems, as it always
// has for every surface.
//
// The handler type is a plain func rather than a named type from engine/module
// because this package must not import the module set (engine/module is
// importable only by the composition root — engine/module/boundary_test.go);
// the built-in module that registers this handler lives there and converts.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// Operation is the catalog id this package implements.
const Operation = tool

// Args is the operation's wire argument shape — the params type the handler
// decodes into. It carries the one argument dead_code takes on every surface
// (MCP's limit, HTTP's max_items, the CLI's -max-items flag); the JSON name is
// the executor's argument vocabulary and is shared with surfaces/client's
// legacy adapter, which pins the two shapes equal by test.
type Args struct {
	// MaxItems caps the item list. Zero (and any non-positive value) selects
	// DefaultMaxItems inside Assemble; nothing here defaults it.
	MaxItems int `json:"max_items"`
}

// Handler returns the dead_code operation handler bound to its two ports.
//
// A nil port is NOT refused here: the module builder (engine/module) is the
// place that fails closed on a missing declared port at composition time. What
// Handler does with nil ports is what the legacy Direct method does with an
// unwired query service — Assemble reports the typed `unavailable` outcome —
// so a composition that bypasses the builder still cannot see a different
// answer from the two paths.
func Handler(graphQuery *query.Service, graphSearch *search.Service) func(context.Context, json.RawMessage) ([]byte, error) {
	deps := resolve.Deps{Query: graphQuery, Search: graphSearch}
	return func(ctx context.Context, raw json.RawMessage) ([]byte, error) {
		args, err := decodeArgs(raw)
		if err != nil {
			return nil, err
		}
		res, err := Assemble(ctx, Params{MaxItems: args.MaxItems, Deps: deps})
		if err != nil {
			return nil, err
		}
		return contract.Serialize(res)
	}
}

// decodeArgs fills Args from the raw request arguments with unknown fields
// REFUSED, mirroring the executor's own decode discipline: a misspelled or
// superseded argument name fails closed instead of being dropped. Empty or
// whitespace-only arguments are the zero value, which is the "no cap given"
// every surface sends today.
func decodeArgs(raw json.RawMessage) (Args, error) {
	var args Args
	if len(bytes.TrimSpace(raw)) == 0 {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return Args{}, fmt.Errorf("%s: decode arguments: %w", Operation, err)
	}
	return args, nil
}
