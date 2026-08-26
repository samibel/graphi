package typeresolve

import (
	"errors"
	"testing"

	"github.com/samibel/graphi/core/registry"
)

// SW-222 (AX-02) AC-2: the semantic-resolver registry declares its collision
// policy, and NewRegistry is deliberately the unfrozen BUILDER — engine/semantic
// still decides whether the JVM resolvers join.
func TestTypeResolveRegistry_DeclaresItsCollisionPolicy(t *testing.T) {
	if CollisionPolicy != registry.PolicyLastWins {
		t.Fatalf("typeresolve.CollisionPolicy = %s, want last-wins", CollisionPolicy)
	}
	r := NewRegistry()
	if got := r.Policy(); got != CollisionPolicy {
		t.Fatalf("Policy() = %s, want %s", got, CollisionPolicy)
	}
	if r.Frozen() {
		t.Fatal("NewRegistry() must stay UNFROZEN — engine/semantic registers onto it")
	}
}

// AC-3: after Freeze a Register fails with the typed frozen error and the
// dispatch order is untouched.
func TestTypeResolveRegistry_FreezeRefusesRegister(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	if !r.Frozen() {
		t.Fatal("Frozen() = false after Freeze()")
	}
	err := r.Register(charResolver{lang: "toy"})
	if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Register after freeze = %v, want registry.ErrFrozen", err)
	}
	if len(r.Resolvers()) != 1 || r.Resolvers()[0].Language() != "go" {
		t.Fatalf("a refused Register mutated the dispatch order: %v", r.Languages())
	}
}

// AC-6: rollback restores the legacy behaviour with no migration.
func TestTypeResolveRegistry_FreezeIsDisableableForRollback(t *testing.T) {
	r := NewRegistry()
	r.Freeze()
	restore := registry.SetFreezeEnforced(false)
	defer restore()

	if err := r.Register(charResolver{lang: "toy"}); err != nil {
		t.Fatalf("with freeze enforcement off, Register = %v, want nil", err)
	}
	if len(r.Resolvers()) != 2 {
		t.Fatalf("with freeze enforcement off, the registration did not apply: %v", r.Languages())
	}
	if !r.Frozen() {
		t.Fatal("the rollback switch must not erase the frozen state")
	}
}
