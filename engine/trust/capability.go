package trust

import "sort"

// This file is the P1 capability matrix (PRD v1.0 §3 "Capability Matrix", §5
// "Capability Level", §8 Phase 10). It answers, per language and machine-
// readably, how strong the evidence graphi can produce for that language is —
// so a reader never has to infer from a result's shape whether a missing edge
// means "no such relationship" or "this language cannot express that
// relationship here".
//
// Everything below is DERIVED from the live registries at call time. Nothing is
// hand-maintained, because PRD v1.0 §8 Phase 10 requires the matrix be bound
// "mit automatisiertem Drift-Test an die tatsächlichen Language Capabilities":
// a copied table would be correct exactly until the next grammar lands.
//
// The three sources each own their fact:
//   - typeresolve.Languages() — which languages can be type-checked
//   - link.Linker.Languages() — which languages have a cross-file resolver
//   - parse.Registry + parse.SymbolCapable — which languages extract symbols
//
// This package deliberately does NOT import them. engine/trust is the pure
// domain core (see the layering note in types.go); the surface rank passes the
// three inputs in, exactly as it already passes freshness and store facts.

// CapabilityLevel is the closed capability vocabulary of PRD v1.0 §5. It grades
// the strongest evidence a language integration can produce — not how well the
// language is supported overall, and not a quality score.
type CapabilityLevel string

const (
	// CapabilityTypedConfirmed — a type checker proves relationships, so edges
	// can reach the `confirmed` tier. Go only, today.
	CapabilityTypedConfirmed CapabilityLevel = "typed-confirmed"
	// CapabilityCrossFileHeuristic — a resolver links references across files
	// and packages, but nothing proves them: edges stay `heuristic` (or
	// `derived` within a package).
	CapabilityCrossFileHeuristic CapabilityLevel = "cross-file-heuristic"
	// CapabilityIntraFileOnly — symbols and their relationships are extracted
	// within a single file; no cross-file reference is ever resolved.
	CapabilityIntraFileOnly CapabilityLevel = "intra-file-only"
	// CapabilityParseOnly — the source parses and its structure is recorded,
	// but no symbol nodes and no relationships are produced.
	CapabilityParseOnly CapabilityLevel = "parse-only"
)

// validCapabilityLevels is the closed set membership check, mirroring the
// closed-enum discipline the rest of this package applies to tiers, verdicts
// and states.
var validCapabilityLevels = map[CapabilityLevel]struct{}{
	CapabilityTypedConfirmed:     {},
	CapabilityCrossFileHeuristic: {},
	CapabilityIntraFileOnly:      {},
	CapabilityParseOnly:          {},
}

// Valid reports whether l is one of the four closed levels.
func (l CapabilityLevel) Valid() bool {
	_, ok := validCapabilityLevels[l]
	return ok
}

// Capability is one language's row in the matrix.
type Capability struct {
	Language string          `json:"language"`
	Level    CapabilityLevel `json:"level"`
}

// CapabilityInputs are the three registry observations the matrix is derived
// from. The caller collects them from the packages that own each fact; this
// package only grades.
//
// SymbolExtraction maps language → whether its parser extracts symbols, and a
// language ABSENT from the map is one whose parser made no declaration. Absence
// is not "false": an undeclared parser is unclassifiable, and the derivation
// omits it rather than reporting a level it cannot support. core/parse's
// UndeclaredSymbolCapability guard exists so that case cannot arise in a
// shipped binary — this is the belt to its braces.
type CapabilityInputs struct {
	Languages        []string
	TypeChecked      []string
	CrossFileLinked  []string
	SymbolExtraction map[string]bool
}

// Capabilities derives the matrix, sorted by language.
//
// Grading is first-match-wins down the strength order, which is what makes the
// levels a ladder rather than a set of flags: Go has a parser, a resolver AND a
// type checker, and reporting it as `typed-confirmed` is the honest summary of
// the strongest evidence available for it.
//
// A language whose parser declared nothing is omitted entirely rather than
// guessed at. Omission is visible (the language is simply absent from the
// matrix); a guessed level would not be.
func Capabilities(in CapabilityInputs) []Capability {
	typed := setOf(in.TypeChecked)
	linked := setOf(in.CrossFileLinked)

	out := make([]Capability, 0, len(in.Languages))
	seen := make(map[string]struct{}, len(in.Languages))
	for _, lang := range in.Languages {
		if lang == "" {
			continue
		}
		if _, dup := seen[lang]; dup {
			continue
		}
		seen[lang] = struct{}{}

		var level CapabilityLevel
		switch {
		case contains(typed, lang):
			level = CapabilityTypedConfirmed
		case contains(linked, lang):
			level = CapabilityCrossFileHeuristic
		default:
			extracts, declared := in.SymbolExtraction[lang]
			if !declared {
				// Fail closed: no declaration, no row. Never a default level.
				continue
			}
			if extracts {
				level = CapabilityIntraFileOnly
			} else {
				level = CapabilityParseOnly
			}
		}
		out = append(out, Capability{Language: lang, Level: level})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out
}

func setOf(xs []string) map[string]struct{} {
	s := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		s[x] = struct{}{}
	}
	return s
}

func contains(s map[string]struct{}, x string) bool {
	_, ok := s[x]
	return ok
}
