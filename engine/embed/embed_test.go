package embed_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// AC-C: the ZERO Registry is the graceful-skip state.
func TestRegistry_ZeroValue_GracefulSkip(t *testing.T) {
	var r embed.Registry // zero value
	if r.Configured() {
		t.Fatal("zero Registry.Configured() = true, want false")
	}
	if e, ok := r.Active(); ok || e != nil {
		t.Fatalf("zero Registry.Active() = (%v, %v), want (nil, false)", e, ok)
	}
}

// The DEFAULT registration registers NOTHING (semantic search OFF by default).
func TestRegisterDefaults_RegistersNothing(t *testing.T) {
	r := embed.NewDefaultRegistry()
	if r.Configured() {
		t.Fatal("default registry Configured() = true, want false (default ships no embedder)")
	}
	if ids := r.IDs(); len(ids) != 0 {
		t.Fatalf("default registry IDs = %v, want empty", ids)
	}
}

// Registering an embedder flips Configured() and makes it Active.
func TestRegistry_RegisterActivates(t *testing.T) {
	r := embed.NewRegistry()
	r.Register(embed.NewMockEmbedder(8))
	if !r.Configured() {
		t.Fatal("Configured() = false after Register, want true")
	}
	e, ok := r.Active()
	if !ok || e == nil {
		t.Fatalf("Active() = (%v, %v), want a non-nil embedder", e, ok)
	}
	if e.ID() != "mock" {
		t.Fatalf("Active().ID() = %q, want mock", e.ID())
	}
}

// Register ignores nil / empty-ID embedders (no-op, registry stays consistent).
func TestRegistry_RegisterNoOp(t *testing.T) {
	r := embed.NewRegistry()
	r.Register(nil)
	if r.Configured() {
		t.Fatal("Register(nil) configured the registry")
	}
}

// AC-D (determinism half): identical input ⇒ value-identical vectors.
func TestMockEmbedder_Deterministic(t *testing.T) {
	m := embed.NewMockEmbedder(16)
	if m.Dim() != 16 {
		t.Fatalf("Dim() = %d, want 16", m.Dim())
	}
	in := []string{"pkg/foo.Bar", "pkg/baz.Qux"}
	v1, err := m.Embed(context.Background(), in)
	if err != nil {
		t.Fatalf("Embed 1: %v", err)
	}
	v2, err := m.Embed(context.Background(), in)
	if err != nil {
		t.Fatalf("Embed 2: %v", err)
	}
	if len(v1) != 2 || len(v2) != 2 {
		t.Fatalf("len = %d/%d, want 2/2", len(v1), len(v2))
	}
	for i := range v1 {
		if len(v1[i]) != 16 {
			t.Fatalf("vector %d dim = %d, want 16", i, len(v1[i]))
		}
		for j := range v1[i] {
			if v1[i][j] != v2[i][j] {
				t.Fatalf("non-deterministic vector at [%d][%d]: %v vs %v", i, j, v1[i][j], v2[i][j])
			}
		}
	}
	// Distinct inputs ⇒ distinct vectors (not all-equal).
	same := true
	for j := range v1[0] {
		if v1[0][j] != v1[1][j] {
			same = false
			break
		}
	}
	if same {
		t.Fatal("distinct inputs produced identical vectors")
	}
}

// AC-H complement (registration layer): the default registry contains no CGO
// embedder, and the guard rejects a planted CGO embedder (anti-vacuity).
func TestAssertNoCgoEmbedder(t *testing.T) {
	// Positive: default registry has no CGO embedder.
	if off := embed.AssertNoCgoEmbedder(embed.NewDefaultRegistry()); len(off) != 0 {
		t.Fatalf("default registry has CGO embedder(s): %v", off)
	}
	// A pure-Go mock is not flagged.
	pure := embed.NewRegistry()
	pure.Register(embed.NewMockEmbedder(8))
	if off := embed.AssertNoCgoEmbedder(pure); len(off) != 0 {
		t.Fatalf("pure-Go mock flagged as CGO: %v", off)
	}
	// Negative: a planted CGO-marked embedder IS rejected (same code path).
	cgo := embed.NewRegistry()
	cgo.Register(fakeCgoEmbedder{})
	off := embed.AssertNoCgoEmbedder(cgo)
	if len(off) != 1 {
		t.Fatalf("planted CGO embedder not rejected: offenders=%v", off)
	}
	if msg := embed.FormatCgoEmbedderFailure(off); msg == "" {
		t.Fatal("FormatCgoEmbedderFailure returned empty for a real offender")
	}
}

// fakeCgoEmbedder implements embed.CgoEmbedder to exercise the negative guard.
type fakeCgoEmbedder struct{}

func (fakeCgoEmbedder) ID() string { return "fake-cgo" }
func (fakeCgoEmbedder) Dim() int   { return 4 }
func (fakeCgoEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (fakeCgoEmbedder) IsCgoEmbedder() {}

// Constructor: empty/unknown selector ⇒ graceful-skip (nil, nil), no error.
func TestConstructor_GracefulSkip(t *testing.T) {
	for _, sel := range []string{"", "   ", "totally-unknown", "unknown:arg"} {
		e, err := embed.Constructor(sel, embed.DefaultConstructors())
		if err != nil {
			t.Fatalf("Constructor(%q) error = %v, want nil (graceful skip)", sel, err)
		}
		if e != nil {
			t.Fatalf("Constructor(%q) = %v, want nil (graceful skip)", sel, e)
		}
	}
}
