package embed_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// SW-222 (AX-02) characterization baseline for the EMBEDDER registry.
//
// Written and made green BEFORE the registry-lifecycle refactor; must pass
// UNCHANGED after it. Two properties are load-bearing for the CGo-free,
// zero-egress default and must survive AX-02:
//   - the ZERO Registry is the graceful-skip state, and
//   - the DEFAULT registry registers NOTHING, so semantic search is off until
//     an embedder is explicitly opted in.
//
// The collision policy is LAST-WINS, and a registration also moves the ACTIVE
// selection — the property `engine/search` keys its semantic path on.

type charEmbedder struct{ id string }

func (e charEmbedder) ID() string { return e.id }
func (e charEmbedder) Dim() int   { return 3 }
func (e charEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0, 0, 0}
	}
	return out, nil
}

// TestBaseline_EmbedRegistry_ZeroValueIsGracefulSkip pins the zero-value
// contract: no embedder, not configured, nothing constructed, nothing dialed.
func TestBaseline_EmbedRegistry_ZeroValueIsGracefulSkip(t *testing.T) {
	var zero embed.Registry
	if zero.Configured() {
		t.Fatal("zero Registry must report Configured() == false")
	}
	if e, ok := zero.Active(); ok || e != nil {
		t.Fatalf("zero Registry Active() = (%v, %v), want (nil, false)", e, ok)
	}
	if ids := zero.IDs(); len(ids) != 0 {
		t.Fatalf("zero Registry IDs() = %v, want empty", ids)
	}
}

// TestBaseline_EmbedRegistry_DefaultRegistersNothing pins the CGo-free,
// zero-egress default: RegisterDefaults registers no embedder at all.
func TestBaseline_EmbedRegistry_DefaultRegistersNothing(t *testing.T) {
	r := embed.NewDefaultRegistry()
	if ids := r.IDs(); len(ids) != 0 {
		t.Fatalf("default embed registry = %v, want empty (semantic search off by default)", ids)
	}
	if r.Configured() {
		t.Fatal("default embed registry must not be Configured()")
	}
}

// TestBaseline_EmbedRegistry_LastWinsAndMovesActive pins the collision policy
// and the active-selection side effect.
func TestBaseline_EmbedRegistry_LastWinsAndMovesActive(t *testing.T) {
	r := embed.NewRegistry()
	r.Register(charEmbedder{id: "one"})
	r.Register(charEmbedder{id: "TWO"})

	if got := strings.Join(r.IDs(), ","); got != "one,two" {
		t.Fatalf("IDs() = %q, want lowercased sorted %q", got, "one,two")
	}
	e, ok := r.Active()
	if !ok || e.ID() != "TWO" {
		t.Fatalf("Active() = (%v, %v), want the LAST registration", e, ok)
	}

	// Re-registering an existing id supersedes it and stays active.
	r.Register(charEmbedder{id: "one"})
	e, ok = r.Active()
	if !ok || e.ID() != "one" {
		t.Fatalf("Active() after re-registration = (%v, %v), want %q", e, ok, "one")
	}
	if got := strings.Join(r.IDs(), ","); got != "one,two" {
		t.Fatalf("re-registration changed the id set: %q", got)
	}
}

// TestBaseline_EmbedRegistry_NilAndEmptyIDAreNoOps pins the two documented
// no-op paths.
func TestBaseline_EmbedRegistry_NilAndEmptyIDAreNoOps(t *testing.T) {
	r := embed.NewRegistry()
	r.Register(nil)
	r.Register(charEmbedder{id: "   "})
	if ids := r.IDs(); len(ids) != 0 {
		t.Fatalf("IDs() after no-op registrations = %v, want empty", ids)
	}
	if r.Configured() {
		t.Fatal("no-op registrations must not configure the registry")
	}
}
