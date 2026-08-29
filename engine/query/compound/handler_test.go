package compound_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/query/compound"
)

func TestHandlerMatchesTheExistingEnginePath(t *testing.T) {
	store, _ := seedGraph(t)
	text := "SEED pkg.A\nHOP out calls\n"
	parsed, err := compound.Parse(text)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	result, err := compound.Execute(context.Background(), store, parsed)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want, err := query.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	got, err := compound.Handler(store)(context.Background(), json.RawMessage(`{"query":"SEED pkg.A\nHOP out calls\n"}`))
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("handler bytes differ from the existing engine path\n  handler: %s\n  engine:  %s", got, want)
	}
}

func TestHandlerPreservesTypedParseErrors(t *testing.T) {
	store, _ := seedGraph(t)
	_, err := compound.Handler(store)(context.Background(), json.RawMessage(`{"query":""}`))
	if !errors.Is(err, compound.ErrInvalidQuery) {
		t.Fatalf("Handler invalid query = %v, want ErrInvalidQuery", err)
	}
}

func TestHandlerRejectsUnknownArguments(t *testing.T) {
	store, _ := seedGraph(t)
	_, err := compound.Handler(store)(context.Background(), json.RawMessage(`{"query":"SEED pkg.A\nHOP out calls\n","limit":1}`))
	if err == nil || !strings.Contains(err.Error(), `unknown field "limit"`) {
		t.Fatalf("Handler accepted an unknown argument: %v", err)
	}
}
