package parse

import (
	"errors"
	"testing"

	"github.com/samibel/graphi/core/registry"
)

// SW-222 (AX-02) AC-2: the parse registry DECLARES its collision policy, and the
// declaration is the last-wins rule ADR 0013 threat T5 names.
func TestParseRegistry_DeclaresItsCollisionPolicy(t *testing.T) {
	if CollisionPolicy != registry.PolicyLastWins {
		t.Fatalf("parse.CollisionPolicy = %s, want last-wins — ADR 0013 T5 keeps this divergence", CollisionPolicy)
	}
	if got := NewRegistry().Policy(); got != CollisionPolicy {
		t.Fatalf("Policy() = %s, want %s", got, CollisionPolicy)
	}
	if !CollisionPolicy.AllowsOverride() {
		t.Fatal("the parse registry must allow an opt-in backend to supersede a default")
	}
}

// AC-3: NewDefaultRegistry is the parse registry's composition root, so it comes
// back frozen and a later Register fails with the typed frozen error — no panic,
// no silent success.
func TestParseRegistry_DefaultRegistryIsFrozen(t *testing.T) {
	r := NewDefaultRegistry()
	if !r.Frozen() {
		t.Fatal("NewDefaultRegistry() must return a frozen registry")
	}

	before := len(r.Languages())
	err := r.Register(charParser{lang: "toy", exts: []string{".toy"}, tag: "t"})
	if err == nil {
		t.Fatal("Register on a frozen registry returned nil — it must fail, not silently succeed")
	}
	if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("Register on a frozen registry = %v, want registry.ErrFrozen", err)
	}
	if got := len(r.Languages()); got != before {
		t.Fatalf("a refused Register still mutated the registry: %d languages, was %d", got, before)
	}
	if _, perr := r.ParserFor("x.toy"); !errors.Is(perr, ErrNoParser) {
		t.Fatalf("a refused Register still installed the parser: %v", perr)
	}

	// The mutable composition is unaffected — the seam is still open/closed.
	m := RegisterDefaults(NewRegistry())
	if m.Frozen() {
		t.Fatal("RegisterDefaults(NewRegistry()) must NOT be frozen")
	}
	if err := m.Register(charParser{lang: "toy", exts: []string{".toy"}, tag: "t"}); err != nil {
		t.Fatalf("Register on the mutable composition: %v", err)
	}
}

// A no-op registration stays a no-op on a frozen registry: nil and empty
// parsers were never registrations, so they are not frozen-registry failures.
func TestParseRegistry_FrozenNoOpsStayNoOps(t *testing.T) {
	r := NewDefaultRegistry()
	if err := r.Register(nil); err != nil {
		t.Fatalf("Register(nil) on a frozen registry = %v, want nil", err)
	}
	if err := r.Register(charParser{}); err != nil {
		t.Fatalf("Register(empty parser) on a frozen registry = %v, want nil", err)
	}
}

// AC-6: the rollback switch disables ENFORCEMENT without any data migration —
// the same frozen registry accepts a registration again, exactly as it did
// before AX-02, and still reports that it was frozen.
func TestParseRegistry_FreezeIsDisableableForRollback(t *testing.T) {
	r := NewDefaultRegistry()
	restore := registry.SetFreezeEnforced(false)
	defer restore()

	if err := r.Register(charParser{lang: "toy", exts: []string{".toy"}, tag: "t"}); err != nil {
		t.Fatalf("with freeze enforcement off, Register = %v, want the legacy nil", err)
	}
	if _, err := r.ParserFor("x.toy"); err != nil {
		t.Fatalf("with freeze enforcement off, the registration did not apply: %v", err)
	}
	if !r.Frozen() {
		t.Fatal("the rollback switch must not erase the frozen state (no migration)")
	}
}
