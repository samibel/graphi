package client

// This file adds the AX-04 generic Executor (SW-224): one narrow seam through
// which an operation can be invoked BY NAME, so a new operation can be attached
// without widening the ~40-method Client interface or the ~15-service Direct
// facade.
//
// # Additive only — nothing dispatches through this yet
//
// No surface routes a request through the Executor. The MCP, HTTP, CLI, TUI and
// daemon paths still call Client methods exactly as they did at the AX-00
// baseline, and the AX-00 golden descriptors, wire names and canonical result
// bytes are untouched. Switching one Labs operation onto this path is SW-226's
// canary; projecting descriptors out of the catalog is SW-225's. Rollback for
// this story is therefore a deletion: remove executor.go, executor_adapters.go
// and their tests and the tree is byte-identical to the legacy-only state
// (AC-6), which executor_rollback_test.go asserts mechanically rather than by
// assertion in a document.
//
// # Where the bytes come from
//
// The Executor transports canonical bytes; it never makes them. Every adapter
// calls the legacy Client method and returns that method's bytes UNCHANGED —
// there is no re-encoding step, no map[string]any staging shape and no second
// serializer. That is the whole reason the byte-parity tests can be exact
// rather than "structurally equivalent": the adapter path and the legacy path
// return the same bytes because they are literally the same call.
//
// # Where the identity comes from
//
// Operation identity and versioning come from engine/opcatalog (SW-223) — the
// single source of truth. The Executor keeps no second operation list: its
// adapter table is keyed by catalog id, and construction fails if an adapter
// names an id the catalog does not declare.
//
// # Vocabulary
//
// Rejections use core/registry's typed lifecycle errors (SW-222). This package
// introduces no parallel error kind: "the executor cannot find what you asked
// for" is registry.ErrMissingDependency, whether the gap is the operation id,
// the contract version, or the legacy adapter.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/opcatalog"
)

// executorRegistry is the short name the typed lifecycle errors carry.
const executorRegistry = "executor"

// Request is one operation invocation addressed by name.
//
// Version is REQUIRED and is matched exactly against the catalog's declared
// version. An empty version is rejected, not defaulted: fail-closed discipline
// says a caller that declares no contract version gets an error today rather
// than a silent behaviour change on the day a version "2" appears.
//
// Arguments is raw JSON so the transport never has to know the shape. It is
// decoded straight into the operation's typed argument struct with unknown
// fields REFUSED, so a misspelled or superseded argument name fails instead of
// being dropped on the floor (standards: superseded spellings get rejected, not
// ignored).
type Request struct {
	Operation string          `json:"operation"`
	Version   string          `json:"version"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// Arguments is the typed argument form of one operation, expressed in the
// LEGACY METHOD's own vocabulary — the Go parameters of the Client method the
// adapter calls.
//
// It is deliberately NOT an MCP or HTTP wire schema. Argument defaulting (MCP's
// depth-of-1, its search limit, its dry-run provenance) stays on the surfaces
// where it lives today; the Executor applies no defaults of its own, because a
// second defaulting site is exactly how two surfaces begin to disagree.
// Projecting a client-facing schema is SW-225's job.
//
// The interface carries an unexported method, so the set of executable
// operations is closed to this package. That is what keeps "no surface
// dispatches through the executor yet" a compile-time property rather than a
// convention.
type Arguments interface {
	// Operation names the catalog operation these arguments belong to.
	Operation() string
	// invoke calls the legacy Client method and returns its canonical result
	// bytes unchanged.
	invoke(ctx context.Context, c Client) ([]byte, error)
}

// argumentsFactory returns a fresh, empty Arguments value for one operation id,
// ready to be decoded into. It takes the id because several operations share
// one argument struct (the ten structural query operations all ride QueryArgs)
// and the struct has to know which one it is.
type argumentsFactory func(operation string) Arguments

// OperationHandler is the surfaces-side view of an ENGINE-SIDE operation
// handler (SW-255 / AX-15): the caller's context and the raw JSON arguments
// in, canonical result bytes out. It is the same shape as
// engine/module.OperationHandler; the composition root converts between the
// two, because this package may not import the module set.
//
// The surfaces learn nothing from it beyond what they already know for a
// legacy adapter — an operation id, request arguments and result bytes. The
// handler's params type, its ports and its serializer stay in engine.
type OperationHandler func(ctx context.Context, arguments json.RawMessage) ([]byte, error)

// OperationHandlerProvider is implemented by a Client that was composed with
// module handlers — client.Direct, when the composition root installs the
// module set's handler table into it. NewExecutorWithCatalog probes for it the
// way the surfaces probe for CapabilityReporter: an optional capability of the
// composed client, discovered on the value it was handed, never fetched from a
// global.
type OperationHandlerProvider interface {
	// OperationHandler returns the handler for one operation id, if the
	// composition contributed one.
	OperationHandler(id string) (OperationHandler, bool)
	// HandledOperations returns the ids that carry a handler, in canonical
	// (sorted) order.
	HandledOperations() []string
}

// Executor invokes catalog operations by name through the legacy Client.
//
// It holds a Client, not a Direct: everything it can do, a surface could already
// do by calling the same method itself. The Executor adds addressing, not
// capability — in particular it adds NO capability check of its own. Whether an
// operation can run on this binding is still decided by Direct's optional-service
// wiring, and the "capability unavailable" sentinels (ErrSavingsUnavailable,
// ErrMemoryUnavailable, ErrAnalysisUnavailable, …) travel back through Execute
// untouched, so a surface renders exactly the error it renders today.
type Executor struct {
	client   Client
	catalog  *opcatalog.Catalog
	adapters map[string]argumentsFactory
	// handlers is the module-handler table (SW-255 / AX-15), taken from the
	// Client when it is an OperationHandlerProvider. Execute prefers an entry
	// here over the legacy adapter for the same id; the adapter stays in
	// adapters regardless (AX-17: nothing transitional is removed until the
	// removal slice).
	handlers map[string]OperationHandler
}

// NewExecutor builds an Executor over the shadow operation catalog.
func NewExecutor(c Client) (*Executor, error) {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		return nil, fmt.Errorf("client: executor needs the operation catalog: %w", err)
	}
	return NewExecutorWithCatalog(c, catalog)
}

// NewExecutorWithCatalog builds an Executor over an explicit catalog.
//
// The catalog must be FROZEN. An executor over a catalog that can still grow
// would be addressing a moving target: an id that resolves now might resolve to
// something else later, which is the mutation-after-composition hazard SW-222's
// freeze exists to close.
func NewExecutorWithCatalog(c Client, catalog *opcatalog.Catalog) (*Executor, error) {
	if c == nil {
		return nil, fmt.Errorf("client: executor requires a client")
	}
	if catalog == nil {
		return nil, fmt.Errorf("client: executor requires an operation catalog")
	}
	if !catalog.Frozen() {
		return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "New", "",
			"%s: refusing an unfrozen operation catalog — operation identity must not change "+
				"under an executor that has already resolved it", executorRegistry)
	}
	adapters, err := legacyAdapters()
	if err != nil {
		return nil, err
	}
	// The catalog is the single source of operation identity: an adapter naming
	// an id the catalog does not declare is a second, contradicting list, and it
	// fails construction rather than shipping.
	for _, id := range sortedAdapterIDs(adapters) {
		if _, ok := catalog.Lookup(id); !ok {
			return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "New", id,
				"%s: legacy adapter %q names an operation the catalog does not declare", executorRegistry, id)
		}
	}
	handlers, err := moduleHandlers(c, catalog)
	if err != nil {
		return nil, err
	}
	return &Executor{client: c, catalog: catalog, adapters: adapters, handlers: handlers}, nil
}

// moduleHandlers copies the module-handler table out of a Client that carries
// one. Like the adapter table it is checked against the catalog: a handler for
// an id the catalog does not declare is a second, contradicting list and fails
// construction. A Client that is not a provider yields an empty table, which is
// the pre-AX-15 executor exactly.
func moduleHandlers(c Client, catalog *opcatalog.Catalog) (map[string]OperationHandler, error) {
	provider, ok := c.(OperationHandlerProvider)
	if !ok {
		return map[string]OperationHandler{}, nil
	}
	ids := provider.HandledOperations()
	handlers := make(map[string]OperationHandler, len(ids))
	for _, id := range ids {
		if _, declared := catalog.Lookup(id); !declared {
			return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "New", id,
				"%s: module handler %q names an operation the catalog does not declare", executorRegistry, id)
		}
		h, present := provider.OperationHandler(id)
		if !present || h == nil {
			return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "New", id,
				"%s: the client lists %q as handled but returns no handler for it", executorRegistry, id)
		}
		handlers[id] = h
	}
	return handlers, nil
}

// Handled returns the canonical-order ids the executor serves through a MODULE
// handler rather than a legacy adapter. It is empty on a Client composed
// without handlers; the shipped composition currently carries compound and
// dead_code.
func (e *Executor) Handled() []string {
	out := make([]string, 0, len(e.handlers))
	for id := range e.handlers {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// handles reports whether Execute can run the operation at all, by either path.
func (e *Executor) handles(operation string) bool {
	if _, ok := e.handlers[operation]; ok {
		return true
	}
	_, ok := e.adapters[operation]
	return ok
}

// Catalog returns every operation spec in canonical (id-sorted) order, served
// straight from the SW-223 catalog. The Executor keeps no second list, so this
// cannot drift from what the catalog declares.
func (e *Executor) Catalog() []opcatalog.OperationSpec { return e.catalog.All() }

// Adapted returns the canonical-order ids that currently have a legacy adapter
// — the subset of Catalog() that Execute can actually run. It is deliberately a
// SUBSET and says so: AX-04 adapts a representative set, and an operation
// without an adapter is rejected loudly by Execute rather than half-served.
func (e *Executor) Adapted() []string { return sortedAdapterIDs(e.adapters) }

// NewRequest is the LEGACY → EXECUTOR adapter: it turns a typed legacy
// argument value into an addressed Request, taking the contract version from
// the catalog so a caller cannot invent one.
//
// Together with Execute (the executor → legacy direction) it closes the
// round trip that AC-3 asks to be proven: NewRequest(args) then Execute is the
// same call, with the same bytes, as invoking the legacy method directly.
func (e *Executor) NewRequest(args Arguments) (Request, error) {
	if args == nil {
		return Request{}, fmt.Errorf("client: executor: nil arguments")
	}
	id := args.Operation()
	version, ok := e.catalog.VersionOf(id)
	if !ok {
		return Request{}, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "NewRequest", id,
			"%s: operation %q is not in the operation catalog", executorRegistry, id)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return Request{}, fmt.Errorf("client: executor: encode %q arguments: %w", id, err)
	}
	return Request{Operation: id, Version: version, Arguments: raw}, nil
}

// Execute is the EXECUTOR → LEGACY adapter: it resolves the request against the
// catalog, decodes the raw arguments into the operation's typed form, calls the
// legacy Client method and returns that method's canonical bytes unchanged.
//
// Three rejections, all typed registry.ErrMissingDependency and none of them a
// silent fallback (AC-5): an operation the catalog does not declare, a contract
// version the catalog does not declare, and an operation with no legacy adapter
// (which is a real state in AX-04 — the catalog holds 56 operations and this
// story adapts a representative subset).
//
// SW-255 (AX-15): when the composition contributed a MODULE HANDLER for the
// operation, Execute hands it the raw arguments and returns its bytes; the
// handler decodes fail-closed into the operation's own params type in engine.
// The legacy adapter for the same id is not consulted — that is what makes the
// kill switch's `active` position a different code path from `legacy` for the
// first time. Every operation without a handler takes the adapter path exactly
// as before.
func (e *Executor) Execute(ctx context.Context, req Request) ([]byte, error) {
	declared, known := e.catalog.VersionOf(req.Operation)
	if !known {
		return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "Execute", req.Operation,
			"%s: operation %q is not in the operation catalog", executorRegistry, req.Operation)
	}
	if !e.catalog.Supports(req.Operation, req.Version) {
		return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "Execute", req.Operation,
			"%s: operation %q has no version %q (the catalog declares version %q)",
			executorRegistry, req.Operation, req.Version, declared)
	}
	if handler, ok := e.handlers[req.Operation]; ok {
		return handler(ctx, req.Arguments)
	}
	factory, ok := e.adapters[req.Operation]
	if !ok {
		return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "Execute", req.Operation,
			"%s: operation %q has no legacy adapter", executorRegistry, req.Operation)
	}
	args, err := decodeArguments(factory, req)
	if err != nil {
		return nil, err
	}
	return args.invoke(ctx, e.client)
}

// DecodeArguments is the halfway point of the executor → legacy direction: it
// resolves a Request to the typed legacy arguments Execute would call with,
// WITHOUT calling anything. It exists so the bidirectional round trip is
// observable — a test (and, later, a surface's argument validation) can check
// that a Request decodes to exactly the arguments it was built from.
func (e *Executor) DecodeArguments(req Request) (Arguments, error) {
	if !e.catalog.Supports(req.Operation, req.Version) {
		return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "DecodeArguments", req.Operation,
			"%s: operation %q at version %q is not in the operation catalog",
			executorRegistry, req.Operation, req.Version)
	}
	factory, ok := e.adapters[req.Operation]
	if !ok {
		return nil, registry.Errorf(registry.ErrMissingDependency, executorRegistry, "DecodeArguments", req.Operation,
			"%s: operation %q has no legacy adapter", executorRegistry, req.Operation)
	}
	return decodeArguments(factory, req)
}

// decodeArguments builds the typed argument value and fills it from the raw
// JSON. Unknown fields are REFUSED rather than ignored, so a misspelled or
// removed argument name fails closed.
func decodeArguments(factory argumentsFactory, req Request) (Arguments, error) {
	args := factory(req.Operation)
	if len(bytes.TrimSpace(req.Arguments)) == 0 {
		return args, nil
	}
	dec := json.NewDecoder(bytes.NewReader(req.Arguments))
	dec.DisallowUnknownFields()
	if err := dec.Decode(args); err != nil {
		return nil, fmt.Errorf("client: executor: decode %q arguments: %w", req.Operation, err)
	}
	return args, nil
}
