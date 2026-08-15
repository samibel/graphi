// Package semantic assembles the product-wide semantic-resolver registry
// (ADR 0007). The assembly CANNOT live in engine/typeresolve: per-language
// binders (engine/jvmresolve) import typeresolve for the seam types, so
// typeresolve registering them back would be an import cycle. This package
// imports both sides and owns the ONE registry the product consumes —
// engine/ingest dispatches its third phase over it, and the trust surface
// derives `typed-confirmed` from its Languages() union, so the capability a
// binary CLAIMS and the passes it RUNS can never disagree.
//
// THE JVM REGISTRANTS ARE DEFAULT-OFF (EnvJVM opt-in), deliberately:
// ADR 0008's decision points are open, no GA-LANG-* evidence row exists, and
// the language-GA program's measure-first rule (D2) needs the binder
// REACHABLE for measurement — not shipped-on. With the flag unset the
// registry holds exactly the go/types resolver and every shipped byte —
// graph, snapshot, trust report, capability matrix — is unchanged. Flipping
// the default is WP-J11 scope, behind green evidence rows; it is a ONE-LINE
// change here and it must never happen as a side effect of anything else.
//
// The env is read per NewRegistry call and is a process-lifetime setting:
// ingest and the trust report both assemble through this package, so within
// one process they observe the same answer.
package semantic

import (
	"os"

	"github.com/samibel/graphi/engine/jvmresolve"
	"github.com/samibel/graphi/engine/typeresolve"
)

// EnvJVM is the opt-in switch for the EXPERIMENTAL JVM declared-type binder
// (ADR 0008): any non-empty value other than "0" registers the java and
// kotlin resolvers. Distinct from the GRAPHI_NO_TYPERESOLVE* kill switches,
// which disable; this one enables — a capability toggle, not an escape hatch.
const EnvJVM = "GRAPHI_JVM_TYPERESOLVE"

func jvmEnabled() bool {
	v := os.Getenv(EnvJVM)
	return v != "" && v != "0"
}

// NewRegistry assembles the semantic-resolver registry for this process.
func NewRegistry() *typeresolve.Registry {
	r := typeresolve.NewRegistry()
	if jvmEnabled() {
		r.Register(jvmresolve.NewResolver(jvmresolve.LangJava))
		r.Register(jvmresolve.NewResolver(jvmresolve.LangKotlin))
	}
	return r
}

// Languages is the product-wide union the trust surface consumes to decide
// which languages report `typed-confirmed` — the single source the P1
// capability matrix binds to. A fresh, sorted copy.
func Languages() []string { return NewRegistry().Languages() }
