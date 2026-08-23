package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/parityreport"
)

// TestPrintCompileCoveragePolicy_AgreesWithTheGate holds the operator log and
// the gate to the SAME verdict, pin by pin, over every shape the rule can see.
//
// The two live in different packages and were extended in different rounds of
// SW-204: the sanity guard landed in parityreport.Finalize while
// printCompileCoveragePolicy still had three arms, so for the one input the
// guard exists to catch — a manifest claiming coverage >= 1.0000 beside
// compiled < source — stderr printed "accepted" while the report appended a
// refusal. The gate was never wrong; the LOG was, and a log that misnarrates
// the single case it was extended for is a defect in the thing an operator
// watches during a five-minute network dispatch.
//
// The invariant this test pins is not "the printer has four arms" — that is an
// implementation detail a refactor may change. It is: for every pin, the stderr
// line says REFUSED if and only if Finalize appends a compile_coverage reason
// naming that pin. Adding an arm to one side without the other fails here.
func TestPrintCompileCoveragePolicy_AgreesWithTheGate(t *testing.T) {
	cases := []struct {
		name string
		pin  string
		cc   *parityreport.CompileCoverageRef
		// wantRefused is asserted against BOTH sides; the test then also
		// asserts the two sides agree with each other, so a mistake in this
		// column cannot make a disagreement pass.
		wantRefused bool
	}{
		{
			name:        "no compile_coverage recorded",
			pin:         "unmeasured",
			cc:          nil,
			wantRefused: true,
		},
		{
			name:        "below 1.0 with no excluded_reason",
			pin:         "halfway",
			cc:          &parityreport.CompileCoverageRef{SourceFiles: 100, CompiledFiles: 50, Coverage: 0.5},
			wantRefused: true,
		},
		{
			name: "below 1.0 with a documented negative",
			pin:  "okio",
			cc: &parityreport.CompileCoverageRef{SourceFiles: 89, CompiledFiles: 0, Coverage: 0.0,
				ExcludedReason: "Kotlin/Native and JS targets are not built by the JVM oracle."},
			wantRefused: false,
		},
		{
			name:        "a full compile, counts agreeing",
			pin:         "guava",
			cc:          &parityreport.CompileCoverageRef{SourceFiles: 623, CompiledFiles: 623, Coverage: 1.0},
			wantRefused: false,
		},
		{
			// THE ONE THE GUARD EXISTS FOR, in the exact shape the review named:
			// stderr used to print "accepted — 0/89 = 1.0000" here.
			name:        "coverage 1.0000 claimed beside 0 of 89 compiled",
			pin:         "liar",
			cc:          &parityreport.CompileCoverageRef{SourceFiles: 89, CompiledFiles: 0, Coverage: 1.0},
			wantRefused: true,
		},
		{
			// The same contradiction with an excluded_reason present: the
			// documented-negative arm must NOT rescue it, on either side.
			name: "coverage 1.0000 beside compiled < source, with an excluded_reason",
			pin:  "liar-with-a-note",
			cc: &parityreport.CompileCoverageRef{SourceFiles: 100, CompiledFiles: 99, Coverage: 1.0,
				ExcludedReason: "one file is generated"},
			wantRefused: true,
		},
		{
			name:        "coverage above 1.0 beside compiled < source",
			pin:         "overclaiming",
			cc:          &parityreport.CompileCoverageRef{SourceFiles: 10, CompiledFiles: 1, Coverage: 1.5},
			wantRefused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pin := parityreport.RepoRef{Name: tc.pin, Tier: 3, SourceFiles: 100, CompileCoverage: tc.cc}

			// The gate.
			rep := parityreport.Report{Family: parityreport.FamilyJVM, Repos: []parityreport.RepoRef{pin}}
			rep.Finalize(0, 0)
			var gateReasons []string
			for _, why := range rep.NotPublishableBecause {
				if strings.HasPrefix(why, parityreport.ReasonPrefixCompileCoverage) && strings.Contains(why, tc.pin) {
					gateReasons = append(gateReasons, why)
				}
			}
			gateRefused := len(gateReasons) > 0

			// The log, over the same report.
			line := pinLine(t, rep, tc.pin)
			logRefused := strings.Contains(line, "REFUSED")

			if logRefused != gateRefused {
				t.Fatalf("stderr and the gate disagree for %s: stderr %q, gate reasons %v",
					tc.pin, line, gateReasons)
			}
			if gateRefused != tc.wantRefused {
				t.Fatalf("want refused=%v for %s, got gate=%v stderr=%q",
					tc.wantRefused, tc.pin, gateRefused, line)
			}
			if !logRefused && !strings.Contains(line, "accepted") {
				t.Fatalf("a non-refusing line must say so plainly: %q", line)
			}
			if tc.cc != nil && !strings.Contains(line, "/") {
				t.Errorf("a line about a measured pin must carry the figure: %q", line)
			}
			// Logged so `go test -v` is itself the transcript: a reader can see
			// the operator line beside the gate's reason rather than take the
			// agreement on trust.
			t.Logf("stderr: %s", strings.TrimSpace(line))
			for _, why := range gateReasons {
				t.Logf("gate:   %s", why)
			}
		})
	}
}

// TestPrintCompileCoveragePolicy_PublishedTriplesRenderUnchanged machine-checks
// the disclosure that docs/rc/parity-matrix-real-repo.md carries beside its
// stderr transcript.
//
// The two SW-204 dispatches ran at c51172b9, BEFORE the sanity guard and before
// this printer's fourth arm existed. The published transcript is therefore
// older than the code it sits beside, and the document now says so. What makes
// a re-dispatch unnecessary rather than merely inconvenient is that the guard's
// predicate — coverage >= 1.0000 AND compiled < source — is FALSE for all three
// published pins, so no per-pin line can take the new arm and no report field
// can move.
//
// That is a claim about THIS printer's output over THOSE reports, so it is
// checked against the reports themselves rather than against a re-typed
// summary: both published artifacts are read off disk, replayed through the
// current Finalize and the current printer, and required to produce zero
// compile_coverage refusals and the same three lines the document publishes.
func TestPrintCompileCoveragePolicy_PublishedTriplesRenderUnchanged(t *testing.T) {
	// The document publishes okio's line truncated (its excluded_reason is long),
	// so okio is matched on the published PREFIX; the other two are matched whole.
	const okioPublishedPrefix = "  okio                     accepted — 0/89 = 0.0000, " +
		"DOCUMENTED NEGATIVE: Not compiled in the oracle's required layout — see `tried`. " +
		"MEASURED, not assumed: the staged compile fails "
	whole := map[string]string{
		"guava":                 "  guava                    accepted — 623/623 = 1.0000",
		"kotlinx.serialization": "  kotlinx.serialization    accepted — 52/52 = 1.0000",
	}

	for _, name := range []string{"parity-matrix-jvm-sw204-run-A.json", "parity-matrix-jvm-sw204-run-B.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "rc", name))
			if err != nil {
				t.Fatalf("read published artifact: %v", err)
			}
			var rep parityreport.Report
			if err := json.Unmarshal(raw, &rep); err != nil {
				t.Fatalf("decode published artifact: %v", err)
			}
			if rep.Family != parityreport.FamilyJVM {
				t.Fatalf("published artifact is not a JVM report: family=%q", rep.Family)
			}
			if len(rep.Repos) != 3 {
				t.Fatalf("want the 3 published pins, got %d", len(rep.Repos))
			}

			// The guard, replayed over the real published figures.
			rep.Finalize(0, 0)
			for _, why := range rep.NotPublishableBecause {
				if strings.HasPrefix(why, parityreport.ReasonPrefixCompileCoverage) {
					t.Fatalf("the guard fires on a PUBLISHED pin, so the dispatches WOULD have to be re-run: %q", why)
				}
			}

			// The printer, replayed over the same report.
			for pin, want := range whole {
				if got := pinLine(t, rep, pin); got != want {
					t.Errorf("published line for %s moved:\n got %q\nwant %q", pin, got, want)
				}
			}
			if got := pinLine(t, rep, "okio"); !strings.HasPrefix(got, okioPublishedPrefix) {
				t.Errorf("published okio line moved:\n got %q\nwant prefix %q", got, okioPublishedPrefix)
			}
		})
	}
}

// TestPrintCompileCoveragePolicy_SilentOffTheJVMFamily guards the other half of
// the correspondence: Finalize scopes the whole rule to Family == FamilyJVM, so
// a Go-family run must produce neither a refusal nor a policy block. A log that
// narrated a policy the gate never applied would be its own inconsistency.
func TestPrintCompileCoveragePolicy_SilentOffTheJVMFamily(t *testing.T) {
	rep := parityreport.Report{Repos: []parityreport.RepoRef{{Name: "cobra", Tier: 1}}}
	rep.Finalize(0, 0)
	for _, why := range rep.NotPublishableBecause {
		if strings.HasPrefix(why, parityreport.ReasonPrefixCompileCoverage) {
			t.Fatalf("the JVM rule fired on a Go-family run: %q", why)
		}
	}
	if out := captureStderr(t, func() { printCompileCoveragePolicy(rep) }); out != "" {
		t.Fatalf("want no policy block off the JVM family, got %q", out)
	}
}

// pinLine returns the single policy line printed for pin, failing if the
// printer emitted none — an operator log that silently omits a pin is as bad as
// one that misnarrates it.
func pinLine(t *testing.T, rep parityreport.Report, pin string) string {
	t.Helper()
	out := captureStderr(t, func() { printCompileCoveragePolicy(rep) })
	var found []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "  ") && strings.Contains(l, pin) {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one policy line naming %q, got %d in:\n%s", pin, len(found), out)
	}
	return found[0]
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	func() {
		defer func() {
			os.Stderr = saved
			_ = w.Close()
		}()
		fn()
	}()
	b := <-done
	_ = r.Close()
	return string(b)
}
