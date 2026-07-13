package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGate(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "publish-lock.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// AC: while the publish lock is engaged, the decision is Locked so the workflow
// pushes no tag and dispatches no release.
func TestEvaluateLockedGateBlocks(t *testing.T) {
	path := writeGate(t, `{"locked": true, "reason": "CT-01: RC not yet Go"}`)
	d := Evaluate(path)
	if !d.Locked {
		t.Fatalf("engaged gate must be Locked, got %+v", d)
	}
	if !strings.Contains(d.Reason, "CT-01") {
		t.Fatalf("reason should carry the gate-file reason, got %q", d.Reason)
	}
	if got := d.OutputLine(); got != "locked=true" {
		t.Fatalf("OutputLine=%q, want locked=true", got)
	}
	if n := d.Notice(); !strings.Contains(n, "publish locked (CT-01)") {
		t.Fatalf("locked notice must name CT-01, got %q", n)
	}
}

// AC: the lock is reversible by a single documented change — flipping locked to
// false in the gate file lifts it, no workflow re-authoring.
func TestEvaluateUnlockedGateAllows(t *testing.T) {
	path := writeGate(t, `{"locked": false, "reason": "RC-01 Go"}`)
	d := Evaluate(path)
	if d.Locked {
		t.Fatalf("disengaged gate must be unlocked, got %+v", d)
	}
	if got := d.OutputLine(); got != "locked=false" {
		t.Fatalf("OutputLine=%q, want locked=false", got)
	}
	if n := d.Notice(); n != "" {
		t.Fatalf("unlocked state emits no lock notice, got %q", n)
	}
}

// AC#5 (the "intentionally red gate creates no tag/release" exit evidence): an
// ungated / broken state must fail closed — no tag, no release. A missing gate
// file is exactly that deliberately-red state.
func TestEvaluateMissingGateFailsClosed(t *testing.T) {
	d := Evaluate(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if !d.Locked {
		t.Fatalf("missing gate file must fail closed (Locked), got %+v", d)
	}
	if got := d.OutputLine(); got != "locked=true" {
		t.Fatalf("OutputLine=%q, want locked=true", got)
	}
}

// AC#5: a malformed gate file is an ungated/broken state — also fail closed.
func TestEvaluateMalformedGateFailsClosed(t *testing.T) {
	path := writeGate(t, `{ this is not json `)
	d := Evaluate(path)
	if !d.Locked {
		t.Fatalf("malformed gate file must fail closed, got %+v", d)
	}
}

// AC#5: a gate file that omits the "locked" field is not a valid unlock — it is
// an ambiguous/broken state and must fail closed rather than silently publish.
func TestEvaluateMissingFieldFailsClosed(t *testing.T) {
	path := writeGate(t, `{"reason": "no locked field here"}`)
	d := Evaluate(path)
	if !d.Locked {
		t.Fatalf("gate file without a locked field must fail closed, got %+v", d)
	}
}
