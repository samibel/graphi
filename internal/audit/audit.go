// Package audit assembles graphi's local-first privacy report from scoped,
// checkable evidence rather than hardcoded strings. It backs
// `graphi privacy-audit` (SW-044).
//
// It checks:
//   - CGo-free build and no telemetry: canonical release binaries report the
//     source-bound evidence embedded after the build gate passed; developer
//     builds fall back to an explicitly labeled checkout scan.
//   - Zero-outbound network: references the real egress contract enforced by
//     internal/canary's dial-attempt guard (loopback-only policy) and asserts the
//     canary surface union exists and covers the surfaces; the full hermetic
//     runtime check runs in CI (`graphi canary`).
//   - No accounts / no required external services: explicit posture
//     declarations, labeled "declared" honestly.
//
// It makes zero network calls and exits non-zero on any failed check.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/samibel/graphi/internal/buildattest"
	"github.com/samibel/graphi/internal/canary"
	"github.com/samibel/graphi/internal/cgoconformance"
	"github.com/samibel/graphi/internal/releaseinfo"
)

// Status of a single audit check.
type Status string

const (
	StatusPass Status = "PASS"
	StatusFail Status = "FAIL"
	// StatusUnverified means the invariant could NOT be observed on this runner
	// (e.g. no network-namespace isolation to prove zero egress). It is NEVER a
	// PASS: it yields a non-zero exit and a distinct posture so a false green is
	// impossible (SW-049 AC-6 false-green prevention).
	StatusUnverified Status = "UNVERIFIED"
)

// Check is one audited invariant with its evidence.
type Check struct {
	Name      string   // short invariant name
	Status    Status   // PASS / FAIL / UNVERIFIED
	Scope     string   // what the evidence describes (binary build or source checkout)
	Evidence  string   // why (names the real guard/scan, not a hardcoded "OK")
	Offenders []string // concrete failures (e.g. CGo packages), empty on PASS
}

// Report is the full audit result.
type Report struct {
	Checks []Check
}

// AllPass reports whether every check passed. A FAIL or an UNVERIFIED check both
// make this false — UNVERIFIED is NEVER treated as a pass (AC-6).
func (r Report) AllPass() bool {
	for _, c := range r.Checks {
		if c.Status != StatusPass {
			return false
		}
	}
	return true
}

// hasFail reports whether any check outright FAILED (a verified violation), as
// distinct from merely UNVERIFIED.
func (r Report) hasFail() bool {
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return true
		}
	}
	return false
}

// ExitCode returns 0 only when every check PASSES. Any FAIL or UNVERIFIED yields
// a non-zero exit — exit 0 means a true, verified green and nothing less (AC-6).
func (r Report) ExitCode() int {
	if r.AllPass() {
		return 0
	}
	return 1
}

// Posture is the overall verdict line: CONFIRMED (all pass) / VIOLATED (any
// outright FAIL) / UNVERIFIED (no FAIL but at least one check unobservable).
func (r Report) Posture() string {
	if r.AllPass() {
		return "CONFIRMED"
	}
	if r.hasFail() {
		return "VIOLATED"
	}
	return "UNVERIFIED"
}

// Render writes a human-readable report to w with a distinct marker per status
// and an overall posture line that matches the exit code (CONFIRMED / VIOLATED /
// UNVERIFIED).
func (r Report) Render(w io.Writer) {
	fmt.Fprintln(w, "graphi privacy-audit")
	fmt.Fprintln(w, "===================")
	for _, c := range r.Checks {
		mark := "✓"
		switch c.Status {
		case StatusFail:
			mark = "✗"
		case StatusUnverified:
			mark = "?"
		}
		scope := ""
		if c.Scope != "" {
			scope = " (" + c.Scope + ")"
		}
		fmt.Fprintf(w, "%s %s [%s]%s — %s\n", mark, c.Name, c.Status, scope, c.Evidence)
		for _, off := range c.Offenders {
			fmt.Fprintf(w, "    · %s\n", off)
		}
	}
	switch r.Posture() {
	case "CONFIRMED":
		fmt.Fprintln(w, "\nlocal-first posture: CONFIRMED (all checks pass)")
	case "VIOLATED":
		fmt.Fprintln(w, "\nlocal-first posture: VIOLATED (see failed checks above)")
	default:
		fmt.Fprintln(w, "\nlocal-first posture: UNVERIFIED (one or more checks could not be verified; not a pass — see each check's remediation)")
	}
}

// Run executes the audit. target is the build target scanned for CGo imports
// (default "./..."). It is fully offline. The zero-outbound check runs a
// representative graphi operation under the platform's default network isolator.
func Run(ctx context.Context, target string) Report {
	return run(ctx, target, canary.DefaultIsolator(), nil, false, nil)
}

// RunSource forces the developer source-scan view even when the binary carries
// build-time evidence. Its PASS describes the selected checkout, never the
// running binary.
func RunSource(ctx context.Context, target string) Report {
	return run(ctx, target, canary.DefaultIsolator(), nil, true, nil)
}

// RunWithIsolator is Run with an injectable isolator + driver, so the live
// isolated exercise's PASS / FAIL / UNVERIFIED branches are unit-testable
// without root/netns. A nil driver uses the default in-process surface driver.
func RunWithIsolator(ctx context.Context, target string, iso canary.Isolator, drv canary.SurfaceDriver) Report {
	return run(ctx, target, iso, drv, true, nil)
}

func run(ctx context.Context, target string, iso canary.Isolator, drv canary.SurfaceDriver, forceSource bool, gate func() (canary.GateResult, error)) Report {
	if target == "" {
		target = "./..."
	}
	var checks []Check
	checks = append(checks, staticPrivacyChecks(ctx, target, forceSource, gate)...)
	checks = append(checks, checkZeroOutbound(ctx, iso, drv))
	checks = append(checks, checkNoAccounts())
	checks = append(checks, checkNoExternalServices())
	return Report{Checks: checks}
}

// RunWithGate is RunWithIsolator with an injectable telemetry-gate function so the
// no-telemetry check's PASS/FAIL branches are unit-testable without shelling out
// to `go list`. A nil gate uses the real canary.RunGate scan.
func RunWithGate(ctx context.Context, target string, iso canary.Isolator, drv canary.SurfaceDriver, gate func() (canary.GateResult, error)) Report {
	return run(ctx, target, iso, drv, true, gate)
}

func staticPrivacyChecks(ctx context.Context, target string, forceSource bool, gate func() (canary.GateResult, error)) []Check {
	if !forceSource {
		att, present, err := buildattest.Embedded()
		switch {
		case err != nil:
			return unavailableStaticChecks("embedded build attestation is invalid; no static privacy conclusion can be made for this binary")
		case present:
			return attestedStaticChecks(att)
		}
	}

	root, err := canary.ResolveModuleDir("")
	if err != nil {
		return unavailableStaticChecks(
			"no embedded build attestation is present and the graphi source module is not available; " +
				"use an official release and verify its SHA256SUMS/build provenance, or run `graphi privacy-audit --source` from a graphi checkout")
	}
	cgo := checkCgoFree(ctx, target)
	cgo.Scope = "developer source scan of " + root + "; not the running binary"
	telemetry := checkNoTelemetryWithGate(gate)
	telemetry.Scope = "developer source scan of " + root + "; not the running binary"
	return []Check{cgo, telemetry}
}

// normalizeBuildTags puts a tag list into the one comparable shape: trimmed,
// empties dropped, deduplicated, sorted. The `-tags` build setting preserves
// command-line order while an attestation's BuildTags are sorted and unique, so
// the two are only comparable as sets.
func normalizeBuildTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	slices.Sort(out)
	return out
}

func unavailableStaticChecks(reason string) []Check {
	scope := "static build property unavailable"
	return []Check{
		{Name: "CGo-free build", Status: StatusUnverified, Scope: scope, Evidence: reason},
		{Name: "No telemetry", Status: StatusUnverified, Scope: scope, Evidence: reason},
	}
}

type binaryBinding struct {
	Revision   string
	Modified   bool
	CGOEnabled string
	BuildTags  string
	SHA256     string
	Arch       string
}

func runningBinaryBinding() (binaryBinding, error) {
	info := releaseinfo.New()
	if info.Commit() == "" {
		return binaryBinding{}, fmt.Errorf("running binary has no VCS revision")
	}
	if info.CGOEnabled() == "" {
		return binaryBinding{}, fmt.Errorf("running binary has no CGO_ENABLED build setting")
	}
	path, err := os.Executable()
	if err != nil {
		return binaryBinding{}, fmt.Errorf("resolve running binary: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return binaryBinding{}, fmt.Errorf("open running binary: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return binaryBinding{}, fmt.Errorf("hash running binary: %w", err)
	}
	return binaryBinding{
		Revision:   info.Commit(),
		Modified:   info.Modified(),
		CGOEnabled: info.CGOEnabled(),
		BuildTags:  info.BuildTags(),
		SHA256:     hex.EncodeToString(h.Sum(nil)),
		Arch:       info.Arch(),
	}, nil
}

func attestedStaticChecks(att buildattest.Privacy) []Check {
	binding, err := runningBinaryBinding()
	if err != nil {
		return unavailableStaticChecks("embedded build evidence could not be bound to the running binary: " + err.Error())
	}
	return attestedStaticChecksFor(binding, att)
}

// attestedStaticChecksFor is the pure half of attestedStaticChecks: every claim
// the attestation makes that the Go toolchain independently recorded into the
// same binary must agree with that recording, or no privacy conclusion is
// accepted. Split from the I/O half so each mismatch is directly testable
// without depending on how the test binary itself was built.
func attestedStaticChecksFor(binding binaryBinding, att buildattest.Privacy) []Check {
	if binding.Revision != att.SourceRevision {
		return unavailableStaticChecks("embedded build evidence names a different source revision than the running binary; no privacy conclusion was accepted")
	}
	if binding.Arch != att.GOOS+"/"+att.GOARCH {
		return unavailableStaticChecks("embedded build evidence names a different target platform than the running binary; no privacy conclusion was accepted")
	}
	if binding.Modified != att.SourceModified {
		return unavailableStaticChecks("embedded build evidence names a different source state than the running binary's recorded vcs.modified; no privacy conclusion was accepted")
	}
	if binding.CGOEnabled != att.CGOEnabled {
		return unavailableStaticChecks("embedded build evidence names a different CGO_ENABLED setting than the one the running binary was linked with; no privacy conclusion was accepted")
	}
	if !slices.Equal(normalizeBuildTags(strings.Split(binding.BuildTags, ",")), normalizeBuildTags(att.BuildTags)) {
		return unavailableStaticChecks("embedded build evidence names a different build-tag set than the one the running binary was linked with; no privacy conclusion was accepted")
	}
	state := "clean"
	if att.SourceModified {
		state = "modified"
	}
	common := fmt.Sprintf(
		"source_commit=%s source_state=%s target=%s/%s build_tags=%s evidence_sha256=%s binary_sha256=%s; "+
			"embedded build evidence is not an independent signature — compare binary_sha256 with the published SHA256SUMS/build provenance or reproduce the source scan",
		att.SourceRevision, state, att.GOOS, att.GOARCH, strings.Join(att.BuildTags, ","), att.EvidenceDigest, binding.SHA256)
	scope := "build-time attestation for this binary"
	return []Check{
		{
			Name:     "Build evidence binding",
			Status:   StatusPass,
			Scope:    scope,
			Evidence: common,
		},
		{
			Name:     "CGo-free build",
			Status:   StatusPass,
			Scope:    scope,
			Evidence: "canonical build contract enforced CGO_ENABLED=0; subject is bound by the Build evidence binding row",
		},
		{
			Name:   "No telemetry",
			Status: StatusPass,
			Scope:  scope,
			Evidence: fmt.Sprintf("%s reported PASS for %s; subject is bound by the Build evidence binding row",
				att.GateID, att.Scope),
		},
	}
}

// checkCgoFree performs a REAL scan of the build graph for CGo imports. It is
// the same engine the CI conformance gate uses — not a hardcoded string.
func checkCgoFree(ctx context.Context, target string) Check {
	c := Check{Name: "CGo-free build", Evidence: "internal/cgoconformance.CgoUsingPackages scan of " + target}
	pkgs, err := cgoconformance.CgoUsingPackages(ctx, target, "0")
	if err != nil {
		c.Status = StatusUnverified
		c.Evidence = "source CGo scan could not run; the Go toolchain and the graphi source module are required, and no conclusion was reached"
		return c
	}
	if len(pkgs) > 0 {
		c.Status = StatusFail
		c.Offenders = pkgs
		c.Evidence = "CGo-importing packages found (must be empty for a static binary)"
		return c
	}
	c.Status = StatusPass
	return c
}

// checkZeroOutbound runs a LIVE representative graphi operation under the
// platform's network isolator and emits a tri-state verdict (SW-049 AC-5/AC-6):
//
//   - isolation available + zero non-loopback dials → PASS ("zero outbound
//     network, verified under loopback-only isolation");
//   - isolation available + a non-loopback dial attempted → FAIL ("egress
//     detected") naming the offending tool + destination;
//   - isolation NOT available (e.g. local macOS, unprivileged runner) →
//     UNVERIFIED — never a false PASS — directing the operator to the CI
//     deny-egress harness.
//
// The exercise reuses the SAME canary engine (dial-attempt guard + in-process
// surface driver) that `graphi canary` uses, so a real egress introduced in any
// surface is observable here too.
func checkZeroOutbound(ctx context.Context, iso canary.Isolator, drv canary.SurfaceDriver) Check {
	c := Check{Name: "Zero outbound network"}
	if iso == nil {
		iso = canary.DefaultIsolator()
	}
	if drv == nil {
		drv = canary.DefaultDriver(io.Discard)
	}

	union := canary.NewSurfaceUnion()
	if len(union.CoveredTools()) == 0 {
		c.Status = StatusFail
		c.Evidence = "canary surface union is empty — egress guard not wired"
		return c
	}

	// No isolation on this runner: we cannot OBSERVE the network layer, so we
	// must NOT claim a pass. This is the AC-6 false-green safety valve.
	if !iso.IsAvailable() {
		c.Status = StatusUnverified
		c.Evidence = "network layer not observable on this runner (no loopback-only isolation); " +
			"run `graphi privacy-audit` under the CI deny-egress harness (Linux netns) to verify"
		return c
	}

	// Live exercise under loopback-only isolation. A clean run (zero non-loopback
	// dial attempts) is a verified PASS; any non-loopback dial → FAIL.
	art, err := canary.Run(ctx, canary.RunConfig{Isolator: iso, Driver: drv, Union: union})
	if err != nil {
		c.Status = StatusFail
		c.Evidence = "egress detected: " + art.FailReason
		for _, v := range art.Violations {
			c.Offenders = append(c.Offenders, fmt.Sprintf("%s → %s", v.Tool, v.Address))
		}
		if len(c.Offenders) == 0 && art.FailReason != "" {
			c.Offenders = append(c.Offenders, art.FailReason)
		}
		return c
	}

	c.Status = StatusPass
	c.Evidence = fmt.Sprintf(
		"zero outbound network, verified live under loopback-only isolation "+
			"(%s); %d surface tool(s) exercised, %d non-loopback dial attempt(s)",
		art.Isolation, len(art.CoveredTools), len(art.Violations))
	return c
}

// The two checks below are honest posture statements. They are labeled
// "declared" rather than "verified" because they are not machine-enforced at
// runtime in this command — they document the repo's invariant. (No-telemetry was
// formerly in this group but is now a REAL canary-backed scan; see
// checkNoTelemetry above.) This honesty is the point of AC-4: do not print a fake
// "OK".

// checkNoTelemetry is now backed by a REAL scan (SW-055 AC#5): the
// internal/canary static gate runs a telemetry/analytics import denylist scan over
// the default build graph PLUS a type-checked outbound-dial AST scan over graphi's
// own source. It is no longer a hard-coded declared-PASS string — a telemetry SDK
// import or an unsanctioned dial introduced anywhere in the default graph (the new
// default-tier parsers included) FAILS this check.
func checkNoTelemetry() Check {
	return checkNoTelemetryWithGate(nil)
}

// checkNoTelemetryWithGate runs the telemetry gate (or the real canary.RunGate
// when gate is nil) and maps its verdict to a Check. A gate execution error yields
// UNVERIFIED (never a false PASS); a "fail" verdict yields FAIL naming the
// offending imports/dials; a clean "pass" yields a VERIFIED pass (backed by the
// real scan, not a declared string).
func checkNoTelemetryWithGate(gate func() (canary.GateResult, error)) Check {
	c := Check{Name: "No telemetry"}
	if gate == nil {
		gate = func() (canary.GateResult, error) { return canary.RunGate(canary.GateConfig{}) }
	}
	res, err := gate()
	if err != nil {
		c.Status = StatusUnverified
		c.Evidence = "source telemetry scan could not run; the Go toolchain and the graphi source module are required, and no conclusion was reached"
		return c
	}
	if res.Verdict != "pass" {
		c.Status = StatusFail
		c.Evidence = "telemetry/analytics gate FAILED: telemetry import or unsanctioned outbound dial in the default graph"
		for _, f := range res.Findings {
			if f.Import != "" {
				c.Offenders = append(c.Offenders, f.Kind+": "+f.Import)
			} else {
				c.Offenders = append(c.Offenders, f.Kind+": "+f.Reason)
			}
		}
		return c
	}
	c.Status = StatusPass
	c.Evidence = "verified by developer source scan: internal/canary static gate (telemetry-import denylist + type-checked outbound-dial scan) found zero telemetry SDKs and zero unsanctioned dials in the default graph"
	if res.EvidenceDigest != "" {
		c.Evidence += "; evidence_sha256=" + res.EvidenceDigest
	}
	return c
}

func checkNoAccounts() Check {
	return Check{Name: "No accounts required",
		Status:   StatusPass,
		Evidence: "declared: no login, no cloud account, no API key required to run any surface"}
}

func checkNoExternalServices() Check {
	return Check{Name: "No required external services",
		Status:   StatusPass,
		Evidence: "declared: all surfaces run against the local engine; no required remote backend"}
}

// RenderText is a small convenience for callers that want the report as a string.
func RenderText(r Report) string {
	var b strings.Builder
	r.Render(&b)
	return b.String()
}
