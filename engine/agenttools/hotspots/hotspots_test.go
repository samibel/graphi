package hotspots

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

var fixedNow = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

// fixtureDeps builds a graph where util/format.go is highly connected and
// docs/notes.md is not in the graph at all.
func fixtureDeps(t *testing.T) resolve.Deps {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()

	mk := func(kind, qn, path string, line int) model.Node {
		n, err := model.NewNode(kind, qn, path, line, 1)
		if err != nil {
			t.Fatalf("node %s: %v", qn, err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatalf("put node %s: %v", qn, err)
		}
		return n
	}
	format := mk("function", "util.Format", "util/format.go", 3)
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	helper := mk("function", "pkg.Helper", "pkg/helper.go", 5)

	edge := func(from, to model.Node, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), "calls", model.TierConfirmed, 0.9, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	edge(run, format, "cmd/app/main.go:12")
	edge(helper, format, "pkg/helper.go:7")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

// history: util/format.go churns 3× (two authors), docs/notes.md churns 5×
// (single author, zero graph endpoints).
func provider() *githistory.InMemoryProvider {
	c := func(sha, author string, age time.Duration, files ...string) githistory.Commit {
		return githistory.Commit{SHA: sha, Author: author, Timestamp: fixedNow.Add(-age), FilesChanged: files}
	}
	return &githistory.InMemoryProvider{Commits: []githistory.Commit{
		c("c5", "alice", 1*time.Hour, "util/format.go", "docs/notes.md"),
		c("c4", "bob", 2*time.Hour, "util/format.go"),
		c("c3", "alice", 3*time.Hour, "util/format.go", "docs/notes.md"),
		c("c2", "alice", 4*time.Hour, "docs/notes.md"),
		c("c1", "alice", 5*time.Hour, "docs/notes.md", "cmd/app/main.go"),
		c("c0", "alice", 6*time.Hour, "docs/notes.md"),
	}}
}

func TestAssembleRanksChurnTimesCentrality(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Assemble(context.Background(), Params{Provider: provider(), Now: fixedNow, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	// util/format.go: 3 commits × (1+2 endpoints... EdgeEndpoints counts both
	// edge ends per file). docs/notes.md: 5 commits × (1+0) = 5. The graph
	// coupling must beat raw churn: util/format.go ranks first.
	if !strings.Contains(res.Summary, "top util/format.go") {
		t.Fatalf("expected the coupled file to outrank the raw-churn file: %q", res.Summary)
	}
	if !strings.Contains(res.Summary, MethodVersion) {
		t.Fatalf("summary missing method version: %q", res.Summary)
	}

	var sawHotFormat, sawHotDocs, sawBus, sawNext bool
	for _, it := range res.Items {
		switch {
		case strings.HasPrefix(it.Reason, "hotspot: util/format.go"):
			sawHotFormat = true
			if !strings.Contains(it.Reason, "3 commit(s)") {
				t.Fatalf("breakdown must show the commit count: %q", it.Reason)
			}
		case strings.HasPrefix(it.Reason, "hotspot: docs/notes.md"):
			sawHotDocs = true
		case strings.HasPrefix(it.Reason, "bus_factor: docs/notes.md"):
			sawBus = true // single-author hotspot
		case strings.HasPrefix(it.Reason, "next: graphi change-impact util/format.go"):
			sawNext = true
		}
	}
	if !sawHotFormat || !sawHotDocs || !sawBus || !sawNext {
		t.Fatalf("missing sections: format=%v docs=%v bus=%v next=%v", sawHotFormat, sawHotDocs, sawBus, sawNext)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestAssembleDegradations(t *testing.T) {
	deps := fixtureDeps(t)

	res, err := Assemble(context.Background(), Params{Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable || !strings.Contains(res.Summary, "no git history") {
		t.Fatalf("nil provider must be typed unavailable: %s (%s)", res.Outcome, res.Summary)
	}

	res, err = Assemble(context.Background(), Params{Provider: &githistory.InMemoryProvider{}, Now: fixedNow, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("empty history must be empty outcome, got %s", res.Outcome)
	}

	res, err = Assemble(context.Background(), Params{Provider: provider(), Deps: resolve.Deps{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("missing graph services must be unavailable, got %s", res.Outcome)
	}
}

func TestAssembleDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	run := func() []byte {
		res, err := Assemble(context.Background(), Params{Provider: provider(), Now: fixedNow, Deps: deps})
		if err != nil {
			t.Fatal(err)
		}
		b, err := contract.Serialize(res)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a, b := run(), run()
	if !bytes.Equal(a, b) {
		t.Fatalf("non-deterministic output:\n%s\n%s", a, b)
	}
}
