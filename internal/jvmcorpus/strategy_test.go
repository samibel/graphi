package jvmcorpus_test

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/jvmcorpus"
)

const manifestPath = "../../corpus/manifest.json"

// TestCheckedInManifest_EveryJVMPinStatesItsStrategy is AC-3 as a gate: the
// checked-in manifest must parse AND satisfy every obligation the three
// strategies carry. It fails the moment a JVM pin is added without a compile
// decision, which is the state SW-173 found the corpus in.
func TestCheckedInManifest_EveryJVMPinStatesItsStrategy(t *testing.T) {
	m, err := jvmcorpus.Load(manifestPath)
	if err != nil {
		t.Fatalf("the checked-in manifest must load and validate: %v", err)
	}
	pins := m.JVMPins()
	if len(pins) != 3 {
		t.Fatalf("expected the three JVM pins (guava, okio, kotlinx.serialization), got %d: %v", len(pins), names(pins))
	}
	for _, p := range pins {
		if p.Compile == nil {
			t.Fatalf("JVM pin %q has no compile strategy", p.Name)
		}
		if len(p.Compile.Reason) < 80 {
			t.Errorf("pin %q reason is %d chars; AC-3 wants the REASON, not a label", p.Name, len(p.Compile.Reason))
		}
	}
}

// TestCheckedInManifest_ToolchainsArePinnedExactly is AC-1 and AC-2. It is the
// gate that would have caught the shipped `releases/latest` kotlinc install: a
// moving reference means the same commit can compile differently on two days,
// and evidence from an unpinned compiler is not evidence.
func TestCheckedInManifest_ToolchainsArePinnedExactly(t *testing.T) {
	m, err := jvmcorpus.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	sawJavac, sawKotlinc := false, false
	for _, p := range m.JVMPins() {
		for tool, version := range p.Compile.Toolchain {
			switch tool {
			case "javac":
				sawJavac = true
			case "kotlinc":
				sawKotlinc = true
			}
			// An exact version starts with a digit. "latest", "stable" and a
			// bare major line ("21.x") do not.
			if version == "" || version[0] < '0' || version[0] > '9' {
				t.Errorf("pin %q toolchain %q = %q is not an exact version (AC-1, AC-2)", p.Name, tool, version)
			}
			if strings.Contains(strings.ToLower(version), "latest") {
				t.Errorf("pin %q toolchain %q is unpinned: %q", p.Name, tool, version)
			}
		}
	}
	if !sawJavac {
		t.Error("no pin names a javac version; AC-2 requires javac to be pinned and recorded")
	}
	if !sawKotlinc {
		t.Error("no pin names a kotlinc version; AC-1 requires kotlinc to be pinned and recorded")
	}
}

// TestCheckedInManifest_ClasspathIsDigestPinned is what makes
// full-dependency-resolution reproducible rather than merely automated. A
// coordinate plus a repository base is a RESOLUTION; a URL plus a sha256 is a
// pin. Only the second survives an upstream re-publish.
func TestCheckedInManifest_ClasspathIsDigestPinned(t *testing.T) {
	m, err := jvmcorpus.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := 0
	for _, p := range m.JVMPins() {
		for _, a := range p.Compile.Classpath {
			artifacts++
			if len(a.SHA256) != 64 {
				t.Errorf("pin %q artifact %q has no sha256", p.Name, a.Coordinate)
			}
			if !strings.HasPrefix(a.URL, "https://") {
				t.Errorf("pin %q artifact %q is not fetched over https: %q", p.Name, a.Coordinate, a.URL)
			}
		}
	}
	if artifacts == 0 {
		t.Fatal("no pinned classpath artifact anywhere — this test would pass vacuously")
	}
}

// TestCheckedInManifest_ExclusionsCarryTheirReason is AC-7: a pin that does not
// back the corpus-scale claim is excluded LOUDLY. okio is the live case — it
// compiles and is still unscorable — so this asserts the reason names the
// mechanism rather than merely asserting some string is present.
func TestCheckedInManifest_ExclusionsCarryTheirReason(t *testing.T) {
	m, err := jvmcorpus.Load(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	excluded := 0
	for _, p := range m.JVMPins() {
		if !p.Compile.ExcludedFromCorpusScale {
			continue
		}
		excluded++
		r := p.Compile.ExclusionReason
		if len(r) < 120 {
			t.Errorf("pin %q exclusion reason is too thin to be a negative result: %q", p.Name, r)
		}
		// A negative result states what was TRIED or what was MEASURED, not
		// just that something did not work.
		if !strings.Contains(r, "MEASURED") && !strings.Contains(r, "measured") && len(p.Compile.Tried) == 0 {
			t.Errorf("pin %q is excluded without recording what was measured or tried (AC-7)", p.Name)
		}
	}
	if excluded == 0 {
		t.Log("no pin is currently excluded; if a pin becomes unscorable this test starts biting")
	}
}

// TestValidate_RejectsUnpinnedToolchain is the red-without-fix half of AC-1: the
// rule is shown to REJECT the exact shape the repository shipped
// (`releases/latest`), not merely to accept the fixed one.
func TestValidate_RejectsUnpinnedToolchain(t *testing.T) {
	bad := jvmcorpus.Manifest{Entries: []jvmcorpus.Entry{{
		Name: "okio", Language: "kotlin", URL: "https://example.invalid/okio",
		Compile: &jvmcorpus.Strategy{
			Strategy:    jvmcorpus.StrategyFullResolution,
			Reason:      "a reason long enough to pass the length rule but with a moving toolchain reference",
			Toolchain:   map[string]string{"kotlinc": "releases/latest"},
			Compiler:    "kotlinc",
			SourceRoots: []string{"src"},
		},
	}}}
	err := bad.Validate()
	if err == nil {
		t.Fatal("a `releases/latest` toolchain must be rejected (AC-1)")
	}
	if !strings.Contains(err.Error(), "MOVING") {
		t.Fatalf("the rejection must say WHY; got %q", err)
	}
}

// TestValidate_RejectsUndigestedArtifact pins the other half: an artifact
// fetched without a digest can change under the pin, so it is refused.
func TestValidate_RejectsUndigestedArtifact(t *testing.T) {
	bad := jvmcorpus.Manifest{Entries: []jvmcorpus.Entry{{
		Name: "guava", Language: "java", URL: "https://example.invalid/guava",
		Compile: &jvmcorpus.Strategy{
			Strategy:    jvmcorpus.StrategyFullResolution,
			Reason:      "a reason long enough to pass the length rule but with an undigested classpath artifact",
			Toolchain:   map[string]string{"javac": "21.0.6"},
			Compiler:    "javac",
			SourceRoots: []string{"src"},
			Classpath: []jvmcorpus.Artifact{{
				Coordinate: "g:a:1", URL: "https://repo.invalid/a.jar", SHA256: "",
			}},
		},
	}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("a classpath artifact without a sha256 must be rejected")
	}
}

// TestValidate_RejectsSilentExclusion is AC-7's core: a pin may be excluded, but
// never quietly.
func TestValidate_RejectsSilentExclusion(t *testing.T) {
	bad := jvmcorpus.Manifest{Entries: []jvmcorpus.Entry{{
		Name: "okio", Language: "kotlin", URL: "https://example.invalid/okio",
		Compile: &jvmcorpus.Strategy{
			Strategy:                jvmcorpus.StrategyNotCompiled,
			Reason:                  "a reason long enough to pass the length rule for an uncompiled pin here",
			Tried:                   []string{"gradle"},
			ExcludedFromCorpusScale: true,
		},
	}}}
	if err := bad.Validate(); err == nil {
		t.Fatal("an exclusion without a reason must be rejected (AC-7)")
	}

	alsoBad := jvmcorpus.Manifest{Entries: []jvmcorpus.Entry{{
		Name: "okio", Language: "kotlin", URL: "https://example.invalid/okio",
		Compile: &jvmcorpus.Strategy{
			Strategy: jvmcorpus.StrategyNotCompiled,
			Reason:   "a reason long enough to pass the length rule for an uncompiled pin here",
			Tried:    []string{"gradle"},
		},
	}}}
	if err := alsoBad.Validate(); err == nil {
		t.Fatal("a not-compiled pin must be marked excluded from the corpus-scale claim (AC-7)")
	}
}

// TestValidate_RejectsUnExcludingACompilingPin is the other direction of AC-7,
// added in SW-173 round 1 (minor-5). The rule above binds only pins recorded as
// not-compiled. A pin that COMPILES but cannot be scored — kotlinx.serialization,
// with 27 open by-name counterexamples — carried its exclusion as data that any
// edit could flip off while leaving the reason behind, which is precisely a
// silent re-admission to the corpus-scale claim.
func TestValidate_RejectsUnExcludingACompilingPin(t *testing.T) {
	compiling := func(excluded bool) jvmcorpus.Manifest {
		return jvmcorpus.Manifest{Entries: []jvmcorpus.Entry{{
			Name: "kotlinx.serialization", Language: "kotlin", URL: "https://example.invalid/kx",
			Compile: &jvmcorpus.Strategy{
				Strategy:                jvmcorpus.StrategyFullResolution,
				Reason:                  "a reason long enough to pass the length rule for a compiling pin here",
				Toolchain:               map[string]string{"kotlinc": "1.9.24"},
				Compiler:                "kotlinc",
				SourceRoots:             []string{"core/commonMain/src"},
				ExcludedFromCorpusScale: excluded,
				ExclusionReason:         "compiles and is reproducible; scoring is not established, 27 of 351 unbacked",
			},
		}}}
	}
	if err := compiling(true).Validate(); err != nil {
		t.Fatalf("the checked-in shape must stay valid: %v", err)
	}
	err := compiling(false).Validate()
	if err == nil {
		t.Fatal("flipping excluded_from_corpus_scale to false while KEEPING the exclusion reason " +
			"must be rejected — that is a pin re-admitted to the corpus-scale claim silently (AC-7)")
	}
	if !strings.Contains(err.Error(), "excluded loudly") {
		t.Errorf("the failure must say what the rule protects, got: %v", err)
	}
}

func names(pins []jvmcorpus.Entry) []string {
	var out []string
	for _, p := range pins {
		out = append(out, p.Name)
	}
	return out
}
