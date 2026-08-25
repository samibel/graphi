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
//
// REBUILD ROUND 1 added two rules and tightened one match, all from the review
// of e3975f9. The path ban above is necessary but was not sufficient: it bans
// graphi PATHS, and the reviewer showed that circular PROSE with no path token
// passed. TestHeroIntraParse_AbstentionsDoNotJustifyThemselvesWithGraphi adds
// the ANSWER-POSITION and AUTHORITY-PHRASE rules — see its own comment block,
// which also states plainly what those two rules still do NOT close.
// isGenericHonestEmpty replaces a `Contains` match with `HasSuffix` (M-2).

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

// isGenericHonestEmpty reports whether id names one of the two generic
// honest-empty shapes.
//
// M-2 (review of e3975f9): this was `strings.Contains`, and the reviewer's
// judgement was that the hatch it opened is closed today only by two numeric
// coincidences — every suite sits EXACTLY at its minAbstentions floor, and
// TestHeroIntraParse_ScenarioResultsAreByteStable hard-codes 16 scenarios. The
// first story that legitimately raises a suite to 17 re-opens it. `Contains`
// exempts any id that merely mentions the token anywhere, so
// `hcss-17-definition-not-found-in-imported-sheet` — an uncited LANGUAGE claim
// about css @import wearing a generic name — reads as generic and skips the
// §5.5 citation rule entirely. HasSuffix is anchored: the id must END in the
// generic shape, which is what the two shipped generic scenarios actually do
// (hcss-03-search-empty, hcss-05-definition-not-found) and mirrors
// isAbstentionScenario's own HasSuffix. Pinned by
// TestHeroIntraParse_GenericHonestEmptyMatchIsSuffixAnchored.
func isGenericHonestEmpty(id string) bool {
	for _, shape := range genericHonestEmptyIDs {
		if strings.HasSuffix(id, shape) {
			return true
		}
	}
	return false
}

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
				if !isGenericHonestEmpty(s.ID) {
					t.Errorf("%s expects %q but is neither named -abstention nor ENDS IN one of the generic honest-empty shapes %v — every other empty answer in these suites is a language claim and must carry the §5.5 citation", s.ID, s.Expect.Outcome, genericHonestEmptyIDs)
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

// TestHeroIntraParse_GenericHonestEmptyMatchIsSuffixAnchored is the
// demonstration for M-2: it pins the difference between the old `Contains`
// match and the `HasSuffix` one, so the tightening cannot be silently undone.
// The first table entry is exactly the id `Contains` would have wrongly
// exempted.
func TestHeroIntraParse_GenericHonestEmptyMatchIsSuffixAnchored(t *testing.T) {
	cases := []struct {
		id      string
		generic bool
		why     string
	}{
		{"hcss-17-definition-not-found-in-imported-sheet", false,
			"the id merely CONTAINS the generic shape; the trailing words make it a css @import claim, which is a LANGUAGE claim and must carry the §5.5 citation"},
		{"hcss-17-search-empty-across-the-import-graph", false,
			"same shape, search side"},
		{"hcss-03-search-empty", true, "the shipped generic search shape"},
		{"hcss-05-definition-not-found", true, "the shipped generic definition shape"},
		{"hhcl-06-callers-abstention", false, "an abstention is classified by isAbstentionScenario, never here"},
	}
	for _, c := range cases {
		if got := isGenericHonestEmpty(c.id); got != c.generic {
			t.Errorf("isGenericHonestEmpty(%q) = %v, want %v — %s", c.id, got, c.generic, c.why)
		}
		// The property that makes the tightening real: for the first two the
		// old Contains match disagrees with the new one.
		contains := false
		for _, shape := range genericHonestEmptyIDs {
			if strings.Contains(c.id, shape) {
				contains = true
			}
		}
		if strings.HasPrefix(c.id, "hcss-17-") && !contains {
			t.Errorf("%q no longer demonstrates the M-2 gap — it must be an id the OLD Contains match would have exempted", c.id)
		}
	}
}

// ── M-3 (review of e3975f9): CIRCULAR PROSE, NOT ONLY CIRCULAR PATHS ────────
//
// circularCitationTokens above bans graphi PATHS. The reviewer's surviving
// bypass was prose: a description reading "graphi's CSS parser comment says
// there is no import system, which is why this is empty" contains no path
// token and passed, while being LANGHONEST-001 in every respect that matters.
//
// The hard part is not firing on correct descriptions. Every shipped
// description legitimately talks about graphi — for the YES-side languages the
// abstention IS graphi's ("graphi's CSS extractor mints selector symbols and
// records no import at all"), and every single one of the 37 ends with the
// sanctioned disclaimer "citing graphi's own parser comment instead would be
// the LANGHONEST-001 circular-abstention defect". A flat ban on the words
// "parser comment" would fire on the disclaimer that exists to forbid the
// thing. So the rule is not about WHICH words appear; it is about WHERE.
//
// Two rules, each targeting one half of the distinction the reviewer drew
// between "describing what graphi does" (allowed) and "offering graphi's
// behaviour AS THE JUSTIFICATION where the spec should be" (forbidden):
//
//	ANSWER-POSITION RULE. §5.5's question — already required verbatim above —
//	is a question about the LANGUAGE. In the text that follows it, the
//	language's own specification must be named BEFORE graphi is mentioned at
//	all. A description that answers "does L's own specification define a
//	construct that names another file?" with graphi's parser has put graphi in
//	the answer position, which is the defect exactly. All 37 shipped
//	descriptions name their spec within 12 characters of the question and do
//	not reach for graphi for at least another 190, so the rule has enormous
//	headroom on correct prose and none at all on the circular form.
//
//	AUTHORITY-PHRASE RULE. Phrases that make graphi's own source the AUTHORITY
//	("parser comment", "graphi's parser", "according to graphi", …) are banned
//	in every sentence EXCEPT one that also names LANGHONEST-001 — i.e. the
//	disclaimer is the only sanctioned place to mention the circular form, and
//	it must name the defect it is disclaiming. No shipped description leaks one
//	of these phrases into any other sentence.
//
// WHAT THIS STILL DOES NOT CLOSE, STATED RATHER THAN GLOSSED. A determined
// author can satisfy both rules and still be circular: name the spec first,
// then write "…and in practice what settles it is how graphi behaves", using
// none of the banned phrases. Separating that from the legitimate "graphi
// declines to extract it" is a judgement about which clause carries the
// justification, and no lexical rule the author can read makes it. The rules
// here close the shape the reviewer actually demonstrated and raise the cost
// of the residue; the residue is real and is recorded as such rather than
// papered over with a broader ban that would fire on correct descriptions.

// graphiMentionTokens are the ways a description refers to graphi or its
// machinery. The ANSWER-POSITION rule requires the language spec to be named
// before any of these, in the text that follows §5.5's question.
var graphiMentionTokens = []string{"graphi", "parser", "extractor"}

// circularAuthorityPhrases make graphi's own source the AUTHORITY for a claim.
// They are legal only inside a sentence that also names LANGHONEST-001, which
// is the sanctioned disclaimer form every shipped description already uses.
var circularAuthorityPhrases = []string{
	"parser comment",
	"parser says",
	"parser states",
	"extractor says",
	"extractor states",
	"graphi's parser",
	"graphi's own parser",
	"graphi's source",
	"graphi's implementation",
	"graphi's code",
	"according to graphi",
	"graphi says",
	"because graphi",
	"the code says",
}

// descriptionSentences splits a scenario description into sentences. YAML
// folded scalars join wrapped lines with spaces and paragraph breaks with
// newlines, so both terminators matter.
func descriptionSentences(desc string) []string {
	var out []string
	for _, para := range strings.Split(desc, "\n") {
		start := 0
		for i := 0; i < len(para); i++ {
			if para[i] != '.' {
				continue
			}
			if i+1 < len(para) && para[i+1] != ' ' {
				continue
			}
			out = append(out, para[start:i+1])
			start = i + 1
		}
		if rest := strings.TrimSpace(para[start:]); rest != "" {
			out = append(out, rest)
		}
	}
	return out
}

func TestHeroIntraParse_AbstentionsDoNotJustifyThemselvesWithGraphi(t *testing.T) {
	for _, rule := range intraParseSpecCiteRules {
		t.Run(rule.lang, func(t *testing.T) {
			checked := 0
			for _, s := range loadIntraParseSuite(t, rule.dir) {
				if !isAbstentionScenario(s) {
					continue
				}
				desc := s.Description

				// ── ANSWER-POSITION RULE ──────────────────────────────────
				qi := strings.Index(desc, spec55Question)
				if qi < 0 {
					// TestHeroIntraParse_AbstentionsCiteTheLanguageSpec
					// already reports the missing question; do not double-report.
					continue
				}
				checked++
				tail := desc[qi+len(spec55Question):]
				tailLower := strings.ToLower(tail)
				specAt := -1
				for _, tok := range rule.specTokens {
					if i := strings.Index(tail, tok); i >= 0 && (specAt < 0 || i < specAt) {
						specAt = i
					}
				}
				graphiAt := -1
				graphiTok := ""
				for _, tok := range graphiMentionTokens {
					if i := strings.Index(tailLower, tok); i >= 0 && (graphiAt < 0 || i < graphiAt) {
						graphiAt, graphiTok = i, tok
					}
				}
				switch {
				case specAt < 0:
					t.Errorf("%s: §5.5's language question is never answered with %s's own specification %v in the text that follows it — the answer position is where LANGHONEST-001 lives", s.ID, rule.lang, rule.specTokens)
				case graphiAt >= 0 && graphiAt < specAt:
					t.Errorf("%s: §5.5's language question is answered with %q (at +%d) BEFORE %s's own specification is named (at +%d) — the language question must be answered by the language's spec; answering it with graphi's behaviour is the LANGHONEST-001 circular abstention, whether or not a graphi source path is quoted. Answer span begins: %q",
						s.ID, graphiTok, graphiAt, rule.lang, specAt, strings.TrimSpace(tail[:min(160, len(tail))]))
				}

				// ── AUTHORITY-PHRASE RULE ─────────────────────────────────
				for _, sent := range descriptionSentences(desc) {
					if strings.Contains(sent, "LANGHONEST-001") {
						continue
					}
					sl := strings.ToLower(sent)
					for _, bad := range circularAuthorityPhrases {
						if strings.Contains(sl, bad) {
							t.Errorf("%s: the sentence %q makes graphi's own source the authority (%q) outside the sanctioned LANGHONEST-001 disclaimer — a description may say what graphi DOES, but graphi's behaviour may not stand where the language's specification should",
								s.ID, strings.TrimSpace(sent), bad)
						}
					}
				}
			}
			if checked < rule.minAbstentions {
				t.Errorf("hero-%s: only %d abstention descriptions reached this rule, want at least %d — this test must have something to check", rule.lang, checked, rule.minAbstentions)
			}
		})
	}
}
