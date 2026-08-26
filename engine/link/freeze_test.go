package link

import (
	"errors"
	"testing"

	"github.com/samibel/graphi/core/registry"
)

// SW-222 (AX-02) AC-2: the link resolver registry declares its collision
// policy, and New() is deliberately the unfrozen BUILDER — engine/ingest freezes
// it when it takes ownership.
func TestLinkRegistry_DeclaresItsCollisionPolicy(t *testing.T) {
	if CollisionPolicy != registry.PolicyLastWins {
		t.Fatalf("link.CollisionPolicy = %s, want last-wins", CollisionPolicy)
	}
	l := New()
	if got := l.Policy(); got != CollisionPolicy {
		t.Fatalf("Policy() = %s, want %s", got, CollisionPolicy)
	}
	if l.Frozen() {
		t.Fatal("New() must stay UNFROZEN — engine/ingest owns the freeze")
	}
}

// AC-3: after Freeze a Register fails with the typed frozen error and the
// published capability list is untouched.
func TestLinkRegistry_FreezeRefusesRegister(t *testing.T) {
	l := New()
	before := len(l.Languages())
	l.Freeze()
	if !l.Frozen() {
		t.Fatal("Frozen() = false after Freeze()")
	}
	err := l.Register(charLinkResolver{lang: "toy"})
	if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Register after freeze = %v, want registry.ErrFrozen", err)
	}
	if got := len(l.Languages()); got != before {
		t.Fatalf("a refused Register moved the published capability list: %d, was %d", got, before)
	}
}

// AC-6: rollback restores the legacy behaviour with no migration.
func TestLinkRegistry_FreezeIsDisableableForRollback(t *testing.T) {
	l := New()
	before := len(l.Languages())
	l.Freeze()
	restore := registry.SetFreezeEnforced(false)
	defer restore()

	if err := l.Register(charLinkResolver{lang: "toy"}); err != nil {
		t.Fatalf("with freeze enforcement off, Register = %v, want nil", err)
	}
	if got := len(l.Languages()); got != before+1 {
		t.Fatalf("with freeze enforcement off, the registration did not apply: %d", got)
	}
	if !l.Frozen() {
		t.Fatal("the rollback switch must not erase the frozen state")
	}
}
