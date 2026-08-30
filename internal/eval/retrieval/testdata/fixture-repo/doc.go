// Package fixture is a small, buildable Go module that exists only to be
// indexed by the retrieval evaluation harness (internal/eval/retrieval,
// story SW-258). It is deliberately doc-comment heavy: the config_docs
// stratum of the mini dataset asks questions that are answered by prose
// rather than by an identifier, and a fixture without prose could not carry
// such a query.
//
// # Architecture
//
// The module is a miniature service with four layers:
//
//   - auth: bearer-token validation (ValidateToken, ParseToken). A request
//     is refused before it reaches any handler when its token is missing,
//     malformed or expired.
//   - config: the environment-driven configuration loader (Load). Every
//     knob has a FIXTURE_ prefixed environment variable and a documented
//     default; an unparseable value is an error, never a silent default.
//   - store: the Store interface with two implementations, MemoryStore
//     (process-local map) and FileStore (one file per key under a root
//     directory). The HTTP handler depends on the interface only.
//   - httpapi: the HTTP handler that ties the layers together. A request
//     flows Handler.ServeHTTP → auth.ValidateToken → Store.Get/Put.
//
// # Request flow
//
// cmd/app wires the layers: it loads the configuration, picks a Store
// implementation from Config.StoreKind, constructs the Handler and serves it.
// Nothing in this module talks to the network on its own; the fixture is
// hermetic by construction.
//
// # Configuration reference
//
// FIXTURE_ADDR sets the listen address (default ":8080"). FIXTURE_STORE
// selects the store implementation ("memory" or "file", default "memory").
// FIXTURE_STORE_ROOT is the FileStore root directory (default "./data").
// FIXTURE_TOKEN_TTL is the token lifetime as a Go duration (default "1h").
package fixture

// Version is the fixture's nominal version. It is a constant rather than a
// build-time stamp so the indexed graph is byte-identical across runs.
const Version = "0.1.0"

// Name is the service name used in log lines and the health endpoint.
const Name = "fixture-service"
