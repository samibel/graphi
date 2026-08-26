package embed_test

import (
	"errors"
	"testing"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/embed"
)

// SW-222 (AX-02) AC-2: the embedder registry declares its collision policy.
func TestEmbedRegistry_DeclaresItsCollisionPolicy(t *testing.T) {
	if embed.CollisionPolicy != registry.PolicyLastWins {
		t.Fatalf("embed.CollisionPolicy = %s, want last-wins", embed.CollisionPolicy)
	}
	if got := embed.NewRegistry().Policy(); got != embed.CollisionPolicy {
		t.Fatalf("Policy() = %s, want %s", got, embed.CollisionPolicy)
	}
}

// AC-3 + the zero-egress default: NewDefaultRegistry is frozen, so the
// "registers nothing" default cannot acquire an embedder after construction.
func TestEmbedRegistry_DefaultRegistryIsFrozenAndEmpty(t *testing.T) {
	r := embed.NewDefaultRegistry()
	if !r.Frozen() {
		t.Fatal("NewDefaultRegistry() must return a frozen registry")
	}
	err := r.Register(charEmbedder{id: "toy"})
	if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Register on the frozen default = %v, want registry.ErrFrozen", err)
	}
	if r.Configured() {
		t.Fatal("a refused Register still configured the default registry")
	}
	if ids := r.IDs(); len(ids) != 0 {
		t.Fatalf("a refused Register still stored the embedder: %v", ids)
	}
}

// The ZERO registry keeps its graceful-skip contract: unfrozen, so an opt-in
// composition root can still register into it.
func TestEmbedRegistry_ZeroValueIsUnfrozen(t *testing.T) {
	var zero embed.Registry
	if zero.Frozen() {
		t.Fatal("the zero Registry must be unfrozen")
	}
	if err := zero.Register(charEmbedder{id: "toy"}); err != nil {
		t.Fatalf("Register on the zero Registry = %v, want nil", err)
	}
	if !zero.Configured() {
		t.Fatal("Register on the zero Registry did not configure it")
	}
}

// AC-3: an explicit composition root freezes after registering its selection,
// which is what cmd/internal/runtime and `graphi index --semantic` do.
func TestEmbedRegistry_FreezeAfterOptIn(t *testing.T) {
	r := embed.NewRegistry()
	if err := r.Register(charEmbedder{id: "opted-in"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	r.Freeze()

	if err := r.Register(charEmbedder{id: "sneaky"}); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Register after freeze = %v, want registry.ErrFrozen", err)
	}
	e, ok := r.Active()
	if !ok || e.ID() != "opted-in" {
		t.Fatalf("a refused Register moved the active embedder: (%v, %v)", e, ok)
	}
}

// AC-6: rollback restores the legacy behaviour with no migration.
func TestEmbedRegistry_FreezeIsDisableableForRollback(t *testing.T) {
	r := embed.NewDefaultRegistry()
	restore := registry.SetFreezeEnforced(false)
	defer restore()

	if err := r.Register(charEmbedder{id: "toy"}); err != nil {
		t.Fatalf("with freeze enforcement off, Register = %v, want nil", err)
	}
	if !r.Configured() {
		t.Fatal("with freeze enforcement off, the registration did not apply")
	}
	if !r.Frozen() {
		t.Fatal("the rollback switch must not erase the frozen state")
	}
}
