package parse

import "sort"

// SymbolCapable is the optional declaration a Parser makes about WHAT it
// extracts: symbol nodes and intra-file edges, or structure only.
//
// It is a sibling of Runtime() in role — a first-class inspection hook declared
// at the registration layer, carrying no per-call state and changing no parsing
// behavior. Runtime() exists so the no-CGO guard can assert purity without
// building; this exists so the P1 trust capability matrix (engine/trust) can
// report a language's capability level without opening a file.
//
// Why a declaration and not an inference: the fact lives in the parser (whether
// it wires a SymbolExtractor), that field is unexported, and there is no
// honest way to read it from outside. Inferring from Runtime() would be a
// coincidence — today JSON is the only stdlib-runtime parser AND the only
// structure-only one, but a future stdlib parser that did extract symbols would
// be misclassified, and a trust surface must not report a capability it cannot
// support.
//
// It is deliberately OPTIONAL at the type level (not a Parser method) so adding
// it breaks no third-party Parser implementation. Completeness is enforced
// where it matters instead: UndeclaredSymbolCapability fails the build for any
// parser registered in the default tier without it.
type SymbolCapable interface {
	// ExtractsSymbols reports whether this parser produces symbol nodes and
	// intra-file edges (true) or only structural AST metadata (false).
	// Implementations declare a constant.
	ExtractsSymbols() bool
}

// ExtractsSymbols reports a parser's declaration and whether it made one.
// known=false means the parser did not declare, and callers must treat the
// capability as unknown — never as either default. Fail closed: an undeclared
// parser is not "probably a symbol extractor".
func ExtractsSymbols(p Parser) (extracts, known bool) {
	sc, ok := p.(SymbolCapable)
	if !ok {
		return false, false
	}
	return sc.ExtractsSymbols(), true
}

// UndeclaredSymbolCapability returns the sorted languages of parsers registered
// in r that do NOT declare SymbolCapable. Empty means the registry is fully
// declared.
//
// This is the completeness guard behind the optional interface: registering a
// language and declaring its extraction capability must happen together, or the
// trust capability matrix would have to guess a level for it. It mirrors
// AssertPureGoDefaults (guard.go) — same registry walk, same one-offender-per-
// language deduplication, same sorted output — and, like it, is tag-independent
// because it inspects in-process registration state rather than the build.
func UndeclaredSymbolCapability(r *Registry) []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	parsers := make([]Parser, 0, len(r.byLang))
	for _, p := range r.byLang {
		parsers = append(parsers, p)
	}
	r.mu.RUnlock()

	seen := make(map[string]struct{})
	var missing []string
	for _, p := range parsers {
		if p == nil {
			continue
		}
		lang := p.Language()
		if _, dup := seen[lang]; dup {
			continue
		}
		seen[lang] = struct{}{}
		if _, known := ExtractsSymbols(p); !known {
			missing = append(missing, lang)
		}
	}
	sort.Strings(missing)
	return missing
}
