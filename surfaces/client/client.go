// Package client defines the transport-agnostic surface client contract.
//
// Both the CLI and MCP surfaces consume a Client so they can run against either
// an in-process service (direct mode) or a hot-index daemon (daemon mode)
// without code changes. The serialized bytes returned by a Client are always
// the canonical output of the shared engine service.
package client

import (
	"context"
	"errors"
)

// ErrSearchUnavailable is returned when a Client has no search service configured.
var ErrSearchUnavailable = errors.New("client: search service unavailable")

// ErrSavingsUnavailable is returned when a Client has no savings ledger
// configured (SW-020). Query/search still work; only the savings readout is
// unavailable.
var ErrSavingsUnavailable = errors.New("client: savings ledger unavailable")

// Client is the thin contract every surface uses to execute structural queries,
// search, and read the savings ledger. Implementations may be in-process or over
// a Unix domain socket.
type Client interface {
	// Query runs a structural query operation and returns the canonical
	// serialized result bytes.
	Query(ctx context.Context, op, symbol string, depth int) ([]byte, error)
	// Search runs a lexical/symbol search and returns the canonical serialized
	// result bytes.
	Search(ctx context.Context, q string, limit int) ([]byte, error)
	// Savings returns the canonical serialized savings-ledger readout (per-call,
	// per-session, cumulative USD + cap flags). It is the single source for the
	// MCP and CLI readouts so both surfaces stay in parity.
	Savings(ctx context.Context) ([]byte, error)
}
