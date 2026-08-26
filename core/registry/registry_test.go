package registry_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/registry"
)

// AC-1: the four lifecycle failures are typed and matchable with errors.Is,
// and the concrete error carries registry / operation / key.
func TestTypedErrors_AreMatchableAndCarryContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind error
		op   string
	}{
		{"duplicate", registry.GuardDuplicate(registry.PolicyFirstWins, "analysis", "analyzer", "impact", true), registry.ErrDuplicate, "Register"},
		{"unsupported-override", registry.GuardReplace(registry.PolicyLastWins, "parse", "parser", "go", true), registry.ErrUnsupportedOverride, "Replace"},
		{"missing-dependency", registry.GuardReplace(registry.PolicyFirstWinsWithReplace, "analysis", "analyzer", "nope", false), registry.ErrMissingDependency, "Replace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				t.Fatal("guard returned nil, want a typed error")
			}
			if !errors.Is(tc.err, tc.kind) {
				t.Fatalf("errors.Is(%v, %v) = false", tc.err, tc.kind)
			}
			var re *registry.Error
			if !errors.As(tc.err, &re) {
				t.Fatalf("errors.As(*registry.Error) = false for %v", tc.err)
			}
			if re.Op != tc.op {
				t.Fatalf("Op = %q, want %q", re.Op, tc.op)
			}
			if re.Registry == "" || re.Key == "" {
				t.Fatalf("error lost context: %+v", re)
			}
		})
	}
}

// AC-2: the policy values are explicit and their capabilities are declared, not
// inferred at each call site.
func TestPolicy_DeclaresItsCapabilities(t *testing.T) {
	for _, tc := range []struct {
		p                       registry.Policy
		str                     string
		override, replace       bool
		duplicateIsAnError      bool
		replaceOfKnownIsAnError bool
	}{
		{registry.PolicyLastWins, "last-wins", true, false, false, true},
		{registry.PolicyFirstWins, "first-wins", false, false, true, true},
		{registry.PolicyFirstWinsWithReplace, "first-wins-with-replace", false, true, true, false},
		{registry.PolicyUnset, "unset", false, false, true, true},
	} {
		t.Run(tc.str, func(t *testing.T) {
			if tc.p.String() != tc.str {
				t.Fatalf("String() = %q, want %q", tc.p.String(), tc.str)
			}
			if tc.p.AllowsOverride() != tc.override {
				t.Fatalf("AllowsOverride() = %v, want %v", tc.p.AllowsOverride(), tc.override)
			}
			if tc.p.AllowsReplace() != tc.replace {
				t.Fatalf("AllowsReplace() = %v, want %v", tc.p.AllowsReplace(), tc.replace)
			}
			gotDup := registry.GuardDuplicate(tc.p, "r", "entry", "k", true) != nil
			if gotDup != tc.duplicateIsAnError {
				t.Fatalf("GuardDuplicate on collision errored = %v, want %v", gotDup, tc.duplicateIsAnError)
			}
			gotRep := registry.GuardReplace(tc.p, "r", "entry", "k", true) != nil
			if gotRep != tc.replaceOfKnownIsAnError {
				t.Fatalf("GuardReplace of a known key errored = %v, want %v", gotRep, tc.replaceOfKnownIsAnError)
			}
		})
	}
}

// A non-colliding registration is always allowed, whatever the policy.
func TestGuardDuplicate_NoCollisionIsAlwaysAllowed(t *testing.T) {
	for _, p := range []registry.Policy{registry.PolicyUnset, registry.PolicyLastWins, registry.PolicyFirstWins, registry.PolicyFirstWinsWithReplace} {
		if err := registry.GuardDuplicate(p, "r", "entry", "k", false); err != nil {
			t.Fatalf("policy %s rejected a non-colliding registration: %v", p, err)
		}
	}
}

// AC-3: after Freeze, a mutation is refused with the FrozenRegistry error —
// it neither panics nor silently succeeds.
func TestLifecycle_FreezeRefusesMutation(t *testing.T) {
	var l registry.Lifecycle
	if l.Frozen() {
		t.Fatal("zero Lifecycle must be unfrozen")
	}
	if err := l.CheckMutable("parse", "Register", "go"); err != nil {
		t.Fatalf("unfrozen CheckMutable = %v, want nil", err)
	}

	l.Freeze()
	if !l.Frozen() {
		t.Fatal("Frozen() = false after Freeze()")
	}
	err := l.CheckMutable("parse", "Register", "go")
	if err == nil {
		t.Fatal("frozen CheckMutable = nil, want ErrFrozen")
	}
	if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("errors.Is(%v, ErrFrozen) = false", err)
	}
	if !strings.Contains(err.Error(), "registry is frozen") {
		t.Fatalf("frozen error message unhelpful: %v", err)
	}
	if !strings.Contains(err.Error(), `"go"`) {
		t.Fatalf("frozen error dropped the key: %v", err)
	}

	// Freeze is idempotent and one-way.
	l.Freeze()
	if err := l.CheckMutable("parse", "Register", ""); err == nil {
		t.Fatal("second Freeze thawed the registry")
	} else if !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("keyless frozen error is not ErrFrozen: %v", err)
	}
}

// AC-6: freeze enforcement is disableable behind an internal switch, and
// disabling it restores the exact pre-AX-02 behaviour with no state to migrate —
// the registry still KNOWS it was frozen, it just stops refusing.
func TestLifecycle_EnforcementSwitchIsTheRollback(t *testing.T) {
	var l registry.Lifecycle
	l.Freeze()

	restore := registry.SetFreezeEnforced(false)
	if registry.FreezeEnforced() {
		t.Fatal("FreezeEnforced() = true after switching enforcement off")
	}
	if err := l.CheckMutable("parse", "Register", "go"); err != nil {
		t.Fatalf("with enforcement off, CheckMutable = %v, want nil (legacy behaviour)", err)
	}
	if !l.Frozen() {
		t.Fatal("disabling enforcement must not erase the frozen state")
	}

	restore()
	if !registry.FreezeEnforced() {
		t.Fatal("restore() did not re-arm enforcement")
	}
	if err := l.CheckMutable("parse", "Register", "go"); !errors.Is(err, registry.ErrFrozen) {
		t.Fatalf("re-armed CheckMutable = %v, want ErrFrozen", err)
	}
}

// The switch is fail-closed: only an explicit off value disarms it, so a typo
// leaves the guard armed.
func TestFreezeEnv_IsFailClosed(t *testing.T) {
	if registry.EnvFreeze != "GRAPHI_REGISTRY_FREEZE" {
		t.Fatalf("rollback switch renamed to %q — update the ADR and the story", registry.EnvFreeze)
	}
	if !registry.FreezeEnforced() {
		t.Fatal("freeze enforcement must be ON by default")
	}
	for _, off := range []string{"0", "false", "off", "no", "OFF", " 0 "} {
		if registry.ParseFreezeEnv(off) {
			t.Fatalf("ParseFreezeEnv(%q) = true, want the rollback to disarm enforcement", off)
		}
	}
	for _, on := range []string{"", "1", "true", "yes", "  ", "0x0", "nope"} {
		if !registry.ParseFreezeEnv(on) {
			t.Fatalf("ParseFreezeEnv(%q) = false — a value that is not an explicit OFF must leave the guard armed", on)
		}
	}
}

// Lifecycle holds no lock of its own and must be safe to consult concurrently
// from inside a registry's critical section (run with -race).
func TestLifecycle_ConcurrentUse(t *testing.T) {
	var l registry.Lifecycle
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if i%4 == 0 {
					l.Freeze()
				}
				_ = l.Frozen()
				_ = l.CheckMutable("r", "Register", "k")
			}
		}(i)
	}
	wg.Wait()
	if !l.Frozen() {
		t.Fatal("registry should be frozen after the concurrent run")
	}
}

// The error message shape is stable enough to read, and the fallback (no
// caller-supplied message) still names registry, op, key and kind.
func TestError_FallbackMessage(t *testing.T) {
	e := &registry.Error{Registry: "parse", Op: "Register", Key: "go", Kind: registry.ErrFrozen}
	got := e.Error()
	for _, want := range []string{"parse", "Register", `"go"`, "registry is frozen"} {
		if !strings.Contains(got, want) {
			t.Fatalf("fallback message %q missing %q", got, want)
		}
	}
	if !errors.Is(e, registry.ErrFrozen) {
		t.Fatal("Unwrap() lost the kind")
	}
	var nilErr *registry.Error
	if nilErr.Error() == "" {
		t.Fatal("nil *Error must not render as empty")
	}
	if nilErr.Unwrap() != nil {
		t.Fatal("nil *Error must unwrap to nil")
	}
}
