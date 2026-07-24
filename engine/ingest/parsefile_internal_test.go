package ingest

import (
	"context"
	"os"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis/intraproctaint"
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
	rootHandle, err := os.OpenRoot(repo)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	defer rootHandle.Close()
	byRel := make(map[string]*ParsedFile, len(units))
	for _, u := range units {
		pf, err := i.parseUnit(context.Background(), rootHandle, u)
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

// TestIngestAll_ReleasesGoASTAfterTaintAnalysis pins the full-pass memory
// contract for Go ASTs: the parse drain runs the per-file intra-proc taint
// analysis and then releases each Go result's Root, so no file's go/ast+
// FileSet survives past its own drain visit — and the findings computed
// before the release are byte-identical to analyzing a fresh parse of the
// same file.
func TestIngestAll_ReleasesGoASTAfterTaintAnalysis(t *testing.T) {
	repo := writeRepoIngest(t, map[string]string{
		"go.mod": "module demo\n\ngo 1.21\n",
		// The direct-SQLi shape from the vuln-go gate: tainted query param
		// concatenated into db.Query — guaranteed to yield an intra-proc
		// finding under the default config, so the test proves the analysis
		// ran BEFORE the AST was released.
		"app/handlers.go": `package app

import (
	"database/sql"
	"net/http"
)

func vulnSQLiDirect(db *sql.DB, w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	rows, _ := db.Query("SELECT * FROM users WHERE id = " + id)
	_ = rows
	_ = w
}
`,
		"app/util.py": "def util():\n    return 1\n",
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
	cfg, cfgErr := intraProcTaintConfig(repo)
	if cfgErr != nil {
		t.Fatalf("taint config: %v", cfgErr)
	}
	ctx := context.Background()
	parsed, err := i.parseUnitsParallel(ctx, repo, units, func(done, k int, pf *ParsedFile) {
		i.analyzeParsedTaint(cfg, pf)
	})
	if err != nil {
		t.Fatalf("parseUnitsParallel: %v", err)
	}

	var goFindings int
	for k, pf := range parsed {
		if pf == nil || pf.skipped || pf.result == nil {
			continue
		}
		if pf.result.Root != nil {
			t.Errorf("%s: Root must be released after the drain's taint analysis", units[k].relPath)
		}
		if units[k].relPath == "app/handlers.go" {
			goFindings = len(pf.taint)
			// Byte-parity: the findings computed before the release must equal
			// analyzing a fresh parse of the same file.
			fresh, err := i.ParseFile(ctx, repo, units[k].relPath)
			if err != nil || fresh == nil || fresh.result == nil {
				t.Fatalf("fresh ParseFile: pf=%v err=%v", fresh, err)
			}
			file, fset, ok := parse.GoAST(fresh.result)
			if !ok {
				t.Fatal("fresh parse lost the Go AST")
			}
			want := intraproctaint.Analyze(file, fset, cfg)
			gotEnc, err := intraproctaint.Encode(pf.taint)
			if err != nil {
				t.Fatalf("encode got: %v", err)
			}
			wantEnc, err := intraproctaint.Encode(want)
			if err != nil {
				t.Fatalf("encode want: %v", err)
			}
			if gotEnc != wantEnc {
				t.Errorf("drain-computed findings diverge from fresh analysis:\n got: %s\nwant: %s", gotEnc, wantEnc)
			}
		}
	}
	if goFindings == 0 {
		t.Fatal("expected at least one intra-proc finding for the planted SQLi flow — the analysis must run before the AST release")
	}
}
