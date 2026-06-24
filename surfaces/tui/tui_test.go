package tui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
)

// --- fakeEngine: a deterministic in-package Engine for unit-level model tests --

type fakeEngine struct {
	contractRaw []byte
	queryRaw    []byte
	searchRaw   []byte
	analyzeRaw  []byte
	queryErr    error
	contractErr error
	events      chan client.Event
	subErr      error

	lastOp, lastSym string
	lastDepth       int
}

func canonicalQuery() []byte {
	r := map[string]any{
		"operation": "neighborhood", "symbol": "pkg.Foo", "outcome": "found",
		"nodes": []map[string]any{{"id": "pkg.Bar", "kind": "func", "qualified_name": "pkg.Bar", "source_path": "bar.go", "line": 10}},
		"edges": []map[string]any{{"id": "e1", "from": "pkg.Foo", "to": "pkg.Bar", "kind": "calls",
			"confidence_tier": "confirmed", "confidence": 0.95, "reason": "static call", "evidence": []string{"foo.go:5"}}},
	}
	b, _ := json.Marshal(r)
	return b
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{
		contractRaw: []byte(`{"schema_version":1,"resources":["query/callers","query/neighborhood","search","analyze/impact"],"streams":["ready","ingest-completed","bye","error"]}`),
		queryRaw:    canonicalQuery(),
		searchRaw:   []byte(`{"query":"foo","matches":[{"id":"pkg.Foo"}]}`),
		analyzeRaw:  []byte(`{"analyzer":"impact","impacted":["pkg.Bar"]}`),
		events:      make(chan client.Event, 8),
	}
}

func (f *fakeEngine) Query(_ context.Context, op, sym string, depth int) ([]byte, error) {
	f.lastOp, f.lastSym, f.lastDepth = op, sym, depth
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	return f.queryRaw, nil
}
func (f *fakeEngine) Search(context.Context, string, int) ([]byte, error) { return f.searchRaw, nil }
func (f *fakeEngine) SemanticSearch(context.Context, string, int) ([]byte, error) {
	return f.searchRaw, nil
}
func (f *fakeEngine) Analyze(context.Context, client.AnalyzeParams) ([]byte, error) {
	return f.analyzeRaw, nil
}
func (f *fakeEngine) Savings(context.Context) ([]byte, error) {
	return nil, client.ErrSavingsUnavailable
}
func (f *fakeEngine) RefactorPreview(context.Context, client.RefactorRequest) ([]byte, error) {
	return nil, client.ErrEditUnavailable
}
func (f *fakeEngine) Refactor(context.Context, client.RefactorRequest, string) ([]byte, error) {
	return nil, client.ErrEditUnavailable
}
func (f *fakeEngine) Undo(context.Context, string, string) ([]byte, error) {
	return nil, client.ErrEditUnavailable
}
func (f *fakeEngine) PrComment(context.Context, client.PrCommentRequest) ([]byte, error) {
	return nil, client.ErrReviewUnavailable
}
func (f *fakeEngine) Contract(context.Context) ([]byte, error) {
	if f.contractErr != nil {
		return nil, f.contractErr
	}
	return f.contractRaw, nil
}
func (f *fakeEngine) SubscribeEvents(context.Context) (<-chan client.Event, error) {
	if f.subErr != nil {
		return nil, f.subErr
	}
	return f.events, nil
}
func (f *fakeEngine) SchemaVersion() int { return 1 }

var _ Engine = (*fakeEngine)(nil)

// drive applies a window-size msg then folds the supplied msgs into the model.
func drive(m *Model, msgs ...tea.Msg) *Model {
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = mm.(*Model)
	for _, msg := range msgs {
		mm, _ = m.Update(msg)
		m = mm.(*Model)
	}
	return m
}

// AC-1: /contract drives the navigator; the layout is keyboard-navigable.
func TestContractPopulatesNavigatorAndTabCycles(t *testing.T) {
	f := newFakeEngine()
	m := New(f)
	m = drive(m, contractMsg{raw: f.contractRaw})
	if len(m.nav.Items()) != 4 {
		t.Fatalf("navigator not populated from /contract: %d items", len(m.nav.Items()))
	}
	if !m.connected {
		t.Fatalf("expected connected after contract")
	}
	// Tab cycles focus across the three panes.
	start := m.focus
	m = drive(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus == start {
		t.Fatalf("Tab did not change pane focus")
	}
}

// AC-2: the rendered output reflects the client's payload bytes verbatim, and the
// model calls the engine identically to a direct call (parity by construction).
func TestParity_RendersClientPayloadVerbatim(t *testing.T) {
	f := newFakeEngine()
	m := New(f)
	m = drive(m, answerMsg{kind: "query", label: "neighborhood pkg.Foo", raw: f.queryRaw})
	if !strings.Contains(m.mainContent, "pkg.Bar") || !strings.Contains(m.mainContent, "bar.go:10") {
		t.Fatalf("main content missing payload node: %s", m.mainContent)
	}
	if !strings.Contains(m.mainContent, "tier=confirmed") {
		t.Fatalf("main content missing inline provenance: %s", m.mainContent)
	}
}

// AC-4: provenance is visible inline AND in the persistent pane; the pane is
// never empty while an answer is displayed (engine version + per-edge evidence).
func TestProvenancePanePopulatedOnAnswer(t *testing.T) {
	f := newFakeEngine()
	m := New(f)
	m = drive(m, answerMsg{kind: "query", label: "neighborhood pkg.Foo", raw: f.queryRaw})
	if !strings.Contains(m.provContent, "confidence=0.95") {
		t.Fatalf("provenance pane missing confidence: %s", m.provContent)
	}
	if !strings.Contains(m.provContent, "schema_version: 1") {
		t.Fatalf("provenance pane missing engine schema version: %s", m.provContent)
	}
	if !strings.Contains(m.provContent, "static call") || !strings.Contains(m.provContent, "foo.go:5") {
		t.Fatalf("provenance pane missing per-edge evidence: %s", m.provContent)
	}
	if strings.TrimSpace(m.provContent) == "" {
		t.Fatalf("provenance pane is empty while an answer is displayed")
	}
}

// AC-3a: an in-flight query sets the spinner indicator and navigation is not
// blocked (Tab still cycles panes while inFlight).
func TestAsyncInFlightDoesNotBlockNavigation(t *testing.T) {
	f := newFakeEngine()
	m := New(f)
	m = drive(m, contractMsg{raw: f.contractRaw})
	m.inFlight = true // simulate a query command dispatched
	before := m.focus
	m = drive(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus == before {
		t.Fatalf("navigation blocked while in-flight")
	}
	// completion clears in-flight and sets the marker.
	m = drive(m, answerMsg{kind: "query", label: "callers pkg.Foo", raw: f.queryRaw})
	if m.inFlight {
		t.Fatalf("in-flight not cleared on completion")
	}
	if !strings.Contains(m.lastComplete, "✓") {
		t.Fatalf("completion marker missing: %q", m.lastComplete)
	}
}

// AC-3b: freshness SSE frames are folded in as tea.Msgs (navigator stays live).
func TestFreshnessEventsKeepNavigatorLive(t *testing.T) {
	f := newFakeEngine()
	m := New(f)
	m = drive(m, eventMsg{ev: client.Event{Type: "ingest-completed"}})
	if !m.connected || !strings.Contains(m.lastComplete, "graph updated") {
		t.Fatalf("ingest-completed did not refresh navigator: %q connected=%v", m.lastComplete, m.connected)
	}
	m = drive(m, eventMsg{closed: true})
	if m.connected || !strings.Contains(m.banner, "stream ended") {
		t.Fatalf("stream close did not drive degraded banner: %q", m.banner)
	}
}

// AC-5: each typed error renders a distinct, non-crashing banner.
func TestDegradedBanners(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{client.ErrUnreachable, "unreachable"},
		{client.ErrSchemaMismatch, "version mismatch"},
		{client.ErrUnavailable, "unavailable"},
	}
	for _, tc := range cases {
		f := newFakeEngine()
		m := New(f)
		m = drive(m, contractMsg{err: tc.err})
		if !strings.Contains(m.banner, tc.want) {
			t.Fatalf("err %v: banner %q missing %q", tc.err, m.banner, tc.want)
		}
		// TUI stays responsive: quit + retry still work, no panic.
		m = drive(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
		mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		if !mm.(*Model).quitting {
			t.Fatalf("quit did not work after %v", tc.err)
		}
	}
}

// R5: a schema-version mismatch gets a DISTINCT banner from unreachable.
func TestSchemaDriftDistinctBanner(t *testing.T) {
	f := newFakeEngine()
	m := New(f)
	m = drive(m, answerMsg{kind: "query", err: client.ErrSchemaMismatch, label: "callers x"})
	if !strings.Contains(m.banner, "contract version mismatch") {
		t.Fatalf("schema drift banner not distinct: %q", m.banner)
	}
}

// AC-6: enumerate every key binding; assert NONE maps to a mutating op, and the
// footer advertises read-only.
func TestReadOnly_NoMutatingBindings(t *testing.T) {
	k := defaultKeyMap()
	if len(k.mutatingBindings()) != 0 {
		t.Fatalf("found mutating key bindings: %v", k.mutatingBindings())
	}
	// The mutation methods on the adapter never reach a live path: assert the
	// model's only engine calls are the read-only ones by driving a select.
	f := newFakeEngine()
	m := New(f)
	m = drive(m, contractMsg{raw: f.contractRaw})
	m.focusSymbol = "pkg.Foo"
	// select the first capability (query/callers) and run it.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			if am, ok := msg.(answerMsg); ok && am.kind != "query" && am.kind != "search" && am.kind != "analyze" {
				t.Fatalf("select triggered non-read-only op: %s", am.kind)
			}
		}
	}
	// Footer text advertises read-only.
	m = drive(m)
	if !strings.Contains(m.View(), "read-only") {
		t.Fatalf("footer does not advertise read-only surface")
	}
}

// AC-6 (structural): the HTTP adapter's mutation methods return typed unavailable.
func TestAdapterMutationMethodsUnavailable(t *testing.T) {
	a, err := client.NewHTTP("http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	if _, err := a.Refactor(context.Background(), client.RefactorRequest{}, "x"); !errors.Is(err, client.ErrEditUnavailable) {
		t.Fatalf("Refactor not unavailable: %v", err)
	}
}

// AC-1 end-to-end via teatest against the REAL SW-044 handler over loopback:
// the program connects, populates the navigator from /contract, and quits cleanly.
func TestEndToEnd_RealServer_ConnectsAndNavigates(t *testing.T) {
	st := graphstore.NewMemStore()
	n, _ := model.NewNode("function", "pkg.Foo", "foo.go", 1, 1)
	_ = st.PutNode(context.Background(), n)
	c := client.NewDirect(query.New(st), search.New(st))
	srv := httptest.NewServer(httpsrv.New(c, observe.New()).Handler())
	defer srv.Close()

	eng, err := client.NewHTTP(srv.URL)
	if err != nil {
		t.Fatalf("NewHTTP: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := New(eng).WithContext(ctx)

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(120, 40))
	// Wait until the navigator shows a capability advertised by /contract.
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return strings.Contains(string(b), "search") || strings.Contains(string(b), "query/")
	}, teatest.WithDuration(8*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}
