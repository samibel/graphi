// Command differential re-runs the SW-263 retrieval module against the
// pinned cobra corpus for every dev query, captures the per-row
// explain breakdown (lexical rank, semantic rank, individual RRF
// contributions, post-diversification rank, classification, graph,
// final score), and writes a JSON file at <new-run-dir>/differential.json
// alongside the eval report. The diff vs the previous run is reported
// per query and per stratum.
//
// One-shot tool, run manually after the eval:
//
//	go run ./cmd/differential -repo <path> -dataset <path> -new <new-run-dir>
//
// The retrieval module's NewGraphReader is used to wire the graph
// reader (defect 3 fix) so the new run reflects the corrected
// implementation end to end.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/embed"
	_ "github.com/samibel/graphi/engine/embed/ollama"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
	evalretrieval "github.com/samibel/graphi/internal/eval/retrieval"
)

const embedderSelector = "ollama"

func main() {
	repoDir := flag.String("repo", "", "path to the cobra checkout")
	datasetPath := flag.String("dataset", "internal/eval/retrieval/testdata/datasets/cobra-v1.json", "dataset path")
	newRun := flag.String("new", "", "new run directory (writes differential.json here)")
	prevRun := flag.String("prev", "", "previous run directory (reads hits-fusion.json and hits-fusion+graph.json from raw/)")
	flag.Parse()
	if *repoDir == "" || *newRun == "" || *datasetPath == "" {
		fmt.Fprintln(os.Stderr, "usage: differential -repo <dir> -dataset <file> -new <dir> [-prev <dir>]")
		os.Exit(2)
	}
	if err := run(*repoDir, *datasetPath, *newRun, *prevRun, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "differential:", err)
		os.Exit(1)
	}
}

type perRowExplain struct {
	Rank           int    `json:"rank"`
	NodeID         string `json:"node_id"`
	Path           string `json:"path"`
	QualifiedName  string `json:"qualified_name"`
	LexicalRank    int    `json:"lexical_rank"`
	SemanticRank   int    `json:"semantic_rank"`
	LexicalRRF     int    `json:"lexical_rrf"`
	SemanticRRF    int    `json:"semantic_rrf"`
	RRF            int    `json:"rrf"`
	Graph          int    `json:"graph"`
	Classification int    `json:"classification"`
	Final          int    `json:"final"`
}

type queryDiff struct {
	ID                string             `json:"id"`
	Stratum           string             `json:"stratum"`
	Top1              float64            `json:"top1_new"`
	NDCG10            float64            `json:"ndcg10_new"`
	Rows              []perRowExplain    `json:"rows"`
	PrevFusion        []string           `json:"prev_fusion_node_ids"`
	PrevFG            []string           `json:"prev_fusion_graph_node_ids"`
	NowFusion         []string           `json:"new_fusion_node_ids"`
	NowFG             []string           `json:"new_fusion_graph_node_ids"`
	PrevFusionMetrics map[string]float64 `json:"prev_fusion_metrics"`
	PrevFGMetrics     map[string]float64 `json:"prev_fusion_graph_metrics"`

	// NoDiversifyNDCG10 / NoDiversifyTop1 / NoDiversifyNodeIDs are the
	// AC-5 attribution diagnostic: the SAME fused row set, scored in the
	// order it had BEFORE the MaxPerFile demotion. It is reconstructed
	// without touching the retrieval module — Retrieve is asked for a
	// Limit deep enough to include every demoted row (diversify demotes,
	// never drops), and the returned rows are re-sorted by the very key
	// the rerank stage sorted them with (Explain.Final desc, node_id asc),
	// which is exactly the pre-diversification order. The delta against
	// NDCG10 is therefore the cost of AC-5's path cap alone, with every
	// other stage held fixed.
	NoDiversifyNDCG10  float64  `json:"no_diversify_ndcg10"`
	NoDiversifyTop1    float64  `json:"no_diversify_top1"`
	NoDiversifyNodeIDs []string `json:"no_diversify_node_ids"`
}

type stratumSummary struct {
	Stratum     string  `json:"stratum"`
	Queries     int     `json:"queries"`
	NewFusion   float64 `json:"new_fusion_ndcg10"`
	PrevFusion  float64 `json:"prev_fusion_ndcg10"`
	DeltaFusion float64 `json:"delta_fusion_ndcg10"`
	NewFG       float64 `json:"new_fusion_graph_ndcg10"`
	PrevFG      float64 `json:"prev_fusion_graph_ndcg10"`
	DeltaFG     float64 `json:"delta_fusion_graph_ndcg10"`

	// NoDiversify is the same stratum's mean ndcg@10 for the fused row
	// set scored before AC-5's MaxPerFile demotion; DeltaDiversify is
	// NewFusion - NoDiversify, i.e. what the path cap costs (negative
	// when the cap helps).
	NoDiversify    float64 `json:"no_diversify_ndcg10"`
	DeltaDiversify float64 `json:"delta_cost_of_diversify_ndcg10"`
}

func run(repoDir, datasetPath, newRunDir, prevRunDir string, w io.Writer) error {
	ctx := context.Background()

	workDir, err := os.MkdirTemp("", "graphi-diff-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	store, err := graphstore.OpenSQLite(filepath.Join(workDir, "diff.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), filepath.Join(workDir, "meta"))
	if err != nil {
		return err
	}
	if err := ing.IngestAll(ctx, repoDir); err != nil {
		return err
	}
	if err := ing.Close(); err != nil {
		return err
	}

	emb, err := embed.Constructor(embedderSelector, embed.DefaultConstructors())
	if err != nil || emb == nil {
		return fmt.Errorf("embedder %s: %v", embedderSelector, err)
	}
	reg := embed.NewRegistry()
	if err := reg.Register(emb); err != nil {
		return err
	}
	reg.Freeze()
	index := embed.NewIndex()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return err
	}
	gsDir := filepath.Join(workDir, "gs")
	if err := os.MkdirAll(gsDir, 0o755); err != nil {
		return err
	}
	genStore, err := embed.OpenSQLiteGenerationStore(ctx, gsDir)
	if err != nil {
		return err
	}
	if _, err := embed.GenerateAndPersist(ctx, reg, nodes, embed.V1DocumentSource{}, index, genStore, embed.GraphGenerationPlaceholder); err != nil {
		_ = genStore.Close()
		return err
	}
	_ = genStore.Close()

	reloadStore, err := embed.OpenSQLiteGenerationStore(ctx, gsDir)
	if err != nil {
		return err
	}
	defer func() { _ = reloadStore.Close() }()
	fp := embed.Fingerprint{ModelID: emb.ID(), Dim: emb.Dim(), DocumentSchema: embed.DocumentSchema, GraphGeneration: embed.GraphGenerationPlaceholder}
	gen, _, err := reloadStore.Active(ctx, fp, nil)
	if err != nil || gen.ID == "" {
		return fmt.Errorf("active generation: %v", err)
	}
	rows, err := reloadStore.Load(ctx, gen.ID)
	if err != nil {
		return err
	}
	vecs := make([]embed.Vector, len(rows))
	for i, r := range rows {
		vecs[i] = embed.Vector{NodeID: r.NodeID, DocumentID: r.DocumentID, Values: r.Vector}
	}
	if err := index.Rebuild(ctx, vecs); err != nil {
		return err
	}
	svc := search.New(store).WithSemantic(reg, index, store).
		WithSemanticState(search.SemanticState{State: embed.StateReady})
	fmt.Fprintln(w, "differential: indexed", len(nodes), "nodes,", len(vecs), "vectors, embedder", emb.ID())

	ds, err := loadDataset(datasetPath)
	if err != nil {
		return err
	}

	deps := resolve.Deps{Query: query.New(store), Search: svc}
	var graphR graphstore.BoundedGraphLookup
	if bg, ok := any(store).(graphstore.BoundedGraphLookup); ok {
		graphR = bg
	}
	r := retrieval.New(deps, svc, graphR)
	probe, err := r.Retrieve(ctx, retrieval.Request{Limit: 1, Mode: retrieval.ModeFusionNoGraph})
	if err != nil {
		return err
	}
	// A lexical top-k plus semantic top-k union contains at most twice the
	// candidate depth. Read that depth from the public Summary rather than
	// depending on retrieval's private arithmetic constants (AC-1).
	deepLimit := 2 * probe.Summary.CandidateK

	var prevHitsFusion, prevHitsFG map[string][]string
	if prevRunDir != "" {
		prevHitsFusion, err = loadHits(filepath.Join(prevRunDir, "raw", "hits-fusion.json"))
		if err != nil {
			return err
		}
		prevHitsFG, err = loadHits(filepath.Join(prevRunDir, "raw", "hits-fusion+graph.json"))
		if err != nil {
			return err
		}
	}

	var diffs []queryDiff
	stratify := map[string]struct {
		N                     int
		NewFusion, PrevFusion float64
		NewFG, PrevFG         float64
		NoDiversify           float64
	}{}
	for _, q := range ds {
		if q.Split != "dev" {
			continue
		}
		fusionRes, err := r.Retrieve(ctx, retrieval.Request{Query: q.Text, Limit: 10, Mode: retrieval.ModeFusionNoGraph})
		if err != nil {
			return err
		}
		fgRes, err := r.Retrieve(ctx, retrieval.Request{Query: q.Text, Limit: 10, Mode: retrieval.ModeAuto})
		if err != nil {
			return err
		}

		// New metrics computed locally so the per-query numbers are
		// byte-equivalent to what the eval runner reports (same scoring
		// code in the same package, called the same way).
		newFusionMetrics := localScore(ctx, store, q, fusionRes.Rows)
		newFGMetrics := localScore(ctx, store, q, fgRes.Rows)

		// AC-5 attribution: reconstruct the pre-diversification order of
		// the SAME fused row set. diversify demotes rather than drops, so
		// a deep enough Limit returns every row the cap pushed down; the
		// rerank stage's sort key (Final desc, node_id asc) restores the
		// order those rows had before the partition.
		deepRes, err := r.Retrieve(ctx, retrieval.Request{Query: q.Text, Limit: deepLimit, Mode: retrieval.ModeFusionNoGraph})
		if err != nil {
			return err
		}
		undiv := append([]retrieval.Row(nil), deepRes.Rows...)
		sort.SliceStable(undiv, func(i, j int) bool {
			if undiv[i].Explain.Final != undiv[j].Explain.Final {
				return undiv[i].Explain.Final > undiv[j].Explain.Final
			}
			return undiv[i].NodeID < undiv[j].NodeID
		})
		if len(undiv) > 10 {
			undiv = undiv[:10]
		}
		undivMetrics := localScore(ctx, store, q, undiv)
		undivIDs := make([]string, 0, len(undiv))
		for _, row := range undiv {
			undivIDs = append(undivIDs, row.NodeID)
		}

		// Per-row explain for fusion.
		var rowExplains []perRowExplain
		qnByID := qnMap(ctx, store, fusionRes.Rows)
		for i, row := range fusionRes.Rows {
			lexRRF := 0
			semRRF := 0
			if row.Explain.LexicalRank > 0 {
				lexRRF = fusionRes.Summary.RRFScale / (fusionRes.Summary.RRFk + row.Explain.LexicalRank)
			}
			if row.Explain.SemanticRank > 0 {
				semRRF = fusionRes.Summary.RRFScale / (fusionRes.Summary.RRFk + row.Explain.SemanticRank)
			}
			rowExplains = append(rowExplains, perRowExplain{
				Rank: i + 1, NodeID: row.NodeID, Path: row.Path,
				QualifiedName: qnByID[row.NodeID],
				LexicalRank:   row.Explain.LexicalRank, SemanticRank: row.Explain.SemanticRank,
				LexicalRRF: lexRRF, SemanticRRF: semRRF, RRF: row.Explain.RRF,
				Graph: row.Explain.Graph, Classification: row.Explain.Classification, Final: row.Explain.Final,
			})
		}

		// Previous-run metrics reconstructed from the recorded hits.
		var prevFusionMetrics, prevFGMetrics map[string]float64
		var prevFusionIDs, prevFGIDs []string
		if prevHitsFusion != nil {
			prevFusionIDs = prevHitsFusion[q.ID]
			prevRows := reconstructRows(ctx, store, prevFusionIDs)
			prevFGIDs = prevHitsFG[q.ID]
			prevFGRows := reconstructRows(ctx, store, prevFGIDs)
			prevFusionMetrics = map[string]float64{"ndcg@10": localScore(ctx, store, q, prevRows).NDCG10, "top1": localScore(ctx, store, q, prevRows).Top1}
			prevFGMetrics = map[string]float64{"ndcg@10": localScore(ctx, store, q, prevFGRows).NDCG10, "top1": localScore(ctx, store, q, prevFGRows).Top1}
		}

		diffs = append(diffs, queryDiff{
			ID: q.ID, Stratum: q.Stratum,
			Top1: newFusionMetrics.Top1, NDCG10: newFusionMetrics.NDCG10,
			Rows:       rowExplains,
			PrevFusion: prevFusionIDs, PrevFG: prevFGIDs,
			NowFusion: idsFor(fusionRes), NowFG: idsFor(fgRes),
			PrevFusionMetrics: prevFusionMetrics, PrevFGMetrics: prevFGMetrics,
			NoDiversifyNDCG10: undivMetrics.NDCG10, NoDiversifyTop1: undivMetrics.Top1,
			NoDiversifyNodeIDs: undivIDs,
		})

		s := stratify[q.Stratum]
		s.N++
		s.NewFusion += newFusionMetrics.NDCG10
		s.NewFG += newFGMetrics.NDCG10
		s.NoDiversify += undivMetrics.NDCG10
		if prevFusionMetrics != nil {
			s.PrevFusion += prevFusionMetrics["ndcg@10"]
			s.PrevFG += prevFGMetrics["ndcg@10"]
		}
		stratify[q.Stratum] = s
	}

	var strata []stratumSummary
	for k, s := range stratify {
		var prevF, prevFG float64
		if prevRunDir != "" {
			prevF = s.PrevFusion / float64(s.N)
			prevFG = s.PrevFG / float64(s.N)
		}
		newF := s.NewFusion / float64(s.N)
		newFG := s.NewFG / float64(s.N)
		noDiv := s.NoDiversify / float64(s.N)
		strata = append(strata, stratumSummary{
			Stratum: k, Queries: s.N,
			NewFusion: newF, PrevFusion: prevF, DeltaFusion: newF - prevF,
			NewFG: newFG, PrevFG: prevFG, DeltaFG: newFG - prevFG,
			NoDiversify: noDiv, DeltaDiversify: newF - noDiv,
		})
	}
	sort.Slice(strata, func(i, j int) bool { return strata[i].Stratum < strata[j].Stratum })

	out := struct {
		GeneratedAt string           `json:"generated_at"`
		Embedder    string           `json:"embedder"`
		PerQuery    []queryDiff      `json:"per_query"`
		PerStratum  []stratumSummary `json:"per_stratum"`
	}{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Embedder:    embedderSelector,
		PerQuery:    diffs,
		PerStratum:  strata,
	}
	bs, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(newRunDir, "differential.json"), append(bs, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintln(w, "differential: wrote", filepath.Join(newRunDir, "differential.json"))
	for _, s := range strata {
		fmt.Fprintf(w, "  %-20s n=%d  fusion: %s -> %s (delta %s)  fusion+graph: %s -> %s (delta %s)\n",
			s.Stratum, s.Queries, f(s.PrevFusion), f(s.NewFusion), f(s.DeltaFusion), f(s.PrevFG), f(s.NewFG), f(s.DeltaFG))
	}
	return nil
}

func f(v float64) string { return fmt.Sprintf("%.4f", v) }

func idsFor(res retrieval.Result) []string {
	out := make([]string, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = r.NodeID
	}
	return out
}

func qnMap(ctx context.Context, store graphstore.Graphstore, rows []retrieval.Row) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		if _, ok := out[r.NodeID]; ok {
			continue
		}
		n, err := store.GetNode(ctx, model.NodeId(r.NodeID))
		if err == nil {
			out[r.NodeID] = n.QualifiedName()
		}
	}
	return out
}

type localMetrics struct {
	NDCG10 float64
	Top1   float64
}

// localScore scores a retrieval row set with THE gate's scorer, not a
// second one. It calls internal/eval/retrieval.Evaluate — the same
// function cmd/retrieval-eval calls — so a number printed here is
// directly comparable with the AC-9 report instead of merely resembling
// it.
//
// The earlier hand-rolled scorer in this file diverged from the gate in
// two ways and produced numbers that did not match the published report
// (e.g. architecture_flow fusion 0.2735 here vs 0.2914 in the report):
// it built the IDCG from the grade>=2 spans only, where the gate's ndcg
// builds it from EVERY judged span (a grade-1 span therefore both earns
// credit and raises the ideal), and it read the hit's line only from
// Row.Span, where the runner's retrievalToRaws falls back to the node's
// declared line when the span is absent. Both are reproduced here by
// delegating instead of re-deriving.
func localScore(ctx context.Context, store graphstore.Graphstore, q retrievalQuery, rows []retrieval.Row) localMetrics {
	m := evalretrieval.Evaluate(hitsFor(ctx, store, rows), evalQuery(q), evalretrieval.DefaultRelevantMinGrade, nil)
	return localMetrics{NDCG10: m.NDCG10, Top1: m.Top1}
}

// hitsFor projects retrieval rows into the scorer's Hit shape exactly as
// the eval runner's retrievalToRaws does: the span's start line when the
// span parses, the node's declared line otherwise, and a row with no
// resolvable path is dropped rather than scored against a path of "".
func hitsFor(ctx context.Context, store graphstore.Graphstore, rows []retrieval.Row) []evalretrieval.Hit {
	out := make([]evalretrieval.Hit, 0, len(rows))
	for _, row := range rows {
		path, line := row.Path, lineStart(row.Span)
		if n, err := store.GetNode(ctx, model.NodeId(row.NodeID)); err == nil {
			if path == "" {
				path = n.SourcePath()
			}
			if line == 0 {
				line = n.Line()
			}
		}
		if path == "" {
			continue
		}
		out = append(out, evalretrieval.Hit{Rank: len(out) + 1, Path: path, Line: line, NodeID: row.NodeID})
	}
	return out
}

// evalQuery lifts this command's local dataset shape into the scorer's.
func evalQuery(q retrievalQuery) evalretrieval.Query {
	js := make([]evalretrieval.Judgement, 0, len(q.Judges))
	for _, j := range q.Judges {
		js = append(js, evalretrieval.Judgement{Path: j.Path, StartLine: j.StartLine, EndLine: j.EndLine, Grade: j.Grade})
	}
	return evalretrieval.Query{ID: q.ID, Stratum: q.Stratum, Split: q.Split, Text: q.Text, Judgements: js}
}

func lineStart(span string) int {
	if span == "" {
		return 0
	}
	for i := 0; i < len(span); i++ {
		if span[i] == '-' {
			n := 0
			for j := 0; j < i; j++ {
				c := span[j]
				if c < '0' || c > '9' {
					return 0
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}

func reconstructRows(ctx context.Context, store graphstore.Graphstore, ids []string) []retrieval.Row {
	out := make([]retrieval.Row, 0, len(ids))
	for _, id := range ids {
		n, err := store.GetNode(ctx, model.NodeId(id))
		var path string
		var line int
		if err == nil {
			path = n.SourcePath()
			line = n.Line()
		}
		row := retrieval.Row{NodeID: id, Path: path}
		if line > 0 {
			row.Span = fmt.Sprintf("%d-%d", line, line)
		}
		out = append(out, row)
	}
	return out
}

func loadHits(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Queries []struct {
			ID   string `json:"id"`
			Hits []struct {
				NodeID string `json:"node_id"`
			} `json:"hits"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make(map[string][]string, len(doc.Queries))
	for _, q := range doc.Queries {
		ids := make([]string, len(q.Hits))
		for i, h := range q.Hits {
			ids[i] = h.NodeID
		}
		out[q.ID] = ids
	}
	return out, nil
}

// retrievalQuery is the local projection of the eval runner's
// query shape; we only need RelevantSpans, so a minimal copy
// suffices.
type retrievalQuery struct {
	ID      string
	Text    string
	Stratum string
	Split   string
	Judges  []judgement
}

type judgement struct {
	Path      string
	StartLine int
	EndLine   int
	Grade     int
}

func (q retrievalQuery) RelevantSpans(minGrade int) []judgement {
	out := []judgement{}
	for _, j := range q.Judges {
		if j.Grade >= minGrade {
			out = append(out, j)
		}
	}
	return out
}

// loadDataset reads the JSON dataset directly so we don't pull the
// whole eval/retrieval package.
func loadDataset(path string) ([]retrievalQuery, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Queries []struct {
			ID      string `json:"id"`
			Text    string `json:"query"`
			Stratum string `json:"stratum"`
			Split   string `json:"split"`
			Judges  []struct {
				Path      string `json:"path"`
				StartLine int    `json:"start_line"`
				EndLine   int    `json:"end_line"`
				Grade     int    `json:"grade"`
			} `json:"judgements"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := make([]retrievalQuery, 0, len(doc.Queries))
	for _, q := range doc.Queries {
		qq := retrievalQuery{ID: q.ID, Stratum: q.Stratum, Split: q.Split, Text: q.Text}
		for _, j := range q.Judges {
			qq.Judges = append(qq.Judges, judgement{Path: j.Path, StartLine: j.StartLine, EndLine: j.EndLine, Grade: j.Grade})
		}
		out = append(out, qq)
	}
	return out, nil
}

var _ = io.Discard
