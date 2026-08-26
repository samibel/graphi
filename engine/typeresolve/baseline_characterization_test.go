package typeresolve

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// SW-222 (AX-02) characterization baseline for the SEMANTIC-RESOLVER registry.
//
// Written and made green BEFORE the registry-lifecycle refactor; must pass
// UNCHANGED after it. `typeresolve.Registry` is LAST-WINS but ORDER-STABLE: a
// re-registration for an already-registered language replaces the resolver and
// KEEPS the original dispatch position, because dispatch order is registration
// order and the ingest pass must stay deterministic.

type charResolver struct {
	lang string
	tag  string
}

func (r charResolver) Language() string            { return r.lang }
func (r charResolver) Subject(relPath string) bool { return false }
func (r charResolver) Input(relPath string) bool   { return false }
func (r charResolver) Owns(relPath string) bool    { return false }
func (r charResolver) Triggers(relPath string) bool {
	return false
}
func (r charResolver) Resolve(files map[string][]byte, committed map[model.NodeId]struct{}) (Result, error) {
	return Result{}, nil
}

// TestBaseline_TypeResolveRegistry_DefaultContents pins the shipped default:
// exactly one registrant, Go, from the go/types pass.
func TestBaseline_TypeResolveRegistry_DefaultContents(t *testing.T) {
	r := NewRegistry()
	if got := strings.Join(r.Languages(), ","); got != "go" {
		t.Fatalf("default semantic resolvers = %q, want %q", got, "go")
	}
	res := r.Resolvers()
	if len(res) != 1 || res[0].Language() != "go" {
		t.Fatalf("Resolvers() = %v, want exactly the go resolver", res)
	}
}

// TestBaseline_TypeResolveRegistry_RegistrationOrderIsDispatchOrder pins that
// Resolvers() returns registration order (NOT sorted order) while Languages()
// returns the sorted union.
func TestBaseline_TypeResolveRegistry_RegistrationOrderIsDispatchOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(charResolver{lang: "zeta"})
	r.Register(charResolver{lang: "alpha"})

	var order []string
	for _, res := range r.Resolvers() {
		order = append(order, res.Language())
	}
	if got := strings.Join(order, ","); got != "go,zeta,alpha" {
		t.Fatalf("dispatch order = %q, want registration order %q", got, "go,zeta,alpha")
	}
	if got := strings.Join(r.Languages(), ","); got != "alpha,go,zeta" {
		t.Fatalf("Languages() = %q, want the sorted union", got)
	}
}

// TestBaseline_TypeResolveRegistry_LastWinsKeepsPosition pins the override
// contract: a later registration for the same language supersedes the earlier
// resolver but does NOT move it in the dispatch order.
func TestBaseline_TypeResolveRegistry_LastWinsKeepsPosition(t *testing.T) {
	r := NewRegistry()
	r.Register(charResolver{lang: "toy", tag: "first"})
	r.Register(charResolver{lang: "other"})
	r.Register(charResolver{lang: "toy", tag: "second"})

	var order []string
	for _, res := range r.Resolvers() {
		order = append(order, res.Language())
	}
	if got := strings.Join(order, ","); got != "go,toy,other" {
		t.Fatalf("override moved the dispatch position: %q", got)
	}
	for _, res := range r.Resolvers() {
		if res.Language() != "toy" {
			continue
		}
		cr, ok := res.(charResolver)
		if !ok {
			t.Fatalf("unexpected resolver type %T", res)
		}
		if cr.tag != "second" {
			t.Fatalf("override did not win: tag = %q, want %q", cr.tag, "second")
		}
	}
}
