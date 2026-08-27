package parse

// RegisterDefaults registers graphi's built-in CGo-free parsers onto r and returns
// r for chaining. Registration is ONE LINE PER LANGUAGE: each constructor wires its
// parser to its SymbolExtractor (the Go path uses the reference goSymbolExtractor),
// so adding a tier-1 language is a single r.Register(NewXxxParser()) line — no
// parser is a special case (SW-052 STEP-0 contract).
//
// The default tier is strictly CGo-free: only pure-Go parsers are registered here.
// Additional tier-1 tree-sitter grammars (sourced from the pure-Go gotreesitter
// runtime + its embedded grammar blobs, selected at build time via subset build
// tags — see internal/release.DefaultGrammarSubsetTags; frozen list in
// bench/lang-budget.md) and the opt-in CGO graphi-broad bundle (behind a build
// tag) plug in through this same seam without editing the existing registrations.
// (Corrected per EP-009-REPLAN-001: go-sitter-forest is entirely CGO and has no
// pure-Go subset; the default tier draws from gotreesitter, not go-sitter-forest.)
func RegisterDefaults(r *Registry) *Registry {
	for _, p := range DefaultParsers() {
		r.Register(p)
	}
	return r
}

// DefaultParsers returns the built-in CGo-free parser set in REGISTRATION
// ORDER — the order RegisterDefaults applies them in, which under this
// registry's last-wins CollisionPolicy is the order that decides which parser
// owns a shared extension.
//
// It exists so a caller can enumerate the built-in set instead of only being
// able to install it (SW-227 / AX-07): the built-in module set contributes these
// parsers one at a time through the module builder, and it can only do that if
// the list is a value rather than a side effect. RegisterDefaults is defined in
// terms of it, so there is exactly one list and the two can never disagree.
func DefaultParsers() []Parser {
	return []Parser{
		NewGoParser(),         // go  — reference SymbolExtractor (go/ast, CGo-free)
		NewJSONParser(),       // json — stdlib structural parser (CGo-free)
		NewTSParser(),         // typescript — pure-Go tree-sitter grammar (CGo-free)
		NewJavaScriptParser(), // javascript — pure-Go gotreesitter grammar (CGo-free)
		NewTSXParser(),        // tsx — pure-Go gotreesitter grammar (CGo-free)
		NewPythonParser(),     // python — pure-Go gotreesitter grammar (CGo-free)
		NewJavaParser(),       // java — pure-Go gotreesitter grammar (CGo-free)
		NewCParser(),          // c — pure-Go gotreesitter grammar (CGo-free)
		NewRubyParser(),       // ruby — pure-Go gotreesitter grammar (CGo-free)
		NewRustParser(),       // rust — pure-Go gotreesitter grammar (CGo-free)
		NewPHPParser(),        // php — pure-Go gotreesitter grammar (CGo-free)
		NewCSharpParser(),     // c_sharp — pure-Go gotreesitter grammar (CGo-free)
		NewKotlinParser(),     // kotlin — pure-Go gotreesitter grammar (CGo-free)
		NewCppParser(),        // cpp — pure-Go gotreesitter grammar (CGo-free)
		NewBashParser(),       // bash — pure-Go gotreesitter grammar (CGo-free)
		NewSQLParser(),        // sql — pure-Go gotreesitter grammar (CGo-free)
		NewLuaParser(),        // lua — pure-Go gotreesitter grammar (CGo-free)
		// HTML is DEFERRED to graphi-broad (SW-056): its pure-Go gotreesitter grammar is
		// present, but its shared scanner core is co-located in the upstream
		// blade_scanner.go (gated grammar_subset_blade), so a subset build with only
		// grammar_subset_html fails to compile, and enabling grammar_subset_blade would
		// embed an unregistered blade.bin blob (prohibited by AC#4). Re-evaluate when the
		// upstream gotreesitter subset packaging splits the html scanner core out.
		NewCSSParser(),      // css — pure-Go gotreesitter grammar (CGo-free)
		NewYAMLParser(),     // yaml — pure-Go gotreesitter grammar (CGo-free)
		NewTOMLParser(),     // toml — pure-Go gotreesitter grammar (CGo-free)
		NewMarkdownParser(), // markdown — pure-Go gotreesitter grammar (CGo-free)
		NewHCLParser(),      // hcl — pure-Go gotreesitter grammar (CGo-free)
	}
}

// NewDefaultRegistry returns a Registry pre-loaded with the built-in parsers,
// FROZEN (SW-222 / AX-02).
//
// The built-in parser set is complete the moment this returns — nothing in the
// product registers a parser afterwards — so this is where "the runtime finished
// composing this registry" happens, and freezing here means every runtime path
// inherits the guarantee without a freeze call of its own. A Register after this
// point returns a registry.ErrFrozen-typed error.
//
// A caller that needs to keep registering (the graphi-broad opt-in seam, a test
// planting an offender for an anti-vacuity guard) composes the mutable form
// directly: RegisterDefaults(NewRegistry()).
func NewDefaultRegistry() *Registry {
	r := RegisterDefaults(NewRegistry())
	r.Freeze()
	return r
}
