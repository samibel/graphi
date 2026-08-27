package runtime

import (
	"fmt"
	"sync/atomic"
)

// CompositionMode selects which composition path Attach and OpenSession use.
//
// AC-6 of SW-227 requires the PRIOR composition path to stay compilable and
// selectable for rollback for at least one release line, with a test that
// exercises it. This is that selector.
//
// It is deliberately NOT an environment variable. SW-226's canary switch is one
// because it is a kill switch for a dispatch path that can produce bytes, and an
// operator must be able to flip it without waiting for a release. This selector
// changes nothing a client can observe — AC-4 requires the two compositions to
// be indistinguishable, and the characterization suite proves it — so exposing
// it as an operator knob would advertise a choice that has no user-visible
// consequence, while inviting a process to run a composition nobody tested it
// with. It follows SW-225's descriptorSource precedent instead: a source-level
// switch, flipped by one line for a rollback, plus a programmatic setter for the
// test that keeps the rolled-back path honest.
type CompositionMode string

const (
	// CompositionBuilder is the AX-07 path: the RuntimeBuilder over the built-in
	// module set. It is the shipped default.
	CompositionBuilder CompositionMode = "builder"
	// CompositionLegacy is the pre-AX-07 path, preserved verbatim: the inline
	// constructor sequence each start path used to run.
	CompositionLegacy CompositionMode = "legacy"
)

// defaultCompositionMode is the position of record for a release. Rolling back
// AX-07 is this one line.
const defaultCompositionMode = CompositionBuilder

var compositionMode atomic.Value // CompositionMode

func init() { compositionMode.Store(defaultCompositionMode) }

// ParseCompositionMode maps a name to a mode, failing closed on anything else.
func ParseCompositionMode(raw string) (CompositionMode, error) {
	switch CompositionMode(raw) {
	case CompositionBuilder:
		return CompositionBuilder, nil
	case CompositionLegacy:
		return CompositionLegacy, nil
	default:
		return "", fmt.Errorf("unknown composition mode %q (want %q or %q)", raw, CompositionBuilder, CompositionLegacy)
	}
}

// CompositionModeSetting reports the mode in force.
func CompositionModeSetting() CompositionMode {
	mode, _ := compositionMode.Load().(CompositionMode)
	if mode == "" {
		return defaultCompositionMode
	}
	return mode
}

// SetCompositionMode installs a mode and returns a function restoring the
// previous one. It mirrors registry.SetFreezeEnforced: an internal rollback and
// test seam, not a product surface, and it is why the legacy composition stays
// executable rather than merely compilable.
func SetCompositionMode(mode CompositionMode) (restore func(), err error) {
	parsed, err := ParseCompositionMode(string(mode))
	if err != nil {
		return nil, err
	}
	previous := CompositionModeSetting()
	compositionMode.Store(parsed)
	return func() { compositionMode.Store(previous) }, nil
}
