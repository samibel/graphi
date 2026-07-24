package ingest

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// TestParseUnit_ReleasesNonGoASTAfterExtraction pins the full-pass memory
// contract: parseUnit must drop the backend AST handle (Root) for every
// non-Go file once extraction has produced the graph elements, and must keep
// the Go AST (the taint pass reads it via parse.GoAST). Without the release,
// the parallel parse phase retains every file's tree-sitter tree — routinely
// 10-40x the source size — until the end of the whole ingest pipeline, which
// on large polyglot workspaces reached tens of GB of peak RSS.
func TestParseUnit_ReleasesNonGoASTAfterExtraction(t *testing.T) {
	repo := writeRepoIngest(t, map[string]string{
		"app/util.py": "def util():\n    return 1\n",
		"shop/cart.go": `package shop
func checkout() int { return 1 }
`,
	})
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	i, err := New(store, NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	units, err := i.walk(repo, nil)
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	byRel := make(map[string]*ParsedFile, len(units))
	for _, u := range units {
		pf, err := i.parseUnit(context.Background(), u)
		if err != nil {
			t.Fatalf("parseUnit %s: %v", u.relPath, err)
		}
		byRel[u.relPath] = pf
	}

	py := byRel["app/util.py"]
	if py == nil || py.skipped || py.result == nil {
		t.Fatalf("python file was not parsed: %+v", py)
	}
	if py.result.Root != nil {
		t.Fatal("non-Go Root must be released after extraction; the parse pool otherwise retains every tree until the end of the pass")
	}
	if len(py.result.Nodes) == 0 {
		t.Fatal("releasing Root must not lose the extracted nodes")
	}

	goPf := byRel["shop/cart.go"]
	if goPf == nil || goPf.skipped || goPf.result == nil {
		t.Fatalf("go file was not parsed: %+v", goPf)
	}
	if _, _, ok := parse.GoAST(goPf.result); !ok {
		t.Fatal("the Go AST must be retained: the intra-proc taint pass consumes it via parse.GoAST")
	}
}
