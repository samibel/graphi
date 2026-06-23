package audit

import (
	"context"
	"strings"
	"testing"
)

func TestAudit_CleanGraph_AllPass(t *testing.T) {
	// Scan the graphi tree itself; it is CGo-free by project invariant, so this
	// is a real PASS path (not a synthetic one).
	r := Run(context.Background(), "./...")
	if !r.AllPass() {
		t.Fatalf("expected all-pass on clean graph; failures: %v", failingNames(r))
	}
	if r.ExitCode() != 0 {
		t.Fatalf("exit code %d, want 0", r.ExitCode())
	}
}

func TestAudit_CgoEvidenceIsRealScan(t *testing.T) {
	// AC-4: the CGo check must be backed by the real scan, not a hardcoded OK.
	r := Run(context.Background(), "./...")
	var cgo Check
	for _, c := range r.Checks {
		if c.Name == "CGo-free build" {
			cgo = c
		}
	}
	if !strings.Contains(cgo.Evidence, "cgoconformance") {
		t.Fatalf("CGo evidence must cite the real scan engine, got: %s", cgo.Evidence)
	}
}

func TestAudit_ZeroOutboundCitesCanaryGuard(t *testing.T) {
	// AC-4: the zero-outbound claim must reference the dial-attempt guard, not be
	// a hardcoded string.
	r := Run(context.Background(), "./...")
	var zo Check
	for _, c := range r.Checks {
		if c.Name == "Zero outbound network" {
			zo = c
		}
	}
	if !strings.Contains(zo.Evidence, "dial-attempt guard") {
		t.Fatalf("zero-outbound evidence must cite the canary dial-attempt guard, got: %s", zo.Evidence)
	}
	if !strings.Contains(zo.Evidence, "surface union") {
		t.Fatalf("zero-outbound evidence must reference the canary surface union, got: %s", zo.Evidence)
	}
}

func TestRender_NonEmpty(t *testing.T) {
	r := Run(context.Background(), "./...")
	txt := RenderText(r)
	if !strings.Contains(txt, "privacy-audit") || !strings.Contains(txt, "CGo-free") {
		t.Fatalf("rendered report missing expected sections:\n%s", txt)
	}
}

func failingNames(r Report) []string {
	var out []string
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			out = append(out, c.Name)
		}
	}
	return out
}
