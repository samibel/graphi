package cgoconformance

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests cover the conformance gate's logic (fast, deterministic) plus a
// small number of real-but-cheap subprocess checks against the live toolchain.
// The full gate (building ./cmd/graphi + running the whole suite under
// CGO_ENABLED=0) is executed by the CI workflow via cmd/cgoconformance; that is
// the integration embodiment of the ACs. The Run() integration test below is
// scoped to a single small package to keep `go test` fast and non-reentrant
// while still proving end-to-end wiring.

func TestCheckName_IsNamedDistinctCheck(t *testing.T) {
	if CheckName != "cgo-free-conformance" {
		t.Fatalf("CheckName = %q, want distinct named check", CheckName)
	}
	if CheckName == "" {
		t.Fatal("CheckName must be non-empty")
	}
}

func TestIsBroadFlavor(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"graphi-broad", true},
		{"github.com/samibel/graphi-broad/cgosqlite", true},
		{"github.com/samibel/graphi/core/parse", false},
		{"", false},
		{"cmd/graphi", false},
	}
	for _, c := range cases {
		if got := IsBroadFlavor(c.in); got != c.want {
			t.Errorf("IsBroadFlavor(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFormatCgoImportFailure_NamesOffendingPackage(t *testing.T) {
	got := FormatCgoImportFailure([]string{"github.com/samibel/graphi/evil/cgosqlite"})
	for _, want := range []string{"github.com/samibel/graphi/evil/cgosqlite", CheckName, DefaultBuildTarget} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatCgoImportFailure missing %q in: %q", want, got)
		}
	}
	if empty := FormatCgoImportFailure(nil); empty != "" {
		t.Errorf("FormatCgoImportFailure(nil) = %q, want empty", empty)
	}
}

func TestSanitizeGoFlags_StripsBroadTag(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"-tags=graphi-broad", ""},
		{"-tags graphi-broad", ""},
		{"-tags=foo,graphi-broad,bar", "-tags=foo,bar"},
		{"graphi-broad", ""},
		{"-v -tags=graphi-broad -race", "-v -race"},
		{"", ""},
		{"-v", "-v"},
	}
	for _, c := range cases {
		if got := SanitizeGoFlags(c.in); got != c.want {
			t.Errorf("SanitizeGoFlags(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEffectiveCgoEnabled_ReflectsEnv(t *testing.T) {
	got, err := EffectiveCgoEnabled(context.Background(), "0")
	if err != nil {
		t.Fatalf("EffectiveCgoEnabled: %v", err)
	}
	if got != "0" {
		t.Errorf("effective CGO_ENABLED = %q, want 0", got)
	}
}

// TestCgoUsingPackages_DefaultGraph_NoOffenders proves the default build graph is
// cgo-free today: the regression detector returns no offenders (excluding the
// broad flavor). This is the live baseline that a future cgo regression would
// break.
func TestCgoUsingPackages_DefaultGraph_NoOffenders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live go-list scan in -short mode")
	}
	offenders, err := CgoUsingPackages(context.Background(), DefaultBuildTarget, "0")
	if err != nil {
		t.Fatalf("CgoUsingPackages: %v", err)
	}
	if len(offenders) != 0 {
		t.Fatalf("default graph has cgo-using packages (regression!): %v", offenders)
	}
}

// TestForestReachablePackages_DefaultGraph_None proves go-sitter-forest (the CGO
// grammar bundle) is NOT reachable from the default build graph — the static,
// import-graph half of SW-055 AC#2/AC#4 (the registration-level half lives in
// core/parse.AssertPureGoDefaults). A future graphi-broad import leaking into the
// default graph would break this.
func TestForestReachablePackages_DefaultGraph_None(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live go-list scan in -short mode")
	}
	offenders, err := ForestReachablePackages(context.Background(), DefaultBuildTarget, "0")
	if err != nil {
		t.Fatalf("ForestReachablePackages: %v", err)
	}
	if len(offenders) != 0 {
		t.Fatalf("go-sitter-forest reachable from default graph (regression!): %v", offenders)
	}
}

func TestFormatForestReachableFailure_NamesOffender(t *testing.T) {
	got := FormatForestReachableFailure([]string{"github.com/alexaandru/go-sitter-forest/fortran"})
	for _, want := range []string{"go-sitter-forest", CheckName, ExcludedBroadFlavor} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatForestReachableFailure missing %q in: %q", want, got)
		}
	}
	if empty := FormatForestReachableFailure(nil); empty != "" {
		t.Errorf("FormatForestReachableFailure(nil) = %q, want empty", empty)
	}
}

// TestAssertStaticLinkage_FreshDefaultBinary builds the default binary under
// CGO_ENABLED=0 and asserts the linkage guarantee holds on this platform.
func TestAssertStaticLinkage_FreshDefaultBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build in -short mode")
	}
	bin := filepath.Join(t.TempDir(), "graphi")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	if out, err := BuildBinary(context.Background(), DefaultBuildTarget, "0", bin); err != nil {
		t.Fatalf("build default binary: %v\n%s", err, out)
	}
	ok, detail, err := AssertStaticLinkage(context.Background(), bin, "0")
	if err != nil {
		t.Fatalf("AssertStaticLinkage: %v", err)
	}
	if !ok {
		t.Fatalf("static linkage failed: %s", detail)
	}
}

// TestRun_PassesOnScopedTarget is an end-to-end run of the gate wired to a small
// package so it stays fast and non-reentrant. CI runs the unscoped gate
// (TestTarget=./...) through cmd/cgoconformance.
func TestRun_PassesOnScopedTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end gate in -short mode")
	}
	if os.Getenv("CGOCONF_SKIP_E2E") == "1" {
		t.Skip("CGOCONF_SKIP_E2E=1")
	}
	res := Run(context.Background(), GateConfig{
		Target:     DefaultBuildTarget,
		TestTarget: "./core/model", // small, fast, proves the wiring
		CGOEnabled: "0",
		Stdout:     testWriter{t},
	})
	if res.Status != StatusPass {
		t.Fatalf("gate %s: reason=%s buildOK=%v testOK=%v static=%v cgo=%v",
			res.Status, res.Reason, res.BuildOK, res.TestOK, res.StaticLinked, res.CgoPackages)
	}
	if res.Name != CheckName {
		t.Errorf("Result.Name = %q, want %q", res.Name, CheckName)
	}
	if res.ExcludedFlavor != ExcludedBroadFlavor {
		t.Errorf("ExcludedFlavor = %q, want %q", res.ExcludedFlavor, ExcludedBroadFlavor)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}
