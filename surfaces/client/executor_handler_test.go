package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/agenttools/deadcode"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// SW-255 (AX-15): the executor prefers a module handler when the composition
// has one, and the parity evidence for that operation is no longer tautological.
//
// Before this story every executor path was a legacy adapter calling the same
// Client method the legacy path calls, so "executor bytes == legacy bytes" was
// a function compared with itself. Here the executor path for dead_code is
// engine/agenttools/deadcode.Handler — a different code path that shares only
// the engine's Assemble + Serialize pair with Direct.DeadCode — and the tests
// below compare those two DIFFERENT paths byte for byte, error for error.

// handlerFor binds the real engine handler to the services a Direct was
// composed over, so the two paths read the same graph and differ only in the
// code between the request and the engine call.
func handlerFor(d *Direct) map[string]OperationHandler {
	return map[string]OperationHandler{
		deadcode.Operation: OperationHandler(deadcode.Handler(d.querySvc, d.searchSvc)),
	}
}

// markingClient wraps a Client so that the legacy DeadCode method can be
// observed — and, when asked, made to answer with a marker that no engine
// handler could produce. It carries the handlers of the Direct it wraps.
type markingClient struct {
	Client
	direct      *Direct
	legacyCalls atomic.Int32
	handlerHits atomic.Int32
	poison      bool
}

var errPoisonedLegacy = errors.New("the legacy adapter ran where the module handler should have")

func (m *markingClient) DeadCode(ctx context.Context, p DeadCodeParams) ([]byte, error) {
	m.legacyCalls.Add(1)
	if m.poison {
		return nil, errPoisonedLegacy
	}
	return m.direct.DeadCode(ctx, p)
}

func (m *markingClient) OperationHandler(id string) (OperationHandler, bool) {
	h, ok := handlerFor(m.direct)[id]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, raw json.RawMessage) ([]byte, error) {
		m.handlerHits.Add(1)
		return h(ctx, raw)
	}, true
}

func (m *markingClient) HandledOperations() []string { return []string{deadcode.Operation} }

// AC-6: a request for an operation with a module handler goes to the handler
// and NOT to the legacy adapter. The legacy method is poisoned, so if the
// adapter ran the test could not pass by coincidence.
func TestAX15_ExecutorDispatchesToTheModuleHandlerNotTheLegacyAdapter(t *testing.T) {
	direct, _ := executorParityFixture(t)
	want, err := direct.DeadCode(context.Background(), DeadCodeParams{MaxItems: 3})
	if err != nil {
		t.Fatalf("legacy baseline: %v", err)
	}

	poisoned := &markingClient{Client: direct, direct: direct, poison: true}
	executor, err := NewExecutor(poisoned)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	if got := executor.Handled(); !reflect.DeepEqual(got, []string{deadcode.Operation}) {
		t.Fatalf("Handled() = %v, want [%s]", got, deadcode.Operation)
	}
	req, err := executor.NewRequest(&DeadCodeArgs{MaxItems: 3})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute went to the legacy adapter (or failed): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("handler bytes differ from the legacy method's\n  handler: %s\n  legacy:  %s", got, want)
	}
	if poisoned.legacyCalls.Load() != 0 {
		t.Fatalf("the legacy adapter ran %d time(s) for an operation with a module handler", poisoned.legacyCalls.Load())
	}
	if poisoned.handlerHits.Load() != 1 {
		t.Fatalf("the module handler ran %d time(s), want exactly 1", poisoned.handlerHits.Load())
	}

	// Every OTHER adapted operation still resolves to its legacy adapter: the
	// adapter table is byte-for-byte what it was, and the handler table holds
	// exactly one entry.
	plain, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor(plain): %v", err)
	}
	if len(plain.Handled()) != 0 {
		t.Fatalf("a Direct without handlers reports Handled() = %v", plain.Handled())
	}
	if !reflect.DeepEqual(executor.Adapted(), plain.Adapted()) {
		t.Fatalf("the adapter table moved:\n  with handlers: %v\n  without:       %v", executor.Adapted(), plain.Adapted())
	}
	if _, stillAdapted := executor.adapters[deadcode.Operation]; !stillAdapted {
		t.Fatal("the legacy adapter for dead_code was removed; AX-17 keeps every transitional path until the removal slice")
	}
}

// AC-7: byte parity between the LEGACY method (Direct.DeadCode, typed) and the
// MODULE HANDLER (through the executor), over the parity fixtures and the
// failure classes — identical bytes, identical error strings, identical
// sentinel classes.
func TestAX15_HandlerByteParityWithLegacy(t *testing.T) {
	populated, _ := executorParityFixture(t)
	emptyStore := graphstore.NewMemStore()
	unindexed := NewDirect(query.New(emptyStore), search.New(emptyStore))
	// Unwired graph services: the shape a Direct has on a store-less attach.
	unavailable := NewDirect(nil, nil)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name   string
		direct *Direct
		ctx    context.Context
		args   DeadCodeArgs
	}{
		{"populated graph, capped", populated, context.Background(), DeadCodeArgs{MaxItems: 3}},
		{"populated graph, engine default", populated, context.Background(), DeadCodeArgs{}},
		{"populated graph, negative cap passes through", populated, context.Background(), DeadCodeArgs{MaxItems: -2}},
		{"populated graph, cap above the row limit", populated, context.Background(), DeadCodeArgs{MaxItems: 10_000}},
		{"unindexed store", unindexed, context.Background(), DeadCodeArgs{MaxItems: 5}},
		{"unavailable graph services", unavailable, context.Background(), DeadCodeArgs{MaxItems: 5}},
		{"cancelled context", populated, cancelled, DeadCodeArgs{MaxItems: 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Legacy: the typed method call a surface makes today.
			wantBytes, wantErr := tc.direct.DeadCode(tc.ctx, DeadCodeParams{MaxItems: tc.args.MaxItems})

			// Module handler: through the executor, with the legacy method
			// poisoned so the comparison cannot be a function against itself.
			poisoned := &markingClient{Client: tc.direct, direct: tc.direct, poison: true}
			executor, err := NewExecutor(poisoned)
			if err != nil {
				t.Fatalf("NewExecutor: %v", err)
			}
			req, err := executor.NewRequest(&tc.args)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			gotBytes, gotErr := executor.Execute(tc.ctx, req)
			if poisoned.legacyCalls.Load() != 0 {
				t.Fatal("the legacy adapter ran; this comparison would be tautological")
			}
			if errors.Is(gotErr, errPoisonedLegacy) {
				t.Fatal("the executor answered from the poisoned legacy method")
			}
			assertSameOutcome(t, "module handler", wantBytes, wantErr, gotBytes, gotErr)
			// The typed sentinel classes travel identically (errors.Is), whether
			// or not this case produces one.
			for _, sentinel := range canaryComparableSentinels {
				if errors.Is(wantErr, sentinel) != errors.Is(gotErr, sentinel) {
					t.Fatalf("sentinel %v: legacy %t, handler %t", sentinel, errors.Is(wantErr, sentinel), errors.Is(gotErr, sentinel))
				}
			}
		})
	}
}

// AC-7, the "bad arguments" class above the typed boundary. Direct.DeadCode
// takes a Go struct and cannot receive malformed JSON, so this class exists
// only on the executor: the legacy ADAPTER path (an executor without handlers)
// and the module handler path must both refuse an unknown field, name it, and
// carry no capability sentinel. The two messages share the decoder's own text
// and differ in prefix — `client: executor: decode "dead_code" arguments:`
// versus `dead_code: decode arguments:` — because the engine does not
// impersonate a surface; the shared suffix is asserted, the prefixes are
// pinned so a change to either is a visible diff.
//
// One further, unavoidable difference is normalised rather than hidden:
// encoding/json names the Go TYPE it failed to fill ("Go struct field
// DeadCodeArgs.max_items" / "Go value of type client.DeadCodeArgs" on the
// adapter path, "Args.max_items" / "deadcode.Args" on the handler path). The
// two params types are pinned field-for-field equal below
// (TestAX15_HandlerParamsTypeMatchesTheAdapterArguments); their NAMES differ by
// construction, so the comparison replaces both with one placeholder and
// asserts everything else — the failure class, the field, the wire type.
func TestAX15_BadArgumentsAreRefusedOnBothExecutorPaths(t *testing.T) {
	normaliseParamsType := func(s string) string {
		r := strings.NewReplacer("client.DeadCodeArgs", "<params>", "deadcode.Args", "<params>",
			"DeadCodeArgs.", "<params>.", "Args.", "<params>.")
		return r.Replace(s)
	}
	direct, _ := executorParityFixture(t)
	legacyPath, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor(legacy adapters): %v", err)
	}
	handlerPath, err := NewExecutor(&markingClient{Client: direct, direct: direct, poison: true})
	if err != nil {
		t.Fatalf("NewExecutor(module handler): %v", err)
	}
	version, _ := legacyPath.catalog.VersionOf(deadcode.Operation)

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"unknown field", `{"max_items":2,"limit":3}`},
		{"mistyped field", `{"max_items":"two"}`},
		{"not an object", `[1,2,3]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := Request{Operation: deadcode.Operation, Version: version, Arguments: json.RawMessage(tc.raw)}
			_, legacyErr := legacyPath.Execute(context.Background(), req)
			_, handlerErr := handlerPath.Execute(context.Background(), req)
			if legacyErr == nil || handlerErr == nil {
				t.Fatalf("bad arguments accepted: legacy=%v handler=%v", legacyErr, handlerErr)
			}
			const legacyPrefix = `client: executor: decode "dead_code" arguments: `
			const handlerPrefix = `dead_code: decode arguments: `
			if !strings.HasPrefix(legacyErr.Error(), legacyPrefix) {
				t.Fatalf("legacy adapter message %q lost its prefix", legacyErr)
			}
			if !strings.HasPrefix(handlerErr.Error(), handlerPrefix) {
				t.Fatalf("module handler message %q lost its prefix", handlerErr)
			}
			a := normaliseParamsType(strings.TrimPrefix(legacyErr.Error(), legacyPrefix))
			b := normaliseParamsType(strings.TrimPrefix(handlerErr.Error(), handlerPrefix))
			if a != b {
				t.Fatalf("the decoder text differs between the paths\n  legacy adapter: %q\n  module handler: %q", a, b)
			}
			if !strings.Contains(a, "<params>") && tc.name != "unknown field" {
				t.Fatalf("the normalisation matched nothing in %q; the decoder message changed shape", a)
			}
			for _, sentinel := range canaryComparableSentinels {
				if errors.Is(legacyErr, sentinel) || errors.Is(handlerErr, sentinel) {
					t.Fatalf("a decode failure carried capability sentinel %v", sentinel)
				}
			}
		})
	}
}

// AC-9: the kill switch is retained and, for the first time, its positions are
// not the same code. `legacy` answers from Direct.DeadCode and never invokes
// the handler; `active` answers from the handler and never invokes the legacy
// method; `shadow` answers from legacy, runs the handler behind it, and records
// zero mismatches because the two paths agree.
func TestAX15_KillSwitchPositionsSelectDifferentCode(t *testing.T) {
	withCleanCanaryRecorder(t)
	direct, _ := executorParityFixture(t)
	want, err := direct.DeadCode(context.Background(), DeadCodeParams{MaxItems: 3})
	if err != nil {
		t.Fatalf("legacy baseline: %v", err)
	}

	for _, tc := range []struct {
		mode        CanaryMode
		legacyCalls int32
		handlerHits int32
	}{
		{CanaryModeLegacy, 1, 0},
		{CanaryModeShadow, 1, 1},
		{CanaryModeActive, 0, 1},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			withCanaryMode(t, tc.mode)
			c := &markingClient{Client: direct, direct: direct}
			got, err := DispatchOperation(context.Background(), c, &DeadCodeArgs{MaxItems: 3})
			if err != nil {
				t.Fatalf("DispatchOperation: %v", err)
			}
			if err := DrainCanaryShadow(context.Background()); err != nil {
				t.Fatalf("DrainCanaryShadow: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%q returned bytes that differ from the legacy method's\n  got:  %s\n  want: %s", tc.mode, got, want)
			}
			if c.legacyCalls.Load() != tc.legacyCalls {
				t.Errorf("%q ran the legacy method %d time(s), want %d", tc.mode, c.legacyCalls.Load(), tc.legacyCalls)
			}
			if c.handlerHits.Load() != tc.handlerHits {
				t.Errorf("%q ran the module handler %d time(s), want %d", tc.mode, c.handlerHits.Load(), tc.handlerHits)
			}
		})
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("shadow recorded %d mismatch(es) between legacy and the module handler: %s", count, last)
	}
}

// AC-9, non-vacuity: shadow now compares legacy against the HANDLER, so a
// handler that disagrees is recorded. Without this the previous test could pass
// with a comparison that never looks at the handler's bytes.
func TestAX15_ShadowRecordsAHandlerThatDisagrees(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)
	direct, _ := executorParityFixture(t)
	c := &disagreeingClient{Client: direct, direct: direct}
	got, err := DispatchOperation(context.Background(), c, &DeadCodeArgs{MaxItems: 3})
	if err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	want, _ := direct.DeadCode(context.Background(), DeadCodeParams{MaxItems: 3})
	if !bytes.Equal(got, want) {
		t.Fatal("shadow did not return the legacy bytes")
	}
	count, last := CanaryMismatches()
	if count != 1 || last.Kind != "bytes" || last.Operation != deadcode.Operation {
		t.Fatalf("a disagreeing handler was not recorded as a byte mismatch: count=%d last=%s", count, last)
	}
}

// disagreeingClient carries a handler whose bytes differ from legacy by one
// trailing byte.
type disagreeingClient struct {
	Client
	direct *Direct
}

func (d *disagreeingClient) OperationHandler(id string) (OperationHandler, bool) {
	h, ok := handlerFor(d.direct)[id]
	if !ok {
		return nil, false
	}
	return func(ctx context.Context, raw json.RawMessage) ([]byte, error) {
		out, err := h(ctx, raw)
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}, true
}

func (d *disagreeingClient) HandledOperations() []string { return []string{deadcode.Operation} }

// A handler table naming an operation the catalog does not declare fails
// construction, exactly as an undeclared legacy adapter does: the catalog is
// the single source of operation identity on both paths.
func TestAX15_ExecutorRefusesAHandlerTheCatalogDoesNotDeclare(t *testing.T) {
	direct, _ := executorParityFixture(t)
	_, err := NewExecutor(&undeclaredHandlerClient{Client: direct})
	if err == nil || !strings.Contains(err.Error(), "no_such_operation") {
		t.Fatalf("an undeclared handler was accepted: %v", err)
	}
}

type undeclaredHandlerClient struct{ Client }

func (u *undeclaredHandlerClient) OperationHandler(string) (OperationHandler, bool) {
	return func(context.Context, json.RawMessage) ([]byte, error) { return []byte("{}"), nil }, true
}
func (u *undeclaredHandlerClient) HandledOperations() []string { return []string{"no_such_operation"} }

// Direct's handler table is immutable once installed and reachable only by
// lookup; the map handed in is copied, so the composition root cannot re-arm
// the client after the fact.
func TestAX15_DirectHandlerTableIsCopiedAndLookupOnly(t *testing.T) {
	direct, _ := executorParityFixture(t)
	table := handlerFor(direct)
	direct = direct.WithOperationHandlers(table)
	delete(table, deadcode.Operation)
	if _, ok := direct.OperationHandler(deadcode.Operation); !ok {
		t.Fatal("mutating the caller's map reached the client's handler table")
	}
	if got := direct.HandledOperations(); !reflect.DeepEqual(got, []string{deadcode.Operation}) {
		t.Fatalf("HandledOperations() = %v", got)
	}
	if _, ok := direct.OperationHandler("framework_map"); ok {
		t.Fatal("a handler was reported for an operation nobody contributed")
	}
	if got := NewDirect(nil, nil).HandledOperations(); len(got) != 0 {
		t.Fatalf("a fresh Direct reports handlers: %v", got)
	}
}

// AC-7: the argument-fidelity walk (executor_argument_fidelity_test.go)
// reflects over DeadCodeArgs. The handler decodes into deadcode.Args instead.
// The walk covers the handler's params type only if the two shapes are the
// same field list with the same wire names — pinned here so the two cannot
// drift apart without this failing.
func TestAX15_HandlerParamsTypeMatchesTheAdapterArguments(t *testing.T) {
	shape := func(v any) []string {
		rt := reflect.TypeOf(v)
		out := make([]string, 0, rt.NumField())
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			out = append(out, f.Name+" "+f.Type.String()+" `"+string(f.Tag)+"`")
		}
		return out
	}
	if got, want := shape(deadcode.Args{}), shape(DeadCodeArgs{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("the handler's params type and the adapter's arguments diverged\n  deadcode.Args: %v\n  DeadCodeArgs:  %v", got, want)
	}
	if fields := shape(DeadCodeArgs{}); len(fields) == 0 {
		t.Fatal("DeadCodeArgs has no fields; the equivalence is vacuous")
	}
}
