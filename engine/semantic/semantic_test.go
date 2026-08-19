package semantic

import (
	"reflect"
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
