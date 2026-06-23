package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/canary"
)

// egressTripwireDriver deliberately records a non-loopback dial attempt under
// isolation, proving the deny-egress harness has teeth: such an attempt MUST
// turn the privacy-audit verdict red. This mirrors the SW-008 "hard-fail rather
// than silently pass" contract (internal/canary/netns.go).
type egressTripwireDriver struct{}

func (egressTripwireDriver) Drive(_ context.Context, _ canary.SurfaceUnion, rec *canary.DialRecorder) error {
	rec.Record(canary.DialAttempt{Tool: "tripwire", Network: "tcp", Address: "8.8.8.8:53"})
	return nil
}

// TestTripwire_RealIsolation_EgressFailsTheGate runs under the platform's REAL
// isolator. On Linux CI (netns available) it proves a deliberate non-loopback
// dial FAILS the audit (red gate). Off-Linux (no isolation) it asserts the
// honest UNVERIFIED + non-zero outcome instead of a false green. Either way the
// audit never reports a verified PASS when egress is present or unobservable.
func TestTripwire_RealIsolation_EgressFailsTheGate(t *testing.T) {
	iso := canary.DefaultIsolator()
	r := RunWithIsolator(context.Background(), "./...", iso, egressTripwireDriver{})

	// Under no circumstances may a tripwire run be a verified green.
	if r.ExitCode() == 0 {
		t.Fatalf("tripwire produced a green exit; posture=%s — the gate has no teeth", r.Posture())
	}
	zo := find(r, "Zero outbound network")
	if iso.IsAvailable() {
		// Linux CI path: the deliberate non-loopback dial must be caught → FAIL.
		if zo.Status != StatusFail {
			t.Fatalf("isolation available but tripwire egress not caught: status=%s evidence=%s", zo.Status, zo.Evidence)
		}
		if !strings.Contains(zo.Evidence, "egress detected") {
			t.Fatalf("FAIL evidence must say 'egress detected', got: %s", zo.Evidence)
		}
		if r.Posture() != "VIOLATED" {
			t.Fatalf("posture = %s, want VIOLATED on a tripwire egress", r.Posture())
		}
	} else {
		// Off-Linux: no isolation → UNVERIFIED, never a false pass (AC-6).
		if zo.Status != StatusUnverified {
			t.Fatalf("no isolation: status=%s, want UNVERIFIED (not a false green)", zo.Status)
		}
		t.Logf("no network isolation on this runner; UNVERIFIED is the honest verdict (Linux CI gate enforces the egress tripwire)")
	}
}
