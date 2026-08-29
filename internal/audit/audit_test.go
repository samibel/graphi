package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/buildattest"
	"github.com/samibel/graphi/internal/canary"
	"github.com/samibel/graphi/internal/releaseinfo"
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

func TestAttestedStaticChecksDescribeAndBindTheRunningBinary(t *testing.T) {
	info := releaseinfo.New()
	if info.Commit() == "" {
		t.Skip("go test binary has no VCS revision")
	}
	checks := attestedStaticChecks(buildattest.Privacy{
		SchemaVersion:  buildattest.PrivacySchemaVersion,
		Status:         "PASS",
		GateID:         buildattest.PrivacyGateID,
		Scope:          buildattest.PrivacyScope,
		SourceRevision: info.Commit(),
		EvidenceDigest: strings.Repeat("a", 64),
		CGOEnabled:     "0",
		GOOS:           strings.Split(info.Arch(), "/")[0],
		GOARCH:         strings.Split(info.Arch(), "/")[1],
	})
	if len(checks) != 3 {
		t.Fatalf("attested checks = %d, want 3", len(checks))
	}
	for _, check := range checks {
		if check.Status != StatusPass || check.Scope != "build-time attestation for this binary" {
			t.Fatalf("attested check is not a scoped PASS: %+v", check)
		}
	}
	binding := find(Report{Checks: checks}, "Build evidence binding")
	for _, want := range []string{"binary_sha256=", "source_commit=" + info.Commit(), "not an independent signature"} {
		if !strings.Contains(binding.Evidence, want) {
			t.Fatalf("attested binding missing %q: %s", want, binding.Evidence)
		}
	}
}

func TestAttestedStaticChecksRejectRevisionMismatch(t *testing.T) {
	checks := attestedStaticChecks(buildattest.Privacy{
		SchemaVersion:  buildattest.PrivacySchemaVersion,
		Status:         "PASS",
		GateID:         buildattest.PrivacyGateID,
		Scope:          buildattest.PrivacyScope,
		SourceRevision: strings.Repeat("f", 40),
		EvidenceDigest: strings.Repeat("a", 64),
		CGOEnabled:     "0",
		GOOS:           "invalid",
		GOARCH:         "invalid",
	})
	for _, check := range checks {
		if check.Status != StatusUnverified {
			t.Fatalf("revision mismatch became %s, want UNVERIFIED: %+v", check.Status, check)
		}
	}
}

func TestSourceUnavailableMessageNamesTheConditionNotTheToolchainExit(t *testing.T) {
	t.Chdir(t.TempDir())
	checks := staticPrivacyChecks(context.Background(), "./...", true, nil)
	if len(checks) != 2 {
		t.Fatalf("static checks = %d, want 2", len(checks))
	}
	for _, check := range checks {
		if check.Status != StatusUnverified {
			t.Fatalf("source-unavailable check = %s, want UNVERIFIED", check.Status)
		}
		if !strings.Contains(check.Evidence, "graphi source module is not available") {
			t.Fatalf("message does not name the real condition: %s", check.Evidence)
		}
		if strings.Contains(check.Evidence, "go list") || strings.Contains(check.Evidence, "exit status") {
			t.Fatalf("message leaked a raw toolchain failure: %s", check.Evidence)
		}
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

// honestBinding is the toolchain ground truth for a binary that was genuinely
// built the canonical way: a clean tree at rev, linked with CGO_ENABLED=0.
func honestBinding(rev string) binaryBinding {
	return binaryBinding{
		Revision:   rev,
		Modified:   false,
		CGOEnabled: "0",
		SHA256:     strings.Repeat("c", 64),
		Arch:       "darwin/arm64",
	}
}

// honestAttestation is the payload the canonical builder would emit for
// honestBinding. Mutating one field at a time isolates a single lie.
func honestAttestation(rev string) buildattest.Privacy {
	return buildattest.Privacy{
		SchemaVersion:  buildattest.PrivacySchemaVersion,
		Status:         "PASS",
		GateID:         buildattest.PrivacyGateID,
		Scope:          buildattest.PrivacyScope,
		SourceRevision: rev,
		SourceModified: false,
		EvidenceDigest: strings.Repeat("a", 64),
		CGOEnabled:     "0",
		GOOS:           "darwin",
		GOARCH:         "arm64",
	}
}

// Control for the mismatch tests below: an attestation that agrees with the
// binding on every cross-checkable field still reaches the attested PASS rows,
// so a mismatch test failing UNVERIFIED is proof of the specific check and not
// of a blanket downgrade.
func TestAttestedStaticChecksAcceptAnAgreeingBinding(t *testing.T) {
	rev := strings.Repeat("d", 40)
	checks := attestedStaticChecksFor(honestBinding(rev), honestAttestation(rev))
	if len(checks) != 3 {
		t.Fatalf("agreeing binding produced %d checks, want 3", len(checks))
	}
	for _, check := range checks {
		if check.Status != StatusPass || check.Scope != "build-time attestation for this binary" {
			t.Fatalf("agreeing binding did not produce an attested PASS: %+v", check)
		}
	}
}

// Forgery 1 from the PR-179 review: a binary built from a genuinely dirty tree
// (the toolchain recorded vcs.modified=true) carrying a hand-forged payload that
// claims source_modified=false. Before the SourceModified cross-check this
// rendered "source_state=clean" and three PASS rows.
func TestAttestedStaticChecksRejectSourceModifiedMismatch(t *testing.T) {
	rev := strings.Repeat("d", 40)
	binding := honestBinding(rev)
	binding.Modified = true // toolchain ground truth: the tree was dirty
	att := honestAttestation(rev)
	att.SourceModified = false // the forged claim

	checks := attestedStaticChecksFor(binding, att)
	if len(checks) != 2 {
		t.Fatalf("source-state mismatch produced %d checks, want the 2 unavailable rows", len(checks))
	}
	for _, check := range checks {
		if check.Status != StatusUnverified {
			t.Fatalf("source-state mismatch became %s, want UNVERIFIED: %+v", check.Status, check)
		}
		if !strings.Contains(check.Evidence, "source state") {
			t.Fatalf("source-state mismatch did not name the condition: %q", check.Evidence)
		}
	}
	// The inverse lie (claiming modified while the binary is clean) is also a
	// disagreement and must not be accepted either.
	binding.Modified = false
	att.SourceModified = true
	for _, check := range attestedStaticChecksFor(binding, att) {
		if check.Status != StatusUnverified {
			t.Fatalf("inverse source-state mismatch became %s, want UNVERIFIED: %+v", check.Status, check)
		}
	}
}

// Forgery 2 from the PR-179 review: a binary genuinely linked with
// CGO_ENABLED=1 carrying a payload that claims cgo_enabled="0". Before the
// CGOEnabled cross-check this printed "CGo-free build [PASS] ... canonical
// build contract enforced CGO_ENABLED=0" — a false statement about itself.
func TestAttestedStaticChecksRejectCGOEnabledMismatch(t *testing.T) {
	rev := strings.Repeat("d", 40)
	binding := honestBinding(rev)
	binding.CGOEnabled = "1" // toolchain ground truth: cgo was on
	att := honestAttestation(rev)
	att.CGOEnabled = "0" // the forged claim

	checks := attestedStaticChecksFor(binding, att)
	if len(checks) != 2 {
		t.Fatalf("cgo mismatch produced %d checks, want the 2 unavailable rows", len(checks))
	}
	for _, check := range checks {
		if check.Status != StatusUnverified {
			t.Fatalf("cgo mismatch became %s, want UNVERIFIED: %+v", check.Status, check)
		}
		if !strings.Contains(check.Evidence, "CGO_ENABLED") {
			t.Fatalf("cgo mismatch did not name the condition: %q", check.Evidence)
		}
		if check.Name == "CGo-free build" && strings.Contains(check.Evidence, "canonical build contract enforced") {
			t.Fatalf("cgo mismatch still claims the canonical contract: %q", check.Evidence)
		}
	}
}

// A binary whose build settings carry no CGO_ENABLED entry has no ground truth
// to compare against, so it must not be treated as agreement.
func TestAttestedStaticChecksRejectMissingCGOGroundTruth(t *testing.T) {
	rev := strings.Repeat("d", 40)
	binding := honestBinding(rev)
	binding.CGOEnabled = ""
	for _, check := range attestedStaticChecksFor(binding, honestAttestation(rev)) {
		if check.Status != StatusUnverified {
			t.Fatalf("absent CGO_ENABLED ground truth became %s, want UNVERIFIED: %+v", check.Status, check)
		}
	}
}

// runningBinaryBinding must refuse to bind unless BOTH toolchain facts it
// cross-checks are actually available, and must carry them through unchanged.
// It asserts in either world, so it never silently skips.
func TestRunningBinaryBindingRefusesIncompleteGroundTruth(t *testing.T) {
	info := releaseinfo.New()
	b, err := runningBinaryBinding()
	if info.Commit() == "" || info.CGOEnabled() == "" {
		if err == nil {
			t.Fatalf("binding succeeded with incomplete ground truth (commit=%q cgo=%q)", info.Commit(), info.CGOEnabled())
		}
		return
	}
	if err != nil {
		t.Fatalf("binding failed with complete ground truth: %v", err)
	}
	if b.CGOEnabled != info.CGOEnabled() {
		t.Fatalf("binding cgo = %q, want the recorded %q", b.CGOEnabled, info.CGOEnabled())
	}
	if b.Modified != info.Modified() {
		t.Fatalf("binding modified = %v, want the recorded %v", b.Modified, info.Modified())
	}
}
