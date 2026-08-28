package divergence

import "sort"

// SW-248 — reachability, as the record's own vocabulary.
//
// Before this file the document had one axis: whether an operation had been
// OBSERVED. On the shipped `shadow` default that axis alone produced a readout
// that was true and misleading — every migrated operation reading `UNKNOWN: no
// dual-run observation has been recorded`, which a reader takes as *not yet*,
// when for a client bound to the default MCP profile the truth is *not
// possible*. A zero that means "never ran" must not read like a zero that means
// "ran and agreed" (SW-232); this is the same distinction applied to the reason
// the zero is there.
//
// So reachability is a SECOND, orthogonal axis. It is not folded into
// OperationState, because the two answer different questions and collapsing
// them would lose one: an operation can be reachable and unobserved, reachable
// and diverged, unreachable and unobserved. Only their combination is a
// complete statement, and the renderer prints both.
//
// The profile picture is INJECTED rather than computed here. This package owns
// the record and its honesty rules; which tools a surface profile advertises is
// a surface fact, and the composition root already imports both sides (see
// cmd/graphi/doctor.go). Injection is also what makes the three readings
// testable: no Stable operation is migrated today, so a test could not
// construct the "reachable in the bound profile but never observed" shape at
// all if the real profiles were compiled in.

// Profile is one shipped surface binding, reduced to what the record needs: how
// an operator selects it, whether it is the binding a stock install gets, and
// which of the operations on the seam it can reach.
type Profile struct {
	// ID is the stable machine identifier (surfaces/mcp.ProfileDefaultID, …).
	ID string `json:"id"`
	// Invocation is the command that binds it, so a readout names something an
	// operator can run.
	Invocation string `json:"invocation"`
	// Default reports whether this is the profile a stock install binds. The
	// readout reasons about exactly one reference profile and this is it.
	Default bool `json:"default"`
	// Reaches lists the seam operations this profile can dispatch.
	Reaches []string `json:"reaches"`
}

// ReachState is one operation's reachability verdict.
//
// The spellings are long on purpose. They are read next to UNKNOWN in a table
// whose whole failure mode was a short word carrying the wrong implication, and
// a state that has to be looked up is a state that gets guessed at.
type ReachState string

const (
	// ReachUnevaluated means no profile picture was supplied, so nothing is
	// known about reachability. It is NOT "reachable": a document assembled
	// without profiles must not imply an answer it was never given. Assess
	// produces it when profiles is nil; the shipped `graphi doctor` path always
	// supplies profiles and therefore never produces it.
	ReachUnevaluated ReachState = "REACHABILITY-NOT-EVALUATED"
	// ReachDefault means the default shipped profile reaches the operation, so
	// a call through a stock install would record an observation. An UNKNOWN
	// beside this genuinely does mean "not yet".
	ReachDefault ReachState = "REACHABLE-IN-DEFAULT-PROFILE"
	// ReachOptIn means only a non-default shipped profile reaches it. An
	// UNKNOWN beside this does NOT mean "not yet" — no call through the bound
	// default can ever change it.
	ReachOptIn ReachState = "OPT-IN-PROFILE-ONLY"
	// ReachNone means no shipped profile reaches it at all. That is a defect in
	// the migration rather than a state an operator can act on, and it is what
	// the CI gate (cmd/seamreach) refuses to let merge.
	ReachNone ReachState = "UNREACHABLE-IN-EVERY-SHIPPED-PROFILE"
)

// reachProse is the one-line meaning of a reachability verdict, kept beside the
// constants so the wording cannot drift from the state it explains.
func reachProse(s ReachState, defaultProfile string) string {
	switch s {
	case ReachDefault:
		return "reachable through " + quoteProfile(defaultProfile) + "; a call would record an observation"
	case ReachOptIn:
		return "NOT reachable through " + quoteProfile(defaultProfile) +
			" — no call through the default profile can ever observe it"
	case ReachNone:
		return "NOT reachable through ANY shipped profile — nothing can ever observe it"
	case ReachUnevaluated:
		return "reachability was not evaluated for this document"
	default:
		return "unrecognised reachability"
	}
}

// reachColumn is the table cell: short enough to sit beside the state, explicit
// enough that the three shapes cannot be confused at a glance.
func reachColumn(v OperationView) string {
	switch v.Reach {
	case ReachDefault, ReachOptIn:
		return joinInvocations(v.ReachedBy)
	case ReachNone:
		return "NONE"
	default:
		return "?"
	}
}

// joinInvocations renders the profile commands that reach an operation.
func joinInvocations(invocations []string) string {
	switch len(invocations) {
	case 0:
		return "NONE"
	case 1:
		return invocations[0]
	}
	out := invocations[0]
	for _, inv := range invocations[1:] {
		out += " | " + inv
	}
	return out
}

func quoteProfile(invocation string) string {
	if invocation == "" {
		return "the default shipped profile"
	}
	return "`" + invocation + "`"
}

// reachIndex is the profile picture turned into per-operation answers.
type reachIndex struct {
	// evaluated is false when no profiles were supplied.
	evaluated bool
	// defaultInvocation is the command that binds the reference profile, empty
	// when the picture declares no default.
	defaultInvocation string
	// byOp maps an operation id to the invocations that reach it, in profile
	// order.
	byOp map[string][]string
	// inDefault marks the operations the reference profile reaches.
	inDefault map[string]bool
}

// newReachIndex builds the lookup. A nil or empty profile list yields an
// unevaluated index — deliberately distinct from a picture that says "no
// profile reaches anything", which is a finding rather than an absence.
func newReachIndex(profiles []Profile) reachIndex {
	idx := reachIndex{byOp: map[string][]string{}, inDefault: map[string]bool{}}
	if len(profiles) == 0 {
		return idx
	}
	idx.evaluated = true
	for _, p := range profiles {
		if p.Default {
			idx.defaultInvocation = p.Invocation
		}
		for _, op := range p.Reaches {
			idx.byOp[op] = append(idx.byOp[op], p.Invocation)
			if p.Default {
				idx.inDefault[op] = true
			}
		}
	}
	return idx
}

// stateOf answers one operation.
func (i reachIndex) stateOf(op string) (ReachState, []string) {
	if !i.evaluated {
		return ReachUnevaluated, nil
	}
	invocations := i.byOp[op]
	switch {
	case len(invocations) == 0:
		return ReachNone, nil
	case i.inDefault[op]:
		return ReachDefault, append([]string(nil), invocations...)
	default:
		return ReachOptIn, append([]string(nil), invocations...)
	}
}

// sortedProfiles copies a profile list with each Reaches set sorted, so the
// document serialises deterministically whatever order the caller assembled.
func sortedProfiles(profiles []Profile) []Profile {
	if len(profiles) == 0 {
		return nil
	}
	out := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		reaches := append([]string(nil), p.Reaches...)
		sort.Strings(reaches)
		p.Reaches = reaches
		out = append(out, p)
	}
	return out
}
