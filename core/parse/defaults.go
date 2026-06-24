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
	r.Register(NewGoParser())         // go  — reference SymbolExtractor (go/ast, CGo-free)
	r.Register(NewJSONParser())       // json — stdlib structural parser (CGo-free)
	r.Register(NewTSParser())         // typescript — pure-Go tree-sitter grammar (CGo-free)
	r.Register(NewJavaScriptParser()) // javascript — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewTSXParser())        // tsx — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewPythonParser())     // python — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewJavaParser())       // java — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewCParser())          // c — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewRubyParser())       // ruby — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewRustParser())       // rust — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewPHPParser())        // php — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewCSharpParser())     // c_sharp — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewKotlinParser())     // kotlin — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewCppParser())        // cpp — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewBashParser())       // bash — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewSQLParser())        // sql — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewLuaParser())        // lua — pure-Go gotreesitter grammar (CGo-free)
	// HTML is DEFERRED to graphi-broad (SW-056): its pure-Go gotreesitter grammar is
	// present, but its shared scanner core is co-located in the upstream
	// blade_scanner.go (gated grammar_subset_blade), so a subset build with only
	// grammar_subset_html fails to compile, and enabling grammar_subset_blade would
	// embed an unregistered blade.bin blob (prohibited by AC#4). Re-evaluate when the
	// upstream gotreesitter subset packaging splits the html scanner core out.
	r.Register(NewCSSParser())      // css — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewYAMLParser())     // yaml — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewTOMLParser())     // toml — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewMarkdownParser()) // markdown — pure-Go gotreesitter grammar (CGo-free)
	r.Register(NewHCLParser())      // hcl — pure-Go gotreesitter grammar (CGo-free)
	return r
}

// NewDefaultRegistry returns a Registry pre-loaded with the built-in parsers.
func NewDefaultRegistry() *Registry {
	return RegisterDefaults(NewRegistry())
}
