package profile

import (
	"fmt"
	"strings"
)

// Profile selects the speed/depth trade-off for an index pass.
type Profile string

const (
	// Fast skips expensive resolve passes and low-value import fanout.
	Fast Profile = "fast"
	// Balanced is the default: bounded linking/resolve.
	Balanced Profile = "balanced"
	// Deep runs full linking and maximum useful edge recall.
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
