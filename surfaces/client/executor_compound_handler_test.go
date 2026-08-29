package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/engine/query/compound"
)

// compoundMarkingClient makes the legacy adapter observable while exposing the
// real engine handler over the same reader. A poisoned legacy call proves that
// Executor.Execute selected the handler rather than merely producing equal
// bytes through the adapter.
type compoundMarkingClient struct {
	Client
	direct      *Direct
	legacyCalls atomic.Int32
	handlerHits atomic.Int32
}

func (m *compoundMarkingClient) Compound(context.Context, string) ([]byte, error) {
	m.legacyCalls.Add(1)
	return nil, errPoisonedLegacy
}

func (m *compoundMarkingClient) OperationHandler(id string) (OperationHandler, bool) {
	if id != compound.Operation {
		return nil, false
	}
	h := compound.Handler(m.direct.querySvc.Reader())
	return func(ctx context.Context, raw json.RawMessage) ([]byte, error) {
		m.handlerHits.Add(1)
		return h(ctx, raw)
	}, true
}

func (m *compoundMarkingClient) HandledOperations() []string {
	return []string{compound.Operation}
}

func TestCompoundExecutorDispatchesToTheModuleHandler(t *testing.T) {
	direct, _ := executorParityFixture(t)
	const queryText = "SEED p.A\nHOP out calls\n"
	want, err := direct.Compound(context.Background(), queryText)
	if err != nil {
		t.Fatalf("legacy baseline: %v", err)
	}

	marked := &compoundMarkingClient{Client: direct, direct: direct}
	executor, err := NewExecutor(marked)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	req, err := executor.NewRequest(&CompoundArgs{Query: queryText})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	got, err := executor.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("Execute selected the legacy adapter or failed: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("compound handler bytes differ\n  handler: %s\n  legacy:  %s", got, want)
	}
	if marked.legacyCalls.Load() != 0 || marked.handlerHits.Load() != 1 {
		t.Fatalf("calls: legacy=%d handler=%d, want 0/1", marked.legacyCalls.Load(), marked.handlerHits.Load())
	}
	if _, stillAdapted := executor.adapters[compound.Operation]; !stillAdapted {
		t.Fatal("the transitional compound adapter was removed before the AX-17 gate")
	}
}

func TestCompoundHandlerErrorParityAndArgumentShape(t *testing.T) {
	direct, _ := executorParityFixture(t)
	handler := compound.Handler(direct.querySvc.Reader())

	_, legacyErr := direct.Compound(context.Background(), "")
	_, handlerErr := handler(context.Background(), json.RawMessage(`{"query":""}`))
	if !errors.Is(legacyErr, compound.ErrInvalidQuery) || !errors.Is(handlerErr, compound.ErrInvalidQuery) {
		t.Fatalf("invalid query sentinel differs: legacy=%v handler=%v", legacyErr, handlerErr)
	}
	if legacyErr.Error() != handlerErr.Error() {
		t.Fatalf("invalid query text differs: legacy=%q handler=%q", legacyErr, handlerErr)
	}

	shape := func(v any) []string {
		rt := reflect.TypeOf(v)
		out := make([]string, 0, rt.NumField())
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			out = append(out, f.Name+" "+f.Type.String()+" `"+string(f.Tag)+"`")
		}
		return out
	}
	if got, want := shape(compound.Args{}), shape(CompoundArgs{}); !reflect.DeepEqual(got, want) {
		t.Fatalf("handler and adapter argument shapes differ\n  handler: %v\n  adapter: %v", got, want)
	}
}
