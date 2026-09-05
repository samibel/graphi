package retrieval

import (
	"strings"
	"testing"
)

// AC-12: the naming rule is executable, and its bite is proven by feeding it
// every forbidden form. A checker that has only ever run on text it accepts is
// not evidence of anything.
func TestCheckQrelBlindSmokeWording_RejectsEveryForbiddenForm(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		wantSub string
	}{
		{
			name:    "system-blind",
			text:    "This is a system-blind evaluation of the holdout, a qrel-blind smoke evaluation by another name.",
			wantSub: "system_blind",
		},
		{
			name:    "system blind, unhyphenated",
			text:    "The raters were system blind. This was a qrel-blind smoke evaluation.",
			wantSub: "system_blind",
		},
		{
			name:    "human panel",
			text:    "A human panel answered every question in this qrel-blind smoke evaluation.",
			wantSub: "human_panel",
		},
		{
			name:    "human-rated panel",
			text:    "The human-rated panel result of this qrel-blind smoke evaluation is 56/64.",
			wantSub: "human_panel",
		},
		{
			name:    "panel of humans",
			text:    "A panel of humans scored the bundles in this qrel-blind smoke evaluation.",
			wantSub: "human_panel",
		},
		{
			name:    "general answerability",
			text:    "This qrel-blind smoke evaluation estimates general answerability for Go work.",
			wantSub: "general_answerability",
		},
		{
			name:    "generally answerable",
			text:    "The bundles are generally answerable, as this qrel-blind smoke evaluation shows.",
			wantSub: "general_answerability",
		},
		{
			name:    "answers Go questions in general",
			text:    "The qrel-blind smoke evaluation shows graphi answers Go questions in general.",
			wantSub: "general_answerability",
		},
		{
			name:    "generalization",
			text:    "This qrel-blind smoke evaluation supports generalization beyond Cobra.",
			wantSub: "general_answerability",
		},
		{
			name:    "no name at all",
			text:    "The holdout evaluation produced 56 passes out of 64.",
			wantSub: "required_name",
		},
		{
			name:    "the wrong name only",
			text:    "The blind evaluation produced 56 passes out of 64 sealed questions.",
			wantSub: "required_name",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckQrelBlindSmokeWording(tc.text)
			if err == nil {
				t.Fatalf("%q was accepted", tc.text)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("rejection %q does not name rule %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// Positive control: the permitted description, including the denials a report
// legitimately needs, is accepted. Without this the rejections above could all
// be produced by a checker that rejects everything.
func TestCheckQrelBlindSmokeWording_AcceptsThePermittedDescription(t *testing.T) {
	for _, text := range []string{
		"This is a qrel-blind smoke evaluation over the sealed holdout.",
		"It is a qrel-blind smoke evaluation. It is not a system-blind evaluation, not a human panel, " +
			"and not an estimate of general answerability.",
		"The qrel-blind smoke evaluation is never a human-panel result and never an estimate of general answerability. " +
			RequiredClaimLimitation,
		"A qrel-blind smoke evaluation: the raters saw our bundle format, so it does not estimate general answerability.",
	} {
		if err := CheckQrelBlindSmokeWording(text); err != nil {
			t.Errorf("permitted text rejected: %v\n%s", err, text)
		}
	}
}

// AC-12's second half: the report must state its own two limitations.
func TestCheckQrelBlindSmokeReport_RequiresItsOwnLimitations(t *testing.T) {
	complete := "This is a qrel-blind smoke evaluation. The raters saw our bundle format and our question set, " +
		"so it is not a system-blind evaluation. Its pass count is a separate gate: the pass count does not " +
		"enter the token estimand (docs/eval/retrieval/methodology.md line 25)."
	if err := CheckQrelBlindSmokeReport(complete); err != nil {
		t.Fatalf("the complete report statement was rejected: %v", err)
	}
	for _, tc := range []struct {
		name    string
		text    string
		wantSub string
	}{
		{
			name: "no bundle-format admission",
			text: "This is a qrel-blind smoke evaluation. The pass count does not enter the token estimand " +
				"(docs/eval/retrieval/methodology.md line 25).",
			wantSub: "raters saw our bundle format",
		},
		{
			name: "no estimand separation",
			text: "This is a qrel-blind smoke evaluation. The raters saw our bundle format " +
				"(docs/eval/retrieval/methodology.md line 25).",
			wantSub: "pass count does not enter the token estimand",
		},
		{
			name: "no methodology citation",
			text: "This is a qrel-blind smoke evaluation. The raters saw our bundle format, and the pass " +
				"count does not enter the token estimand.",
			wantSub: "methodology citation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckQrelBlindSmokeReport(tc.text)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("rejection %q does not name %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// A denial cannot be used to smuggle an assertion past the stripper: the
// residue of "not a human panel" is empty, but "a human panel, not a system-
// blind evaluation" still names a human panel.
func TestCheckQrelBlindSmokeWording_DenialStrippingCannotLaunderAnAssertion(t *testing.T) {
	err := CheckQrelBlindSmokeWording(
		"A human panel ran this qrel-blind smoke evaluation, which is not a system-blind evaluation.")
	if err == nil {
		t.Fatal("an assertion beside an approved denial was accepted")
	}
	if !strings.Contains(err.Error(), "human_panel") {
		t.Errorf("rejection %q does not name the human_panel rule", err.Error())
	}
}
