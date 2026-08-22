package semantic

import (
	"reflect"
	"sort"
	"testing"
)

// TestDefaultOff pins the shipped default: without the opt-in the registry is
// exactly the go/types resolver — byte-identical product behavior, and the
// capability surface claims nothing the passes do not run.
func TestDefaultOff(t *testing.T) {
	t.Setenv(EnvJVM, "")
	if got, want := Languages(), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default registry = %v, want %v", got, want)
	}
	t.Setenv(EnvJVM, "0")
	if got := Languages(); !reflect.DeepEqual(got, []string{"go"}) {
		t.Fatalf("explicit 0 must stay off: %v", got)
	}
}

// TestOptInRegistersJVM pins the experimental opt-in: the JVM registrants
// appear, and Languages() — the trust surface's source — reflects them.
func TestOptInRegistersJVM(t *testing.T) {
	t.Setenv(EnvJVM, "1")
	if got, want := Languages(), []string{"go", "java", "kotlin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("opt-in registry = %v, want %v", got, want)
	}
	langs := map[string]bool{}
	for _, r := range NewRegistry().Resolvers() {
		langs[r.Language()] = true
	}
	if !langs["go"] || !langs["java"] || !langs["kotlin"] {
		t.Fatalf("resolvers = %v", langs)
	}
}

// TestRegistry_OwnsIsDisjointAndCoversSubject pins the two properties the
// typeresolve.Resolver.Owns contract rests on (ADR 0008 ruling D9), across the
// PRODUCT-WIDE registry. This package is the only one that can: it is the only
// place every registrant exists at once.
//
//  1. SUPERSET — Owns ⊇ Subject for each registrant. A file this resolver
//     CHECKS must be a file whose nodes it owns; otherwise the pass would emit
//     an edge it can never sweep, and that edge would be immortal.
//  2. DISJOINT — no path is owned by two registrants. Two owners would each
//     sweep the other's edges, which is exactly the mixed-directory defect D9
//     removes, reintroduced one layer down.
//
// The path set is representative rather than exhaustive: every extension the
// registrants actually key on, plus near misses that must be owned by nobody.
func TestRegistry_OwnsIsDisjointAndCoversSubject(t *testing.T) {
	t.Setenv(EnvJVM, "1")
	resolvers := NewRegistry().Resolvers()
	if len(resolvers) != 3 {
		t.Fatalf("expected the three registrants under the opt-in, got %d", len(resolvers))
	}
	paths := []string{
		"main.go", "pkg/util.go", "pkg/util_test.go", "go.mod",
		// The mixed directory itself: one path per language in ONE directory.
		"mix/App.java", "mix/App.kt",
		"deep/nested/Thing.java", "deep/nested/Thing.kt",
		// Owned by nobody.
		"readme.md", "mix/notes.md", "build.gradle", "src/App.kts", "a.gotmpl",
	}
	for _, p := range paths {
		var owners []string
		for _, r := range resolvers {
			if r.Subject(p) && !r.Owns(p) {
				t.Errorf("SUPERSET violated: %s.Subject(%q) is true but Owns is false — "+
					"the pass would emit edges from a file it can never sweep", r.Language(), p)
			}
			if r.Owns(p) {
				owners = append(owners, r.Language())
			}
		}
		if len(owners) > 1 {
			t.Errorf("DISJOINT violated: %q is owned by %v — each would sweep the other's edges", p, owners)
		}
	}
	// Non-vacuity: the mixed directory really does split across two owners, so
	// the disjointness check above is exercising the case it exists for.
	var javaOwns, kotlinOwns bool
	for _, r := range resolvers {
		if r.Language() == "java" {
			javaOwns = r.Owns("mix/App.java") && !r.Owns("mix/App.kt")
		}
		if r.Language() == "kotlin" {
			kotlinOwns = r.Owns("mix/App.kt") && !r.Owns("mix/App.java")
		}
	}
	if !javaOwns || !kotlinOwns {
		t.Fatalf("a mixed directory must partition into two sweep units: java=%v kotlin=%v", javaOwns, kotlinOwns)
	}
}

// TestCapabilities_JVMOptInFlip is the C8 gate's "the flip works" half: with
// the opt-in env var set, the registry holds java+kotlin, and tearing the
// opt-in down (t.Setenv auto-restores on cleanup) returns the registry to the
// default-off shape. The test is built around three explicit state probes
// (off → on → off) so the assertion has DEMONSTRATED-RED CAPABILITY — if the
// registration logic were inverted (always-on or always-off), at least one
// probe would fire. A test that only checks the post-flip state would pass
// vacuously under an "always register" bug (the SW-152 / SW-170 lesson:
// green-by-default is not a gate).
//
// The test deliberately stops at the product-wide registry boundary. The
// surface-level end-to-end proof (CLI / MCP print java+kotlin at
// typed-confirmed) lives at surfaces/client/capability_test.go's
// TestCapabilities_JVMOptInFlip — that one walks through languageCapabilities
// to the trust surface, this one pins the seam itself, the place the
// registration actually happens and the place a future regression would
// touch first.
func TestCapabilities_JVMOptInFlip(t *testing.T) {
	// Probe 1: shipped default is off. Without this baseline the post-flip
	// assertion would pass even if the registry were always-on.
	t.Setenv(EnvJVM, "")
	if got, want := Languages(), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pre-flip probe: Languages() = %v, want %v — the test's baseline "+
			"is not the shipped default-off posture", got, want)
	}

	// Probe 2: the flip. With the opt-in set the JVM registrants appear AND
	// the resolver set agrees (a registrant that registered in Languages()
	// but not in Resolvers() would be a phantom).
	t.Setenv(EnvJVM, "1")
	if got, want := Languages(), []string{"go", "java", "kotlin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-flip probe: Languages() = %v, want %v — the flip did NOT reach the registry", got, want)
	}
	byLang := map[string]bool{}
	for _, r := range NewRegistry().Resolvers() {
		byLang[r.Language()] = true
	}
	if !byLang["go"] || !byLang["java"] || !byLang["kotlin"] {
		t.Fatalf("post-flip probe: Resolvers() = %v — a language in Languages() is missing from Resolvers()", byLang)
	}

	// Probe 3: t.Setenv restores the pre-flip env on cleanup, so the
	// post-cleanup state is re-asserted by the next test (TestDefaultOff /
	// TestRegistry_*) running with EnvJVM="" / EnvJVM="1" as its own
	// t.Setenv. The cleanup contract is the test's "tear down to default-OFF"
	// the re-scope requires.
}

// TestCapabilities_JVMOptInFlip_KillSwitch is the C8 gate's kill-switch
// sibling: the opt-out (EnvJVM = "0") unregisters the JVM registrants AFTER
// they have been registered under the opt-in. It runs both directions in one
// test so the kill switch is exercised as the *sequence* it is in practice
// (a user turns the binder on for measurement, then off again before
// committing). The test is the demonstration that the env var is the single
// switch — flipping it is enough to flip the registry — and that the kill
// path is symmetric with the live path, not a separate code path with a
// separate bug surface.
//
// DEMONSTRATED-RED CAPABILITY. If the kill switch were removed (e.g.
// `jvmEnabled()` switched from `v != "" && v != "0"` to just `v != ""`),
// Probe 3 below would fail with the JVM registrants still in the registry.
// If the flip logic were inverted (always-on), Probe 1 would fail with the
// JVM registrants in Languages() before the env was set.
func TestCapabilities_JVMOptInFlip_KillSwitch(t *testing.T) {
	// Probe 1: baseline — default off, no JVM in the registry.
	if got, want := Languages(), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("baseline probe: Languages() = %v, want %v", got, want)
	}

	// Probe 2: live — opt-in registers java+kotlin.
	t.Setenv(EnvJVM, "1")
	if got, want := Languages(), []string{"go", "java", "kotlin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("live probe: Languages() = %v, want %v — the opt-in did NOT register the JVM binders", got, want)
	}
	languagesWhileLive := append([]string(nil), Languages()...)
	sort.Strings(languagesWhileLive)

	// Probe 3: kill switch — set the env to the explicit "0" form (the
	// documented kill switch for the experimental binder), and verify the
	// JVM registrants drop out of the registry in the SAME process. This is
	// the demonstrated-red half: a kill switch that no longer kills makes
	// this assertion fire, not silently leave a phantom registrant behind.
	t.Setenv(EnvJVM, "0")
	if got, want := Languages(), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("kill-switch probe: Languages() = %v, want %v — the kill switch (EnvJVM=0) did NOT unregister the JVM binders",
			got, want)
	}
	// Sanity: the live registry's union was genuinely non-trivial. Without
	// this check a "kill switch" test that started with a degenerate live
	// state could pass under any kill-switch bug.
	if !contains(languagesWhileLive, "java") || !contains(languagesWhileLive, "kotlin") {
		t.Fatalf("kill-switch test was not exercising a non-trivial live state: live Languages() = %v "+
			"(the test is vacuous if the live state never included java+kotlin)", languagesWhileLive)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
