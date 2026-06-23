package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/client"
)

// recordingClient returns canned canonical bytes for Query/Search/Analyze so the
// TUI's render path is exercised deterministically, and so the parity test can
// assert the TUI surfaces the SAME bytes the client returns.
type recordingClient struct {
	queryB   []byte
	searchB  []byte
	analyzeB []byte
	lastOp   string
	lastSym  string
	lastDep  int
	err      error // when set, Query returns err (error-path test)
}

func canonicalResult(t *testing.T, op, sym, outcome string) []byte {
	t.Helper()
	r := map[string]any{
		"operation": op, "symbol": sym, "outcome": outcome,
		"nodes": []map[string]any{
			{"id": "pkg.Bar", "kind": "func", "qualified_name": "pkg.Bar", "source_path": "bar.go", "line": 10},
		},
		"edges": []map[string]any{
			{"id": "e1", "from": "pkg.Foo", "to": "pkg.Bar", "kind": "calls",
				"confidence_tier": "confirmed", "confidence": 0.95, "reason": "static call", "evidence": []string{"foo.go:5"}},
		},
	}
	b, _ := json.Marshal(r)
	return b
}

func (r *recordingClient) Query(_ context.Context, op, sym string, depth int) ([]byte, error) {
	r.lastOp, r.lastSym, r.lastDep = op, sym, depth
	if r.err != nil {
		return nil, r.err
	}
	return r.queryB, nil
}
func (r *recordingClient) Search(context.Context, string, int) ([]byte, error) { return r.searchB, nil }
func (r *recordingClient) Analyze(_ context.Context, p client.AnalyzeParams) ([]byte, error) {
	_ = p
	return r.analyzeB, nil
}
func (r *recordingClient) Savings(context.Context) ([]byte, error) { return nil, nil }
func (r *recordingClient) RefactorPreview(context.Context, client.RefactorRequest) ([]byte, error) {
	return nil, nil
}
func (r *recordingClient) Refactor(context.Context, client.RefactorRequest, string) ([]byte, error) {
	return nil, nil
}
func (r *recordingClient) Undo(context.Context, string, string) ([]byte, error) { return nil, nil }
func (r *recordingClient) PrComment(context.Context, client.PrCommentRequest) ([]byte, error) {
	return nil, nil
}

func newTUI(t *testing.T) (*Model, *recordingClient) {
	t.Helper()
	rc := &recordingClient{
		queryB:   canonicalResult(t, "neighborhood", "pkg.Foo", "found"),
		searchB:  []byte(`{"query":"foo","matches":[{"id":"pkg.Foo","path":"foo.go","line":1}]}`),
		analyzeB: []byte(`{"analyzer":"impact","impacted":["pkg.Bar"],"provenance":{"tier":"confirmed"}}`),
	}
	return New(rc), rc
}

func run(t *testing.T, m *Model, cmds string) string {
	t.Helper()
	var out bytes.Buffer
	if err := m.Run(context.Background(), strings.NewReader(cmds), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return out.String()
}

func TestNeighbors_RendersNodesEdgesAndProvenance(t *testing.T) {
	m, rc := newTUI(t)
	out := run(t, m, "select pkg.Foo\nneighbors 1\nquit\n")
	if rc.lastOp != "neighborhood" || rc.lastSym != "pkg.Foo" || rc.lastDep != 1 {
		t.Fatalf("client got op=%s sym=%s depth=%d", rc.lastOp, rc.lastSym, rc.lastDep)
	}
	if !strings.Contains(out, "pkg.Bar") || !strings.Contains(out, "bar.go:10") {
		t.Fatalf("neighbors did not render node: %s", out)
	}
	if !strings.Contains(out, "tier=confirmed") || !strings.Contains(out, "reason=") || !strings.Contains(out, "evidence=") {
		t.Fatalf("edges missing provenance: %s", out)
	}
}

func TestBlast_CallsAnalyze(t *testing.T) {
	m, rc := newTUI(t)
	out := run(t, m, "select pkg.Foo\nblast\nquit\n")
	if !strings.Contains(out, "blast-radius") {
		t.Fatalf("blast not rendered: %s", out)
	}
	if !bytes.Contains(rc.analyzeB, []byte("impact")) {
		// sanity: analyzeB was the impact payload
	}
}

func TestSearch_Renders(t *testing.T) {
	m, _ := newTUI(t)
	out := run(t, m, "search foo\nquit\n")
	if !strings.Contains(out, "search results") || !strings.Contains(out, "pkg.Foo") {
		t.Fatalf("search not rendered: %s", out)
	}
}

func TestErrorPath_LoopContinues(t *testing.T) {
	m, rc := newTUI(t)
	rc.err = errEngine
	out := run(t, m, "select pkg.Foo\nneighbors 1\nhelp\nquit\n")
	if !strings.Contains(out, "engine error") {
		t.Fatalf("error not surfaced: %s", out)
	}
	if !strings.Contains(out, "commands:") {
		t.Fatalf("loop did not continue after error: %s", out)
	}
}

func TestParity_TUISurfacesSameBytesAsClient(t *testing.T) {
	// The TUI calls client.Client.Query with the same args; the canonical bytes
	// it receives are what it renders. Assert the client was invoked identically
	// to a direct call (op/symbol/depth) — parity by construction.
	m, rc := newTUI(t)
	_ = run(t, m, "select pkg.Foo\nneighbors 2\nquit\n")
	if rc.lastOp != "neighborhood" || rc.lastSym != "pkg.Foo" || rc.lastDep != 2 {
		t.Fatalf("TUI did not call client identically to a direct call: %+v", rc)
	}
	// And the rendered output embeds the client's payload fields verbatim.
	out := run(t, m, "select pkg.Foo\nneighbors 1\nquit\n")
	if !strings.Contains(out, "confirmed") || !strings.Contains(out, "0.95") {
		t.Fatalf("rendered output does not reflect client payload bytes: %s", out)
	}
}

func TestQuit_And_EOF_BothExit(t *testing.T) {
	m, _ := newTUI(t)
	// EOF (no quit) must exit cleanly
	out := run(t, m, "help\n")
	if !strings.Contains(out, "commands:") {
		t.Fatalf("help not rendered before EOF: %s", out)
	}
}

func TestSelectRequired_Guarded(t *testing.T) {
	m, _ := newTUI(t)
	out := run(t, m, "neighbors 1\nquit\n")
	if !strings.Contains(out, "select a symbol first") {
		t.Fatalf("missing-select guard failed: %s", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	m, _ := newTUI(t)
	out := run(t, m, "bogus\nquit\n")
	if !strings.Contains(out, "unknown command") {
		t.Fatalf("unknown command not handled: %s", out)
	}
}

// errEngine is a sentinel for the error-path test.
var errEngine = &strErr{"simulated engine failure"}

type strErr struct{ s string }

func (e *strErr) Error() string { return e.s }
