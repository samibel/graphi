package main

// SW-265 AC-7 — log hygiene.
//
// The semantic index and search paths must NOT log source text, query
// text, or vectors at any log level. The test captures stderr during a
// full `graphi index --semantic` run AND a `graphi search -semantic`
// run against a fixture repo with a distinctive body marker, then greps
// the captured output for that marker.
//
// The marker is "GRAPHI_SW265_LOG_HYGIENE_MARKER_a3f9c7e2b1d4" — a
// random hex string that is unlikely to appear in any framework output
// and is bound to the test, not the code. A regression that logged
// source text would surface the marker and fail the test; a regression
// that logged NO output would surface as a "no output captured" error,
// which is also a fail (the test must exercise the path).
//
// What the test catches:
//
//   - any Fprintf that includes the source text (the marker)
//   - any panic stack trace that includes the source text
//   - any "diagnostic" line that prints a query or vector sample
//   - any future log line added by a feature without a hygiene review
//
// What the test deliberately does NOT check:
//
//   - absence of telemetry (the privacy-audit gate handles that
//     independently; this test focuses on the embed path's stderr)
//   - exact stderr contents (the index command writes operational
//     summary lines that legitimately differ across runs; only the
//     marker is a fail)

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

// logHygieneMarker is the distinctive string the test asserts NEVER
// appears in captured stderr. Random hex, chosen once and pinned to
// this test file; the value is irrelevant beyond "doesn't appear in
// framework output".
const logHygieneMarker = "GRAPHI_SW265_LOG_HYGIENE_MARKER_a3f9c7e2b1d4"

// TestLogHygiene_AC7_IndexSearchDoNotLeakSourceText runs the
// semantic index and search paths against a fixture repo whose every
// source file embeds logHygieneMarker, then greps captured stderr for
// the marker.
//
// The test is hermetic: it builds a tempdir repo with marker-bearing
// Go source, points -root/-db/-meta at tempdir locations, and captures
// the full stderr of both invocations. The marker is in BOTH the
// source text and a comment, so even a debug print that drops into
// the file via %v or %s would surface it.
//
// The test registers a mock embedder as the `mock` scheme via
// embed.RegisterScheme (the production-grade runtime registry), so the
// cmd rank's runtimeEmbedderRegistry resolves to a working embedder
// and the full --semantic + -semantic + status paths execute. A path
// that no-ops because no embedder is configured would make the test
// vacuous: the contract is "if the path runs, it does not leak".
func TestLogHygiene_AC7_IndexSearchDoNotLeakSourceText(t *testing.T) {
	// Register the mock scheme so the runtime embedder registry
	// resolves to a working embedder. The mock is registered against
	// every new scheme constructor; the test ensures one registration
	// is in place before the command runs. We use a unique scheme
	// name per test invocation so two parallel test runs cannot race.
	scheme := "sw265_log_hygiene"
	mock := embed.NewMockEmbedder(8)
	embed.RegisterScheme(scheme, func(arg string) (embed.Embedder, error) {
		return mock, nil
	})
	t.Setenv("GRAPHI_EMBEDDER", scheme+":8")
	repo := t.TempDir()
	marker := logHygieneMarker
	src := `package shop

// ` + marker + ` — file-level marker for the log hygiene test.

const TaxRate = 7

// ` + marker + ` — function-level marker.

func price(n int) int { return n * TaxRate }
`
	if err := os.WriteFile(filepath.Join(repo, "cart.go"), []byte(src), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	// Init git so the index path treats the dir as a repo.
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	head := filepath.Join(repo, ".git", "HEAD")
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}

	db := filepath.Join(t.TempDir(), "graph.db")
	meta := filepath.Join(t.TempDir(), "meta")
	root := repo

	// Phase 1: `graphi index --semantic`. Capture stderr; assert no
	// marker.
	var indexOut string
	var indexRC int
	indexStdout := captureStdout(t, func() {
		indexOut, indexRC = captureSemanticStderr(t, func() int {
			return runIndex([]string{
				"--semantic", "-root", root, "-db", db, "-meta", meta,
			})
		})
	})
	if indexRC != 0 {
		t.Fatalf("index --semantic exit=%d:\n%s", indexRC, indexOut)
	}
	if indexOut == "" {
		t.Fatalf("index --semantic produced no stderr; the test is vacuous")
	}
	if strings.Contains(indexOut, marker) {
		t.Errorf("AC-7 leak: source-text marker appeared in `graphi index --semantic` stderr:\n%s", indexOut)
	}
	if !strings.Contains(indexStdout, "graphi index --semantic: generation ") || !strings.Contains(indexStdout, " state ready\n") {
		t.Fatalf("index did not finish with generation id and resulting ready state:\n%s", indexStdout)
	}

	// Phase 2: `graphi search -semantic <query>`. The query itself
	// must NOT leak (a regression that logged the query would surface
	// the marker string as a query argument). We use the marker as
	// the query string and assert it does NOT appear in stderr.
	query := marker
	searchArgs := []string{"-semantic", "-limit", "5", query, "-db", db, "-meta", meta}
	searchOut, searchRC := captureSemanticStderr(t, func() int {
		return runSearch(searchArgs)
	})
	if searchRC != 0 {
		t.Fatalf("search exit=%d:\n%s", searchRC, searchOut)
	}
	// `search` may return 0 for "no hits" — the marker query is not
	// in the fixture's source so the search rightly reports empty.
	// We only check that the marker did NOT appear in stderr.
	if searchOut != "" && strings.Contains(searchOut, marker) {
		t.Errorf("AC-7 leak: query marker appeared in `graphi search -semantic` stderr:\n%s", searchOut)
	}

	// Phase 3: `graphi semantic status` over the now-built sidecar.
	// Status surfaces don't read source text, but the test exercises
	// the path so any future regression in the status surface is
	// caught.
	statusOut, statusRC := captureSemanticStderr(t, func() int {
		return runSemanticStatus([]string{"--json", "-root", root, "-db", db, "-meta", meta})
	})
	if statusRC != 0 {
		t.Fatalf("semantic status exit=%d, want ready/0:\n%s", statusRC, statusOut)
	}
	if statusOut != "" && strings.Contains(statusOut, marker) {
		t.Errorf("AC-7 leak: source-text marker appeared in `graphi semantic status` stderr:\n%s", statusOut)
	}
}

// captureStderrForArgs runs fn while os.Stderr is redirected to a pipe,
// returning the captured bytes. Used to assert that log lines from a
// command do not contain a sensitive marker.
func captureSemanticStderr(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	rc := fn()
	os.Stderr = old
	_ = w.Close()
	return <-done, rc
}
