package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
)

// --- notebook fixture builder -------------------------------------------------

// nbCellIn is a compact description of one nbformat cell for the fixture builder.
type nbCellIn struct {
	Type   string          // "code" | "markdown" | "raw" | (anything for unknown)
	Source string          // verbatim source; ignored when Raw is set
	Lang   string          // optional per-cell metadata.vscode.languageId override
	ID     string          // optional nbformat >=4.5 cell id
	Raw    json.RawMessage // when non-nil, used verbatim as the "source" field
	NoSrc  bool            // when true, omit the "source" field entirely
}

// buildNotebook serializes an nbformat document with the given kernel language and
// cells. Input determinism is irrelevant (only parse OUTPUT must be deterministic);
// the builder simply produces valid nbformat JSON.
func buildNotebook(t *testing.T, kernelLang string, cells []nbCellIn) []byte {
	t.Helper()
	cellList := make([]map[string]any, 0, len(cells))
	for _, c := range cells {
		m := map[string]any{"cell_type": c.Type}
		if !c.NoSrc {
			if c.Raw != nil {
				m["source"] = c.Raw
			} else {
				m["source"] = c.Source
			}
		}
		if c.ID != "" {
			m["id"] = c.ID
		}
		if c.Lang != "" {
			m["metadata"] = map[string]any{"vscode": map[string]any{"languageId": c.Lang}}
		}
		cellList = append(cellList, m)
	}
	doc := map[string]any{
		"cells": cellList,
		"metadata": map[string]any{
			"kernelspec": map[string]any{"language": kernelLang},
		},
		"nbformat":       4,
		"nbformat_minor": 5,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("build notebook: %v", err)
	}
	return b
}

// nbParser is a notebook-aware parser over the default registry.
func nbParser() ingest.Parser {
	return ingest.NewNotebookParser(parse.NewDefaultRegistry())
}

// symbolSet returns {qualifiedName: kind} for every non-file node.
func symbolSet(nodes []model.Node) map[string]string {
	out := make(map[string]string)
	for _, n := range nodes {
		if n.Kind() == parse.KindFile {
			continue
		}
		out[n.QualifiedName()] = string(n.Kind())
	}
	return out
}

// nbSnapshotBytes ingests files (full) through the notebook-aware parser and
// returns the store snapshot bytes.
func nbSnapshotBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	repo := writeRepo(t, files)
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, nbParser())
	if err := ing.IngestAll(context.Background(), repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	snap := filepath.Join(t.TempDir(), "s")
	if err := store.Snapshot(context.Background(), snap); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	b, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	return b
}

// cellProv is the parsed (cell index, cell id, intra-cell line) provenance
// recovered from a notebook-cell provenance edge's evidence string.
type cellProv struct {
	index int
	id    string
	line  int
	found bool
}

// parseCellProv decodes the canonical evidence string
// "<path>#cell=<index>[;id=<id>];line=<intraLine>" emitted by the adapter.
func parseCellProv(t *testing.T, ev string) cellProv {
	t.Helper()
	hash := strings.LastIndex(ev, "#cell=")
	if hash < 0 {
		t.Fatalf("evidence %q missing #cell= segment", ev)
	}
	cp := cellProv{found: true, index: -1, line: -1}
	for _, kv := range strings.Split(ev[hash+1:], ";") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		switch k {
		case "cell":
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("bad cell index in %q: %v", ev, err)
			}
			cp.index = n
		case "id":
			cp.id = v
		case "line":
			n, err := strconv.Atoi(v)
			if err != nil {
				t.Fatalf("bad line in %q: %v", ev, err)
			}
			cp.line = n
		}
	}
	return cp
}

// nbCommitProvenance ingests files (full) through the notebook-aware parser into a
// fresh store and returns, per symbol qualified name, its committed node plus the
// cell provenance recovered from the committed notebook-cell edge anchored on the
// notebook file node. This exercises the SAME production Parse/IngestAll path the
// product runs — provenance is asserted on the committed graph, not a test-only
// return value.
func nbCommitProvenance(t *testing.T, files map[string]string) (map[string]model.Node, map[string]cellProv) {
	t.Helper()
	ctx := context.Background()
	repo := writeRepo(t, files)
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, nbParser())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	byID := map[model.NodeId]model.Node{}
	byQN := map[string]model.Node{}
	for _, n := range nodes {
		byID[n.ID()] = n
		byQN[n.QualifiedName()] = n
	}
	edges, err := store.Edges(ctx, graphstore.Query{EdgeKind: ingest.NotebookCellEdgeKind})
	if err != nil {
		t.Fatalf("Edges: %v", err)
	}
	provByQN := map[string]cellProv{}
	for _, e := range edges {
		ev := e.Evidence()
		if len(ev) != 1 {
			t.Fatalf("cell-provenance edge wants exactly 1 evidence, got %v", ev)
		}
		from, ok := byID[e.From()]
		if !ok {
			t.Fatalf("provenance edge from unknown node %s", e.From())
		}
		provByQN[from.QualifiedName()] = parseCellProv(t, ev[0])
	}
	return byQN, provByQN
}

// --- AC1: python-equivalence + cell tagging on the COMMITTED graph ------------

func TestNotebook_PythonEquivalence(t *testing.T) {
	ctx := context.Background()
	// Two code cells: imports+class in cell 0, function in cell 2 (a markdown cell
	// sits between them to exercise stable indexing). Both end in a newline so the
	// fixed concat policy reproduces the equivalent .py byte-for-byte.
	cell0 := "import os\n\nclass Store:\n    def checkout(self):\n        return 1\n"
	cell1 := "TAX = 7\n\ndef price(c):\n    return TAX\n"

	nb := buildNotebook(t, "python", []nbCellIn{
		{Type: "code", Source: cell0, ID: "c0"},
		{Type: "markdown", Source: "# heading\n"},
		{Type: "code", Source: cell1, ID: "c1"},
	})

	// Symbol-set equivalence vs the equivalent .py (production Parse path).
	p := ingest.NewNotebookParser(parse.NewDefaultRegistry())
	res, err := p.Parse(ctx, "pkg/analysis.ipynb", nb)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	reg := parse.NewDefaultRegistry()
	pyRes, err := reg.Parse(ctx, "pkg/equiv.py", []byte(cell0+cell1))
	if err != nil {
		t.Fatalf("py parse: %v", err)
	}
	got := symbolSet(res.Nodes)
	want := symbolSet(pyRes.Nodes)
	if len(want) == 0 {
		t.Fatal("equivalent .py produced no symbols (fixture broken)")
	}
	for qn, kind := range want {
		if got[qn] != kind {
			t.Errorf("notebook symbol %q kind = %q, want %q", qn, got[qn], kind)
		}
	}
	for qn := range got {
		if _, ok := want[qn]; !ok {
			t.Errorf("notebook produced extra symbol %q not in .py equivalent", qn)
		}
	}
	var fileNodes int
	for _, n := range res.Nodes {
		if n.Kind() == parse.KindFile {
			fileNodes++
			if n.QualifiedName() != "pkg/analysis.ipynb" {
				t.Errorf("file node QN = %q, want notebook path", n.QualifiedName())
			}
		}
	}
	if fileNodes != 1 {
		t.Errorf("want exactly 1 notebook file node, got %d", fileNodes)
	}

	// AC1 core: every symbol is tagged in the COMMITTED graph with its owning cell
	// (index AND id) plus the intra-cell line. checkout came from cell index 0
	// (id c0, intra line 4), price from cell index 2 (id c1, intra line 3) — indices
	// are stable across the interleaved markdown cell.
	byQN, prov := nbCommitProvenance(t, map[string]string{"pkg/analysis.ipynb": string(nb)})

	checkProv := func(qn string, wantIdx int, wantID string, wantLine int) {
		t.Helper()
		cp, ok := prov[qn]
		if !ok {
			t.Fatalf("symbol %q has no committed cell-provenance edge", qn)
		}
		if cp.index != wantIdx || cp.id != wantID {
			t.Errorf("%s provenance = (index %d, id %q), want (index %d, id %q)", qn, cp.index, cp.id, wantIdx, wantID)
		}
		if cp.line != wantLine {
			t.Errorf("%s provenance intra-cell line = %d, want %d", qn, cp.line, wantLine)
		}
		// The committed symbol node's Line is the same intra-cell line (not the
		// synthetic-buffer line) — the (cell id, intra-cell line) pair is coherent.
		if n, ok := byQN[qn]; ok && n.Line() != wantLine {
			t.Errorf("%s committed node line = %d, want intra-cell %d", qn, n.Line(), wantLine)
		}
	}
	checkProv("pkg.checkout", 0, "c0", 4)
	checkProv("pkg.price", 2, "c1", 3)
}

// --- AC2: markdown/raw interleave, no symbols, stable indices in the graph ----

func TestNotebook_NonCodeCellsInterleave(t *testing.T) {
	nb := buildNotebook(t, "python", []nbCellIn{
		{Type: "markdown", Source: "# title\n"},
		{Type: "code", Source: "def a():\n    return 1\n", ID: "a"},
		{Type: "raw", Source: "raw text\n"},
		{Type: "code", Source: "def b():\n    return 2\n", ID: "b"},
	})
	byQN, prov := nbCommitProvenance(t, map[string]string{"pkg/nb.ipynb": string(nb)})

	if _, ok := byQN["pkg.a"]; !ok {
		t.Errorf("missing symbol pkg.a; got %v", byQN)
	}
	if _, ok := byQN["pkg.b"]; !ok {
		t.Errorf("missing symbol pkg.b; got %v", byQN)
	}
	// markdown/raw contribute no symbols: exactly the two functions carry cell
	// provenance in the committed graph.
	if len(prov) != 2 {
		t.Errorf("want 2 cell-tagged symbols (markdown/raw leak none), got %d: %v", len(prov), prov)
	}
	// Code-cell indices remain stable despite interleaving (1 and 3).
	if cp := prov["pkg.a"]; cp.index != 1 || cp.id != "a" {
		t.Errorf("pkg.a provenance = (index %d, id %q), want (1, a)", cp.index, cp.id)
	}
	if cp := prov["pkg.b"]; cp.index != 3 || cp.id != "b" {
		t.Errorf("pkg.b provenance = (index %d, id %q), want (3, b)", cp.index, cp.id)
	}
}

// --- AC3: graceful degradation (four cases) -----------------------------------

func TestNotebook_GracefulDegradation(t *testing.T) {
	ctx := context.Background()
	p := ingest.NewNotebookParser(parse.NewDefaultRegistry())

	// hasOnlyFileNode asserts the result is non-nil, error-free and carries the
	// notebook file node (file still registered) and no symbols.
	hasOnlyFileNode := func(t *testing.T, path string, src []byte) {
		t.Helper()
		res, err := p.Parse(ctx, path, src)
		if err != nil {
			t.Fatalf("Parse returned error (must degrade): %v", err)
		}
		if res == nil {
			t.Fatal("nil result")
		}
		if res.Meta.Path != path {
			t.Errorf("meta path = %q, want %q", res.Meta.Path, path)
		}
		var file, syms int
		for _, n := range res.Nodes {
			if n.Kind() == parse.KindFile {
				file++
			} else {
				syms++
			}
		}
		if file != 1 {
			t.Errorf("want 1 file node, got %d", file)
		}
		if syms != 0 {
			t.Errorf("want 0 symbols on degraded notebook, got %d", syms)
		}
	}

	// (a) invalid JSON.
	hasOnlyFileNode(t, "pkg/bad.ipynb", []byte("{not valid json"))

	// (b) unknown kernel language (no registered parser).
	unknownKernel := buildNotebook(t, "brainfuck", []nbCellIn{
		{Type: "code", Source: "+++."},
	})
	hasOnlyFileNode(t, "pkg/unknown.ipynb", unknownKernel)

	// (c) missing source + unknown cell_type degrade per-cell to no-op, file still
	// registered. (A code cell with no source and an unknown cell type.)
	mixed := buildNotebook(t, "python", []nbCellIn{
		{Type: "code", NoSrc: true},
		{Type: "weird", Source: "whatever\n"},
	})
	hasOnlyFileNode(t, "pkg/mixed.ipynb", mixed)

	// (d) a valid notebook where one cell has a missing source but another is fine:
	// the bad cell is skipped, the good cell still yields a symbol.
	partial := buildNotebook(t, "python", []nbCellIn{
		{Type: "code", NoSrc: true},
		{Type: "code", Source: "def ok():\n    return 1\n"},
	})
	res, err := p.Parse(ctx, "pkg/partial.ipynb", partial)
	if err != nil {
		t.Fatalf("partial parse error: %v", err)
	}
	if _, ok := symbolSet(res.Nodes)["pkg.ok"]; !ok {
		t.Errorf("good cell symbol pkg.ok missing after sibling degraded: %v", symbolSet(res.Nodes))
	}
}

// --- AC4: determinism (ingest twice -> byte-identical) ------------------------

func TestNotebook_DeterministicTwice(t *testing.T) {
	files := map[string]string{
		"pkg/analysis.ipynb": string(buildNotebook(t, "python", []nbCellIn{
			{Type: "code", Source: "import os\n\ndef helper():\n    return 1\n", ID: "h"},
			{Type: "markdown", Source: "# notes\n"},
			{Type: "code", Source: "def main():\n    return helper()\n", ID: "m"},
		})),
		"pkg/util.py": "def aux():\n    return 0\n",
	}
	a := nbSnapshotBytes(t, files)
	b := nbSnapshotBytes(t, files)
	if !bytes.Equal(a, b) {
		t.Fatalf("notebook ingest not deterministic:\nA=%s\nB=%s", a, b)
	}
}

// --- AC5: full-vs-incremental byte parity with a notebook in the corpus -------

func TestNotebook_IncrementalVsFull(t *testing.T) {
	ctx := context.Background()
	nbSrc := string(buildNotebook(t, "python", []nbCellIn{
		{Type: "code", Source: "import os\n\ndef helper():\n    return 1\n", ID: "h"},
		{Type: "markdown", Source: "# notes\n"},
		{Type: "code", Source: "def main():\n    return helper()\n", ID: "m"},
	}))

	initial := map[string]string{
		"pkg/analysis.ipynb": nbSrc,
		"pkg/util.py":        "def aux():\n    return 0\n",
	}

	storeInc := graphstore.NewMemStore()
	t.Cleanup(func() { _ = storeInc.Close() })
	iInc := newIngester(t, storeInc, nbParser())
	repo := writeRepo(t, initial)
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}
	// Mutate a sibling source file; the notebook is unchanged.
	mustWrite(t, repo, "pkg/util.py", "def aux():\n    return 42\n")
	if err := iInc.IngestChanged(ctx, repo, []string{"pkg/util.py"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	incSnap := filepath.Join(t.TempDir(), "inc")
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	incBytes, _ := os.ReadFile(incSnap)

	mutated := map[string]string{
		"pkg/analysis.ipynb": nbSrc,
		"pkg/util.py":        "def aux():\n    return 42\n",
	}
	fullBytes := nbSnapshotBytes(t, mutated)

	if !bytes.Equal(incBytes, fullBytes) {
		t.Fatalf("notebook incremental != full (byte-level):\ninc =%s\nfull=%s", incBytes, fullBytes)
	}
}

// --- AC6: decorator transparency (layer/CGo/egress structurally enforced by the
// repo-wide internal/layerguard + core/parse purity guards run under `go test
// ./...`; here we prove the decorator is transparent for non-.ipynb input). -----

func TestNotebook_DelegatesNonNotebook(t *testing.T) {
	ctx := context.Background()
	src := []byte("def f():\n    return 1\n")
	reg := parse.NewDefaultRegistry()
	direct, err := reg.Parse(ctx, "pkg/x.py", src)
	if err != nil {
		t.Fatalf("direct: %v", err)
	}
	deco, err := ingest.NewNotebookParser(parse.NewDefaultRegistry()).Parse(ctx, "pkg/x.py", src)
	if err != nil {
		t.Fatalf("decorated: %v", err)
	}
	if d, n := symbolSet(direct.Nodes), symbolSet(deco.Nodes); len(d) != len(n) {
		t.Fatalf("decorator altered non-notebook symbols: direct=%v deco=%v", d, n)
	} else {
		for qn, k := range d {
			if n[qn] != k {
				t.Errorf("decorator changed %q: %q vs %q", qn, k, n[qn])
			}
		}
	}
}

// --- AC7: cross-cell references resolve within the notebook -------------------

func TestNotebook_CrossCellRefs(t *testing.T) {
	ctx := context.Background()
	nbSrc := string(buildNotebook(t, "python", []nbCellIn{
		{Type: "code", Source: "def helper():\n    return 1\n", ID: "h"},
		{Type: "markdown", Source: "# between\n"},
		{Type: "code", Source: "def main():\n    return helper()\n", ID: "m"},
	}))
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing := newIngester(t, store, nbParser())
	repo := writeRepo(t, map[string]string{"pkg/analysis.ipynb": nbSrc})
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	id := func(qn string) model.NodeId {
		for _, n := range nodes {
			if n.QualifiedName() == qn {
				return n.ID()
			}
		}
		t.Fatalf("symbol %q not found among %d nodes", qn, len(nodes))
		return ""
	}

	svc := query.New(store)
	callees, err := svc.Callees(ctx, id("pkg.main"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsNode(callees, id("pkg.helper")) {
		t.Errorf("cross-cell call main->helper did not resolve: %+v", callees.Nodes)
	}
}
