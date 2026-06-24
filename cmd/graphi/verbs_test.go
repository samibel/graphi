package main

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
)

// TestRewriteVerbArgs covers the argv reshaping contract: bare positional ->
// -symbol, explicit -symbol passthrough, leading -db/-daemon front-pull, and
// preservation of trailing flags in order.
func TestRewriteVerbArgs(t *testing.T) {
	cases := []struct {
		name string
		op   string
		in   []string
		want []string
	}{
		{"bare positional", "callers", []string{"B"}, []string{"callers", "-symbol", "B"}},
		{"explicit symbol passthrough", "callers", []string{"-symbol", "B"}, []string{"callers", "-symbol", "B"}},
		{"explicit symbol= passthrough", "callers", []string{"-symbol=B"}, []string{"callers", "-symbol=B"}},
		{"no args", "callers", []string{}, []string{"callers"}},
		{"leading -db moved to prefix then op", "callers", []string{"-db", "x.db", "B"},
			[]string{"-db", "x.db", "callers", "-symbol", "B"}},
		{"leading -db= moved to prefix", "callers", []string{"-db=x.db", "B"},
			[]string{"-db=x.db", "callers", "-symbol", "B"}},
		{"leading -daemon moved to prefix", "callers", []string{"-daemon", "/s.sock", "B"},
			[]string{"-daemon", "/s.sock", "callers", "-symbol", "B"}},
		{"trailing -depth preserved", "neighborhood", []string{"A", "-depth", "2"},
			[]string{"neighborhood", "-symbol", "A", "-depth", "2"}},
		{"trailing -direction preserved", "impact", []string{"A", "-direction", "reverse"},
			[]string{"impact", "-symbol", "A", "-direction", "reverse"}},
		{"db + bare + trailing flag", "impact", []string{"-db", "x.db", "A", "-max-nodes", "5"},
			[]string{"-db", "x.db", "impact", "-symbol", "A", "-max-nodes", "5"}},
		{"first remaining token is a flag (no promotion)", "callers", []string{"-depth", "3"},
			[]string{"callers", "-depth", "3"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rewriteVerbArgs(c.op, c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("rewriteVerbArgs(%q, %v) = %v, want %v", c.op, c.in, got, c.want)
			}
		})
	}
}

// TestAnalyzeVerbSet asserts the analyze verb set is derived from the real
// analyzers and contains impact + taint (the lock-step guarantee).
func TestAnalyzeVerbSet(t *testing.T) {
	set := analyzeVerbSet()
	for _, want := range []string{"impact", "taint"} {
		if !set[want] {
			t.Fatalf("analyzeVerbSet() missing %q; got %v", want, set)
		}
	}
}

// seedVerbStore builds a tiny in-process store (A calls B) reusing the parity
// fixture shape so verb-parity assertions exercise a real query/analyze path.
func seedVerbStore(t *testing.T) (*graphstore.MemStore, map[string]model.NodeId) {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	ids := map[string]model.NodeId{}
	nodes := map[string]model.Node{}
	for _, name := range []string{"A", "B", "C"} {
		n, err := model.NewNode("function", "p."+name, "p/"+name+".go", 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
		ids[name] = n.ID()
		nodes[name] = n
	}
	mk := func(from, to string, kind string) {
		e, err := model.NewEdge(nodes[from].ID(), nodes[to].ID(), kind, model.TierConfirmed, 1, "r", []string{"e"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	mk("A", "B", string(query.EdgeKindCalls))
	mk("B", "C", string(query.EdgeKindCalls))
	return store, ids
}

// TestVerbQueryParity asserts a real byte-equality contract: driving cli.Run
// with the SHORT-verb args rewritten by rewriteVerbArgs (minus the op token,
// which Run consumes as args[0]) produces output byte-identical to driving it
// with the long-form args. This proves `graphi callers X` == `graphi query
// callers -symbol X`.
func TestVerbQueryParity(t *testing.T) {
	store, ids := seedVerbStore(t)
	c := client.NewDirect(query.New(store), search.New(store))
	sym := string(ids["B"])

	// Long form: `query callers -symbol B` -> cli.Run sees [callers -symbol B].
	long := []string{"callers", "-symbol", sym}
	// Short form: `graphi callers B`. rewriteVerbArgs("callers", ["B"]) yields
	// the full long-form argv; cli.Run is driven with that same slice.
	short := rewriteVerbArgs("callers", []string{sym})

	if !bytes.Equal(runCLI(t, c, long), runCLI(t, c, short)) {
		t.Fatalf("query verb parity mismatch:\nlong:  %s\nshort: %s",
			runCLI(t, c, long), runCLI(t, c, short))
	}
}

// TestVerbAnalyzeParity is the analyze-side byte-equality contract for impact:
// `graphi impact X` == `graphi analyze impact -symbol X`.
func TestVerbAnalyzeParity(t *testing.T) {
	store, ids := seedVerbStore(t)
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	sym := string(ids["B"])

	long := []string{"impact", "-symbol", sym}
	short := rewriteVerbArgs("impact", []string{sym})

	if !bytes.Equal(runAnalysisCLI(t, c, long), runAnalysisCLI(t, c, short)) {
		t.Fatalf("analyze verb parity mismatch:\nlong:  %s\nshort: %s",
			runAnalysisCLI(t, c, long), runAnalysisCLI(t, c, short))
	}
}

func runCLI(t *testing.T, c client.Client, args []string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := cli.Run(context.Background(), c, args, &out, &errOut); err != nil {
		t.Fatalf("cli.Run(%v): %v (stderr: %s)", args, err, errOut.String())
	}
	return out.Bytes()
}

func runAnalysisCLI(t *testing.T, c client.Client, args []string) []byte {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := cli.RunAnalysis(context.Background(), c, args, &out, &errOut); err != nil {
		t.Fatalf("cli.RunAnalysis(%v): %v (stderr: %s)", args, err, errOut.String())
	}
	return out.Bytes()
}
