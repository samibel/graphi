package module

import (
	"fmt"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/deadcode"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/opcatalog"
)

// Built-in module ids. They are stable identifiers: a module id appears in the
// composition inventory and in another module's Requires, so renaming one is a
// contract change, not a refactor.
const (
	// IDParse contributes the built-in CGo-free parser set.
	IDParse = "core.parse"
	// IDAnalysis contributes the built-in analyzer set.
	IDAnalysis = "engine.analysis"
	// IDDeadCode contributes the dead_code operation — spec AND handler — the
	// first built-in module whose operation runs in engine (SW-255 / AX-15).
	IDDeadCode = "engine.deadcode"
	// IDOperations contributes the operation catalog: every spec no
	// handler-bearing module claims (55 of 56 after AX-15).
	IDOperations = "engine.operations"
)

// handlerBearing lists the operation ids a built-in module contributes WITH a
// handler, so engine.operations knows which specs are no longer its to
// contribute. It is a list of what moved and not a predicate, for the same
// reason surfaces/client's migratedOperations is: moving an operation into
// its own module is a deliberate act that brings its own parity evidence.
var handlerBearing = map[string]string{
	deadcode.Operation: IDDeadCode,
}

// builtinVersion is the contract version every built-in module carries. The
// built-ins version together because they ship together — they are one binary,
// not independently released artifacts, and pretending otherwise would put a
// version number nobody maintains into the inventory.
const builtinVersion = "1"

// Builtins returns graphi's built-in module set, already added and validated.
//
// Four modules today, and the dependency edges are real rather than
// decorative: the operation catalog is ordered LAST because it is the inventory
// of what the capability modules registered. Composing it before the parsers and
// analyzers exist would mean advertising operations over registries that had not
// been populated yet — the ordering states that dependency instead of leaving it
// to the order somebody happened to write the calls in.
//
// engine.deadcode (SW-255 / AX-15) is the first module that contributes a spec
// TOGETHER WITH its handler. It requires the same two capability modules the
// catalog module requires, for the same reason, and composes before
// engine.operations by the lexicographic tie-break — which also means that if
// engine.operations ever stopped skipping dead_code, Build would fail with
// registry.ErrDuplicate naming both modules rather than silently double-
// registering the spec.
func Builtins() (*Set, error) {
	s := NewSet()
	for _, m := range []Module{
		{
			Manifest: Manifest{ID: IDParse, Version: builtinVersion},
			Register: func(b *Builder) error {
				for _, p := range parse.DefaultParsers() {
					if err := b.AddParser(p); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			Manifest: Manifest{ID: IDAnalysis, Version: builtinVersion},
			Register: func(b *Builder) error {
				in := b.Inputs()
				for _, a := range analysis.DefaultAnalyzers(in.Reader, in.WatchProvider) {
					if err := b.AddAnalyzer(a); err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			Manifest: Manifest{ID: IDDeadCode, Version: builtinVersion, Requires: []string{IDParse, IDAnalysis}},
			Register: func(b *Builder) error {
				shadow, err := opcatalog.Shadow()
				if err != nil {
					return err
				}
				// The spec is READ from the catalog, not retyped: a module that
				// restated the operation's metadata would be a second source of
				// truth, which is what the catalog exists to remove.
				spec, ok := shadow.Lookup(deadcode.Operation)
				if !ok {
					return fmt.Errorf("%s: the operation catalog does not declare %q", registryName, deadcode.Operation)
				}
				return b.AddOperationContribution(OperationContribution{
					Spec: spec,
					Bind: func(p Ports) (OperationHandler, error) {
						return deadcode.Handler(p.GraphQuery, p.GraphSearch), nil
					},
				})
			},
		},
		{
			Manifest: Manifest{ID: IDOperations, Version: builtinVersion, Requires: []string{IDParse, IDAnalysis}},
			Register: func(b *Builder) error {
				shadow, err := opcatalog.Shadow()
				if err != nil {
					return err
				}
				for _, spec := range shadow.All() {
					if _, moved := handlerBearing[spec.ID]; moved {
						continue // contributed, with its handler, by its own module
					}
					if err := b.AddOperation(spec); err != nil {
						return err
					}
				}
				return nil
			},
		},
	} {
		if err := s.Add(m); err != nil {
			return nil, err
		}
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return s, nil
}

// BuildBuiltins is the one-call form the composition root uses: the built-in
// set, validated, ordered, registered and frozen.
func BuildBuiltins(in Inputs) (*Composition, error) {
	s, err := Builtins()
	if err != nil {
		return nil, err
	}
	return s.Build(in)
}
