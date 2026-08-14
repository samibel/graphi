package coverage

// The GA LANGUAGE AXIS, machine-encoded (language-GA program §6 / WP-J1,
// ADR 0007). Until this check existed, docs/stability-tiers.md's GA
// conjunction carried "language = Go ← NOT encoded in the matrix": the one GA
// axis a prose edit could flip. A `category: ga-language` matrix row now
// declares a language GA at a stated capability level, and CheckGALanguages
// binds that declaration two ways:
//
//	(i)  REGISTRY: the declared capability level must equal the level the live
//	     registries derive for the language (the engine/trust ladder, assembled
//	     by surfaces/client.LanguageCapabilities — the SAME derivation the
//	     trust report serves, so the gate and the product cannot disagree).
//	(ii) EVIDENCE: every GA-LANG-<lang>-* row in docs/rc/evidence-index.yaml
//	     must read PASS with evidence URI and sha (the WP0 honesty rule), and a
//	     language other than go must HAVE such rows — a GA flip without
//	     evidence rows would be vacuously green. Go alone carries no
//	     GA-LANG-* rows: its GA evidence predates this mechanism (the P0/P1
//	     programs — docs/rc/focused-core-rc.md, the candidate freeze records,
//	     docs/rc/parity-classes.yaml) and is not re-keyed retroactively.
//
// The third binding the program names — demotion is legal but loud — needs
// run-over-run state (a removed row is invisible to a stateless check) and is
// carried by review + git history instead; this comment is the record of that
// choice.
//
// ga-language is deliberately NOT in codeDerivedCategories: bare registration
// is exactly what this check must NOT trust (a resolver can register and prove
// nothing — see the SQL note in docs/language-support.md). Its truth comes
// from the capability DERIVATION plus evidence, via this check alone.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/trust"
)

// GAEvidencePrefix prefixes the evidence-index gate ids that bind a language's
// GA claim: GA-LANG-<lang>-<gate> (language-GA program §2, gate ids G2..G9).
const GAEvidencePrefix = "GA-LANG-"

// gaGrandfatheredLanguage is the one language whose GA claim needs no
// GA-LANG-* evidence rows (see the package comment above).
const gaGrandfatheredLanguage = "go"

// GAEvidenceGate is one evidence-index row fact the GA-language check
// consumes: the gate id, and whether the row reads PASS with BOTH evidence URI
// and sha. The caller (cmd/coverage) folds internal/evidence's honesty rule
// into Passed so this package does not re-parse the index.
type GAEvidenceGate struct {
	ID     string
	Passed bool
}

// GALanguageReport is the outcome of CheckGALanguages.
type GALanguageReport struct {
	// Languages are the checked ga-language row ids, sorted.
	Languages []string
	// Violations, empty on pass, name each broken binding.
	Violations []string
}

// Pass reports whether every ga-language row is fully bound.
func (r GALanguageReport) Pass() bool { return len(r.Violations) == 0 }

// Format renders a deterministic, human-readable report for CI logs.
func (r GALanguageReport) Format() string {
	if r.Pass() {
		return fmt.Sprintf("ga-language check PASS — %d GA language(s) bound to the live capability derivation and the evidence index: %s.\n",
			len(r.Languages), strings.Join(r.Languages, ", "))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ga-language check FAILED — %d violation(s):\n", len(r.Violations))
	for _, v := range r.Violations {
		fmt.Fprintf(&b, "  - %s\n", v)
	}
	b.WriteString("A language becomes GA only with registries deriving its declared capability AND green GA-LANG-* evidence rows — never by editing prose or this matrix alone.\n")
	return b.String()
}

// CheckGALanguages verifies every `category: ga-language` matrix row against
// the live capability derivation and the evidence-index facts. An empty
// ga-language set passes vacuously (the matrix then simply encodes "no GA
// language", which the day-one go row makes unreachable in the checked-in
// tree).
func CheckGALanguages(matrix []Capability, derived []trust.Capability, gates []GAEvidenceGate) GALanguageReport {
	levelOf := map[string]trust.CapabilityLevel{}
	for _, c := range derived {
		levelOf[c.Language] = c.Level
	}

	var rep GALanguageReport
	seen := map[string]bool{}
	for _, row := range matrix {
		if row.Category != CategoryGALanguage {
			continue
		}
		if seen[row.ID] {
			rep.Violations = append(rep.Violations, fmt.Sprintf("duplicate ga-language row %q", row.ID))
			continue
		}
		seen[row.ID] = true
		rep.Languages = append(rep.Languages, row.ID)

		// A GA language must actually ship; planned/partial cannot be GA.
		if row.Status != StatusShipped {
			rep.Violations = append(rep.Violations, fmt.Sprintf("ga-language row %q has status %q (a GA language must be shipped)", row.ID, row.Status))
		}

		// (i) registry binding.
		lvl, ok := levelOf[row.ID]
		switch {
		case !ok:
			rep.Violations = append(rep.Violations, fmt.Sprintf("ga-language row %q: the live registries derive NO capability for this language (no parser declaration?)", row.ID))
		case string(lvl) != row.CapabilityLevel:
			rep.Violations = append(rep.Violations, fmt.Sprintf("ga-language row %q declares capability %q but the live registries derive %q — the declaration must follow the code, never lead it", row.ID, row.CapabilityLevel, lvl))
		}

		// (ii) evidence binding.
		prefix := GAEvidencePrefix + row.ID + "-"
		found := 0
		for _, g := range gates {
			if !strings.HasPrefix(g.ID, prefix) {
				continue
			}
			found++
			if !g.Passed {
				rep.Violations = append(rep.Violations, fmt.Sprintf("ga-language row %q: evidence gate %s does not read PASS with evidence URI and sha (UNKNOWN/STALE/FAIL count as not passed)", row.ID, g.ID))
			}
		}
		if found == 0 && row.ID != gaGrandfatheredLanguage {
			rep.Violations = append(rep.Violations, fmt.Sprintf("ga-language row %q: no %s* rows exist in the evidence index — a GA claim without evidence rows is vacuous (create them per the language-GA program §2, born UNKNOWN)", row.ID, prefix))
		}
	}
	sort.Strings(rep.Languages)
	sort.Strings(rep.Violations)
	return rep
}

// validateGALanguageRow is the parse-time validation LoadMatrix applies:
// the `capability` field is REQUIRED on ga-language rows, must come from
// engine/trust's closed level vocabulary, and is ILLEGAL anywhere else — a
// capability level on a parser row would be a second, unchecked place to claim
// language strength.
func validateGALanguageRow(c *Capability) error {
	if c.Category == CategoryGALanguage {
		if c.CapabilityLevel == "" {
			return fmt.Errorf("row %q missing capability level (ga-language rows must declare one of the engine/trust levels)", c.Key())
		}
		if !trust.CapabilityLevel(c.CapabilityLevel).Valid() {
			return fmt.Errorf("row %q has invalid capability %q (want one of engine/trust's closed levels, e.g. typed-confirmed)", c.Key(), c.CapabilityLevel)
		}
		return nil
	}
	if c.CapabilityLevel != "" {
		return fmt.Errorf("row %q carries a capability level but is not a ga-language row — the level is only claimable where CheckGALanguages verifies it", c.Key())
	}
	return nil
}
