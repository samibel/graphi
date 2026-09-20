package retrieval

import (
	"context"
	"errors"
	"testing"
)

// A multi-word question must be able to recover a complementary lexical
// candidate even when the semantic channel fills the requested result limit.
func TestNaturalLanguageCanPromoteComplementaryNameMatch(t *testing.T) {
	e := newEngine(&fakeLexical{hits: []lexicalHit{
		{NodeID: "implementation", Kind: "function", QualifiedName: "tree.AttachChild", Path: "tree.go", Line: 30},
	}}, &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "accessor", Kind: "function", QualifiedName: "tree.Child", Path: "tree.go", Line: 10, CosineScore: .60},
		{NodeID: "implementation", Kind: "function", QualifiedName: "tree.AttachChild", Path: "tree.go", Line: 30, CosineScore: .55},
	}}, nil)
	res, err := e.Retrieve(context.Background(), Request{Query: "attach a child", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].NodeID != "implementation" {
		t.Fatalf("complementary name evidence cannot displace semantic prefix: %+v", res.Rows)
	}
	x := res.Rows[0].Explain
	if x.Final != x.Base+x.RRF+x.Graph+x.Classification {
		t.Fatalf("score breakdown is incomplete: %+v", x)
	}
}

// fakeCalleeGraph is a minimal graphReader fake: it reports a fixed callee
// set for one wrapper node id and zero degree/callees for everything else.
type fakeCalleeGraph struct {
	wrapperID string
	callees   []calleeCandidate
	err       error
}

func (f *fakeCalleeGraph) inboundDegree(ctx context.Context, id string, cap int) (int, error) {
	return 0, nil
}

func (f *fakeCalleeGraph) calleeCandidates(ctx context.Context, id string, limit int) ([]calleeCandidate, error) {
	if f.err != nil {
		return nil, f.err
	}
	if id != f.wrapperID {
		return nil, nil
	}
	if limit < len(f.callees) {
		return f.callees[:limit], nil
	}
	return f.callees, nil
}

func TestNaturalLanguageGraphFailureIsNotSuccessfulRetrieval(t *testing.T) {
	want := errors.New("graph read failed")
	e := newEngine(&fakeLexical{}, &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "wrapper", Kind: "function", QualifiedName: "p.Run", Path: "p.go", Line: 1, CosineScore: .8},
	}}, &fakeCalleeGraph{err: want})
	if _, err := e.Retrieve(t.Context(), Request{Query: "run a job"}); !errors.Is(err, want) {
		t.Fatalf("graph failure hidden as successful retrieval: %v", err)
	}
}

// A bounded wrapper->callee expansion must be able to admit a candidate that
// never appeared in either the lexical or semantic top-K list at all — the
// documented cb-19 failure (ExecuteC ranks well; its callee execute never
// enters the candidate pool). The callee is only reachable through the
// wrapper's outgoing "calls" edge, never through name or semantic similarity
// alone (its query-term overlap is deliberately weak here), so admission
// must come from the graph expansion, not from nameTermScore.
func TestNaturalLanguageExpandsWrapperCallee(t *testing.T) {
	graph := &fakeCalleeGraph{
		wrapperID: "wrapper",
		callees: []calleeCandidate{
			{NodeID: "callee", Kind: "method", QualifiedName: "cobra.Command.unrelatedHelper", Path: "command.go", Line: 900},
		},
	}
	e := newEngine(&fakeLexical{}, &fakeSemantic{available: true, state: StateReady, hits: []semanticHit{
		{NodeID: "wrapper", Kind: "method", QualifiedName: "cobra.Command.ExecuteC", Path: "command.go", Line: 1052, CosineScore: .70},
		{NodeID: "filler1", Kind: "method", QualifiedName: "cobra.Command.SetOut", Path: "command.go", Line: 10, CosineScore: .30},
		{NodeID: "filler2", Kind: "method", QualifiedName: "cobra.Command.SetErr", Path: "command.go", Line: 20, CosineScore: .29},
	}}, graph)
	res, err := e.Retrieve(context.Background(), Request{Query: "lifecycle of running a command", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rows {
		if r.NodeID == "callee" {
			return
		}
	}
	t.Fatalf("wrapper's direct callee was not admitted before the Top-K cut: %+v", res.Rows)
}
