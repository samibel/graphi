package retrieval

// Executable naming discipline for the SW-280 qrel-blind smoke evaluation.
//
// The shape is deliberately SW-274's CheckClaimSentence: approved denials are
// removed first, then the residue is scanned with a closed pattern set, so a
// document may state "this is not a system-blind evaluation" without tripping
// the rule that forbids calling it one.
//
// The rule this enforces is not stylistic. Three different overstatements are
// available for free to anyone describing this evaluation — that the raters
// were blind to the system, that a panel of humans rated it, and that the
// result estimates whether graphi answers Go questions in general — and each
// one would convert a smoke gate over 64 sealed Cobra questions into a claim
// the evidence cannot support.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// approvedDenials are the exact phrasings a document may use to state what this
// evaluation is NOT. They are removed before the forbidden patterns are
// scanned. Every entry is a denial; none of them can assert the thing it names.
var approvedDenials = []string{
	"not a system-blind evaluation",
	"never a system-blind evaluation",
	"never as a system-blind evaluation",
	"never system-blind",
	"not system-blind",
	"is not a human panel",
	"not a human panel",
	"never a human panel",
	"never as a human panel",
	"not a human-panel result",
	"never a human-panel result",
	"not a human-rated panel",
	"never a human-rated panel",
	"not a human-rated panel result",
	"not an estimate of general answerability",
	"never an estimate of general answerability",
	"never as an estimate of general answerability",
	"not an estimate of whether graphi answers go questions in general",
	"not an estimate for go repositories generally",
	"does not estimate general answerability",
	"cannot be described as an estimate of general answerability",
}

var qrelBlindForbidden = []struct {
	rule string
	re   *regexp.Regexp
}{
	{"system_blind", regexp.MustCompile(`(?i)system[[:space:]-]*blind`)},
	{"human_panel", regexp.MustCompile(`(?i)human[[:space:]-]*(?:rated[[:space:]-]*)?panel`)},
	{"human_panel", regexp.MustCompile(`(?i)\bpanel[[:space:]-]+of[[:space:]-]+humans?\b`)},
	{"general_answerability", regexp.MustCompile(`(?i)general(?:ly)?[[:space:]-]+answerab(?:le|ility)`)},
	{"general_answerability", regexp.MustCompile(`(?i)answerabilit(?:y|ies)[^.;]{0,40}\bin general\b`)},
	{"general_answerability", regexp.MustCompile(`(?i)\banswers?\b[^.;]{0,40}\bgo questions\b[^.;]{0,40}\bin general\b`)},
	{"general_answerability", regexp.MustCompile(`(?i)\bgo questions in general\b`)},
	{"general_answerability", regexp.MustCompile(`(?i)\bestimate[sd]?\b[^.;]{0,40}\bgeneral answerability\b`)},
	{"general_answerability", regexp.MustCompile(`(?i)\bgenerali[sz](?:e|es|ed|ing|ation)\b`)},
}

// WordingViolation is one naming rule a text broke.
type WordingViolation struct {
	Rule  string
	Match string
}

// CheckQrelBlindSmokeWording rejects any text describing this evaluation with a
// forbidden name and requires the one permitted name.
//
// It is applied to the evaluation report, to the run directory's README, and to
// the methodology section. Feeding it each forbidden form is how its bite is
// proven; a checker that has only run on text it accepts is not evidence.
func CheckQrelBlindSmokeWording(text string) error {
	// Normalize whitespace first. A document wraps its lines, and an approved
	// denial split across a line break is still an approved denial; a forbidden
	// phrase split across one is still forbidden.
	normalized := collapseWhitespace(strings.ToLower(text))
	residue := normalized
	// Longest denial first, so a shorter denial cannot consume the prefix of a
	// longer one and leave an assertion behind.
	denials := append([]string(nil), approvedDenials...)
	sort.Slice(denials, func(i, j int) bool { return len(denials[i]) > len(denials[j]) })
	for _, denial := range denials {
		residue = strings.ReplaceAll(residue, collapseWhitespace(denial), " ")
	}
	// The frozen claim limitation is a denial too, and reports quote it.
	residue = strings.ReplaceAll(residue, collapseWhitespace(strings.ToLower(RequiredClaimLimitation)), " ")

	var violations []WordingViolation
	if !strings.Contains(normalized, QrelBlindSmokeEvaluationName) {
		violations = append(violations, WordingViolation{Rule: "required_name", Match: "missing the phrase " + QrelBlindSmokeEvaluationName})
	}
	for _, pattern := range qrelBlindForbidden {
		for _, match := range pattern.re.FindAllString(residue, -1) {
			violations = append(violations, WordingViolation{Rule: pattern.rule, Match: strings.TrimSpace(match)})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].Rule == violations[j].Rule {
			return violations[i].Match < violations[j].Match
		}
		return violations[i].Rule < violations[j].Rule
	})
	seen := map[string]bool{}
	parts := make([]string, 0, len(violations))
	for _, v := range violations {
		part := fmt.Sprintf("%s (%q)", v.Rule, v.Match)
		if seen[part] {
			continue
		}
		seen[part] = true
		parts = append(parts, part)
	}
	return fmt.Errorf("retrieval %s wording rejected: %s", QrelBlindSmokeEvaluationName, strings.Join(parts, "; "))
}

// collapseWhitespace replaces every run of whitespace with a single space, so
// line wrapping cannot hide a phrase from either the denial stripper or the
// forbidden patterns.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// requiredReportStatements are the two limitations AC-12 requires the report to
// state in its own words. The checker looks for the load-bearing noun phrases
// rather than a fixed sentence, so a report may phrase them naturally, but it
// cannot omit them.
var requiredReportStatements = []struct {
	name string
	re   *regexp.Regexp
}{
	{"raters saw our bundle format", regexp.MustCompile(`(?i)\braters?\b[^.]{0,80}\bsaw\b[^.]{0,80}\bbundle format\b`)},
	{"pass count does not enter the token estimand", regexp.MustCompile(`(?i)\bpass count\b[^.]{0,120}\b(?:does not|never)\b[^.]{0,40}\benters?\b[^.]{0,40}\btoken estimand\b`)},
	{"methodology citation", regexp.MustCompile(`docs/eval/retrieval/methodology\.md`)},
}

// CheckQrelBlindSmokeReport applies the naming rule and additionally requires
// the report to state its own two limitations.
func CheckQrelBlindSmokeReport(text string) error {
	if err := CheckQrelBlindSmokeWording(text); err != nil {
		return err
	}
	var missing []string
	for _, statement := range requiredReportStatements {
		if !statement.re.MatchString(text) {
			missing = append(missing, statement.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("retrieval %s report rejected: it does not state %s", QrelBlindSmokeEvaluationName, strings.Join(missing, "; "))
	}
	return nil
}
