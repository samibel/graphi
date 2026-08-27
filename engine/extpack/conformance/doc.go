// Package conformance is graphi's extension contract-test harness (SW-230 /
// AX-10): the checks an extension author runs to prove a contribution behaves
// the way graphi proves its own do.
//
// # Two subjects, one report
//
// The kernel has two things an author can contribute, and they fail in different
// ways, so the harness verifies each on its own terms and renders both into one
// Report:
//
//   - VerifyContribution takes an OPERATION contribution (ADR 0013 tier B: an
//     opcatalog.OperationSpec, the handler that runs it, and fixtures) and
//     checks the spec, its read-only permission envelope, host API
//     compatibility, that it can be projected onto the surfaces, that it is
//     deterministic, and that it touches only the host ports it declared.
//   - VerifyPack takes a DECLARATIVE PACK directory (tier A) and checks the
//     manifest and artifact schemas positionally, host API compatibility, that
//     merging it is byte-deterministic, and that every merged rule carries the
//     provenance a consumer renders.
//
// # The harness has to be able to fail, or it certifies nothing
//
// Every check here is proven in BOTH directions by conformance_selftest_test.go:
// a deliberately non-deterministic contribution and a contribution that reaches
// for an undeclared port must FAIL, and the honest control must PASS. That is the
// same discipline the rule-pack attack suite applies to fail-closed claims — a
// check that has never been observed failing is a check nobody has tested.
//
// The port gate is where this matters most. It records a violation BEFORE it
// returns the refusal, so a handler that swallows the error still fails the
// harness. A gate whose enforcement depended on the handler cooperating would be
// enforcing nothing.
//
// # Ports: the surface ADR 0013 deferred here
//
// ADR 0013 §6 declines to name the host ports and says "SW-230". This package
// answers the honesty half of that question rather than the plumbing half: the
// port VOCABULARY is engine/opcatalog's (named after the real seams in
// surfaces/client.Direct), and what the harness adds is that a contribution
// reaches every port THROUGH a gate keyed on its own declaration. Port
// signatures stay opaque to the harness on purpose — it hands back whatever
// handle the host registered — because a harness that knew each port's Go type
// would have to grow a dependency on every engine service, and would then be
// unusable from the place a pack author sits.
//
// # Layering and posture
//
// ENGINE rank. It depends on core/registry, engine/extpack, engine/opcatalog and
// the standard library, so every surface and every out-of-tree author can import
// it. It deliberately does NOT import a surface: the surface-projection check
// takes the projector as a parameter and validates what it renders, so the real
// MCP and HTTP projections are supplied by the caller (see
// surfaces/ax10_worked_example_test.go) instead of being re-implemented here as
// a second projection site.
//
// Nothing in this package opens a socket, runs a subprocess or evaluates
// anything a pack ships. It is a test harness for a read-only,
// zero-egress extension model and it holds itself to the same posture.
package conformance
