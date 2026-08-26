// Package registry is graphi's shared registry-lifecycle vocabulary
// (SW-222 / AX-02).
//
// graphi grew several independent registry seams — the parser registry
// (core/parse), the analyzer registry (engine/analysis), the semantic-resolver
// registry (engine/typeresolve), the embedder registry (engine/embed) and the
// link-resolver registry (engine/link). Each was correct for its own package
// and each chose its own collision rule, so the SAME act — "register a thing" —
// silently superseded a built-in in one registry and errored in another, and
// none of them had a point after which mutation stopped. ADR 0013 records that
// divergence as threat T5 and hands this package two jobs.
//
// # What this package does
//
//   - It gives every participating registry ONE lifecycle vocabulary:
//     Register → Override → Validate → Freeze → Execute, with four typed
//     errors (ErrDuplicate, ErrUnsupportedOverride, ErrMissingDependency,
//     ErrFrozen) that callers can match with errors.Is.
//   - It makes each registry's collision policy EXPLICIT and machine-readable
//     (Policy, reported by a Policy() method on each participating registry).
//
// # What this package deliberately does NOT do
//
// It does not harmonise the policies. `core/parse` stays last-wins so an opt-in
// backend can supersede a stdlib default; `engine/analysis` stays
// first-wins-with-error so a duplicate analyzer name is a loud programming
// fault. AX-02 is a behaviour-preserving refactor: the divergence becomes
// declared configuration instead of an accident of implementation, and each
// package's characterization baseline
// (`baseline_characterization_test.go`) proves it survived unchanged.
//
// # Freeze
//
// A registry that has finished composition is frozen. After Freeze, every
// mutation entry point returns an ErrFrozen-typed error — it does not panic and
// it does not silently succeed. Freeze is one-way and carries no state beyond a
// single flag, which is what makes the rollback in FreezeEnforced a switch
// rather than a migration.
//
// # Layering
//
// This package sits at CORE rank with no dependencies beyond the standard
// library, so every layer of `cmd → surfaces → engine → core` may import it.
package registry

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

// Policy declares what a registry does when a registration collides with a key
// that is already registered. It is stated per registry rather than shared,
// because graphi's registries genuinely disagree and the disagreement is
// deliberate (ADR 0013 T5).
type Policy uint8

const (
	// PolicyUnset is the zero value. It is never a valid policy for a
	// participating registry; guards treat it as "no collision handling
	// declared" and refuse overrides.
	PolicyUnset Policy = iota

	// PolicyLastWins: a later registration for the same key SUPERSEDES the
	// earlier one. Registration is the override mechanism; there is no separate
	// override entry point and no duplicate error. Used by core/parse (an
	// opt-in CGO grammar may supersede a stdlib default), engine/embed,
	// engine/typeresolve and engine/link.
	//
	// SECURITY NOTE (ADR 0013 D5.3 / T5): a last-wins seam silently shadows a
	// built-in. Third-party extension code must never reach one.
	PolicyLastWins

	// PolicyFirstWins: a later registration for the same key is REJECTED with
	// an ErrDuplicate-typed error and the first registration keeps the slot.
	// There is no override entry point at all.
	PolicyFirstWins

	// PolicyFirstWinsWithReplace is PolicyFirstWins plus one narrow, sanctioned
	// override path: an explicit Replace of an ALREADY-REGISTERED key. Replace
	// refuses unknown keys (ErrMissingDependency) precisely so it cannot become
	// a second registration path. Used by engine/analysis, whose composition
	// root re-arms already-registered analyzers with an injected dependency.
	PolicyFirstWinsWithReplace
)

// String renders the policy for diagnostics and package docs.
func (p Policy) String() string {
	switch p {
	case PolicyLastWins:
		return "last-wins"
	case PolicyFirstWins:
		return "first-wins"
	case PolicyFirstWinsWithReplace:
		return "first-wins-with-replace"
	default:
		return "unset"
	}
}

// AllowsOverride reports whether a plain Register may supersede an existing
// entry.
func (p Policy) AllowsOverride() bool { return p == PolicyLastWins }

// AllowsReplace reports whether the policy sanctions an explicit override entry
// point (a Replace of an already-registered key).
func (p Policy) AllowsReplace() bool { return p == PolicyFirstWinsWithReplace }

// The four typed lifecycle failures. Callers match them with errors.Is; the
// concrete error carries the registry, operation and key (see Error).
var (
	// ErrDuplicate: a key was registered twice on a registry whose policy
	// rejects duplicates.
	ErrDuplicate = errors.New("duplicate registration")

	// ErrUnsupportedOverride: an override was attempted against a registry
	// whose policy has no sanctioned override path.
	ErrUnsupportedOverride = errors.New("override not supported by collision policy")

	// ErrMissingDependency: the operation needs something that is not
	// registered — most commonly a Replace naming a key nobody registered.
	ErrMissingDependency = errors.New("missing dependency")

	// ErrFrozen: the registry finished composition and no longer accepts
	// mutation.
	ErrFrozen = errors.New("registry is frozen")
)

// Error is the concrete lifecycle error. It carries the registry name, the
// operation, and the colliding or missing key, and unwraps to one of the four
// sentinels above.
//
// The message is supplied by the caller rather than derived. That is
// deliberate: this vocabulary was retrofitted onto registries whose exact error
// strings are already asserted by existing tests, and AX-02 must not change one
// observable byte. New call sites should still pass a message in the same
// "<package>: <what happened>" shape as their neighbours.
type Error struct {
	// Registry is the registry's short name, e.g. "parse" or "analysis".
	Registry string
	// Op is the lifecycle operation, e.g. "Register", "Replace" or "Freeze".
	Op string
	// Key is the colliding, missing or rejected key; empty when not applicable.
	Key string
	// Kind is one of ErrDuplicate, ErrUnsupportedOverride,
	// ErrMissingDependency or ErrFrozen.
	Kind error

	msg string
}

// Error implements error.
func (e *Error) Error() string {
	if e == nil {
		return "<nil registry error>"
	}
	if e.msg != "" {
		return e.msg
	}
	var b strings.Builder
	b.WriteString(e.Registry)
	b.WriteString(": ")
	b.WriteString(e.Op)
	if e.Key != "" {
		fmt.Fprintf(&b, " %q", e.Key)
	}
	if e.Kind != nil {
		b.WriteString(": ")
		b.WriteString(e.Kind.Error())
	}
	return b.String()
}

// Unwrap exposes the sentinel so errors.Is(err, registry.ErrFrozen) works.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Kind
}

// Errorf builds a typed lifecycle error with a caller-supplied message.
func Errorf(kind error, reg, op, key, format string, a ...any) *Error {
	return &Error{Registry: reg, Op: op, Key: key, Kind: kind, msg: fmt.Sprintf(format, a...)}
}

// GuardDuplicate applies a registry's collision policy to one Register call and
// returns nil when the registration may proceed.
//
// reg is the registry's short name ("analysis"), subject the noun it registers
// ("analyzer"), key the registration key, and exists reports whether key is
// already registered. Under PolicyLastWins the collision IS the point, so nil is
// returned; under the first-wins policies a collision is ErrDuplicate.
func GuardDuplicate(p Policy, reg, subject, key string, exists bool) error {
	if !exists || p.AllowsOverride() {
		return nil
	}
	return Errorf(ErrDuplicate, reg, "Register", key, "%s: %s %q already registered", reg, subject, key)
}

// GuardReplace applies a registry's collision policy to one EXPLICIT override.
// It returns ErrUnsupportedOverride when the policy has no sanctioned override
// path, and ErrMissingDependency when the key to override is not registered.
//
// No registry shipped today reaches the ErrUnsupportedOverride branch: every
// participating registry either has no Replace-style entry point at all, or
// declares PolicyFirstWinsWithReplace. The branch exists because the policy is
// now data — a future registrant (or a rule pack under SW-229) can declare a
// policy that forbids overriding, and this guard is where that declaration
// becomes an error instead of a comment.
func GuardReplace(p Policy, reg, subject, key string, exists bool) error {
	if !p.AllowsReplace() {
		return Errorf(ErrUnsupportedOverride, reg, "Replace", key,
			"%s: %s %q cannot be replaced: collision policy is %s", reg, subject, key, p)
	}
	if !exists {
		return Errorf(ErrMissingDependency, reg, "Replace", key, "%s: %s %q not registered", reg, subject, key)
	}
	return nil
}

// Lifecycle is the embeddable freeze state shared by graphi's runtime
// registries. Its ZERO VALUE is the unfrozen state, so a registry whose zero
// value is meaningful (engine/embed's graceful-skip Registry) keeps that
// property. It is safe for concurrent use and holds no lock of its own, so a
// registry may consult it from inside its own critical section.
type Lifecycle struct {
	frozen atomic.Bool
}

// Freeze marks composition complete. It is idempotent and one-way: there is no
// Unfreeze, because a registry that could be thawed would give an extension a
// door back into a seam ADR 0013 closed. To disable ENFORCEMENT (the rollback
// path), see FreezeEnforced.
func (l *Lifecycle) Freeze() { l.frozen.Store(true) }

// Frozen reports whether Freeze has been called. It reports the real state
// regardless of whether enforcement is switched on, so a diagnostic never lies
// about what happened.
func (l *Lifecycle) Frozen() bool { return l.frozen.Load() }

// CheckMutable returns nil when the registry may still be mutated, and an
// ErrFrozen-typed error when it may not. When freeze enforcement is disabled it
// always returns nil — the legacy, pre-AX-02 behaviour exactly.
func (l *Lifecycle) CheckMutable(reg, op, key string) error {
	if !l.frozen.Load() || !FreezeEnforced() {
		return nil
	}
	if key == "" {
		return Errorf(ErrFrozen, reg, op, key, "%s: %s after freeze: registry is frozen", reg, op)
	}
	return Errorf(ErrFrozen, reg, op, key, "%s: %s %q after freeze: registry is frozen", reg, op, key)
}

// EnvFreeze NAMES the internal rollback switch for freeze ENFORCEMENT (AC-6).
// Setting it to "0" (or "false"/"off"/"no") makes every CheckMutable return nil,
// so the registries behave exactly as they did before AX-02. It changes no
// stored state and needs no migration: registries still record that they were
// frozen, they simply stop refusing mutation.
//
// This is an INTERNAL escape hatch for rolling back a regression, not a
// supported product knob — it is deliberately absent from user-facing help, and
// nothing in the default path sets it.
//
// This package NAMES the variable but does not READ it, following core/profile's
// EnvName/ResolveProfile split: a core leaf declares configuration, the wiring
// layer resolves it. cmd/graphi applies it once at startup. Enforcement is
// consulted lazily on every CheckMutable, so applying the switch after a
// registry has already frozen still works — there is no initialisation-order
// hazard to get wrong.
const EnvFreeze = "GRAPHI_REGISTRY_FREEZE"

var freezeEnforced atomic.Bool

func init() { freezeEnforced.Store(true) }

// ParseFreezeEnv maps an EnvFreeze value to an enforcement decision.
// Enforcement is ON unless explicitly switched off — fail-closed, so a typo
// leaves the guard armed rather than silently disarming it.
func ParseFreezeEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// FreezeEnforced reports whether freeze enforcement is currently armed.
func FreezeEnforced() bool { return freezeEnforced.Load() }

// SetFreezeEnforced flips enforcement programmatically and returns a function
// restoring the previous setting. It exists for tests of the rollback path and
// for a composition root that must disable enforcement before any registry is
// built; it is not part of any product surface.
func SetFreezeEnforced(on bool) (restore func()) {
	prev := freezeEnforced.Swap(on)
	return func() { freezeEnforced.Store(prev) }
}
