package parse

// RegisterDefaults registers graphi's built-in CGo-free parsers onto r and returns
// r for chaining. Registration is ONE LINE PER LANGUAGE: each constructor wires its
// parser to its SymbolExtractor (the Go path uses the reference goSymbolExtractor),
// so adding a tier-1 language is a single r.Register(NewXxxParser()) line — no
// parser is a special case (SW-052 STEP-0 contract).
//
// The default tier is strictly CGo-free: only pure-Go parsers are registered here.
// Additional tier-1 tree-sitter grammars (from the maintained pure-Go subset of
// go-sitter-forest, frozen in bench/lang-budget.md) and the opt-in CGO graphi-broad
// bundle (behind a build tag) plug in through this same seam without editing the
// existing registrations.
func RegisterDefaults(r *Registry) *Registry {
	r.Register(NewGoParser())   // go  — reference SymbolExtractor (go/ast, CGo-free)
	r.Register(NewJSONParser()) // json — stdlib structural parser (CGo-free)
	return r
}

// NewDefaultRegistry returns a Registry pre-loaded with the built-in parsers.
func NewDefaultRegistry() *Registry {
	return RegisterDefaults(NewRegistry())
}
