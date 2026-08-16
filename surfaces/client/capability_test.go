package client

import (
	"sort"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/link"
	"github.com/samibel/graphi/engine/semantic"
	"github.com/samibel/graphi/engine/trust"
)

// The P1 capability matrix (PRD v1.0 §3) must be bound "mit automatisiertem
// Drift-Test an die tatsächlichen Language Capabilities" (§8 Phase 10). These
// tests are that binding: every assertion is made against the LIVE registries,
// never against a copied expectation, so adding a grammar or a resolver moves
// the matrix — and a language that lands without a capability declaration fails
// the build rather than receiving a guessed level.

// TestCapabilities_CoverEveryRegisteredLanguage is the completeness half. Every
// parseable language gets exactly one row from the closed level set. A gap here
// means some language is silently missing from a surface whose whole job is to
// say what graphi can and cannot see.
func TestCapabilities_CoverEveryRegisteredLanguage(t *testing.T) {
	registered := parse.NewDefaultRegistry().Languages()
	caps := languageCapabilities()

	if len(caps) != len(registered) {
		t.Fatalf("matrix has %d rows for %d registered languages — every parseable language needs a row\nregistered: %v\nmatrix: %v",
			len(caps), len(registered), registered, caps)
	}
	byLang := make(map[string]trust.CapabilityLevel, len(caps))
	for _, c := range caps {
		if !c.Level.Valid() {
			t.Errorf("%s has level %q, outside the closed set", c.Language, c.Level)
		}
		if _, dup := byLang[c.Language]; dup {
			t.Errorf("%s appears twice in the matrix", c.Language)
		}
		byLang[c.Language] = c.Level
	}
	for _, lang := range registered {
		if _, ok := byLang[lang]; !ok {
			t.Errorf("registered language %q has no capability row", lang)
		}
	}
}

// TestCapabilities_MatchTheLiveRegistries is the correctness half: it
// recomputes the expected level for each language straight from the three
// registries and compares. This is what makes the matrix drift-proof — the
// expectation is not written down anywhere, it is re-derived.
func TestCapabilities_MatchTheLiveRegistries(t *testing.T) {
	typed := make(map[string]bool)
	// semantic.Languages() — NOT typeresolve.Languages() — is the product's
	// TypeChecked source (trust_report.go languageCapabilities). Deriving the
	// oracle from the same union is what makes "the gate and the product cannot
	// disagree" (ADR 0007) a proven property under the env-gated JVM opt-in, not
	// just under the default: with the binder off both unions are {go} and this
	// is unchanged; with it on the oracle tracks java/kotlin exactly as the
	// surface does, instead of falling a level behind at typeresolve's Go-only view.
	for _, l := range semantic.Languages() {
		typed[l] = true
	}
	linked := make(map[string]bool)
	for _, l := range link.New().Languages() {
		linked[l] = true
	}
	registry := parse.NewDefaultRegistry()

	for _, c := range languageCapabilities() {
		var want trust.CapabilityLevel
		switch {
		case typed[c.Language]:
			want = trust.CapabilityTypedConfirmed
		case linked[c.Language]:
			want = trust.CapabilityCrossFileHeuristic
		default:
			p, err := registry.ParserForLang(c.Language)
			if err != nil {
				t.Errorf("%s is in the matrix but has no parser: %v", c.Language, err)
				continue
			}
			extracts, known := parse.ExtractsSymbols(p)
			if !known {
				t.Errorf("%s is in the matrix but its parser declares no capability", c.Language)
				continue
			}
			want = trust.CapabilityIntraFileOnly
			if !extracts {
				want = trust.CapabilityParseOnly
			}
		}
		if c.Level != want {
			t.Errorf("%s level = %q, registries say %q", c.Language, c.Level, want)
		}
	}
}

// TestCapabilities_KnownAnchors pins the three levels that are load-bearing for
// how a reader interprets a result, at the languages that define them today.
// Unlike the tests above these ARE written-down expectations — deliberately, so
// that silently losing Go's type checker or JSON's structural-only status is
// caught as the product regression it would be, not absorbed as "the registries
// changed, so the matrix changed".
func TestCapabilities_KnownAnchors(t *testing.T) {
	got := make(map[string]trust.CapabilityLevel)
	for _, c := range languageCapabilities() {
		got[c.Language] = c.Level
	}
	for lang, want := range map[string]trust.CapabilityLevel{
		"go":         trust.CapabilityTypedConfirmed,     // go/types confirms edges
		"python":     trust.CapabilityCrossFileHeuristic, // resolver, no type checker
		"typescript": trust.CapabilityCrossFileHeuristic,
		"json":       trust.CapabilityParseOnly, // structural AST, no symbols
		"yaml":       trust.CapabilityIntraFileOnly,
		"css":        trust.CapabilityIntraFileOnly,
	} {
		if got[lang] != want {
			t.Errorf("%s = %q, want %q", lang, got[lang], want)
		}
	}
}

// TestCapabilities_JVMOptInFlip pins what the WP-J11 default-flip PRODUCES at
// the surface, so the flip is a provable one-liner and not a leap of faith: with
// the experimental JVM binder registered, the capability matrix the CLI and MCP
// print reports java AND kotlin at typed-confirmed — the same union semantic.
// Languages() feeds — while every other language keeps its level. This is the
// end-to-end proof that the env-gated wiring reaches the user-visible surface,
// the piece the semantic-package unit tests (which stop at Languages()) cannot
// give. It also guards the inverse: nothing here promotes a language the binder
// does not actually resolve.
func TestCapabilities_JVMOptInFlip(t *testing.T) {
	t.Setenv(semantic.EnvJVM, "1")

	got := make(map[string]trust.CapabilityLevel)
	for _, c := range languageCapabilities() {
		got[c.Language] = c.Level
	}

	// The two the opt-in resolves: the flip's whole payload.
	for _, lang := range []string{"java", "kotlin"} {
		if got[lang] != trust.CapabilityTypedConfirmed {
			t.Errorf("with the JVM binder on, %s = %q, want %q (the flip must reach the surface)",
				lang, got[lang], trust.CapabilityTypedConfirmed)
		}
	}
	// Go is unaffected — always typed-confirmed via go/types.
	if got["go"] != trust.CapabilityTypedConfirmed {
		t.Errorf("go = %q, want %q", got["go"], trust.CapabilityTypedConfirmed)
	}
	// A language the JVM binder does not touch must NOT be promoted: the opt-in
	// registers java+kotlin only, so python stays at its heuristic level.
	if got["python"] != trust.CapabilityCrossFileHeuristic {
		t.Errorf("python = %q, want %q — the opt-in must not promote unrelated languages",
			got["python"], trust.CapabilityCrossFileHeuristic)
	}
}

// TestCapabilities_Deterministic pins sorted, repeatable output. The document
// is byte-compared across CLI and MCP and across repeated runs; an unsorted map
// walk here would break both.
func TestCapabilities_Deterministic(t *testing.T) {
	first := languageCapabilities()
	for i := 0; i < 20; i++ {
		next := languageCapabilities()
		if len(next) != len(first) {
			t.Fatalf("run %d returned %d rows, first returned %d", i, len(next), len(first))
		}
		for j := range first {
			if next[j] != first[j] {
				t.Fatalf("run %d differs at %d: %+v vs %+v", i, j, next[j], first[j])
			}
		}
	}
	langs := make([]string, 0, len(first))
	for _, c := range first {
		langs = append(langs, c.Language)
	}
	if !sort.StringsAreSorted(langs) {
		t.Errorf("matrix is not sorted by language: %v", langs)
	}
}
