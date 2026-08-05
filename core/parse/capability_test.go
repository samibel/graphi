package parse

import (
	"context"
	"testing"
)

// TestEveryDefaultParserDeclaresSymbolCapability is the completeness guard, and
// the whole reason the SymbolCapable declaration exists rather than being
// inferred.
//
// The P1 capability matrix (engine/trust) reports, per language, whether graphi
// can confirm relationships by type-checking, resolve them across files, see
// them only within one file, or merely parse the structure. The bottom two
// levels turn on whether a parser extracts symbols at all — a fact only the
// parser knows, since its extractor is unexported.
//
// A new language whose parser forgets the declaration must NOT silently receive
// a guessed level on a trust surface. This test is what makes that impossible:
// registration and declaration land together or CI is red.
func TestEveryDefaultParserDeclaresSymbolCapability(t *testing.T) {
	r := NewDefaultRegistry()
	if missing := UndeclaredSymbolCapability(r); len(missing) != 0 {
		t.Fatalf("parsers registered without a SymbolCapable declaration: %v\n"+
			"add `func (*XxxParser) ExtractsSymbols() bool { … }` — the trust capability matrix "+
			"must never guess a level for a registered language", missing)
	}
}

// TestUndeclaredSymbolCapability_CatchesAPlantedOffender proves the guard is not
// vacuous, mirroring AssertPureGoDefaults' planted-offender discipline
// (guard.go): the same code path that accepts the real defaults must reject a
// parser that skips the declaration.
func TestUndeclaredSymbolCapability_CatchesAPlantedOffender(t *testing.T) {
	r := NewDefaultRegistry()
	r.Register(undeclaredParser{})
	missing := UndeclaredSymbolCapability(r)
	if len(missing) != 1 || missing[0] != "planted-undeclared" {
		t.Fatalf("guard did not catch the planted offender: %v", missing)
	}
}

// TestSymbolCapabilityDeclarations pins the two ends of the scale against the
// real registry. Go extracts symbols (the reference SymbolExtractor); JSON is
// the structural outlier — it parses to an AST and emits no symbol nodes, and
// it is the one default parser with no extractor at all.
func TestSymbolCapabilityDeclarations(t *testing.T) {
	r := NewDefaultRegistry()
	for _, tc := range []struct {
		lang string
		want bool
	}{
		{"go", true},
		{"json", false},
		{"python", true},
		{"typescript", true},
	} {
		p, err := r.ParserForLang(tc.lang)
		if err != nil {
			t.Errorf("ParserForLang(%q): %v", tc.lang, err)
			continue
		}
		sc, ok := p.(SymbolCapable)
		if !ok {
			t.Errorf("%s parser does not declare SymbolCapable", tc.lang)
			continue
		}
		if got := sc.ExtractsSymbols(); got != tc.want {
			t.Errorf("%s ExtractsSymbols() = %v, want %v", tc.lang, got, tc.want)
		}
	}
}

// undeclaredParser is a minimal Parser that deliberately omits the
// SymbolCapable declaration — the planted offender above.
type undeclaredParser struct{}

func (undeclaredParser) Language() string     { return "planted-undeclared" }
func (undeclaredParser) Extensions() []string { return []string{".plantedundeclared"} }
func (undeclaredParser) Runtime() Runtime     { return RuntimeStdlib }
func (undeclaredParser) Parse(_ context.Context, _ string, _ []byte) (*ParseResult, error) {
	return &ParseResult{}, nil
}
