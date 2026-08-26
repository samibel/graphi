package ingest

import (
	"errors"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/typeresolve"
)

// SW-222 (AX-02) AC-3, at the ingest composition root. The Ingester owns the
// linker and the semantic-resolver registry; both are fully composed by the time
// a constructor returns, so both must come back FROZEN. The registries are
// unexported, which is why this invariant is pinned from inside the package.

// frozenTestResolver is a semantic resolver used only to attempt a post-freeze
// registration.
type frozenTestResolver struct{}

func (frozenTestResolver) Language() string             { return "toy" }
func (frozenTestResolver) Subject(relPath string) bool  { return false }
func (frozenTestResolver) Input(relPath string) bool    { return false }
func (frozenTestResolver) Owns(relPath string) bool     { return false }
func (frozenTestResolver) Triggers(relPath string) bool { return false }
func (frozenTestResolver) Resolve(files map[string][]byte, committed map[model.NodeId]struct{}) (typeresolve.Result, error) {
	return typeresolve.Result{}, nil
}

func TestIngesterFreezesItsResolverRegistries(t *testing.T) {
	metaDir := t.TempDir()
	ing, err := New(graphstore.NewMemStore(), parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })

	if !ing.linker.Frozen() {
		t.Fatal("ingest.New must freeze the linker's resolver registry")
	}
	if !ing.semantic.Frozen() {
		t.Fatal("ingest.New must freeze the semantic-resolver registry")
	}

	// link.Resolver cannot be implemented outside package link (its Resolve
	// returns the unexported intent type), so the refusal itself is pinned by
	// engine/link's own TestLinkRegistry_FreezeRefusesRegister; what belongs
	// here is that ingest actually flipped the flag, asserted above.
	if serr := ing.semantic.Register(frozenTestResolver{}); !errors.Is(serr, registry.ErrFrozen) {
		t.Fatalf("Register on the frozen semantic registry = %v, want registry.ErrFrozen", serr)
	}
	if got := ing.semantic.Languages(); len(got) != 1 || got[0] != "go" {
		t.Fatalf("the frozen semantic registry changed: %v", got)
	}
}

func TestReadOnlyIngesterFreezesItsResolverRegistries(t *testing.T) {
	metaDir := t.TempDir()
	// NewReadOnly requires an existing sidecar; create it with the read-write
	// constructor first.
	rw, err := New(graphstore.NewMemStore(), parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	if cerr := rw.Close(); cerr != nil {
		t.Fatalf("close: %v", cerr)
	}

	ro, err := NewReadOnly(graphstore.NewMemStore(), parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		t.Fatalf("ingest.NewReadOnly: %v", err)
	}
	t.Cleanup(func() { _ = ro.Close() })

	if !ro.linker.Frozen() || !ro.semantic.Frozen() {
		t.Fatal("ingest.NewReadOnly must freeze both resolver registries")
	}
	if serr := ro.semantic.Register(frozenTestResolver{}); !errors.Is(serr, registry.ErrFrozen) {
		t.Fatalf("Register on the frozen read-only semantic registry = %v, want registry.ErrFrozen", serr)
	}
}
