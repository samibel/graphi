package main

// SW-202 AC-4 — THE §5.5 CITATION DISCIPLINE, AS A TEST RATHER THAN AS PROSE.
//
// docs/plan/2026-08-per-language-ga-template-v1.md §5.5 states the rule this
// file enforces: when a hero scenario asserts an abstention, the row must say
// "that L defines no cross-file construct, WITH A CITATION TO THE LANGUAGE
// SPECIFICATION, not to graphi's parser — because the parser comment 'no
// import system' is a statement about graphi and would make the abstention
// circular". That circular form is filed as LANGHONEST-001.
//
// Correct prose rots. This file makes the discipline mechanical for all six
// intra/parse residual suites at once, so a new abstention scenario cannot be
// added without it, and an existing one cannot be edited out of it:
//
//   - a scenario whose id ends in "-abstention" MUST cite its language's own
//     specification by name, MUST pose §5.5's question verbatim, MUST name
//     LANGHONEST-001, and MUST NOT reference any graphi implementation path;
//   - the converse is closed too: a scenario that expects `empty` or
//     `not_found` and is NOT named "-abstention" must be one of the two
//     generic honest-empty shapes every hero suite in this repository carries
//     (search-empty, definition-not-found). Renaming a file is therefore not
//     a way out of the rule;
//   - each suite must carry at least a stated minimum of abstention
//     scenarios, so this test cannot pass by finding nothing to check.

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/scenario"
)

// specCiteRule is one language's §5.5 citation contract.
type specCiteRule struct {
	lang string
	// dir is the scenario directory under corpus/.
	dir string
	// specTokens are the language-specification names the description must
	// carry. Every token must be present.
	specTokens []string
	// minAbstentions is the floor on abstention scenarios in the suite; it
	// keeps this test from passing vacuously on an emptied directory.
	minAbstentions int
}

// intraParseSpecCiteRules is the closed table of the six intra/parse residual
// languages. The spec names are the ones this repository already published in
// docs/rc/parity-classes-<lang>.yaml (SW-199), so the hero suites and the
// parity-class records cite the same specifications.
var intraParseSpecCiteRules = []specCiteRule{
	{"css", "hero-css", []string{"CSS Syntax Module Level 3"}, 5},
	{"hcl", "hero-hcl", []string{"HCL Native Syntax and Structure"}, 5},
	{"json", "hero-json", []string{"RFC 8259"}, 12},
	{"markdown", "hero-markdown", []string{"CommonMark"}, 5},
	{"toml", "hero-toml", []string{"TOML v1.0.0"}, 5},
	{"yaml", "hero-yaml", []string{"YAML 1.2"}, 5},
}

// spec55Question is §5.5's test, posed in the scenario's own words. Requiring
// it verbatim keeps every abstention description answering the LANGUAGE
// question first, which is the half LANGHONEST-001 is about.
const spec55Question = "own specification define a construct that names another file"

// circularCitationTokens are graphi implementation references. An abstention
// description that reaches for one of these is answering the language
// question with graphi's source, which IS the LANGHONEST-001 defect. Prose
// may still describe what graphi does — "graphi's CSS extractor mints
// selector symbols and records no import at all" is fine and is used — it
// just may not do so by pointing at graphi's own files as the authority.
var circularCitationTokens = []string{
	"core/parse/",
	"engine/link/",
	"engine/agenttools/",
	"internal/",
	"surfaces/",
	"parser_",
	"resolve_",
}

// genericHonestEmptyIDs are the two honest-empty shapes that are properties of
// the OPERATION rather than of the language, and so carry no §5.5 citation.
// Every other empty/not_found scenario must be an abstention.
var genericHonestEmptyIDs = []string{"search-empty", "definition-not-found"}

func loadIntraParseSuite(t *testing.T, dir string) []scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot(t), "corpus", dir, "*.yaml"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	sort.Strings(files)
	out := make([]scenario.Scenario, 0, len(files))
	for _, f := range files {
		s, err := scenario.LoadScenario(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		t.Fatalf("%s has no scenarios", dir)
	}
	return out
}

func isAbstentionScenario(s scenario.Scenario) bool {
	return strings.HasSuffix(s.ID, "-abstention")
}

func TestHeroIntraParse_AbstentionsCiteTheLanguageSpec(t *testing.T) {
	for _, rule := range intraParseSpecCiteRules {
		t.Run(rule.lang, func(t *testing.T) {
			suite := loadIntraParseSuite(t, rule.dir)
			abstentions := 0
			for _, s := range suite {
				if !isAbstentionScenario(s) {
					continue
				}
				abstentions++
				desc := s.Description
				if desc == "" {
					t.Errorf("%s: abstention scenario has no description at all", s.ID)
					continue
				}
				if !strings.Contains(desc, "§5.5") {
					t.Errorf("%s: description does not name §5.5, the section that governs the abstention path", s.ID)
				}
				if !strings.Contains(desc, spec55Question) {
					t.Errorf("%s: description does not pose §5.5's language question (%q), so it is not clear whether the abstention is the language's or graphi's", s.ID, spec55Question)
				}
				for _, tok := range rule.specTokens {
					if !strings.Contains(desc, tok) {
						t.Errorf("%s: description does not cite %s's own specification (%q) — an abstention justified by anything else is LANGHONEST-001", s.ID, rule.lang, tok)
					}
				}
				if !strings.Contains(desc, "LANGHONEST-001") {
					t.Errorf("%s: description does not name LANGHONEST-001, the defect class this citation exists to avoid", s.ID)
				}
				for _, bad := range circularCitationTokens {
					if strings.Contains(desc, bad) {
						t.Errorf("%s: description cites graphi's own implementation (%q) inside an abstention claim — citing graphi's parser to justify graphi's abstention is circular and IS LANGHONEST-001", s.ID, bad)
					}
				}
			}
			if abstentions < rule.minAbstentions {
				t.Errorf("hero-%s carries %d abstention scenarios, want at least %d — this test must have something to check", rule.lang, abstentions, rule.minAbstentions)
			}
		})
	}
}

// TestHeroIntraParse_EveryEmptyOutcomeIsClassified closes the escape hatch:
// an abstention cannot dodge the citation rule by not being named one.
func TestHeroIntraParse_EveryEmptyOutcomeIsClassified(t *testing.T) {
	for _, rule := range intraParseSpecCiteRules {
		t.Run(rule.lang, func(t *testing.T) {
			for _, s := range loadIntraParseSuite(t, rule.dir) {
				switch s.Expect.Outcome {
				case "empty", "not_found":
				default:
					continue
				}
				if isAbstentionScenario(s) {
					continue
				}
				generic := false
				for _, id := range genericHonestEmptyIDs {
					if strings.Contains(s.ID, id) {
						generic = true
					}
				}
				if !generic {
					t.Errorf("%s expects %q but is neither named -abstention nor one of the generic honest-empty shapes %v — every other empty answer in these suites is a language claim and must carry the §5.5 citation", s.ID, s.Expect.Outcome, genericHonestEmptyIDs)
				}
			}
		})
	}
}

// TestHeroIntraParse_JsonRefIsAttributedToJsonSchema pins the one §5.5
// derivation that is specific to json: `$ref` is JSON Schema's construct, not
// json's own, so any abstention description that reaches for `$ref` must say
// whose construct it is.
func TestHeroIntraParse_JsonRefIsAttributedToJsonSchema(t *testing.T) {
	mentions := 0
	for _, s := range loadIntraParseSuite(t, "hero-json") {
		if !isAbstentionScenario(s) || !strings.Contains(s.Description, "$ref") {
			continue
		}
		mentions++
		if !strings.Contains(s.Description, "JSON Schema") {
			t.Errorf("%s names $ref without attributing it to JSON Schema — $ref is JSON Schema's construct layered on json, and treating it as json's own is the derivation §5.5 forbids", s.ID)
		}
	}
	if mentions == 0 {
		t.Error("no hero-json abstention description mentions $ref — the json §5.5 derivation is the reason this suite exists and must be stated somewhere")
	}
}
