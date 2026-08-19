package typeresolve

import (
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// TestGoResolver_PathPredicates pins the exact path gates the ingest pass
// applied inline before the registry seam existed (WP-J0). Any drift here is a
// behavior change of the shipped Go pass, not a refactor.
func TestGoResolver_PathPredicates(t *testing.T) {
	r := goResolver{}
	cases := []struct {
		path                           string
		subject, input, triggers, owns bool
	}{
		{"main.go", true, true, true, true},
		{"pkg/util.go", true, true, true, true},
		// Test files steer GroupPackages' skip bookkeeping (input) but are
		// neither checked (subject) nor able to change the result (triggers).
		// They ARE owned: a node sourced there is Go's, and no other registrant
		// may claim it (ADR 0008 D9).
		{"main_test.go", false, true, false, true},
		{"pkg/util_test.go", false, true, false, true},
		// go.mod steers import resolution (input, triggers) but is never a
		// subject: a repo with only a go.mod has nothing to check. It mints no
		// node either, so it is outside the sweep domain.
		{"go.mod", false, true, true, false},
		// A nested go.mod is NOT the module file the resolver reads
		// (files["go.mod"]); it matches none of the predicates.
		{"sub/go.mod", false, false, false, false},
		{"readme.md", false, false, false, false},
		{"a.java", false, false, false, false},
	}
	for _, c := range cases {
		if got := r.Subject(c.path); got != c.subject {
			t.Errorf("Subject(%q) = %v, want %v", c.path, got, c.subject)
		}
		if got := r.Input(c.path); got != c.input {
			t.Errorf("Input(%q) = %v, want %v", c.path, got, c.input)
		}
		if got := r.Triggers(c.path); got != c.triggers {
			t.Errorf("Triggers(%q) = %v, want %v", c.path, got, c.triggers)
		}
		if got := r.Owns(c.path); got != c.owns {
			t.Errorf("Owns(%q) = %v, want %v", c.path, got, c.owns)
		}
	}
}

// TestGoResolver_OwnsNarrowingIsUnobservable pins the load-bearing half of the
// claim on goResolver.Owns: the ONLY thing D9's language key removes from the
// Go sweep's reach is a confirmed calls/references/implements edge whose
// from-node is sourced OUTSIDE a .go file, and under the shipped default no
// such edge can exist, because go/types is then the only registrant and it
// emits only from Go sources.
//
// It pins the predicate half mechanically. The "no other producer" half is a
// property of other packages and is stated where it belongs — engine/link's
// TierConfirmed prohibition (engine/link/link.go:60) and the ingest kind gate
// (typeresolveKind: calls/references/implements only, so `defines` and
// `notebook_cell` never enter the sweep).
func TestGoResolver_OwnsNarrowingIsUnobservable(t *testing.T) {
	r := goResolver{}
	// Every path the Go sweep could previously reach in a checked directory and
	// can no longer: none of them is a Go source, so none of them can be the
	// from-node of an edge this pass emitted.
	for _, p := range []string{"pkg/readme.md", "pkg/App.java", "pkg/App.kt", "pkg/notes.ipynb", "pkg/schema.sql"} {
		if r.Owns(p) {
			t.Errorf("Owns(%q) = true: a non-Go path must be outside the Go sweep domain", p)
		}
		if r.Subject(p) {
			t.Errorf("Subject(%q) = true: the Go pass cannot emit an edge from a non-Go file", p)
		}
	}
	// And the converse: every path the pass CAN emit from is still owned, so
	// the narrowing removes nothing the pass produces.
	for _, p := range []string{"main.go", "deep/nested/pkg/file.go"} {
		if !r.Subject(p) || !r.Owns(p) {
			t.Errorf("%q: Subject=%v Owns=%v, want both true", p, r.Subject(p), r.Owns(p))
		}
	}
}

// TestNewRegistry_GoIsTheOnlyRegistrant pins WP-J0's behavior-preservation
// premise: until a second language lands (WP-J3+), the registry holds exactly
// the go/types resolver, so the seam cannot change shipped behavior.
func TestNewRegistry_GoIsTheOnlyRegistrant(t *testing.T) {
	reg := NewRegistry()
	if got, want := reg.Languages(), []string{"go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Languages() = %v, want %v", got, want)
	}
	rs := reg.Resolvers()
	if len(rs) != 1 || rs[0].Language() != "go" {
		t.Fatalf("Resolvers() = %d entries, want exactly the go resolver", len(rs))
	}
	// The package-level accessor the trust surface consumes must be the
	// registry union — and a fresh copy each call, never shared state.
	a, b := Languages(), Languages()
	if !reflect.DeepEqual(a, []string{"go"}) {
		t.Fatalf("Languages() = %v, want [go]", a)
	}
	a[0] = "mutated"
	if b[0] != "go" || Languages()[0] != "go" {
		t.Fatal("Languages() must return a fresh copy, not shared backing storage")
	}
}

// TestRegistry_RegisterOrderAndOverride pins the open/closed contract:
// dispatch order is registration order, a re-registration overrides in place
// (keeping its position), and Languages() is sorted independently of order.
func TestRegistry_RegisterOrderAndOverride(t *testing.T) {
	reg := NewRegistry()
	reg.Register(stubResolver{lang: "kotlin"})
	reg.Register(stubResolver{lang: "java"})

	if got, want := reg.Languages(), []string{"go", "java", "kotlin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Languages() = %v, want %v (sorted)", got, want)
	}
	var order []string
	for _, r := range reg.Resolvers() {
		order = append(order, r.Language())
	}
	if want := []string{"go", "kotlin", "java"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("dispatch order = %v, want registration order %v", order, want)
	}

	// Override keeps position and count.
	reg.Register(stubResolver{lang: "kotlin", marker: "v2"})
	rs := reg.Resolvers()
	if len(rs) != 3 {
		t.Fatalf("override must not grow the registry: %d resolvers", len(rs))
	}
	if got := rs[1].(stubResolver).marker; got != "v2" {
		t.Fatalf("override must replace in place: marker %q, want %q", got, "v2")
	}
}

// stubResolver is a minimal registrant for registry-shape tests.
type stubResolver struct {
	lang   string
	marker string
}

func (s stubResolver) Language() string   { return s.lang }
func (stubResolver) Subject(string) bool  { return false }
func (stubResolver) Input(string) bool    { return false }
func (stubResolver) Triggers(string) bool { return false }
func (stubResolver) Owns(string) bool     { return false }
func (stubResolver) Resolve(map[string][]byte, map[model.NodeId]struct{}) (Result, error) {
	return Result{}, nil
}
