package doctor

import (
	"context"
	"testing"
)

type staticCheck struct {
	id       string
	category string
	result   CheckResult
}

func (s staticCheck) ID() string       { return s.id }
func (s staticCheck) Category() string { return s.category }
func (s staticCheck) Run(ctx context.Context, env Env) CheckResult {
	return s.result
}

func TestRegistryPreservesOrder(t *testing.T) {
	r := NewRegistry()
	r.Register(staticCheck{id: "a", category: "x", result: StringResult("a", "x", "ok", StatusPass)})
	r.Register(staticCheck{id: "b", category: "x", result: StringResult("b", "x", "ok", StatusPass)})
	checks := r.Checks()
	if len(checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(checks))
	}
	if checks[0].ID() != "a" || checks[1].ID() != "b" {
		t.Fatalf("order not preserved: %v", []string{checks[0].ID(), checks[1].ID()})
	}
}

func TestRunnerExecutesAllChecks(t *testing.T) {
	r := NewRegistry()
	r.Register(staticCheck{id: "a", category: "x", result: StringResult("a", "x", "ok", StatusPass)})
	r.Register(staticCheck{id: "b", category: "x", result: StringResult("b", "x", "ok", StatusPass)})
	report := NewRunner(r).Run(context.Background(), nil)
	if len(report.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(report.Results))
	}
}

func TestWorstStatus(t *testing.T) {
	cases := []struct {
		name   string
		status []Status
		want   Status
	}{
		{"empty", nil, StatusPass},
		{"all pass", []Status{StatusPass, StatusPass}, StatusPass},
		{"one warn", []Status{StatusPass, StatusWarn}, StatusWarn},
		{"one fail", []Status{StatusPass, StatusFail}, StatusFail},
		{"fail over warn", []Status{StatusWarn, StatusFail}, StatusFail},
		{"unverified between warn and info", []Status{StatusInfo, StatusUnverified}, StatusUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var results []CheckResult
			for i, s := range tc.status {
				results = append(results, CheckResult{ID: string(rune('a' + i)), Category: "x", Status: s})
			}
			got := Report{Results: results}.WorstStatus()
			if got != tc.want {
				t.Fatalf("worst status: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		in   []Status
		want int
	}{
		{"empty", nil, 0},
		{"all pass", []Status{StatusPass, StatusPass}, 0},
		{"warn only", []Status{StatusWarn, StatusInfo, StatusUnverified}, 0},
		{"one fail", []Status{StatusPass, StatusFail}, 1},
		{"all fail", []Status{StatusFail, StatusFail}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			results := make([]CheckResult, len(tc.in))
			for i, s := range tc.in {
				results[i] = CheckResult{ID: "id", Category: "cat", Status: s}
			}
			if got := ExitCode(results); got != tc.want {
				t.Fatalf("ExitCode: got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestStatusValid(t *testing.T) {
	for _, s := range []Status{StatusPass, StatusWarn, StatusFail, StatusInfo, StatusUnverified} {
		if !s.Valid() {
			t.Fatalf("status %q should be valid", s)
		}
	}
	if Status("bogus").Valid() {
		t.Fatal("bogus status should be invalid")
	}
}

func TestReadOnlyEnvHasNoWriter(t *testing.T) {
	// The Env interface in this package deliberately exposes no write method.
	// This compile-time assertion proves the invariant: if a future change adds
	// a writer to the interface, this test will fail to compile.
	var _ Env = (*readOnlyEnv)(nil)
}

// readOnlyEnv is a minimal Env used only for the compile-time assertion above.
type readOnlyEnv struct{}

func (readOnlyEnv) RepoRoot() string           { return "" }
func (readOnlyEnv) DBPath() string             { return "" }
func (readOnlyEnv) MCPConfig() MCPConfigReader { return nil }
func (readOnlyEnv) Release() ReleaseInfo       { return nil }
func (readOnlyEnv) State() StateReader         { return nil }
