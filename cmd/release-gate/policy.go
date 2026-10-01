package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// This file is the decision SW-249 and SW-250 deliberately did not take.
//
// SW-249 made skips visible. SW-250 carried a structured UNVERIFIED across the
// process boundary and explicitly refused to say what it MEANS, routing it onto
// the pre-existing warning channel so the release outcome was unchanged. This
// file changes that outcome, on purpose, in one place.
//
// # Why the policy is here and not in the workflow file
//
// The obvious implementation treats testgate's UNVERIFIED exit code as
// non-blocking in `.github/workflows/*.yml`. That would put the release policy
// in the least-reviewed artifact in the chain: a YAML file that no test
// compiles, no type checks, and that a one-line edit can loosen invisibly.
// SW-243 set the precedent the other way — its refusal rules live beside the
// constant they protect. So the workflow states a FACT about where it is
// running, and this file decides what that fact means.

// Context names WHERE a run is happening. It is a fact, never a policy.
type Context string

const (
	// ContextPR is a pull request. A measurement that could not be taken is
	// not a reason to refuse a change.
	ContextPR Context = "pr"

	// ContextRelease is `main` and the release path. There is no SHA-bound
	// substitute evidence in this repository, so "UNKNOWN is non-blocking" and
	// "no regression slips through" cannot both hold. On the release line a
	// missing measurement is not an approval.
	ContextRelease Context = "release"
)

// DefaultContext is deliberately the STRICT context (AC-6).
//
// A forgotten flag must fail safe. The cost of being needlessly strict on a
// pull request is one re-run; the cost of being accidentally lenient on the
// release line is a release nobody measured. Those are not symmetric, so the
// default is not a matter of taste.
const DefaultContext = ContextRelease

// ParseContext resolves the -context flag. An empty value takes the strict
// default; an unrecognised value is an error rather than a silent fallback,
// because "release-gate -context=main" quietly meaning `release` is luck, and
// quietly meaning `pr` would be a hole.
func ParseContext(raw string) (Context, error) {
	switch c := Context(strings.TrimSpace(raw)); c {
	case "":
		return DefaultContext, nil
	case ContextPR, ContextRelease:
		return c, nil
	default:
		return DefaultContext, fmt.Errorf(
			"unknown -context %q: expected %q or %q (empty means %q, the strict default)",
			raw, ContextPR, ContextRelease, DefaultContext)
	}
}

// GateState is one of the FOUR answers a gate can give. The defect this spec
// exists to close is that the release verdict collapsed them onto a boolean.
type GateState string

const (
	// StatePass — the gate ran and what it measures is healthy.
	StatePass GateState = "PASS"

	// StateFail — the gate ran and what it measures is NOT healthy.
	StateFail GateState = "FAIL"

	// StateUnverified — the gate ran and positively demonstrated that its own
	// resolution was insufficient. This is not evidence of health and it is
	// not a failure; it is the absence of a measurement.
	StateUnverified GateState = "UNVERIFIED"

	// StateError — the gate's machinery produced no usable answer: it could
	// not be run, its verdict was unreadable, its own statements about itself
	// disagreed, or it did not report at all.
	StateError GateState = "ERROR"
)

// Blocks is THE policy — four states by two contexts, in one function:
//
//	state        pr       release
//	PASS         allow    allow
//	FAIL         BLOCK    BLOCK
//	UNVERIFIED   allow    BLOCK
//	ERROR        BLOCK    BLOCK
//
// Exactly one cell differs between the two contexts, and that single cell is
// the whole decision this story makes: UNVERIFIED is the only context-sensitive
// state.
//
// ERROR is not a softer FAIL. A required gate that silently did not run is the
// same hole as UNKNOWN rendering green, so it blocks in every context (AC-5).
//
// The comparison is written `c != ContextPR` rather than `c == ContextRelease`
// so that a Context value this policy has never heard of blocks rather than
// waves through; ParseContext already rejects those at the boundary, and this
// is the second lock on the same door.
func (c Context) Blocks(state GateState) bool {
	switch state {
	case StatePass:
		return false
	case StateUnverified:
		return c != ContextPR
	case StateFail, StateError:
		return true
	default:
		// A state this policy has never heard of is not an approval.
		return true
	}
}

// GateError marks a gate whose MACHINERY did not deliver a usable answer, as
// distinct from a gate that ran and found a problem (a plain error → FAIL) and
// from one that ran and could not resolve its measurement (*UnverifiedError →
// UNVERIFIED). See UnverifiedError in runners.go for the other half of the
// pair.
type GateError struct{ Detail string }

func (e *GateError) Error() string { return e.Detail }

// classifyGate maps a Runner outcome onto the four states. The two typed
// errors are the only way to reach UNVERIFIED and ERROR: an ordinary error is
// a FAIL. That direction matters — the spec requires UNVERIFIED to be HARDER
// to reach than FAIL, never easier, so nothing is inferred from message text.
func classifyGate(err error) GateState {
	switch {
	case err == nil:
		return StatePass
	case errors.As(err, new(*UnverifiedError)):
		return StateUnverified
	case errors.As(err, new(*GateError)):
		return StateError
	default:
		return StateFail
	}
}

// requiredGates is the DECLARED set of gates a run must contain, written out
// here rather than derived from DefaultGates().
//
// Deriving it from the producer would make "which gates ran" and "which gates
// must run" the same statement, and absence could then never be detected —
// deleting a gate would delete the requirement with it. AC-5 exists precisely
// because a gate that silently stops running is invisible. TestDefaultGates...
// in policy_test.go keeps the declaration and the producer in step.
var requiredGates = []string{"bench-budget", "coverage", "privacy", "testgate"}

// GateOutcome is one constituent gate's answer, its state, and what this
// context did about it.
type GateOutcome struct {
	Name     string
	State    GateState
	Detail   string
	Required bool
	// Blocking records the policy's answer for this state in this context.
	Blocking bool
}

// evaluateGates runs every gate and applies the policy. A gate named in
// requiredGates but absent from gates is ERROR — not skipped, not assumed
// green (AC-5).
func evaluateGates(ctx Context, gates map[string]Runner) []GateOutcome {
	names := make([]string, 0, len(gates)+len(requiredGates))
	seen := make(map[string]bool, len(gates)+len(requiredGates))
	for name := range gates {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	required := make(map[string]bool, len(requiredGates))
	for _, name := range requiredGates {
		required[name] = true
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	sort.Strings(names)

	outcomes := make([]GateOutcome, 0, len(names))
	for _, name := range names {
		out := GateOutcome{Name: name, Required: required[name]}
		runner, present := gates[name]
		switch {
		case !present:
			out.State = StateError
			out.Detail = "required gate absent from this run — it did not report at all, " +
				"which is indistinguishable from a gate that was deleted or silently skipped"
		default:
			_, err := runner.Run()
			out.State = classifyGate(err)
			if err != nil {
				out.Detail = err.Error()
			}
		}
		out.Blocking = ctx.Blocks(out.State)
		outcomes = append(outcomes, out)
	}
	return outcomes
}

// unverifiedGateNames lists the gates that reported UNVERIFIED, blocking or
// not. Used by the publish guard: whether an unverified measurement BLOCKS is
// context-dependent, but whether it may be written into release evidence
// carrying a PASS verdict is not (AC-4).
func unverifiedGateNames(outcomes []GateOutcome) []string {
	var names []string
	for _, o := range outcomes {
		if o.State == StateUnverified {
			names = append(names, o.Name)
		}
	}
	return names
}
