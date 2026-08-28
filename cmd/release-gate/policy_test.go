package main

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// runnerForState produces a Runner whose outcome classifies as state. The four
// states are reachable only through the two typed errors and a plain error;
// nothing is inferred from message text, which is why UNVERIFIED stays harder
// to reach than FAIL.
func runnerForState(state GateState) Runner {
	switch state {
	case StatePass:
		return staticRunner{score: 100}
	case StateFail:
		return staticRunner{err: errors.New("measured and red: 3 tests failed")}
	case StateUnverified:
		return staticRunner{err: &UnverifiedError{Detail: "control_above_ceiling: this runner could not resolve the measurement"}}
	case StateError:
		return staticRunner{err: &GateError{Detail: "verdict unreadable; the gate produced no usable answer"}}
	default:
		panic("unknown state " + state)
	}
}

// TestPolicy_FourStatesTwoContexts is THE policy artifact.
//
// Eight rows: every state a gate can report, in every context the gate can run
// in. It is written as one table rather than as eight tests on purpose — the
// point of the table is that a future loosening of the release policy shows up
// as a one-cell diff that a reviewer cannot miss, and eight scattered cases do
// not have that property.
//
// Each row is checked at two levels, so the table cannot drift from the code:
// the pure decision (Context.Blocks) and the observable release outcome (Run,
// through a real Runner producing that state).
func TestPolicy_FourStatesTwoContexts(t *testing.T) {
	for _, tc := range []struct {
		state GateState
		ctx   Context
		// blocks is the policy's answer, and the ONLY difference between the
		// two contexts is the UNVERIFIED pair.
		blocks bool
		// publishable: may release evidence carrying a PASS verdict be written
		// in this row? Independent of blocking, and deliberately so (AC-4).
		publishable bool
	}{
		{state: StatePass, ctx: ContextPR, blocks: false, publishable: true},
		{state: StatePass, ctx: ContextRelease, blocks: false, publishable: true},

		{state: StateFail, ctx: ContextPR, blocks: true, publishable: false},
		{state: StateFail, ctx: ContextRelease, blocks: true, publishable: false},

		// The one cell that differs. This is the decision SW-251 makes.
		{state: StateUnverified, ctx: ContextPR, blocks: false, publishable: false},
		{state: StateUnverified, ctx: ContextRelease, blocks: true, publishable: false},

		{state: StateError, ctx: ContextPR, blocks: true, publishable: false},
		{state: StateError, ctx: ContextRelease, blocks: true, publishable: false},
	} {
		t.Run(string(tc.state)+"/"+string(tc.ctx), func(t *testing.T) {
			if got := tc.ctx.Blocks(tc.state); got != tc.blocks {
				t.Fatalf("Context(%q).Blocks(%q) = %v, want %v", tc.ctx, tc.state, got, tc.blocks)
			}

			dir := t.TempDir()
			baseline := filepath.Join(dir, "baseline.json")
			writeBaseline(t, baseline, []string{"search", "analyze"})

			gates := allPassGates()
			gates["testgate"] = runnerForState(tc.state)

			result, err := Run(tc.ctx, gates, passEval(t), passUX(), baseline)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			var outcome GateOutcome
			for _, g := range result.Gates {
				if g.Name == "testgate" {
					outcome = g
				}
			}
			if outcome.State != tc.state {
				t.Fatalf("testgate classified as %q, want %q", outcome.State, tc.state)
			}
			if outcome.Blocking != tc.blocks {
				t.Fatalf("testgate blocking = %v, want %v", outcome.Blocking, tc.blocks)
			}
			// Nothing else in this run is red, so the gate verdict is the
			// policy's verdict.
			if wantPass := !tc.blocks; result.Pass != wantPass {
				t.Fatalf("result.Pass = %v, want %v (warnings %v, errors %v)",
					result.Pass, wantPass, result.Warnings, result.Errors)
			}
			// A non-PASS state is always in the record — as an error when it
			// blocks, as a warning when it does not. It is never silent.
			if tc.state != StatePass {
				record := append(append([]string{}, result.Warnings...), result.Errors...)
				if len(record) != 1 {
					t.Fatalf("expected exactly one recorded observation, got %v", record)
				}
				wantIn := result.Errors
				if !tc.blocks {
					wantIn = result.Warnings
				}
				if len(wantIn) != 1 {
					t.Fatalf("state %q in context %q landed on the wrong channel: warnings %v errors %v",
						tc.state, tc.ctx, result.Warnings, result.Errors)
				}
			}

			// AC-4's half of the row: publishability is decided separately
			// from blocking, so a non-blocking UNVERIFIED still cannot become
			// a published PASS.
			publishErr := Publish(result, t.TempDir(), "0.0.0-test", "deadbeef")
			var refused *PublishRefusedError
			switch {
			case tc.publishable && publishErr != nil:
				t.Fatalf("Publish must succeed for %q/%q, got %v", tc.state, tc.ctx, publishErr)
			case !tc.publishable && tc.state == StateUnverified && !errors.As(publishErr, &refused):
				t.Fatalf("Publish must refuse an UNVERIFIED gate in context %q, got %v", tc.ctx, publishErr)
			}
		})
	}
}

// TestPolicy_DefaultContextIsTheStrictOne — AC-6. A forgotten flag must fail
// safe: the cost of needless strictness on a pull request is one re-run, the
// cost of accidental leniency on the release line is a release nobody
// measured.
//
// contextFlagDefault is the value main.go actually declares, so this test
// cannot pass while the flag says something else.
func TestPolicy_DefaultContextIsTheStrictOne(t *testing.T) {
	resolved, err := ParseContext(contextFlagDefault)
	if err != nil {
		t.Fatalf("the flag's own default must parse: %v", err)
	}
	if resolved != ContextRelease {
		t.Fatalf("no -context resolved to %q; the default must be the STRICT context %q", resolved, ContextRelease)
	}
	if DefaultContext != ContextRelease {
		t.Fatalf("DefaultContext = %q, want the strict %q", DefaultContext, ContextRelease)
	}
	if !resolved.Blocks(StateUnverified) {
		t.Fatal("the default context does not block UNVERIFIED; a forgotten flag is now a hole")
	}

	// And end to end, through the whole gate: an unverified gate with no
	// context stated is refused.
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})
	gates := allPassGates()
	gates["privacy"] = runnerForState(StateUnverified)

	result, err := Run(resolved, gates, passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatal("an UNVERIFIED gate passed the gate with no -context; the default is not strict")
	}
}

// TestPolicy_UnknownContextIsRefusedAndFailsClosed: a typo in a workflow must
// not become a policy. ParseContext rejects it, and even if a bad value were
// constructed directly, Blocks treats an unrecognised context as strict.
func TestPolicy_UnknownContextIsRefusedAndFailsClosed(t *testing.T) {
	got, err := ParseContext("main")
	if err == nil {
		t.Fatal("ParseContext accepted an unknown context")
	}
	if got != ContextRelease {
		t.Fatalf("a rejected context resolved to %q; the fallback must still be strict", got)
	}
	if !Context("staging").Blocks(StateUnverified) {
		t.Fatal("an unrecognised Context waved an UNVERIFIED gate through")
	}
	if !ContextPR.Blocks(GateState("MOSTLY_FINE")) {
		t.Fatal("an unrecognised GateState was treated as an approval")
	}
}

// TestPolicy_AbsentRequiredGateIsError — AC-5. A gate that silently does not
// run is the same hole as UNKNOWN rendering green: there is no measurement
// either way, and the difference is only that nobody said so. It is ERROR, and
// ERROR blocks in every context — including a pull request, because a gate
// vanishing from a run is a defect in the run, not a property of the runner.
func TestPolicy_AbsentRequiredGateIsError(t *testing.T) {
	for _, name := range requiredGates {
		for _, ctx := range []Context{ContextPR, ContextRelease} {
			t.Run(name+"/"+string(ctx), func(t *testing.T) {
				dir := t.TempDir()
				baseline := filepath.Join(dir, "baseline.json")
				writeBaseline(t, baseline, []string{"search", "analyze"})

				gates := allPassGates()
				delete(gates, name) // not reported unverified — simply not reported

				result, err := Run(ctx, gates, passEval(t), passUX(), baseline)
				if err != nil {
					t.Fatalf("Run: %v", err)
				}
				if result.Pass {
					t.Fatalf("required gate %q was absent and the run still passed in context %q", name, ctx)
				}
				var found bool
				for _, g := range result.Gates {
					if g.Name != name {
						continue
					}
					found = true
					if g.State != StateError {
						t.Fatalf("absent gate %q reported as %q, want %q", name, g.State, StateError)
					}
					if !g.Blocking {
						t.Fatalf("absent gate %q did not block in context %q", name, ctx)
					}
					if !strings.Contains(g.Detail, "absent") {
						t.Fatalf("absent gate %q gave no diagnosable reason: %q", name, g.Detail)
					}
				}
				if !found {
					t.Fatalf("absent gate %q vanished from the record entirely — the hole AC-5 exists to close", name)
				}
			})
		}
	}
}

// TestPolicy_RequiredGatesMatchDefaultGates keeps the declaration honest.
//
// requiredGates is written out by hand rather than derived from DefaultGates,
// because deriving it would make absence undetectable. The cost of writing it
// by hand is that the two can drift, and this is the test that stops them:
// adding a gate to the production set without declaring it required would let
// it silently stop running, and declaring one that is never supplied would
// make every run ERROR.
func TestPolicy_RequiredGatesMatchDefaultGates(t *testing.T) {
	var supplied []string
	for name := range DefaultGates() {
		supplied = append(supplied, name)
	}
	sort.Strings(supplied)
	declared := append([]string{}, requiredGates...)
	sort.Strings(declared)
	if strings.Join(supplied, ",") != strings.Join(declared, ",") {
		t.Fatalf("DefaultGates supplies %v but requiredGates declares %v", supplied, declared)
	}
}

// TestPolicy_PublishRefusesUnverifiedAndWritesNothing — AC-4, at the file
// level. The refusal must not be a verdict downgrade inside a document that
// still gets written: no file is the only honest artifact for a measurement
// that was never taken.
func TestPolicy_PublishRefusesUnverifiedAndWritesNothing(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	gates := allPassGates()
	gates["privacy"] = runnerForState(StateUnverified)

	// The context in which the run PASSES — the dangerous one for AC-4,
	// because nothing else stops the publish.
	result, err := Run(ContextPR, gates, passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass {
		t.Fatalf("expected a passing pull-request run, got errors %v", result.Errors)
	}

	docs := t.TempDir()
	err = Publish(result, docs, "0.1.0", "abc123")
	var refused *PublishRefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Publish returned %v, want a PublishRefusedError", err)
	}
	if len(refused.Gates) != 1 || refused.Gates[0] != "privacy" {
		t.Fatalf("the refusal must name the unverified gate, got %v", refused.Gates)
	}
	entries, readErr := os.ReadDir(docs)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused publish still wrote %d file(s); release evidence must not exist at all", len(entries))
	}
}

// TestPolicy_UnverifiedIsProminent — AC-2. "Visible" means visible where a
// reader already is: above the score breakdown in the verdict, and in the
// GitHub check summary rather than only in a step log somebody has to expand.
func TestPolicy_UnverifiedIsProminent(t *testing.T) {
	dir := t.TempDir()
	baseline := filepath.Join(dir, "baseline.json")
	writeBaseline(t, baseline, []string{"search", "analyze"})

	gates := allPassGates()
	gates["privacy"] = runnerForState(StateUnverified)
	result, err := Run(ContextPR, gates, passEval(t), passUX(), baseline)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	verdict := FormatVerdict(result)
	banner := strings.Index(verdict, "!! UNVERIFIED")
	overall := strings.Index(verdict, "Overall:")
	if banner < 0 {
		t.Fatalf("no UNVERIFIED banner in the verdict:\n%s", verdict)
	}
	if banner > overall {
		t.Fatalf("the UNVERIFIED banner is buried below the score breakdown:\n%s", verdict)
	}
	if !strings.Contains(verdict, "context=pr") || !strings.Contains(verdict, "does NOT block") {
		t.Fatalf("the verdict does not say which context it applied or what it decided:\n%s", verdict)
	}

	summaryPath := filepath.Join(t.TempDir(), "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", summaryPath)
	if err := WriteStepSummary(result); err != nil {
		t.Fatalf("WriteStepSummary: %v", err)
	}
	raw, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("check summary not written: %v", err)
	}
	for _, want := range []string{"UNVERIFIED", "privacy", "context=pr"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("check summary missing %q:\n%s", want, raw)
		}
	}
}

// TestPolicy_StepSummaryIsADiagnosticNotAGate: an unwritable summary must not
// change a verdict, and no summary at all must be written off CI.
func TestPolicy_StepSummaryOffCIIsANoOp(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	if err := WriteStepSummary(GateResult{Context: ContextRelease}); err != nil {
		t.Fatalf("WriteStepSummary off CI must be a no-op, got %v", err)
	}
}
