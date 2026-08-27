package conformance

import (
	"fmt"
	"sort"
	"sync"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/opcatalog"
)

// registryName is the short name the typed lifecycle errors carry. The
// vocabulary is SW-222's, reused rather than re-invented: "you asked for a port
// this contribution does not have" is registry.ErrMissingDependency, not a new
// error kind.
const registryName = "conformance"

// Host is what a contribution's handler reaches the graph through.
//
// There is exactly one method, and it is the whole point of the type: a
// contribution cannot hold a service it did not ask for by name. A handler that
// closed over a query service directly would be honest only by inspection, and
// port honesty is precisely the property this harness has to be able to fail.
//
// The handle is returned as `any` deliberately. Typing it would mean this package
// knowing every engine service's Go type, which would grow a dependency on
// every seam a port names and make the harness unimportable from the place a
// pack author sits. The host registers the handles; the contribution asserts the
// type it expects. What the harness owns is the AUTHORISATION, not the shape.
type Host interface {
	// Use authorises access to one declared port and returns the handle the host
	// registered for it. It fails when the port was not declared.
	Use(port opcatalog.Port) (any, error)
}

// Ports is the set of handles a host makes available to a contribution under
// test. A port with no handle is a HARNESS setup gap, reported as one, rather
// than a contribution failure — the two are told apart because confusing them
// would let a missing fixture read as a broken extension.
type Ports map[opcatalog.Port]any

// gate is the Host implementation the harness runs a contribution behind.
//
// It records every request — granted or refused — before answering it. That
// ordering is the enforcement: a handler that ignores the returned error, or
// recovers from it, has still been recorded, so `port-honesty` fails on what the
// contribution DID rather than on whether it admitted to it.
type gate struct {
	mu       sync.Mutex
	declared map[opcatalog.Port]bool
	handles  Ports
	used     map[opcatalog.Port]bool
	refused  map[opcatalog.Port]bool
	missing  map[opcatalog.Port]bool
}

func newGate(declared []opcatalog.Port, handles Ports) *gate {
	g := &gate{
		declared: make(map[opcatalog.Port]bool, len(declared)),
		handles:  handles,
		used:     map[opcatalog.Port]bool{},
		refused:  map[opcatalog.Port]bool{},
		missing:  map[opcatalog.Port]bool{},
	}
	for _, p := range declared {
		g.declared[p] = true
	}
	return g
}

// Use implements Host.
func (g *gate) Use(port opcatalog.Port) (any, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.declared[port] {
		g.refused[port] = true
		return nil, registry.Errorf(registry.ErrMissingDependency, registryName, "Use", string(port),
			"%s: port %q was not declared by this contribution; a contribution reaches only the ports "+
				"its spec lists, and the list is what the permission projection is derived from",
			registryName, port)
	}
	handle, ok := g.handles[port]
	if !ok {
		g.missing[port] = true
		return nil, registry.Errorf(registry.ErrMissingDependency, registryName, "Use", string(port),
			"%s: the harness has no handle registered for declared port %q — supply one in "+
				"Contribution.Ports, or the run proves nothing about that port", registryName, port)
	}
	g.used[port] = true
	return handle, nil
}

// merge folds one run's observations into an accumulating gate view.
type portObservations struct {
	used    map[opcatalog.Port]bool
	refused map[opcatalog.Port]bool
	missing map[opcatalog.Port]bool
}

func newObservations() *portObservations {
	return &portObservations{
		used:    map[opcatalog.Port]bool{},
		refused: map[opcatalog.Port]bool{},
		missing: map[opcatalog.Port]bool{},
	}
}

func (o *portObservations) absorb(g *gate) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for p := range g.used {
		o.used[p] = true
	}
	for p := range g.refused {
		o.refused[p] = true
	}
	for p := range g.missing {
		o.missing[p] = true
	}
}

// verdict turns the accumulated observations into the port-honesty result.
//
// It fails in three directions, and each is a different lie:
//
//	refused   the contribution reached for a port it did not declare — the
//	          undeclared-access case AC-3 names.
//	missing   a declared port had no handle, so the run did not actually
//	          exercise it. This is the harness's own gap and says so.
//	unused    a declared port was never touched across the whole fixture suite.
//	          Over-declaration is the quieter half of the same dishonesty: the
//	          permission set a surface shows a user is DERIVED from the port
//	          list, so a port declared and never used asks the user to grant
//	          something the code does not need.
func (o *portObservations) verdict(declared []opcatalog.Port) error {
	if len(o.refused) > 0 {
		return fmt.Errorf("%s: the handler reached for undeclared port(s) %s; declared: %s",
			registryName, portList(keysOf(o.refused)), portList(declared))
	}
	if len(o.missing) > 0 {
		return fmt.Errorf("%s: no handle was registered for declared port(s) %s, so this run does not "+
			"exercise them", registryName, portList(keysOf(o.missing)))
	}
	var unused []opcatalog.Port
	for _, p := range declared {
		if !o.used[p] {
			unused = append(unused, p)
		}
	}
	if len(unused) > 0 {
		return fmt.Errorf("%s: declared port(s) %s were never used across the fixture suite; a port "+
			"declared and not needed asks a user to grant access the contribution does not take",
			registryName, portList(unused))
	}
	return nil
}

func keysOf(m map[opcatalog.Port]bool) []opcatalog.Port {
	out := make([]opcatalog.Port, 0, len(m))
	for p := range m {
		out = append(out, p)
	}
	return out
}

func portList(ports []opcatalog.Port) string {
	names := make([]string, 0, len(ports))
	for _, p := range ports {
		names = append(names, string(p))
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "(none)"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}
