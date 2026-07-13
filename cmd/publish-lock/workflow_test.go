package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	committedGate = "../../.github/publish-lock.json"
	autoRelease   = "../../.github/workflows/auto-release.yml"
)

// TestCommittedGateIsEngaged pins the shipped posture: the checked-in gate file
// is LOCKED, so the live auto-release chain publishes nothing until RC-01 lifts
// it. This is the standing state the whole G0 program depends on.
func TestCommittedGateIsEngaged(t *testing.T) {
	d := Evaluate(committedGate)
	if !d.Locked {
		t.Fatalf("committed %s must be engaged (locked); got %+v", committedGate, d)
	}
	if d.OutputLine() != "locked=true" {
		t.Fatalf("committed gate must emit locked=true, got %q", d.OutputLine())
	}
}

// TestAutoReleaseGatesMutatingStepsOnLock is the workflow-level assertion the
// story asks for: it proves the auto-release logic short-circuits BEFORE tag
// push and BEFORE `gh workflow run` while locked. Rather than run `act`, it
// asserts the two mutating steps (git tag push, release.yml dispatch) are
// statically gated on `steps.lock.outputs.locked != 'true'`, and that the lock
// step that produces that output exists. Combined with TestCommittedGateIs
// Engaged (committed gate → locked=true), a locked run cannot reach either
// mutating step.
func TestAutoReleaseGatesMutatingStepsOnLock(t *testing.T) {
	raw, err := os.ReadFile(autoRelease)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(raw)

	// The lock-check step must run the gate binary and expose its id.
	if !strings.Contains(yaml, "go run ./cmd/publish-lock") {
		t.Fatal("auto-release.yml must run `go run ./cmd/publish-lock` to evaluate the lock")
	}
	if !regexp.MustCompile(`(?m)^\s*id:\s*lock\s*$`).MatchString(yaml) {
		t.Fatal("the publish-lock step must have `id: lock` so downstream steps can gate on its output")
	}

	// Every step that pushes a tag or dispatches release.yml must be guarded by
	// the lock output. Anchor on the step names so the assertion is precise.
	guard := "steps.lock.outputs.locked != 'true'"
	for _, step := range []string{
		"create and push the tag if it doesn't exist yet",
		"dispatch release.yml if this tag has no release yet",
	} {
		ifLine := stepIfLine(t, yaml, step)
		if !strings.Contains(ifLine, guard) {
			t.Fatalf("mutating step %q must be gated on the lock (%q); its if was: %q", step, guard, ifLine)
		}
	}

	// Defense-in-depth: the raw mutating commands must not appear outside a
	// lock-gated step. There must be no un-guarded `git push origin` of a tag or
	// `gh workflow run release.yml`. We assert both commands are present exactly
	// once (they live only in the two gated steps).
	if got := strings.Count(yaml, "gh workflow run release.yml"); got != 1 {
		t.Fatalf("expected exactly one `gh workflow run release.yml` (in the gated dispatch step), found %d", got)
	}
	if got := strings.Count(yaml, "git push origin"); got != 1 {
		t.Fatalf("expected exactly one tag push (in the gated tag step), found %d", got)
	}
}

// stepIfLine returns the `if:` expression attached to the named workflow step.
func stepIfLine(t *testing.T, yaml, stepName string) string {
	t.Helper()
	lines := strings.Split(yaml, "\n")
	nameIdx := -1
	for i, ln := range lines {
		if strings.Contains(ln, "name: "+stepName) {
			nameIdx = i
			break
		}
	}
	if nameIdx == -1 {
		t.Fatalf("step %q not found in auto-release.yml", stepName)
	}
	// The `if:` for a step precedes or follows `name:` within the same step
	// block; scan a small window around the name line for the step's if.
	ifRe := regexp.MustCompile(`^\s*if:\s*(.*)$`)
	for i := nameIdx; i < len(lines) && i < nameIdx+4; i++ {
		if m := ifRe.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	for i := nameIdx - 1; i >= 0 && i > nameIdx-4; i-- {
		if m := ifRe.FindStringSubmatch(lines[i]); m != nil {
			return m[1]
		}
	}
	t.Fatalf("step %q has no `if:` guard", stepName)
	return ""
}
