package module

import (
	"github.com/samibel/graphi/core/parse"
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
	// IDOperations contributes the operation catalog.
	IDOperations = "engine.operations"
)

// builtinVersion is the contract version every built-in module carries. The
// built-ins version together because they ship together — they are one binary,
// not independently released artifacts, and pretending otherwise would put a
// version number nobody maintains into the inventory.
const builtinVersion = "1"

// Builtins returns graphi's built-in module set, already added and validated.
//
// Three modules today, and the dependency edges are real rather than
// decorative: the operation catalog is ordered LAST because it is the inventory
// of what the capability modules registered. Composing it before the parsers and
// analyzers exist would mean advertising operations over registries that had not
// been populated yet — the ordering states that dependency instead of leaving it
// to the order somebody happened to write the calls in.
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
			Manifest: Manifest{ID: IDOperations, Version: builtinVersion, Requires: []string{IDParse, IDAnalysis}},
			Register: func(b *Builder) error {
				shadow, err := opcatalog.Shadow()
				if err != nil {
					return err
				}
				for _, spec := range shadow.All() {
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
