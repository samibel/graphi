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
		path                     string
		subject, input, triggers bool
	}{
		{"main.go", true, true, true},
		{"pkg/util.go", true, true, true},
		// Test files steer GroupPackages' skip bookkeeping (input) but are
		// neither checked (subject) nor able to change the result (triggers).
		{"main_test.go", false, true, false},
		{"pkg/util_test.go", false, true, false},
		// go.mod steers import resolution (input, triggers) but is never a
		// subject: a repo with only a go.mod has nothing to check.
		{"go.mod", false, true, true},
		// A nested go.mod is NOT the module file the resolver reads
		// (files["go.mod"]); it matches none of the predicates.
		{"sub/go.mod", false, false, false},
		{"readme.md", false, false, false},
		{"a.java", false, false, false},
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
func (stubResolver) Resolve(map[string][]byte, map[model.NodeId]struct{}) (Result, error) {
	return Result{}, nil
}
