package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/canary"
)

// fakeIsolator lets tests drive the isolation-available branch without root.
type fakeIsolator struct{ available bool }

func (f fakeIsolator) IsAvailable() bool { return f.available }
func (f fakeIsolator) Run(fn func() error) error {
	return fn()
}

// fakeDriver optionally injects dial attempts to simulate an egress violation.
type fakeDriver struct {
	inject []canary.DialAttempt
}

func (f fakeDriver) Drive(_ context.Context, _ canary.SurfaceUnion, rec *canary.DialRecorder) error {
	for _, a := range f.inject {
		rec.Record(a)
	}
	return nil
}

// AC-5: isolation available + a clean representative op → PASS, exit 0.
func TestAudit_LiveExercise_CleanRun_Pass(t *testing.T) {
	r := RunWithIsolator(context.Background(), "./...", fakeIsolator{available: true}, fakeDriver{})
	if !r.AllPass() {
		t.Fatalf("expected all-pass; failures: %v", failingNames(r))
	}
	if r.ExitCode() != 0 {
		t.Fatalf("exit = %d, want 0", r.ExitCode())
	}
	if r.Posture() != "CONFIRMED" {
		t.Fatalf("posture = %s, want CONFIRMED", r.Posture())
	}
	zo := find(r, "Zero outbound network")
	if zo.Status != StatusPass || !strings.Contains(zo.Evidence, "verified live under loopback-only isolation") {
		t.Fatalf("zero-outbound not a verified live pass: %+v", zo)
	}
}

// AC-5: isolation available + a non-loopback dial → FAIL "egress detected", non-zero.
func TestAudit_LiveExercise_EgressDetected_Fail(t *testing.T) {
	drv := fakeDriver{inject: []canary.DialAttempt{
		{Tool: "query:callers", Network: "tcp", Address: "telemetry.example.com:443"},
	}}
	r := RunWithIsolator(context.Background(), "./...", fakeIsolator{available: true}, drv)
	if r.AllPass() {
		t.Fatal("expected NOT all-pass when egress is detected")
	}
	if r.ExitCode() == 0 {
		t.Fatal("exit must be non-zero on egress")
	}
	if r.Posture() != "VIOLATED" {
		t.Fatalf("posture = %s, want VIOLATED", r.Posture())
	}
	zo := find(r, "Zero outbound network")
	if zo.Status != StatusFail || !strings.Contains(zo.Evidence, "egress detected") {
		t.Fatalf("zero-outbound not a FAIL with egress evidence: %+v", zo)
	}
	if len(zo.Offenders) == 0 || !strings.Contains(strings.Join(zo.Offenders, " "), "telemetry.example.com") {
		t.Fatalf("offenders must name the destination: %v", zo.Offenders)
	}
}

// AC-6 (critical): isolation NOT available → UNVERIFIED, non-zero, never PASS.
func TestAudit_NoIsolation_Unverified_NotPass(t *testing.T) {
	r := RunWithIsolator(context.Background(), "./...", fakeIsolator{available: false}, fakeDriver{})
	if r.AllPass() {
		t.Fatal("UNVERIFIED must NOT count as all-pass (false-green prevention)")
	}
	if r.ExitCode() == 0 {
		t.Fatal("UNVERIFIED must yield a non-zero exit")
	}
	if r.Posture() != "UNVERIFIED" {
		t.Fatalf("posture = %s, want UNVERIFIED", r.Posture())
	}
	zo := find(r, "Zero outbound network")
	if zo.Status != StatusUnverified {
		t.Fatalf("zero-outbound status = %s, want UNVERIFIED", zo.Status)
	}
	if zo.Status == StatusPass {
		t.Fatal("UNVERIFIED collapsed to PASS — false green")
	}
	// Render must visually distinguish UNVERIFIED from PASS.
	txt := RenderText(r)
	if !strings.Contains(txt, "? Zero outbound network") {
		t.Fatalf("render missing the '?' UNVERIFIED marker:\n%s", txt)
	}
	if !strings.Contains(txt, "[UNVERIFIED]") {
		t.Fatalf("render missing the [UNVERIFIED] status tag:\n%s", txt)
	}
	if !strings.Contains(txt, "posture: UNVERIFIED") {
		t.Fatalf("render missing the UNVERIFIED posture line:\n%s", txt)
	}
}

// UNVERIFIED status string and render text are distinct from PASS.
func TestPosture_TriState(t *testing.T) {
	pass := Report{Checks: []Check{{Name: "x", Status: StatusPass}}}
	if pass.Posture() != "CONFIRMED" || pass.ExitCode() != 0 {
		t.Fatalf("pass: posture=%s exit=%d", pass.Posture(), pass.ExitCode())
	}
	unv := Report{Checks: []Check{{Name: "x", Status: StatusPass}, {Name: "y", Status: StatusUnverified}}}
	if unv.Posture() != "UNVERIFIED" || unv.ExitCode() == 0 || unv.AllPass() {
		t.Fatalf("unverified: posture=%s exit=%d allpass=%v", unv.Posture(), unv.ExitCode(), unv.AllPass())
	}
	// A FAIL outranks UNVERIFIED in the posture line.
	mixed := Report{Checks: []Check{{Name: "y", Status: StatusUnverified}, {Name: "z", Status: StatusFail}}}
	if mixed.Posture() != "VIOLATED" {
		t.Fatalf("mixed posture = %s, want VIOLATED", mixed.Posture())
	}
}

func TestAudit_CgoEvidenceIsRealScan(t *testing.T) {
	r := RunWithIsolator(context.Background(), "./...", fakeIsolator{available: true}, fakeDriver{})
	cgo := find(r, "CGo-free build")
	if !strings.Contains(cgo.Evidence, "cgoconformance") {
		t.Fatalf("CGo evidence must cite the real scan engine, got: %s", cgo.Evidence)
	}
}

func TestRender_NonEmpty(t *testing.T) {
	r := RunWithIsolator(context.Background(), "./...", fakeIsolator{available: true}, fakeDriver{})
	txt := RenderText(r)
	if !strings.Contains(txt, "privacy-audit") || !strings.Contains(txt, "CGo-free") {
		t.Fatalf("rendered report missing expected sections:\n%s", txt)
	}
}

func find(r Report, name string) Check {
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	return Check{}
}

func failingNames(r Report) []string {
	var out []string
	for _, c := range r.Checks {
		if c.Status != StatusPass {
			out = append(out, c.Name+":"+string(c.Status))
		}
	}
	return out
}
