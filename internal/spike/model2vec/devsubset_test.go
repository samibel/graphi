package model2vec

// AC-5: the SW-258 development subset, scored by the SW-258 scorer over
// rankings produced by THIS spike's embedder with brute-force cosine, twice
// per repository: over the product's NodeText v1 documents (kind + qualified
// name — what semantic_name_only embeds) and over a body+doc text for the
// same nodes (doc comment + declaration source, built from go/ast — a
// stand-in for the SemanticDocument v2 SW-260 will define). These are SPIKE
// numbers, not product numbers: no engine seam ran, no fusion, no index.
//
// Two repositories: the hermetic fixture (always, when the artifact is
// present) and cobra at its SW-258 pin (when the read-only checkout SW-258's
// tests also look for is present). The reports they sit beside are
// docs/eval/retrieval/runs/2026-08-30-local/{fixture,cobra}-v1-report.json.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/eval/retrieval"
)

const (
	retrievalTestdata = "../../eval/retrieval/testdata"
	fixtureRepo       = retrievalTestdata + "/fixture-repo"
	fixtureDataset    = retrievalTestdata + "/datasets/fixture-v1.json"
	cobraDataset      = retrievalTestdata + "/datasets/cobra-v1.json"
	// cobraPinnedSHA is corpus/manifest.json's pin (internal/eval/retrieval/datasets_test.go).
	cobraPinnedSHA = "a0a6ae020bb3899ff0276067863e50523f897370"
)

func TestSpikeDevSubset_Fixture(t *testing.T) {
	runDevSubset(t, fixtureRepo, fixtureDataset)
}

func TestSpikeDevSubset_Cobra(t *testing.T) {
	root := os.Getenv("GRAPHI_CORPUS_COBRA")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory")
		}
		root = filepath.Join(home, ".cache", "graphi", "corpus", "cobra")
	}
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		t.Skipf("SKIP: cobra checkout absent at %s (set GRAPHI_CORPUS_COBRA)", root)
	}
	head, err := retrieval.CheckoutHEAD(context.Background(), root)
	if err != nil {
		t.Skipf("SKIP: cobra checkout at %s: %v", root, err)
	}
	if head != cobraPinnedSHA {
		t.Skipf("SKIP: cobra checkout at %s is %s, dataset pins %s", root, head, cobraPinnedSHA)
	}
	runDevSubset(t, root, cobraDataset)
}

// docVariant names one document text per node.
type docVariant struct {
	name string
	text func(n model.Node) string
}

func runDevSubset(t *testing.T, root, datasetPath string) {
	t.Helper()
	m := loadPinnedModel(t)
	ds, err := retrieval.LoadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := retrieval.CheckSpanCoverage(root, ds.Dataset); err != nil {
		t.Fatal(err)
	}
	nodes := indexRepo(t, root)
	bodyDoc, covered := bodyDocTexts(t, root, nodes)
	t.Logf("%s: %d nodes; body+doc text available for %d of them (the rest fall back to NodeText v1)", ds.Dataset.ID, len(nodes), covered)

	variants := []docVariant{
		{"name_only (NodeText v1)", embed.NodeText},
		{"body+doc (go/ast doc comment + declaration)", func(n model.Node) string {
			if s, ok := bodyDoc[string(n.ID())]; ok {
				return s
			}
			return embed.NodeText(n)
		}},
	}
	minGrade := ds.Dataset.MinGrade()
	for _, v := range variants {
		texts := make([]string, len(nodes))
		for i, n := range nodes {
			texts[i] = v.text(n)
		}
		docVecs := m.Embed(texts)
		var results []retrieval.QueryResult
		for _, q := range ds.Dataset.Queries {
			hits := rank(m, q.Text, nodes, docVecs, retrieval.TopK)
			results = append(results, retrieval.QueryResult{
				ID: q.ID, Stratum: q.Stratum, Split: q.Split, Hits: hits,
				Metrics: retrieval.Evaluate(hits, q, minGrade, retrieval.TokenBudgets),
			})
		}
		overall, strata, splits := retrieval.AggregateAll(results, retrieval.TokenBudgets)
		t.Logf("=== spike numbers: %s over %s ===", v.name, ds.Dataset.ID)
		t.Logf("%-20s %s", "overall", fmtAgg(overall))
		for _, s := range []string{retrieval.SplitDev, retrieval.SplitHoldout} {
			t.Logf("%-20s %s", "split "+s, fmtAgg(splits[s]))
		}
		devByStratum := map[string][]retrieval.QueryResult{}
		for _, r := range results {
			if r.Split == retrieval.SplitDev {
				devByStratum[r.Stratum] = append(devByStratum[r.Stratum], r)
			}
		}
		for _, s := range retrieval.Strata {
			t.Logf("%-20s %s", s, fmtAgg(strata[s]))
			t.Logf("%-20s %s", "  dev only", fmtAgg(retrieval.Aggregate(devByStratum[s], retrieval.TokenBudgets)))
		}
		for _, r := range results {
			first := "none"
			if r.Metrics.FirstRelevantRank != nil {
				first = fmt.Sprint(*r.Metrics.FirstRelevantRank)
			}
			top := ""
			if len(r.Hits) > 0 {
				top = fmt.Sprintf("%s %s @%s:%d", r.Hits[0].Kind, r.Hits[0].QualifiedName, r.Hits[0].Path, r.Hits[0].Line)
			}
			t.Logf("  %s [%s/%s] first relevant rank %s; top hit: %s", r.ID, r.Stratum, r.Split, first, top)
		}
	}
}

func fmtAgg(a retrieval.AggregateMetrics) string {
	if a.Status != retrieval.StatusMeasured {
		return fmt.Sprintf("queries=%d scored=%d %s", a.Queries, a.Scored, a.Status)
	}
	s := fmt.Sprintf("queries=%d scored=%d", a.Queries, a.Scored)
	for _, k := range []string{retrieval.MetricRecall10, retrieval.MetricMRR10, retrieval.MetricNDCG10, retrieval.MetricTop1, retrieval.MetricRecall5, retrieval.MetricNegativeHitRate5} {
		if v, ok := a.Metrics[k]; ok {
			s += fmt.Sprintf(" %s=%.4f", k, v)
		}
	}
	return s
}

// indexRepo ingests root the way internal/eval/retrieval does and returns
// its nodes in canonical id order.
func indexRepo(t *testing.T, root string) []model.Node {
	t.Helper()
	ctx := context.Background()
	work := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(work, "spike.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), filepath.Join(work, "meta"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatal(err)
	}
	if err := ing.Close(); err != nil {
		t.Fatal(err)
	}
	var nodes []model.Node
	if err := graphstore.ForEachNode(ctx, store, func(n model.Node) error {
		nodes = append(nodes, n)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID() < nodes[j].ID() })
	if len(nodes) == 0 {
		t.Fatalf("index of %s has no nodes", root)
	}
	return nodes
}

// bodyDocTexts builds the body+doc text per node id from go/ast: for a
// declaration at the node's (path, line), the NodeText v1 line, the doc
// comment and the declaration's source; for a file node, its path and the
// file's package doc. Returns the map and how many nodes it covers.
func bodyDocTexts(t *testing.T, root string, nodes []model.Node) (map[string]string, int) {
	t.Helper()
	byPos := map[string]string{} // "path:line" → text
	fileDoc := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); p != root && (name == ".git" || name == "vendor" || name == "node_modules" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, p, src, parser.ParseComments)
		if err != nil {
			return nil // the ingest walk may have skipped it too; a file without decls carries nothing here
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if f.Doc != nil {
			fileDoc[rel] = f.Doc.Text()
		}
		text := func(doc *ast.CommentGroup, from, to token.Pos) string {
			var b strings.Builder
			if doc != nil {
				b.WriteString(doc.Text())
			}
			b.Write(src[fset.Position(from).Offset:fset.Position(to).Offset])
			return b.String()
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				line := fset.Position(d.Name.Pos()).Line
				byPos[fmt.Sprintf("%s:%d", rel, line)] = text(d.Doc, d.Pos(), d.End())
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					var names []*ast.Ident
					var doc *ast.CommentGroup
					switch s := spec.(type) {
					case *ast.TypeSpec:
						names, doc = []*ast.Ident{s.Name}, s.Doc
					case *ast.ValueSpec:
						names, doc = s.Names, s.Doc
					default:
						continue
					}
					if doc == nil && len(d.Specs) == 1 {
						doc = d.Doc
					}
					from, to := spec.Pos(), spec.End()
					if len(d.Specs) == 1 {
						from, to = d.Pos(), d.End()
					}
					for _, name := range names {
						line := fset.Position(name.Pos()).Line
						byPos[fmt.Sprintf("%s:%d", rel, line)] = text(doc, from, to)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, n := range nodes {
		if n.Kind() == "file" {
			if doc, ok := fileDoc[n.SourcePath()]; ok {
				out[string(n.ID())] = "file " + n.SourcePath() + "\n" + doc
			}
			continue
		}
		if s, ok := byPos[fmt.Sprintf("%s:%d", n.SourcePath(), n.Line())]; ok {
			out[string(n.ID())] = embed.NodeText(n) + "\n" + s
		}
	}
	return out, len(out)
}

// rank scores every node by cosine (dot product of the normalised vectors,
// accumulated in float64) and returns the top-k as scorer hits; ties break on
// (path, line, id) so the ranking is deterministic.
func rank(m *Model, query string, nodes []model.Node, docVecs [][]float32, k int) []retrieval.Hit {
	qv := m.Embed([]string{query})[0]
	type scored struct {
		i     int
		score float64
	}
	all := make([]scored, len(nodes))
	for i, dv := range docVecs {
		var dot float64
		for j := range dv {
			dot += float64(dv[j]) * float64(qv[j])
		}
		all[i] = scored{i, dot}
	}
	sort.SliceStable(all, func(a, b int) bool {
		if all[a].score != all[b].score {
			return all[a].score > all[b].score
		}
		na, nb := nodes[all[a].i], nodes[all[b].i]
		if na.SourcePath() != nb.SourcePath() {
			return na.SourcePath() < nb.SourcePath()
		}
		if na.Line() != nb.Line() {
			return na.Line() < nb.Line()
		}
		return na.ID() < nb.ID()
	})
	var hits []retrieval.Hit
	for r, s := range all[:min(k, len(all))] {
		n := nodes[s.i]
		hits = append(hits, retrieval.Hit{Rank: r + 1, Path: n.SourcePath(), Line: n.Line(), NodeID: string(n.ID()), Kind: n.Kind(), QualifiedName: n.QualifiedName()})
	}
	return hits
}
