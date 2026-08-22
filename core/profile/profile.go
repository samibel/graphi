package profile

import (
	"fmt"
	"strings"
)

// Profile selects the speed/depth trade-off for an index pass.
//
// HONEST STATE OF THE LADDER (ADR 0010, 2026-08-16): only Fast is behaviourally
// distinct today. Balanced and Deep produce IDENTICAL graphs — verified by
// indexing a real clone under both and comparing node/edge sets and per-kind
// histograms — because the one thing that separated them was an import
// aggregation that turned out to be parity-unsafe and to drop true edges, and
// it was removed rather than repaired (PARITY-003). The two names stay: they are
// CLI values and the persisted `index.profile` metadata that `graphi status`,
// `trust-report` and `doctor` report. Do not read "bounded" as a reduction that
// still happens.
type Profile string

const (
	// Fast skips expensive resolve passes (no typeresolve) and drops `imports`
	// edges entirely. This is the only profile that reduces the graph today.
	Fast Profile = "fast"
	// Balanced is the default. It runs full linking and resolve; since ADR 0010
	// it is graph-identical to Deep.
	Balanced Profile = "balanced"
	// Deep runs full linking and maximum useful edge recall — currently the
	// same graph as Balanced.
	Deep Profile = "deep"
)

// All returns the supported profiles in canonical order.
func All() []Profile {
	return []Profile{Fast, Balanced, Deep}
}

// String returns the profile name.
func (p Profile) String() string {
	return string(p)
}

// Parse validates and normalizes a profile string.
func Parse(s string) (Profile, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	p := Profile(s)
	switch p {
	case Fast, Balanced, Deep:
		return p, nil
	default:
		return "", fmt.Errorf("invalid profile %q: must be one of %s", s, strings.Join([]string{string(Fast), string(Balanced), string(Deep)}, "|"))
	}
}

// EnvName is the environment variable that can set the profile.
const EnvName = "GRAPHI_INDEX_PROFILE"

// ResolveProfile returns the active profile using precedence:
// explicit flag > environment variable > default Balanced.
// flagValue is nil when the flag was not provided; envValue is the raw env value.
func ResolveProfile(flagValue *string, envValue string) (Profile, error) {
	if flagValue != nil {
		return Parse(*flagValue)
	}
	if envValue != "" {
		return Parse(envValue)
	}
	return Balanced, nil
}
