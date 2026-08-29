package deadcode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// SW-255 (AX-15): the engine-side handler.
//
// The handler is the FIRST operation handler that lives in engine rather than
// behind a surfaces/client adapter. These tests pin the two things AC-2 asks of
// it: it decodes the wire arguments fail-closed into the operation's own
// params type, and it ends in the same Assemble + contract.Serialize pair the
// legacy Direct method ends in — no second serializer, no second default.

// TestHandler_MatchesAssembleSerialize is the in-package half of the parity
// evidence: for a real and an empty graph, the handler's bytes are exactly
// Serialize(Assemble(...)) for the same arguments.
func TestHandler_MatchesAssembleSerialize(t *testing.T) {
	deps := fixtureDeps(t)
	handler := Handler(deps.Query, deps.Search)

	for _, tc := range []struct {
		name string
		raw  string
		want Params
	}{
		{"capped", `{"max_items":2}`, Params{Deps: deps, MaxItems: 2}},
		{"zero selects the engine default", `{"max_items":0}`, Params{Deps: deps}},
		{"empty arguments", ``, Params{Deps: deps}},
		{"whitespace arguments", "  \n", Params{Deps: deps}},
		{"negative is passed through verbatim", `{"max_items":-4}`, Params{Deps: deps, MaxItems: -4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Assemble(context.Background(), tc.want)
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			want, err := contract.Serialize(res)
			if err != nil {
				t.Fatalf("Serialize: %v", err)
			}
			got, err := handler(context.Background(), json.RawMessage(tc.raw))
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("handler bytes differ from Serialize(Assemble)\n  handler: %s\n  direct:  %s", got, want)
			}
			if len(got) == 0 {
				t.Fatal("the case produced no bytes and proves nothing")
			}
		})
	}
}

// TestHandler_RefusesUnknownFields is AC-2's DisallowUnknownFields clause: a
// misspelled or superseded argument name fails instead of being dropped.
func TestHandler_RefusesUnknownFields(t *testing.T) {
	deps := fixtureDeps(t)
	handler := Handler(deps.Query, deps.Search)
	_, err := handler(context.Background(), json.RawMessage(`{"max_items":2,"limit":3}`))
	if err == nil {
		t.Fatal("an unknown argument field was accepted")
	}
	if !strings.Contains(err.Error(), `"limit"`) || !strings.Contains(err.Error(), Operation) {
		t.Fatalf("the rejection names neither the field nor the operation: %v", err)
	}
	if _, err := handler(context.Background(), json.RawMessage(`{"max_items":"two"}`)); err == nil {
		t.Fatal("a mistyped argument was accepted")
	}
}

// TestHandler_UnavailableAndEmptyOutcomesTravel pins the two typed outcomes
// the AX-06 canary already asserts on the legacy path: an absent graph port is
// the typed `unavailable` envelope (not an error), and an empty graph is the
// typed `empty` envelope.
func TestHandler_UnavailableAndEmptyOutcomesTravel(t *testing.T) {
	decode := func(t *testing.T, raw []byte) contract.Result {
		t.Helper()
		var res contract.Result
		if err := json.Unmarshal(raw, &res); err != nil {
			t.Fatalf("handler bytes are not a contract result: %v (%s)", err, raw)
		}
		return res
	}

	unavailable := Handler(nil, nil)
	raw, err := unavailable(context.Background(), nil)
	if err != nil {
		t.Fatalf("nil ports must degrade to the typed unavailable envelope, got error %v", err)
	}
	if got := decode(t, raw).Outcome; got != contract.OutcomeUnavailable {
		t.Fatalf("outcome = %s, want %s", got, contract.OutcomeUnavailable)
	}

	store := graphstore.NewMemStore()
	empty := Handler(query.New(store), search.New(store))
	raw, err = empty(context.Background(), json.RawMessage(`{"max_items":5}`))
	if err != nil {
		t.Fatalf("empty graph: %v", err)
	}
	if got := decode(t, raw).Outcome; got != contract.OutcomeEmpty {
		t.Fatalf("outcome = %s, want %s", got, contract.OutcomeEmpty)
	}
}

// TestHandler_CancelledContextIsTheSameErrorAsAssemble: the handler forwards
// the caller's context untouched, so a cancellation surfaces exactly as the
// engine reports it — same class, same text — rather than being wrapped.
func TestHandler_CancelledContextIsTheSameErrorAsAssemble(t *testing.T) {
	deps := fixtureDeps(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, wantErr := Assemble(ctx, Params{Deps: deps, MaxItems: 3})
	_, gotErr := Handler(deps.Query, deps.Search)(ctx, json.RawMessage(`{"max_items":3}`))
	switch {
	case wantErr == nil && gotErr == nil:
		// The fixture store does not observe cancellation; both paths agree on
		// success, which is still parity. Say so rather than pass silently.
		t.Log("the memstore does not observe context cancellation; both paths succeeded identically")
	case wantErr == nil || gotErr == nil:
		t.Fatalf("cancellation parity broken: Assemble=%v handler=%v", wantErr, gotErr)
	default:
		if gotErr.Error() != wantErr.Error() || errors.Is(gotErr, context.Canceled) != errors.Is(wantErr, context.Canceled) {
			t.Fatalf("cancellation error differs: Assemble=%v handler=%v", wantErr, gotErr)
		}
	}
}

// TestArgs_IsTheOperationsOwnParamsType pins the wire shape the handler decodes
// into: exactly one field, max_items, matching what the three surfaces send
// today (surfaces/client.DeadCodeArgs carries the same single field — the
// cross-package equivalence is asserted in surfaces/client).
func TestArgs_IsTheOperationsOwnParamsType(t *testing.T) {
	raw, err := json.Marshal(Args{MaxItems: 7})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"max_items":7}` {
		t.Fatalf("Args wire shape = %s, want {\"max_items\":7}", raw)
	}
}
