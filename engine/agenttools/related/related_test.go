package related

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

type lookupProbe struct {
	*graphstore.MemStore
	calls int
	err   error
}

func (p *lookupProbe) NodesByID(ctx context.Context, ids []model.NodeId) ([]model.Node, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return p.MemStore.NodesByID(ctx, ids)
}

// fixtureDeps builds the shared in-memory relation graph (see explain tests).
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
	run := mk("function", "main.Run", "cmd/app/main.go", 10)
	helper := mk("function", "pkg.Helper", "pkg/helper.go", 5)
	format := mk("function", "util.Format", "util/format.go", 3)
	testFn := mk("function", "tests.TestFormat", "util/format_test.go", 8)

	edge := func(from, to model.Node, kind string, tier model.ConfidenceTier, conf float64, ev string) {
		e, err := model.NewEdge(from.ID(), to.ID(), kind, tier, conf, "test fixture", []string{ev})
		if err != nil {
			t.Fatalf("edge: %v", err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatalf("put edge: %v", err)
		}
	}
	edge(run, format, "calls", model.TierConfirmed, 0.95, "cmd/app/main.go:12")
	edge(testFn, format, "calls", model.TierConfirmed, 0.9, "util/format_test.go:9")
	edge(helper, format, "references", model.TierHeuristic, 0.4, "pkg/helper.go:7")
	edge(run, helper, "calls", model.TierDerived, 0.8, "cmd/app/main.go:14")

	return resolve.Deps{Query: query.New(store), Search: search.New(store)}
}

func TestFilesForFileAnchorBothDirections(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Files(context.Background(), deps, "pkg/helper.go", DirectionBoth, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	// pkg.Helper is called by main.Run (dependent) and references util.Format
	// (dependency): both neighbor files rank, own file never appears.
	var paths []string
	for _, it := range res.Items {
		paths = append(paths, it.RefID)
		if it.RefID == "pkg/helper.go" {
			t.Fatal("anchor's own file must not be ranked")
		}
		if it.Reason == "" {
			t.Fatalf("every file needs a reason: %+v", it)
		}
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 related files, got %v", paths)
	}
	if err := contract.ValidateResult(res); err != nil {
		t.Fatalf("invalid result: %v", err)
	}
}

func TestFilesDirectionFilters(t *testing.T) {
	deps := fixtureDeps(t)

	depsOnly, err := Files(context.Background(), deps, "pkg/helper.go", DirectionDependencies, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(depsOnly.Items) != 1 || depsOnly.Items[0].RefID != "util/format.go" {
		t.Fatalf("dependencies must yield util/format.go only, got %+v", depsOnly.Items)
	}

	dependents, err := Files(context.Background(), deps, "pkg/helper.go", DirectionDependents, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dependents.Items) != 1 || dependents.Items[0].RefID != "cmd/app/main.go" {
		t.Fatalf("dependents must yield cmd/app/main.go only, got %+v", dependents.Items)
	}
}

func TestFilesInvalidDirection(t *testing.T) {
	deps := fixtureDeps(t)
	if _, err := Files(context.Background(), deps, "pkg/helper.go", "sideways", 0); err == nil {
		t.Fatal("expected error for invalid direction")
	}
}

func TestFilesSymbolAnchorRanksByConfidence(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Files(context.Background(), deps, "util.Format", DirectionDependents, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeFound {
		t.Fatalf("expected found, got %s (%s)", res.Outcome, res.Summary)
	}
	// Three dependents; confirmed callers outrank the heuristic reference.
	if len(res.Items) != 3 {
		t.Fatalf("expected 3 dependent files, got %d", len(res.Items))
	}
	if res.Items[0].RefID != "cmd/app/main.go" {
		t.Fatalf("highest-confidence dependent must rank first, got %q", res.Items[0].RefID)
	}
	if res.Items[2].RefID != "pkg/helper.go" {
		t.Fatalf("heuristic reference must rank last, got %q", res.Items[2].RefID)
	}
	if len(res.Evidence) == 0 {
		t.Fatal("expected evidence citations")
	}
}

func TestFilesUnresolvedAnchorIsEmpty(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Files(context.Background(), deps, "zzz.NotThere", DirectionBoth, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeEmpty {
		t.Fatalf("expected empty, got %s", res.Outcome)
	}
}

func TestFilesUnavailableWithoutServices(t *testing.T) {
	res, err := Files(context.Background(), resolve.Deps{}, "anything", DirectionBoth, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomeUnavailable {
		t.Fatalf("expected unavailable, got %s", res.Outcome)
	}
}

func TestFilesRequiresAnchor(t *testing.T) {
	if _, err := Files(context.Background(), resolve.Deps{}, "", DirectionBoth, 0); err == nil {
		t.Fatal("expected error for empty anchor")
	}
}

func TestFilesDeterministic(t *testing.T) {
	deps := fixtureDeps(t)
	a, err := Files(context.Background(), deps, "util.Format", DirectionBoth, 0)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Files(context.Background(), deps, "util.Format", DirectionBoth, 0)
	if err != nil {
		t.Fatal(err)
	}
	ab, _ := contract.Serialize(a)
	bb, _ := contract.Serialize(b)
	if string(ab) != string(bb) {
		t.Fatal("related_files output is not deterministic")
	}
}

func TestFilesTruncationMarksPartial(t *testing.T) {
	deps := fixtureDeps(t)
	res, err := Files(context.Background(), deps, "util.Format", DirectionDependents, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != contract.OutcomePartial {
		t.Fatalf("expected partial, got %s", res.Outcome)
	}
	if !res.Limits.Truncated {
		t.Fatalf("limits must record truncation: %+v", res.Limits)
	}
	if !strings.Contains(res.Summary, "related files") {
		t.Fatalf("unexpected summary: %q", res.Summary)
	}
}

func TestFilesBatchesHydrationAndPropagatesFailure(t *testing.T) {
	baseDeps := fixtureDeps(t)
	store := baseDeps.Query.Reader().(*graphstore.MemStore)
	probe := &lookupProbe{MemStore: store}
	deps := resolve.Deps{Query: query.New(probe), Search: search.New(probe)}

	if _, err := Files(context.Background(), deps, "util.Format", DirectionDependents, 0); err != nil {
		t.Fatalf("Files: %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("neighbor hydration used %d NodesByID calls, want one batch", probe.calls)
	}

	wantErr := errors.New("hydrate failed")
	probe.calls = 0
	probe.err = wantErr
	if _, err := Files(context.Background(), deps, "util.Format", DirectionDependents, 0); !errors.Is(err, wantErr) {
		t.Fatalf("Files hydration error = %v, want %v", err, wantErr)
	}
	if probe.calls != 1 {
		t.Fatalf("failed hydration used %d NodesByID calls, want one", probe.calls)
	}
}
