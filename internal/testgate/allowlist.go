// Package testgate expresses the `CGO_ENABLED=0 go test ./...`-green CI assertion
// as an EXPLICIT expected-failure allowlist (SW-055 AC#3/AC#7). The default suite
// is green IFF every test passes EXCEPT exactly the two known internal/mcpconfig
// root-perms tests — and only when running as root, since those tests force a
// write failure via os.Chmod(dir, 0o500) which root bypasses. The allowlist is
// structured DATA (no wildcard, length asserted == 2) consumed via `go test -json`
// so a new regression cannot hide behind the carve-out: if a third test fails, or
// an allowlisted test starts passing/disappears, the gate fails loudly.
package testgate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ExpectedFailure names one carve-out test that is expected to FAIL under the
// privilege condition. It is fully qualified (package + test name) — never a
// package-level skip or a wildcard — so the carve-out is exact.
type ExpectedFailure struct {
	Package string // full import path, e.g. github.com/samibel/graphi/internal/mcpconfig
	Test    string // exact top-level test name
	Reason  string // why it is expected to fail (root bypasses the forced write failure)
}

// Key is the package\x00test identity used for set membership.
func (e ExpectedFailure) Key() string { return e.Package + "\x00" + e.Test }

func (e ExpectedFailure) String() string { return e.Package + "." + e.Test }

// expectedFailuresUnderRoot is the SINGLE SOURCE OF TRUTH for the carve-out: the
// EXACTLY TWO internal/mcpconfig root-perms tests. They force a write failure by
// chmod'ing the parent dir to 0o500; root bypasses that, so they fail ONLY as root
// and pass otherwise. ExpectedFailures() asserts len == 2 — adding a wildcard or a
// third entry is a deliberate, reviewable change, never an accident.
var expectedFailuresUnderRoot = []ExpectedFailure{
	{
		Package: "github.com/samibel/graphi/internal/mcpconfig",
		Test:    "TestFixture_Unwritable_FailsAndLeavesOriginalIntact",
		Reason:  "forces write failure via os.Chmod(dir,0o500); root bypasses the permission, so the test fails only under root",
	},
	{
		Package: "github.com/samibel/graphi/internal/mcpconfig",
		Test:    "TestBackupFailureAbortsBeforeTouchingConfig",
		Reason:  "forces write failure via os.Chmod(dir,0o500); root bypasses the permission, so the test fails only under root",
	},
}

// ExpectedFailures returns the privilege-conditional carve-out set. When euid != 0
// the carve-out is EMPTY (those two tests are expected to PASS as a normal user);
// when euid == 0 it is exactly the two root-perms tests. It panics if the static
// list is ever not exactly two entries — a guard against silent wildcarding.
func ExpectedFailures(euid int) []ExpectedFailure {
	if len(expectedFailuresUnderRoot) != 2 {
		panic(fmt.Sprintf("testgate: expected-failure allowlist must be EXACTLY 2 entries, got %d (no wildcard, no drift)", len(expectedFailuresUnderRoot)))
	}
	if euid != 0 {
		return nil // non-root: the two tests are expected to pass
	}
	out := make([]ExpectedFailure, len(expectedFailuresUnderRoot))
	copy(out, expectedFailuresUnderRoot)
	return out
}

// TestEvent is the subset of a `go test -json` event this gate consumes.
type TestEvent struct {
	Action  string `json:"Action"` // "pass" | "fail" | "skip" | "run" | "output" | ...
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

// EvaluateResult is the gate verdict.
type EvaluateResult struct {
	Green           bool
	UnexpectedFails []string // failing tests NOT on the allowlist (real regressions)
	MissingExpected []string // allowlisted tests that did NOT fail (started passing / disappeared)
	MatchedExpected []string // allowlisted tests that correctly failed
}

// Evaluate consumes a `go test -json` stream and decides whether the run is green
// under the allowlist for the given euid. The run is GREEN iff:
//   - no test outside the allowlist failed (no hidden regression), AND
//   - every allowlisted test for this privilege level actually failed AND exists.
//
// A failing allowlisted test that starts passing, or a third failing test, both
// flip the verdict to not-green and are named — so a regression cannot hide behind
// the carve-out, and a stale carve-out cannot mask a now-passing test.
func Evaluate(r io.Reader, euid int) (EvaluateResult, error) {
	allow := ExpectedFailures(euid)
	allowSet := make(map[string]ExpectedFailure, len(allow))
	for _, e := range allow {
		allowSet[e.Key()] = e
	}

	failed := make(map[string]struct{})  // package\x00test of every failing test
	matched := make(map[string]struct{}) // allowlisted keys that failed
	var unexpected []string

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var ev TestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // tolerate non-JSON interleaved output
		}
		if ev.Action != "fail" || ev.Test == "" {
			continue // only test-level fail events (skip package-level fail summaries)
		}
		key := ev.Package + "\x00" + ev.Test
		failed[key] = struct{}{}
		if _, ok := allowSet[key]; ok {
			matched[key] = struct{}{}
		} else {
			unexpected = append(unexpected, ev.Package+"."+ev.Test)
		}
	}
	if err := sc.Err(); err != nil {
		return EvaluateResult{}, fmt.Errorf("testgate: read go test -json stream: %w", err)
	}

	res := EvaluateResult{}
	for _, e := range allow {
		if _, ok := matched[e.Key()]; ok {
			res.MatchedExpected = append(res.MatchedExpected, e.String())
		} else {
			res.MissingExpected = append(res.MissingExpected, e.String())
		}
	}
	res.UnexpectedFails = unexpected
	sort.Strings(res.UnexpectedFails)
	sort.Strings(res.MissingExpected)
	sort.Strings(res.MatchedExpected)

	res.Green = len(res.UnexpectedFails) == 0 && len(res.MissingExpected) == 0
	return res, nil
}

// FormatVerdict renders a human-readable summary of an EvaluateResult.
func FormatVerdict(res EvaluateResult, euid int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "test-allowlist gate (euid=%d): ", euid)
	if res.Green {
		fmt.Fprintf(&b, "GREEN — only the %d allowlisted carve-out test(s) failed\n", len(res.MatchedExpected))
	} else {
		b.WriteString("NOT GREEN\n")
	}
	if len(res.UnexpectedFails) > 0 {
		fmt.Fprintf(&b, "  unexpected failures (regressions, cannot hide behind the carve-out): %v\n", res.UnexpectedFails)
	}
	if len(res.MissingExpected) > 0 {
		fmt.Fprintf(&b, "  allowlisted tests that did NOT fail (started passing / disappeared): %v\n", res.MissingExpected)
	}
	return b.String()
}
