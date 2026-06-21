package canary

import (
	"context"
	"strings"
	"testing"
)

// fakeIsolator lets tests control IsAvailable() and observe the isolated fn.
type fakeIsolator struct {
	available bool
	ran       bool
}

func (f *fakeIsolator) IsAvailable() bool { return f.available }
func (f *fakeIsolator) Run(fn func() error) error {
	f.ran = true
	return fn()
}

// fakeDriver records the union it was asked to drive and optionally injects dial
// attempts into the recorder (to simulate a non-loopback violation).
type fakeDriver struct {
	called      bool
	drivenUnion SurfaceUnion
	inject      []DialAttempt // dial attempts to record during Drive
	driveErr    error
}

func (f *fakeDriver) Drive(_ context.Context, u SurfaceUnion, rec *DialRecorder) error {
	f.called = true
	f.drivenUnion = u
	for _, a := range f.inject {
		rec.Record(a)
	}
	return f.driveErr
}

// AC: "Given the canary executes, when no non-loopback packets are captured,
// then it completes successfully (verdict pass)." Covered by: zero dials → pass.
func TestRun_PassOnZeroNonLoopbackDials(t *testing.T) {
	iso := &fakeIsolator{available: true}
	drv := &fakeDriver{} // no dial attempts
	art, err := Run(context.Background(), RunConfig{
		Isolator: iso, Driver: drv, Union: NewSurfaceUnion(),
	})
	if err != nil {
		t.Fatalf("expected nil error on pass, got %v", err)
	}
	if art.Verdict != "pass" {
		t.Fatalf("verdict = %q, want pass", art.Verdict)
	}
	if !iso.ran {
		t.Fatal("isolator.Run was not invoked; isolation was bypassed")
	}
	if len(art.Violations) != 0 {
		t.Fatalf("expected 0 violations, got %d", len(art.Violations))
	}
	if len(art.CoveredTools) == 0 {
		t.Fatal("artifact must list covered tools")
	}
}

// AC: "Given any tool attempts a non-loopback connection, when packets are
// observed, then the canary fails the build and reports the offending tool,
// destination."
func TestRun_FailsOnNonLoopbackDial(t *testing.T) {
	iso := &fakeIsolator{available: true}
	drv := &fakeDriver{inject: []DialAttempt{
		{Tool: "query:callers", Network: "tcp", Address: "telemetry.example.com:443"},
	}}
	art, err := Run(context.Background(), RunConfig{
		Isolator: iso, Driver: drv, Union: NewSurfaceUnion(),
	})
	if err == nil {
		t.Fatal("expected non-nil error on violation; got nil")
	}
	if art.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail", art.Verdict)
	}
	if !strings.Contains(art.FailReason, "telemetry.example.com") {
		t.Fatalf("fail reason must name destination, got %q", art.FailReason)
	}
	if !strings.Contains(art.FailReason, "query:callers") {
		t.Fatalf("fail reason must name offending tool, got %q", art.FailReason)
	}
	if len(art.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d", len(art.Violations))
	}
}

// AC: "Given the loopback-only allowance, when an in-process local server or
// IPC uses 127.0.0.1/::1, then those connections are explicitly permitted and
// do not trip the assertion." (refinement S3)
func TestRun_LoopbackDialsArePermitted(t *testing.T) {
	iso := &fakeIsolator{available: true}
	drv := &fakeDriver{inject: []DialAttempt{
		{Tool: "daemon", Network: "unix", Address: "/tmp/graphi.sock"}, // Unix IPC
		{Tool: "http-sse", Network: "tcp", Address: "127.0.0.1:8080"},  // IPv4 loopback
		{Tool: "http-sse", Network: "tcp", Address: "[::1]:8080"},      // IPv6 loopback
		{Tool: "http-sse", Network: "tcp", Address: "localhost:8080"},  // localhost name
	}}
	art, err := Run(context.Background(), RunConfig{
		Isolator: iso, Driver: drv, Union: NewSurfaceUnion(),
	})
	if err != nil {
		t.Fatalf("loopback dials must not fail; got %v", err)
	}
	if art.Verdict != "pass" {
		t.Fatalf("verdict = %q, want pass (loopback allowed)", art.Verdict)
	}
	if len(art.DialAttempts) != 4 {
		t.Fatalf("recorded %d attempts, want 4", len(art.DialAttempts))
	}
}

// AC: "Given the canary runs in CI, when no network namespace/sandbox
// isolation is available on the runner, then the job hard-fails (rather than
// silently passing)." (refinement S2)
func TestRun_HardFailsWhenIsolationUnavailable(t *testing.T) {
	iso := &fakeIsolator{available: false} // cannot isolate
	drv := &fakeDriver{}                   // would "pass" silently — must NOT be trusted
	art, err := Run(context.Background(), RunConfig{
		Isolator: iso, Driver: drv, Union: NewSurfaceUnion(),
	})
	if err == nil {
		t.Fatal("must hard-fail when isolation unavailable; got nil error")
	}
	if art.Verdict != "no-isolation" {
		t.Fatalf("verdict = %q, want no-isolation", art.Verdict)
	}
	if drv.called {
		// the driver must NOT have been invoked when isolation is unavailable
		t.Fatal("driver must not run without isolation; canary must hard-fail first")
	}
}

// AC: "Given the canary runs in CI, when it produces results, then it emits a
// machine-readable artifact (covered-tool list + packet-capture summary)."
func TestArtifact_IsMachineReadableJSON(t *testing.T) {
	iso := &fakeIsolator{available: true}
	drv := &fakeDriver{inject: []DialAttempt{
		{Tool: "search", Network: "tcp", Address: "1.2.3.4:80"},
	}}
	art, _ := Run(context.Background(), RunConfig{
		Isolator: iso, Driver: drv, Union: NewSurfaceUnion(),
	})
	b, err := MarshalArtifact(art)
	if err != nil {
		t.Fatalf("MarshalArtifact: %v", err)
	}
	s := string(b)
	for _, key := range []string{`"verdict"`, `"covered_tools"`, `"dial_attempts"`, `"violations"`, `"started_at"`, `"duration_ms"`} {
		if !strings.Contains(s, key) {
			t.Fatalf("artifact JSON missing key %s", key)
		}
	}
}

// AC (gate): telemetry-import detection names the offending import path.
func TestGate_DetectsTelemetryImport(t *testing.T) {
	cfg := GateConfig{
		ModuleDir: ".",
		GraphCommand: func(string) ([]string, error) {
			return []string{
				"fmt",
				"go.opentelemetry.io/otel",
				"go.opentelemetry.io/otel/trace",
				"github.com/segmentio/analytics-go/v3",
				"net/http",
			}, nil
		},
	}
	// Skip the AST scan over the real tree for this unit by pointing ModuleDir at
	// a temp-ish empty dir is not trivial; instead rely on the fact that the
	// telemetry finding is produced from the graph deps regardless of AST scan.
	res, err := RunGate(cfg)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	if res.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail (telemetry imports present)", res.Verdict)
	}
	foundOtel, foundSegment := false, false
	for _, f := range res.Findings {
		if f.Kind == "telemetry-import" && strings.HasPrefix(f.Import, "go.opentelemetry.io/otel") {
			foundOtel = true
		}
		if f.Kind == "telemetry-import" && strings.HasPrefix(f.Import, "github.com/segmentio/analytics-go") {
			foundSegment = true
		}
	}
	if !foundOtel {
		t.Error("expected OpenTelemetry import to be flagged")
	}
	if !foundSegment {
		t.Error("expected Segment analytics import to be flagged")
	}
}

// AC (gate): a clean graph passes.
func TestGate_PassesOnCleanGraph(t *testing.T) {
	cfg := GateConfig{
		ModuleDir: ".",
		GraphCommand: func(string) ([]string, error) {
			return []string{"fmt", "net/http", "os", "github.com/samibel/graphi/core/model"}, nil
		},
	}
	res, err := RunGate(cfg)
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	// Only telemetry-import findings come from the injected clean deps; the AST
	// scan over the real tree may surface allowlisted-only dials (which are
	// excluded). Assert no telemetry-import findings.
	for _, f := range res.Findings {
		if f.Kind == "telemetry-import" {
			t.Fatalf("clean graph must not produce telemetry findings; got %+v", f)
		}
	}
}

// AC: surface union is derived programmatically and includes every canonical
// query operation + search + CLI commands.
func TestNewSurfaceUnion_IncludesAllQueryOpsAndSearch(t *testing.T) {
	u := NewSurfaceUnion()
	covered := u.CoveredTools()
	for _, want := range []string{"query:callers", "query:callees", "query:references", "query:definition", "query:neighborhood", "search", "parse", "mcp", "daemon"} {
		if !contains(covered, want) {
			t.Errorf("covered union missing %q; got %v", want, covered)
		}
	}
}

// DialAttempt.IsLoopback across all loopback forms + a non-loopback.
func TestDialAttempt_IsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"1.2.3.4:80", false},
		{"telemetry.example.com:443", false},
	}
	for _, c := range cases {
		d := DialAttempt{Network: "tcp", Address: c.addr}
		if got := d.IsLoopback(); got != c.want {
			t.Errorf("IsLoopback(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
	// Unix IPC is local, never egress.
	if !(DialAttempt{Network: "unix", Address: "/tmp/x.sock"}.IsLoopback()) {
		t.Error("unix socket must be treated as local/loopback")
	}
}
