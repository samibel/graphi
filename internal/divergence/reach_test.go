package divergence

import (
	"encoding/json"
	"strings"
	"testing"
)

// SW-248 — the three shapes.
//
// The story's discriminating test is not that any one of these renders
// correctly; it is that they render DIFFERENTLY. Before this change all three
// produced the same sentence — "no dual-run observation has been recorded" —
// and the reason they were empty, which is the only thing that told a reader
// whether to wait, was nowhere in the output.
//
// So the assertions come in pairs: each shape says its own thing, and each
// shape does NOT say the other two's.

// twoProfiles is the shipped picture in miniature: a default profile that
// reaches `reachable` and an opt-in profile that reaches everything named.
func twoProfiles(defaultReaches []string, optInReaches []string) []Profile {
	return []Profile{
		{ID: "mcp-default", Invocation: "graphi mcp", Default: true, Reaches: defaultReaches},
		{ID: "mcp-labs", Invocation: "graphi mcp -labs", Reaches: optInReaches},
	}
}

// shapeOne: reachable in the bound profile, simply not called yet.
func shapeOne(t *testing.T) Document {
	t.Helper()
	return Assess(Report{Directory: "/state/executor-divergence"},
		[]string{"reachable_op"},
		twoProfiles([]string{"reachable_op"}, []string{"reachable_op"}))
}

// shapeTwo: one operation reachable and observed, one the bound profile cannot
// reach at all. Mixed on purpose — an all-unreachable document is shape three,
// and the two must not be allowed to collapse into each other.
func shapeTwo(t *testing.T) Document {
	t.Helper()
	rep := Report{
		Directory: "/state/executor-divergence",
		Segments:  1,
		Operations: []OperationRecord{
			{Operation: "reachable_op", Observations: 4},
		},
	}
	return Assess(rep, []string{"reachable_op", "labs_only_op"},
		twoProfiles([]string{"reachable_op"}, []string{"reachable_op", "labs_only_op"}))
}

// shapeThree: the condition on a stock v0.11.0 install today — an empty record
// and not one operation on the seam that the default profile can reach.
func shapeThree(t *testing.T) Document {
	t.Helper()
	ops := []string{"dead_code", "repo_overview"}
	return Assess(Report{Directory: "/state/executor-divergence"}, ops, twoProfiles(nil, ops))
}

// shapeFour is not one of the three the story names, and is here because the
// gate it backs (cmd/seamreach) can only be trusted if the readout can say what
// the gate refuses to merge: an operation no profile reaches at all.
func shapeFour(t *testing.T) Document {
	t.Helper()
	return Assess(Report{Directory: "/state/executor-divergence"},
		[]string{"orphan_op"},
		twoProfiles(nil, nil))
}

func TestSW248_ShapeOne_ReachableButNotYetObserved(t *testing.T) {
	doc := shapeOne(t)
	if doc.State != StateUnknown {
		t.Fatalf("state = %q, want %q — an empty but fillable record is UNKNOWN", doc.State, StateUnknown)
	}
	if doc.Operations[0].Reach != ReachDefault {
		t.Fatalf("reach = %q, want %q", doc.Operations[0].Reach, ReachDefault)
	}
	out := renderString(t, doc)
	t.Logf("SHAPE 1 — reachable, not yet observed:\n%s", out)
	if !strings.Contains(out, "NOT YET OBSERVED, but reachable: reachable_op") {
		t.Errorf("shape 1 does not say the operation is merely unobserved:\n%s", out)
	}
	if !strings.Contains(out, `a call will change it`) {
		t.Errorf("shape 1 does not tell the reader a call would fill it:\n%s", out)
	}
	if strings.Contains(out, "NOT OBSERVABLE") || strings.Contains(out, "CANNOT FILL") {
		t.Errorf("shape 1 borrows shape 2/3's wording, so the two cannot be told apart:\n%s", out)
	}
}

func TestSW248_ShapeTwo_NotReachableInTheBoundProfile(t *testing.T) {
	doc := shapeTwo(t)
	var labs OperationView
	for _, op := range doc.Operations {
		if op.Operation == "labs_only_op" {
			labs = op
		}
	}
	if labs.State != StateUnknown || labs.Reach != ReachOptIn {
		t.Fatalf("labs_only_op = (%q, %q), want (%q, %q)", labs.State, labs.Reach, StateUnknown, ReachOptIn)
	}
	out := renderString(t, doc)
	t.Logf("SHAPE 2 — unreachable in the bound profile:\n%s", out)
	if !strings.Contains(out, "NOT OBSERVABLE through `graphi mcp`: labs_only_op") {
		t.Errorf("shape 2 does not say the operation cannot be reached:\n%s", out)
	}
	if !strings.Contains(out, `"not possible`) {
		t.Errorf("shape 2 does not distinguish 'not possible' from 'not yet':\n%s", out)
	}
	if !strings.Contains(out, "still not a statement that the two paths\n  agree") {
		t.Errorf("shape 2 drops the honesty rule while explaining the new one:\n%s", out)
	}
	if strings.Contains(out, "NOT YET OBSERVED, but reachable: labs_only_op") {
		t.Errorf("shape 2 is filed under shape 1's heading:\n%s", out)
	}
	// The row must name the profile that WOULD reach it, or the reader is told
	// what is impossible without being told what is possible.
	if !strings.Contains(out, "graphi mcp -labs") {
		t.Errorf("shape 2 does not name a profile that reaches the operation:\n%s", out)
	}
}

func TestSW248_ShapeThree_TheRecordCannotFillAtAll(t *testing.T) {
	doc := shapeThree(t)
	if doc.State != StateUnobservable {
		t.Fatalf("state = %q, want %q — a record that cannot fill is not the same finding as one that has not",
			doc.State, StateUnobservable)
	}
	out := renderString(t, doc)
	t.Logf("SHAPE 3 — nothing on the seam is reachable at all:\n%s", out)
	if !strings.Contains(out, "THIS RECORD CANNOT FILL in `graphi mcp`") {
		t.Errorf("shape 3 does not say the record can never fill:\n%s", out)
	}
	if !strings.Contains(out, "waiting longer will not change it") {
		t.Errorf("shape 3 leaves 'wait for it' as a reasonable reading:\n%s", out)
	}
	if !strings.Contains(out, "NONE of the 2 operation(s) on the seam is reachable") {
		t.Errorf("shape 3's header does not carry the count:\n%s", out)
	}
	// Shape 3 is shape 2 for every operation, so it legitimately contains the
	// per-operation wording. What it must NOT do is stop there: the whole-record
	// verdict is the addition, and its absence is what made the shipped binary
	// read as "not yet".
	if strings.Contains(out, "state:      UNKNOWN —") {
		t.Errorf("shape 3 reports the plain UNKNOWN verdict, which reads as 'not yet':\n%s", out)
	}
}

func TestSW248_ShapeFour_ReachableThroughNoProfileAtAll(t *testing.T) {
	doc := shapeFour(t)
	if doc.Operations[0].Reach != ReachNone {
		t.Fatalf("reach = %q, want %q", doc.Operations[0].Reach, ReachNone)
	}
	out := renderString(t, doc)
	t.Logf("SHAPE 4 — no shipped profile reaches it:\n%s", out)
	if !strings.Contains(out, "NOT REACHABLE THROUGH ANY SHIPPED PROFILE: orphan_op") {
		t.Errorf("shape 4 does not say nothing can reach it:\n%s", out)
	}
	if !strings.Contains(out, "a defect in the migration") {
		t.Errorf("shape 4 does not say this is a build defect rather than a setting:\n%s", out)
	}
}

// TestSW248_TheThreeShapesAreMutuallyDistinguishable is the story's own
// acceptance test: if two of the renderings read alike, the change has not done
// its job. It compares the whole outputs rather than trusting the per-shape
// assertions above, because those could all pass on three documents that a
// human still could not tell apart.
func TestSW248_TheThreeShapesAreMutuallyDistinguishable(t *testing.T) {
	shapes := map[string]string{
		"1-reachable-unobserved": renderString(t, shapeOne(t)),
		"2-unreachable-in-bound": renderString(t, shapeTwo(t)),
		"3-record-cannot-fill":   renderString(t, shapeThree(t)),
		"4-unreachable-anywhere": renderString(t, shapeFour(t)),
	}
	names := []string{"1-reachable-unobserved", "2-unreachable-in-bound", "3-record-cannot-fill", "4-unreachable-anywhere"}
	for i, a := range names {
		for _, b := range names[i+1:] {
			if shapes[a] == shapes[b] {
				t.Fatalf("%s and %s render identically:\n%s", a, b, shapes[a])
			}
		}
	}
	// Identical bytes is the weakest possible bar. The stronger one is that each
	// shape carries a sentence a reader can act on, and that the sentence
	// appears ONLY where it is true.
	//
	// "Only where it is true" is not the same as "in exactly one shape", and
	// pretending otherwise would be a worse test than none. Some conditions
	// genuinely nest: shape 4's operation is reachable through no profile at
	// all, so it is also unreachable through the default one and its record
	// also cannot fill — it earns shape 3's sentence honestly. What must never
	// happen is the reverse, a shape claiming a condition it does not meet, so
	// each marker is asserted present in the shapes that hold it and absent
	// from the shapes that do not.
	markers := []struct {
		marker string
		holds  []string
	}{
		{"NOT YET OBSERVED, but reachable", []string{"1-reachable-unobserved"}},
		// Shape 4 does NOT carry this one, and that is the renderer's choice
		// rather than an oversight: "no shipped profile reaches it" is strictly
		// stronger than "the default profile does not", and printing both would
		// make the weaker sentence the one a skimming reader takes away.
		{"NOT OBSERVABLE through `graphi mcp`", []string{
			"2-unreachable-in-bound", "3-record-cannot-fill"}},
		{"THIS RECORD CANNOT FILL", []string{"3-record-cannot-fill", "4-unreachable-anywhere"}},
		{"NOT REACHABLE THROUGH ANY SHIPPED PROFILE", []string{"4-unreachable-anywhere"}},
	}
	for _, m := range markers {
		holds := map[string]bool{}
		for _, name := range m.holds {
			holds[name] = true
		}
		for name, text := range shapes {
			got := strings.Contains(text, m.marker)
			if got && !holds[name] {
				t.Errorf("%s carries %q, a condition it does not meet", name, m.marker)
			}
			if !got && holds[name] {
				t.Errorf("%s is missing %q, a condition it does meet", name, m.marker)
			}
		}
	}
	// The pair the story is actually about: shape 1 and shape 2 are both an
	// UNKNOWN with zero observations, and the ONLY thing that separates them is
	// which sentence explains why. Assert that separation directly rather than
	// inferring it from the table above.
	if strings.Contains(shapes["1-reachable-unobserved"], "NOT OBSERVABLE through") {
		t.Error("shape 1 tells the reader an operation cannot be observed when a call would observe it")
	}
	if strings.Contains(shapes["2-unreachable-in-bound"], "NOT YET OBSERVED, but reachable: labs_only_op") {
		t.Error("shape 2 files an unreachable operation under 'not yet'")
	}
}

// TestSW248_UnevaluatedReachabilityAssertsNothing pins the fourth condition the
// type system allows: a document assembled without a profile picture. It must
// not read as "reachable" and must not read as "unreachable" — an absent answer
// rendered as either would be the same class of defect this story closes, in
// one direction or the other.
func TestSW248_UnevaluatedReachabilityAssertsNothing(t *testing.T) {
	doc := Assess(Report{Directory: "/state/executor-divergence"}, []string{"dead_code"}, nil)
	if doc.ReachEvaluated {
		t.Fatal("a document built with no profiles claims its reachability was evaluated")
	}
	if doc.Operations[0].Reach != ReachUnevaluated {
		t.Fatalf("reach = %q, want %q", doc.Operations[0].Reach, ReachUnevaluated)
	}
	if doc.State != StateUnknown {
		t.Fatalf("state = %q, want %q — without a profile picture the record cannot be called unfillable",
			doc.State, StateUnknown)
	}
	out := renderString(t, doc)
	t.Logf("SHAPE 0 — reachability not evaluated:\n%s", out)
	if !strings.Contains(out, "Reachability was NOT evaluated") {
		t.Errorf("an unevaluated document does not say so:\n%s", out)
	}
	if strings.Contains(out, "THIS RECORD CANNOT FILL") || strings.Contains(out, "NOT YET OBSERVED, but reachable") {
		t.Errorf("an unevaluated document asserts a reachability it was never given:\n%s", out)
	}
}

// TestSW248_ObservationsOutrankTheUnreachabilityVerdict guards the narrowness of
// AC-4's condition. If an operator DID bind the opt-in profile and record
// something, the record has filled — telling them it cannot would be the mirror
// image of the defect.
func TestSW248_ObservationsOutrankTheUnreachabilityVerdict(t *testing.T) {
	rep := Report{
		Directory:  "/state/executor-divergence",
		Segments:   1,
		Operations: []OperationRecord{{Operation: "labs_only_op", Observations: 3}},
	}
	doc := Assess(rep, []string{"labs_only_op"}, twoProfiles(nil, []string{"labs_only_op"}))
	if doc.State == StateUnobservable {
		t.Fatalf("a record with %d observation(s) was reported as unfillable", doc.Observations)
	}
	if doc.State != StateAgreed {
		t.Fatalf("state = %q, want %q", doc.State, StateAgreed)
	}
	out := renderString(t, doc)
	if strings.Contains(out, "THIS RECORD CANNOT FILL") {
		t.Errorf("a record that demonstrably filled is reported as unfillable:\n%s", out)
	}
}

// TestSW248_JSONCarriesTheReachabilityAxis keeps the machine form level with the
// human one. `graphi doctor -divergence --json` is what a CI leg reads, and a
// distinction that exists only in prose cannot be gated on.
func TestSW248_JSONCarriesTheReachabilityAxis(t *testing.T) {
	raw := jsonBytes(t, shapeThree(t))
	var got struct {
		State              string `json:"state"`
		ReachEvaluated     bool   `json:"reach_evaluated"`
		DefaultProfile     string `json:"default_profile"`
		ReachableInDefault int    `json:"reachable_in_default"`
		Profiles           []struct {
			ID      string   `json:"id"`
			Default bool     `json:"default"`
			Reaches []string `json:"reaches"`
		} `json:"profiles"`
		Operations []struct {
			Operation string   `json:"operation"`
			Reach     string   `json:"reach"`
			ReachedBy []string `json:"reached_by"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if got.State != string(StateUnobservable) {
		t.Errorf("json state = %q, want %q", got.State, StateUnobservable)
	}
	if !got.ReachEvaluated || got.DefaultProfile != "graphi mcp" || got.ReachableInDefault != 0 {
		t.Errorf("json reach summary = (%v, %q, %d), want (true, \"graphi mcp\", 0)",
			got.ReachEvaluated, got.DefaultProfile, got.ReachableInDefault)
	}
	if len(got.Profiles) != 2 {
		t.Fatalf("json echoes %d profile(s), want 2 — a reader must be able to check the conclusion", len(got.Profiles))
	}
	for _, op := range got.Operations {
		if op.Reach != string(ReachOptIn) {
			t.Errorf("%s: json reach = %q, want %q", op.Operation, op.Reach, ReachOptIn)
		}
		if len(op.ReachedBy) != 1 || op.ReachedBy[0] != "graphi mcp -labs" {
			t.Errorf("%s: json reached_by = %v, want [graphi mcp -labs]", op.Operation, op.ReachedBy)
		}
	}
}

// TestSW248_TheTableSeparatesStateFromReachability guards the shape of the
// readout itself. Folding reachability into OperationState would have been the
// smaller diff and would have lost a fact: an operation can be unreachable AND
// diverged, and a single column cannot say both.
func TestSW248_TheTableSeparatesStateFromReachability(t *testing.T) {
	out := renderString(t, shapeTwo(t))
	if !strings.Contains(out, "STATE") || !strings.Contains(out, "REACHABLE VIA") {
		t.Fatalf("the table does not carry both axes:\n%s", out)
	}
	for _, want := range []string{"reachable_op", "labs_only_op"} {
		if !strings.Contains(out, want) {
			t.Errorf("the table omits %s:\n%s", want, out)
		}
	}
}
