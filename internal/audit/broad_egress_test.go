//go:build graphi_broad

package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/internal/canary"
)

// These tests run ONLY under `-tags graphi_broad` (CGO_ENABLED=1), the live broad
// CGO lane (SW-056 SEC-E / AC2). They are the ONLY mechanism that covers C-level
// egress: the static canary.RunGate AST scan is Go-source-only and structurally
// blind to a C-level socket()/connect(), and the default-tier egress_test runs the
// CGO=0 default registry (it does NOT transfer to the forest path). Both jobs in
// privacy-audit-broad.yml are pinned to Linux netns: a non-Linux/unprivileged
// runner cannot deny egress and the isolator reports UNVERIFIED (never a false
// green), exactly mirroring the SW-049 privacy-audit gate.

const zigBroadEgressSource = `const std = @import("std");

fn add(a: i32, b: i32) i32 {
    return a + b;
}

pub fn main() void {
    const x = add(1, 2);
    _ = x;
}
`

// broadForestParseDriver drives the FOREST (CGO) parse path under the deny-egress
// isolator. A correct broad parse performs pure-CPU CST work and dials nothing —
// neither at the Go level nor (under real netns) at the C level. It records no dial,
// so under real isolation the "Zero outbound network" check is CONFIRMED.
type broadForestParseDriver struct{ t *testing.T }

func (d broadForestParseDriver) Drive(ctx context.Context, _ canary.SurfaceUnion, _ *canary.DialRecorder) error {
	reg := parse.RegisterBroad(parse.NewRegistry())
	// A thin fixture may legitimately return a parse error; egress is the property
	// under test, so the parse error is intentionally ignored.
	_, _ = reg.Parse(ctx, "pkg/sample.zig", []byte(zigBroadEgressSource))
	return nil
}

// broadEgressTripwireDriver deliberately records a non-loopback dial under
// isolation, proving the broad-lane deny-egress harness has teeth: such an attempt
// MUST turn the verdict red. A silently-passing CGO-lane audit is worse than none
// (DN-3) — this is the broad analogue of the SW-049 tripwire.
type broadEgressTripwireDriver struct{}

func (broadEgressTripwireDriver) Drive(_ context.Context, _ canary.SurfaceUnion, rec *canary.DialRecorder) error {
	rec.Record(canary.DialAttempt{Tool: "broad-tripwire", Network: "tcp", Address: "8.8.8.8:53"})
	return nil
}

// TestBroadForestPath_RealIsolation_ZeroEgress runs the FOREST (CGO) parse path
// under the platform's REAL isolator. On Linux CI (netns available) it proves the
// broad smoke parse performs ZERO outbound network (CONFIRMED / green). Off-Linux
// (no isolation) it asserts the honest UNVERIFIED outcome — never a false green —
// so the broad lane's zero-egress guarantee is only credited where it is actually
// observed (the gating Linux job).
func TestBroadForestPath_RealIsolation_ZeroEgress(t *testing.T) {
	iso := canary.DefaultIsolator()
	r := RunWithIsolator(context.Background(), "./...", iso, broadForestParseDriver{t: t})

	zo := find(r, "Zero outbound network")
	if iso.IsAvailable() {
		// Linux CI path: the forest parse must be observed dialing NOTHING.
		if zo.Status != StatusPass {
			t.Fatalf("broad forest parse under netns must be zero-egress (CONFIRMED); got status=%s evidence=%s", zo.Status, zo.Evidence)
		}
		if r.ExitCode() != 0 {
			t.Fatalf("broad forest parse under netns must be a verified green; posture=%s exit=%d", r.Posture(), r.ExitCode())
		}
	} else {
		if zo.Status != StatusUnverified {
			t.Fatalf("no isolation: status=%s, want UNVERIFIED (not a false green)", zo.Status)
		}
		t.Logf("no network isolation on this runner; UNVERIFIED is the honest verdict (Linux CI gate enforces the broad-lane zero-egress)")
	}
}

// TestBroadTripwire_RealIsolation_EgressFailsTheGate proves the broad-lane
// deny-egress harness actually denies egress: a deliberate non-loopback dial under
// real isolation must turn the verdict red (FAIL). Off-Linux it must report
// UNVERIFIED, never a false pass.
func TestBroadTripwire_RealIsolation_EgressFailsTheGate(t *testing.T) {
	iso := canary.DefaultIsolator()
	r := RunWithIsolator(context.Background(), "./...", iso, broadEgressTripwireDriver{})

	if r.ExitCode() == 0 {
		t.Fatalf("broad tripwire produced a green exit; posture=%s — the CGO-lane gate has no teeth", r.Posture())
	}
	zo := find(r, "Zero outbound network")
	if iso.IsAvailable() {
		if zo.Status != StatusFail {
			t.Fatalf("isolation available but broad tripwire egress not caught: status=%s evidence=%s", zo.Status, zo.Evidence)
		}
		if !strings.Contains(zo.Evidence, "egress detected") {
			t.Fatalf("FAIL evidence must say 'egress detected', got: %s", zo.Evidence)
		}
		if r.Posture() != "VIOLATED" {
			t.Fatalf("posture = %s, want VIOLATED on a broad tripwire egress", r.Posture())
		}
	} else {
		if zo.Status != StatusUnverified {
			t.Fatalf("no isolation: status=%s, want UNVERIFIED (not a false green)", zo.Status)
		}
		t.Logf("no network isolation on this runner; UNVERIFIED is the honest verdict (Linux CI gate enforces the broad-lane egress tripwire)")
	}
}
