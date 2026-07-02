package canary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/samibel/graphi/core/graphstore"
)

// Artifact is the machine-readable canary result (SW-008 AC: "emits a
// machine-readable artifact (covered-tool list + packet-capture summary) proving
// the whole surface was exercised under denial"). Stable JSON schema for CI
// consumers and audit.
type Artifact struct {
	Verdict      string        `json:"verdict"` // "pass" | "fail" | "no-isolation"
	CoveredTools []string      `json:"covered_tools"`
	DialAttempts []DialAttempt `json:"dial_attempts"`
	Violations   []DialAttempt `json:"violations"` // non-loopback subset
	Isolation    string        `json:"isolation"`  // isolator kind / "unavailable"
	StartedAt    time.Time     `json:"started_at"`
	DurationMS   int64         `json:"duration_ms"`
	FailReason   string        `json:"fail_reason,omitempty"`
}

// RunConfig parameterizes a canary run. Fields are injectable so the preflight,
// isolation, and verdict logic are all unit-testable without netns.
type RunConfig struct {
	Isolator Isolator
	Driver   SurfaceDriver
	Union    SurfaceUnion
}

// Run executes the hermetic egress canary:
//
//  1. Preflight — require that isolation is actually available; if not, HARD-FAIL
//     (no-isolation verdict) rather than silently passing (SW-008 AC + S2).
//  2. Drive — exercise every tool/command in the union inside isolation,
//     recording any dial attempts.
//  3. Verdict — pass iff zero non-loopback dial attempts were observed; on any
//     violation, fail naming the offending tool + destination.
//
// It returns the Artifact and a non-nil error only for HARD-FAIL conditions
// (no isolation available). A "fail" verdict (violations observed) is returned
// as an Artifact with Verdict="fail" AND a wrapping error so CI gates naturally
// fail; callers wanting the structured detail should read the Artifact.
func Run(ctx context.Context, cfg RunConfig) (Artifact, error) {
	start := time.Now()
	art := Artifact{
		StartedAt: start,
		Isolation: isolatorKind(cfg.Isolator),
	}

	if cfg.Isolator == nil {
		cfg.Isolator = defaultIsolator()
		art.Isolation = isolatorKind(cfg.Isolator)
	}
	if cfg.Union.CoveredTools() == nil || len(cfg.Union.CoveredTools()) == 0 {
		cfg.Union = NewSurfaceUnion()
	}

	// Step 1: preflight — prove isolation is in effect. This is the single most
	// important correctness property: a silently-passing canary is worse than
	// none (refinement S2). Missing isolation is a HARD-FAIL, not a soft skip.
	if !cfg.Isolator.IsAvailable() {
		art.Verdict = "no-isolation"
		art.CoveredTools = cfg.Union.CoveredTools()
		art.DurationMS = time.Since(start).Milliseconds()
		art.FailReason = "runner cannot provide loopback-only network isolation; refusing to run to avoid masking egress"
		return art, &IsolationError{Reason: art.FailReason}
	}

	rec := NewDialRecorder()

	// Step 2: drive the surface union under isolation.
	drive := func() error {
		if cfg.Driver == nil {
			return errors.New("canary: nil surface driver")
		}
		return cfg.Driver.Drive(ctx, cfg.Union, rec)
	}
	if err := cfg.Isolator.Run(drive); err != nil {
		art.Verdict = "fail"
		art.CoveredTools = cfg.Union.CoveredTools()
		art.DialAttempts = rec.All()
		art.Violations = rec.NonLoopback()
		art.DurationMS = time.Since(start).Milliseconds()
		art.FailReason = "isolated surface drive failed: " + err.Error()
		return art, fmt.Errorf("canary: %w", err)
	}

	// Step 3: verdict on dial attempts. The assertion is on ATTEMPT, not packets
	// seen (refinement D2/S1): any non-loopback dial attempt fails the canary.
	art.DialAttempts = rec.All()
	art.Violations = rec.NonLoopback()
	art.CoveredTools = cfg.Union.CoveredTools()
	art.DurationMS = time.Since(start).Milliseconds()

	if len(art.Violations) > 0 {
		art.Verdict = "fail"
		v := art.Violations[0]
		art.FailReason = fmt.Sprintf("non-loopback dial attempted by tool %q to %s", v.Tool, v.Address)
		return art, fmt.Errorf("canary: %s", art.FailReason)
	}

	art.Verdict = "pass"
	return art, nil
}

// DefaultDriver returns the canonical in-process surface driver over a fresh
// in-memory store, exercising the real graphi surfaces (query / search / CLI).
// It is the representative operation used by both the canary and the
// privacy-audit live exercise. out receives surface stdout; pass io.Discard
// unless debugging.
func DefaultDriver(out io.Writer) SurfaceDriver {
	return NewInProcessDriver(graphstore.NewMemStore(), out)
}

// MarshalArtifact serializes a canary artifact to stable JSON for the CI
// evidence artifact.
func MarshalArtifact(a Artifact) ([]byte, error) {
	return json.MarshalIndent(a, "", "  ")
}

// isolatorKind returns a short human-readable label for the isolator in use.
func isolatorKind(i Isolator) string {
	if i == nil {
		return "nil"
	}
	switch any(i).(type) {
	case noIsolation:
		return "unavailable(non-linux-or-unprivileged)"
	default:
		return fmt.Sprintf("%T", i)
	}
}
