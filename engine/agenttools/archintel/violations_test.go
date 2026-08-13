package archintel

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
)

func TestViolationsReportsBackEdge(t *testing.T) {
	res, err := Violations(context.Background(), ViolationsParams{Deps: layeredDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	if !strings.Contains(res.Summary, "1 finding(s)") || !strings.Contains(res.Summary, "1 unexpected dependencies") {
		t.Fatalf("summary must count the single back-edge: %q", res.Summary)
	}
	reasons := allReasons(t, res)
	want := regexp.MustCompile(`unexpected dependency: storage \(community \d+\) → domain \(community \d+\) — 1 edge\(s\) against the dominant direction \(domain → storage, 2 edge\(s\)\)`)
	if !want.MatchString(reasons) {
		t.Fatalf("missing %s in:\n%s", want, reasons)
	}
}

func TestViolationsReportsCycle(t *testing.T) {
	res, err := Violations(context.Background(), ViolationsParams{Deps: cycleDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	reasons := allReasons(t, res)
	if !strings.Contains(reasons, "cycle: ") || !strings.Contains(reasons, "dependency direction loops") {
		t.Fatalf("expected a cycle finding in:\n%s", reasons)
	}
	// The loop closes: label sequence web → domain → storage in some rotation,
	// with the first label repeated at the end.
	if !strings.Contains(res.Summary, "1 cycle(s)") {
		t.Fatalf("summary must count the cycle: %q", res.Summary)
	}
}

// godDeps builds hub + a/b/c where hub concentrates the inter-community edge
// mass and touches every other community.
func godDeps(t *testing.T) resolve.Deps {
	t.Helper()
	f := newFixture(t)
	f.group("hub")
	f.group("alpha")
	f.group("beta")
	f.group("gamma")
	f.edge("hub.A", "alpha.A", 0.5)
	f.edge("hub.B", "alpha.B", 0.5)
	f.edge("hub.C", "alpha.C", 0.5)
	f.edge("hub.A", "beta.A", 0.5)
	f.edge("hub.B", "beta.B", 0.5)
	f.edge("hub.C", "beta.C", 0.5)
	f.edge("hub.A", "gamma.A", 0.5)
	f.edge("hub.B", "gamma.B", 0.5)
	f.edge("hub.C", "gamma.C", 0.5)
	// Thin independent dependencies so hub's share is <100% while every other
	// community stays under the 50% god threshold (no cycle, no reverse edge).
	f.edge("alpha.A", "beta.A", 0.5)
	f.edge("alpha.B", "gamma.B", 0.5)
	return f.deps()
}

func TestViolationsReportsGodModule(t *testing.T) {
	res, err := Violations(context.Background(), ViolationsParams{Deps: godDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	reasons := allReasons(t, res)
	if !strings.Contains(reasons, "god module: hub (community") {
		t.Fatalf("expected god-module finding for hub in:\n%s", reasons)
	}
	if !strings.Contains(res.Summary, "1 god module(s)") {
		t.Fatalf("summary must count the god module: %q", res.Summary)
	}
}

// cleanDeps is a strictly layered two-community graph with no reverse traffic.
func cleanDeps(t *testing.T) resolve.Deps {
	t.Helper()
	f := newFixture(t)
	f.group("app")
	f.group("lib")
	f.edge("app.A", "lib.A", 0.5)
	f.edge("app.B", "lib.B", 0.5)
	return f.deps()
}

func TestViolationsCleanIsCited(t *testing.T) {
	res, err := Violations(context.Background(), ViolationsParams{Deps: cleanDeps(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "clean") {
		t.Fatalf("expected clean summary: %q", res.Summary)
	}
	reasons := allReasons(t, res)
	if !strings.Contains(reasons, "clean: no cycles") {
		t.Fatalf("clean must be a first-class item:\n%s", reasons)
	}
}

func TestViolationsUnavailableAndDeterministic(t *testing.T) {
	res, err := Violations(context.Background(), ViolationsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("nil deps must degrade to unavailable, got %s", res.Outcome)
	}

	deps := cycleDeps(t)
	a, err := Violations(context.Background(), ViolationsParams{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Violations(context.Background(), ViolationsParams{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	ab, err := contract.Serialize(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := contract.Serialize(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ab, bb) {
		t.Fatalf("violations output not byte-deterministic:\n%s\n%s", ab, bb)
	}
}
