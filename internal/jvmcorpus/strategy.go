// Package jvmcorpus is the CI-ONLY compile strategy for the JVM corpus pins
// (SW-173, W0.e stage 3). Like internal/jvmgroundtruth it NEVER ships: nothing
// under cmd/graphi, core/, engine/ or surfaces/ imports it, so the product stays
// CGo-free, toolchain-free and zero-egress. It is driven by tests and by the
// dispatch-only jvm-corpus workflow.
//
// # Why a strategy is a decision and not a script
//
// The oracle can only compare against bytecode it has, so pointing it at guava,
// okio or kotlinx.serialization means compiling them — and none of the three is
// a `javac $(find …)` job. Two facts make the choice load-bearing:
//
//   - An UNPINNED compiler produces unreproducible evidence, and unreproducible
//     evidence is not evidence under this programme's rules. So every toolchain
//     that touches a pin is named with an exact version, and the version is
//     recorded beside the figures it produced.
//
//   - Obtaining a classpath means either resolving real dependencies or
//     accepting compile errors and scoring only what resolved — and the two
//     produce DIFFERENT DENOMINATORS. A coverage figure whose denominator is
//     unstated is unreadable, so the strategy is carried as data beside the pin
//     and travels with every figure derived from it.
//
// # The three strategies, and what each obliges
//
//	full-dependency-resolution
//	    The pin's compile classpath is resolved COMPLETELY, from a digest-pinned
//	    lockfile — exact artifact URLs with exact sha256s, no resolver, no
//	    version ranges, no transitive discovery. Obliges: every classpath entry
//	    carries a coordinate, a URL and a sha256, and the compile must produce
//	    ZERO errors. If it does not, the strategy was the wrong one and the pin
//	    moves to accept-errors or not-compiled rather than quietly under-
//	    reporting.
//
//	accept-errors-and-score-what-resolved
//	    The compile is run knowing some units will fail, and only what compiled
//	    is scored. Obliges: the denominator is published — how many source files
//	    and classes were actually compiled out of how many exist at the pin —
//	    because a coverage figure without its denominator is not publishable.
//	    That is the same rule W1.b applies to the binding rate.
//
//	not-compiled
//	    The pin could not be compiled reproducibly within the story. Obliges: a
//	    NEGATIVE RESULT recording what was tried, and explicit exclusion from
//	    the corpus-scale claim. A pin that cannot be compiled is excluded
//	    loudly, never silently omitted (AC-7). Negative results are recorded,
//	    never deleted (discipline D5).
//
// # Why this reads the manifest with its own narrow struct
//
// internal/corpus owns corpus/manifest.json for the PR-gate corpus runner and
// is validated by sixteen tests that have nothing to do with compiling. Rather
// than widen that schema — and its blast radius — for a concern with a different
// lifecycle, this package reads the same file through a struct holding only the
// fields a compile strategy needs. json.Unmarshal ignores the fields each side
// does not name, so the two readers coexist without either constraining the
// other. The cost is one extra parse of a 20 KB file in a dispatch-only job.
package jvmcorpus

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// The three strategy names. They are a CLOSED set: an unrecognised strategy is
// a validation failure, never a default, because "we did not say" must not be
// able to masquerade as "we resolved everything".
const (
	StrategyFullResolution = "full-dependency-resolution"
	StrategyAcceptErrors   = "accept-errors-and-score-what-resolved"
	StrategyNotCompiled    = "not-compiled"
)

// Manifest is the compile-strategy view of corpus/manifest.json.
type Manifest struct {
	Entries []Entry `json:"entries"`
}

// Entry is the compile-strategy view of one corpus entry.
type Entry struct {
	Name     string    `json:"name"`
	Language string    `json:"language"`
	Ref      string    `json:"ref"`
	SHA      string    `json:"sha"`
	URL      string    `json:"url"`
	Compile  *Strategy `json:"jvm_compile,omitempty"`
}

// Strategy is one pin's compile decision, its reason, and everything needed to
// reproduce it.
type Strategy struct {
	// Strategy is one of the three constants above.
	Strategy string `json:"strategy"`
	// Reason states WHY this strategy and not another, in the pin's own terms.
	// It is required on every strategy including the successful one: "it worked"
	// is not a reason, and the next reader needs to know what was rejected.
	Reason string `json:"reason"`
	// Toolchain names every compiler that touches this pin, with an EXACT
	// version. `releases/latest` and friends are validation failures (AC-1/AC-2).
	Toolchain map[string]string `json:"toolchain,omitempty"`
	// SourceRoots are the pin-relative directories whose sources are compiled.
	// They are OVERLAID at their package-relative paths into one staging root,
	// so the paths graphi ingests and the paths javap reconstructs from
	// SourceFile headers agree by construction rather than by coincidence.
	SourceRoots []string `json:"source_roots,omitempty"`
	// CommonSourceRoots is the subset of SourceRoots holding Kotlin-
	// multiplatform COMMON sources. kotlinc needs them named explicitly
	// (-Xcommon-sources); without that, every `@OptionalExpectation` declaration
	// is rejected with "can only be used in common module sources" and the pin
	// does not compile at all. Empty for javac pins.
	CommonSourceRoots []string `json:"common_source_roots,omitempty"`
	// Compiler is the executable that compiles this pin: "javac" or "kotlinc".
	Compiler string `json:"compiler,omitempty"`
	// CompilerArgs are the exact flags, in order. They are DATA rather than
	// script text because they are part of what makes a compile reproducible:
	// okio and kotlinx.serialization both need multiplatform flags taken from
	// the pin's own build files, and a flag that lives only in a shell script is
	// a flag nobody can diff against the pin.
	CompilerArgs []string `json:"compiler_args,omitempty"`
	// Classpath is the digest-pinned lockfile. Required, and required to be
	// complete, under full-dependency-resolution.
	Classpath []Artifact `json:"classpath,omitempty"`
	// Tried records what was attempted for a pin that could not be compiled.
	// Required under not-compiled: an exclusion whose reasoning is not written
	// down is indistinguishable from an omission.
	Tried []string `json:"tried,omitempty"`
	// ExcludedFromCorpusScale marks a pin that does not back the corpus-scale
	// claim. It is a SEPARATE axis from Strategy, because "compiles" and "can be
	// scored by this oracle" turned out to be different questions: okio compiles
	// cleanly and is still unscorable, because its Kotlin-multiplatform
	// expect/actual layout gives 36 of its 89 JVM-target sources a package-
	// relative path that another source also claims, and javap's SourceFile
	// attribute cannot tell them apart. Collapsing the two axes would have
	// forced okio to be recorded either as "not compiled" (false) or as
	// corpus-scale evidence (misleading).
	ExcludedFromCorpusScale bool `json:"excluded_from_corpus_scale,omitempty"`
	// ExclusionReason states WHY, and is required whenever the exclusion is set.
	// An exclusion without a reason is indistinguishable from an omission, which
	// is the failure AC-7 exists to prevent.
	ExclusionReason string `json:"exclusion_reason,omitempty"`
}

// Artifact is one digest-pinned classpath entry.
type Artifact struct {
	// Coordinate is the Maven coordinate, for humans and for tracing the
	// version back to the pin's own build files.
	Coordinate string `json:"coordinate"`
	// URL is the exact artifact URL. No repository base + resolver: the URL is
	// the whole address.
	URL string `json:"url"`
	// SHA256 is the artifact's digest, verified before the artifact is placed
	// on a classpath. A mismatch fails the run closed — a compile against
	// unexpected bytes is not the compile the strategy describes.
	SHA256 string `json:"sha256"`
	// Role is how the artifact reaches the compiler: "classpath" (default) or
	// "compiler-plugin" (kotlinc -Xplugin=). The distinction is not cosmetic —
	// SW-173's review question asks specifically whether TRANSITIVELY FETCHED
	// COMPILER PLUGINS are pinned, and kotlinx.serialization's plugin is exactly
	// that case. Modelling it as a role means the plugin is carried by the same
	// digest-pinned mechanism as any other artifact instead of being smuggled in
	// through a flag string.
	Role string `json:"role,omitempty"`
}

// RoleCompilerPlugin marks an artifact passed as kotlinc -Xplugin=.
const RoleCompilerPlugin = "compiler-plugin"

var (
	sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// unpinnedRe catches the moving references AC-1 forbids. `releases/latest`
	// is the one the jvm-groundtruth workflow actually shipped; the rest are the
	// obvious neighbours, listed so the rule is about MOVING refs rather than
	// about one string someone remembered.
	unpinnedRe = regexp.MustCompile(`(?i)\b(latest|master|main|HEAD|nightly|snapshot|SNAPSHOT)\b`)
)

// Load reads and validates the compile-strategy view of a corpus manifest.
func Load(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("jvmcorpus: read manifest %q: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("jvmcorpus: parse manifest %q: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// JVMLanguages is the set of pin languages this strategy governs.
var JVMLanguages = map[string]bool{"java": true, "kotlin": true, "jvm": true}

// JVMPins returns every JVM-language entry that names a remote repository, in
// manifest order. Local fixture entries (no URL) are excluded: they are checked
// in, compiled by the hermetic tests, and have no pin to reproduce.
func (m Manifest) JVMPins() []Entry {
	var out []Entry
	for _, e := range m.Entries {
		if JVMLanguages[e.Language] && e.URL != "" {
			out = append(out, e)
		}
	}
	return out
}

// Validate enforces the obligations each strategy carries. It is deliberately
// strict and fails closed: every rule here exists because breaking it would let
// a figure be published that reads as covering more than it measured.
func (m Manifest) Validate() error {
	for _, e := range m.JVMPins() {
		if e.Compile == nil {
			return fmt.Errorf("jvmcorpus: JVM pin %q has no jvm_compile strategy; "+
				"every JVM pin must state its compile strategy and reason (AC-3)", e.Name)
		}
		if err := e.Compile.validate(e.Name); err != nil {
			return err
		}
	}
	return nil
}

func (s *Strategy) validate(pin string) error {
	switch s.Strategy {
	case StrategyFullResolution, StrategyAcceptErrors, StrategyNotCompiled:
	default:
		return fmt.Errorf("jvmcorpus: pin %q has unknown strategy %q; must be one of %s, %s, %s",
			pin, s.Strategy, StrategyFullResolution, StrategyAcceptErrors, StrategyNotCompiled)
	}
	if strings.TrimSpace(s.Reason) == "" {
		return fmt.Errorf("jvmcorpus: pin %q states strategy %q with no reason (AC-3)", pin, s.Strategy)
	}

	// AC-1/AC-2: every toolchain is pinned to an exact version.
	if s.Strategy != StrategyNotCompiled && len(s.Toolchain) == 0 {
		return fmt.Errorf("jvmcorpus: pin %q compiles but names no toolchain version (AC-1, AC-2)", pin)
	}
	for tool, version := range s.Toolchain {
		if strings.TrimSpace(version) == "" {
			return fmt.Errorf("jvmcorpus: pin %q names toolchain %q with no version (AC-1, AC-2)", pin, tool)
		}
		if unpinnedRe.MatchString(version) {
			return fmt.Errorf("jvmcorpus: pin %q pins toolchain %q to the MOVING reference %q; "+
				"the same commit would compile differently on two days, and evidence from an unpinned "+
				"compiler is not evidence (AC-1)", pin, tool, version)
		}
	}

	// The exclusion axis, checked for every strategy.
	if s.ExcludedFromCorpusScale && strings.TrimSpace(s.ExclusionReason) == "" {
		return fmt.Errorf("jvmcorpus: pin %q is excluded from the corpus-scale claim with no reason; "+
			"an exclusion without a stated reason is indistinguishable from a silent omission (AC-7)", pin)
	}

	switch s.Strategy {
	case StrategyFullResolution:
		if len(s.SourceRoots) == 0 {
			return fmt.Errorf("jvmcorpus: pin %q resolves fully but names no source roots", pin)
		}
		if strings.TrimSpace(s.Compiler) == "" {
			return fmt.Errorf("jvmcorpus: pin %q resolves fully but names no compiler", pin)
		}
		for i, a := range s.Classpath {
			if strings.TrimSpace(a.Coordinate) == "" {
				return fmt.Errorf("jvmcorpus: pin %q classpath[%d] has no coordinate", pin, i)
			}
			if strings.TrimSpace(a.URL) == "" {
				return fmt.Errorf("jvmcorpus: pin %q classpath entry %q has no URL; "+
					"a base repository plus a resolver is not a pin", pin, a.Coordinate)
			}
			if !sha256Re.MatchString(a.SHA256) {
				return fmt.Errorf("jvmcorpus: pin %q classpath entry %q has no valid sha256 (%q); "+
					"an artifact fetched without a digest can change under the pin", pin, a.Coordinate, a.SHA256)
			}
			if unpinnedRe.MatchString(a.URL) {
				return fmt.Errorf("jvmcorpus: pin %q classpath entry %q resolves through the MOVING reference %q",
					pin, a.Coordinate, a.URL)
			}
		}
	case StrategyAcceptErrors:
		if len(s.SourceRoots) == 0 {
			return fmt.Errorf("jvmcorpus: pin %q accepts errors but names no source roots", pin)
		}
	case StrategyNotCompiled:
		if len(s.Tried) == 0 {
			return fmt.Errorf("jvmcorpus: pin %q is not compiled but records nothing that was TRIED; "+
				"a negative result without its attempts is an omission (AC-7)", pin)
		}
		if !s.ExcludedFromCorpusScale {
			return fmt.Errorf("jvmcorpus: pin %q is not compiled and must be marked "+
				"excluded_from_corpus_scale — an uncompilable pin is excluded loudly, never silently (AC-7)", pin)
		}
	}
	return nil
}

// ToolchainLine renders the pinned toolchain deterministically, for recording
// beside the evidence a pin produced (AC-1, AC-2).
func (s *Strategy) ToolchainLine() string {
	if len(s.Toolchain) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(s.Toolchain))
	for k := range s.Toolchain {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+" "+s.Toolchain[k])
	}
	return strings.Join(parts, ", ")
}
