package parse

// RegisterDefaults registers graphi's built-in CGo-free parsers onto r and returns
// r for chaining. The Go-AST precision path and the stdlib JSON structural parser
// are both registered here through the ordinary Register path — no parser is a
// special case. Additional tier-1 tree-sitter grammars (and the opt-in CGO
// graphi-broad bundle behind a build tag) plug in through this same seam.
func RegisterDefaults(r *Registry) *Registry {
	r.Register(NewGoParser())
	r.Register(NewJSONParser())
	return r
}

// NewDefaultRegistry returns a Registry pre-loaded with the built-in parsers.
func NewDefaultRegistry() *Registry {
	return RegisterDefaults(NewRegistry())
}
