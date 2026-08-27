package archintel

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/extpack"
)

// SW-229 AC-6: end-to-end proof that an architecture-rules pack changes what the
// existing Labs `architecture_violations` analyzer reports — and AC-3's byte
// comparison that with no pack in effect it reports exactly what it did before.

func testPackRef() extpack.Ref {
	return extpack.Ref{ID: "graphi.layering", Version: "1.0.0", SHA256: strings.Repeat("ab", 32)}
}

// TestAC3_NoRulesMeansByteIdenticalOutput is the rollback contract for this
// consumer: an empty rule set must not change one byte of the canonical result.
func TestAC3_NoRulesMeansByteIdenticalOutput(t *testing.T) {
	before, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := contract.Serialize(before)
	if err != nil {
		t.Fatal(err)
	}
	for _, rules := range [][]extpack.ArchRule{nil, {}} {
		after, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t), Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		got, err := contract.Serialize(after)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(baseline) {
			t.Errorf("an empty rule set changed the canonical bytes:\n got=%s\nwant=%s", got, baseline)
		}
	}
}

// TestAC6_ADeclaredRuleProducesACitedFindingNamingItsPack.
func TestAC6_ADeclaredRuleProducesACitedFindingNamingItsPack(t *testing.T) {
	ref := testPackRef()
	// layeredDeps has ONE edge storage → domain, which is the back-edge the
	// built-in heuristic already reports. A declared rule turns the same fact
	// into a stated-constraint violation that quotes the rule and its pack.
	rules := []extpack.ArchRule{{
		ID:          "no-storage-to-domain",
		From:        "storage",
		To:          "domain",
		Description: "the storage layer must not reach back into the domain",
		Pack:        ref,
	}}
	res, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t), Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	reasons := allReasons(t, res)
	if !strings.Contains(reasons, "declared-rule violation") {
		t.Fatalf("no declared-rule finding in:\n%s", reasons)
	}
	for _, want := range []string{
		"no-storage-to-domain",
		"the storage layer must not reach back into the domain",
		ref.ID, ref.Version, ref.SHA256,
	} {
		if !strings.Contains(reasons, want) {
			t.Errorf("the finding must carry %q:\n%s", want, reasons)
		}
	}
	if !strings.Contains(res.Summary, "1 declared-rule violation(s) against 1 pack rule(s)") {
		t.Errorf("summary must count the declared-rule violations: %q", res.Summary)
	}
	// The finding must be ranked above the heuristic bands so it is not pushed
	// out of a capped item list by a coupling row.
	var ruleRank, backEdgeRank int
	for _, it := range res.Items {
		switch {
		case strings.HasPrefix(it.RefID, "rule:"):
			ruleRank = it.Rank
		case strings.HasPrefix(it.RefID, "backedge:"):
			backEdgeRank = it.Rank
		}
	}
	if ruleRank == 0 || backEdgeRank == 0 {
		t.Fatalf("expected both a rule item and a back-edge item: %+v", res.Items)
	}
	if ruleRank <= backEdgeRank {
		t.Errorf("a stated rule must outrank a threshold heuristic: rule=%d backedge=%d", ruleRank, backEdgeRank)
	}
}

// TestARuleThatHoldsIsReportedAsHeld: a clean verdict must say which rules were
// checked, or "clean" is indistinguishable from "no rules were in effect".
func TestARuleThatHoldsIsReportedAsHeld(t *testing.T) {
	rules := []extpack.ArchRule{{
		ID: "no-web-to-storage", From: "web", To: "storage",
		Description: "the web layer must go through the domain", Pack: testPackRef(),
	}}
	res, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t), Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(allReasons(t, res), "declared-rule violation") {
		t.Fatalf("the rule holds in this fixture; it must not fire:\n%s", allReasons(t, res))
	}
	if !strings.Contains(res.Summary, "0 declared-rule violation(s) against 1 pack rule(s)") {
		t.Errorf("summary must state that the rules were checked and held: %q", res.Summary)
	}
}

// TestRuleEvaluationIsDeterministic — the same inputs must produce the same
// canonical bytes, twice.
func TestRuleEvaluationIsDeterministic(t *testing.T) {
	rules := []extpack.ArchRule{
		{ID: "r-b", From: "storage", To: "domain", Description: "b", Pack: testPackRef()},
		{ID: "r-a", From: "domain", To: "storage", Description: "a", Pack: testPackRef()},
	}
	render := func() string {
		res, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t), Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		data, err := contract.Serialize(res)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if first, second := render(), render(); first != second {
		t.Errorf("two identical runs disagree:\n A=%s\n B=%s", first, second)
	}
}

// TestRuleTextIsLengthBoundedInTheEmittedItem: rule ids and descriptions are
// pack-controlled text on their way into an artifact, and standards require them
// bounded before they get there.
func TestRuleTextIsLengthBoundedInTheEmittedItem(t *testing.T) {
	long := strings.Repeat("d", extpack.MaxFieldLength*2)
	rules := []extpack.ArchRule{{
		ID: "r", From: "storage", To: "domain", Description: long,
		Pack: extpack.Ref{ID: strings.Repeat("p", extpack.MaxFieldLength*2), Version: "1", SHA256: strings.Repeat("f", 64)},
	}}
	res, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t), Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	reasons := allReasons(t, res)
	if strings.Contains(reasons, long) {
		t.Error("a pack-supplied description reached the artifact verbatim")
	}
	if !strings.Contains(reasons, "[truncated]") {
		t.Errorf("truncation must be visible in the emitted text:\n%s", reasons)
	}
	for _, it := range res.Items {
		if len(it.RefID) > 4*extpack.MaxFieldLength {
			t.Errorf("ref id is unbounded (%d bytes): %q", len(it.RefID), it.RefID)
		}
	}
}
