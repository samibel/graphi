package retrieval

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/eval"
)

// Baseline names one ranking method the runner can execute (AC-3).
type Baseline string

// The seven baselines. They are executed by name and in this order.
const (
	// BaselineLexical is engine/search.Service.Search: the store's FTS5
	// (SQLite) ranking over qualified names.
	BaselineLexical Baseline = "lexical"
	// BaselineHybridV1 is search_hybrid/1 (engine/agenttools/hybridsearch):
	// lexical retrieval plus identifier, path and degree signals, no vectors.
	BaselineHybridV1 Baseline = "hybrid_v1"
	// BaselineSemanticNameOnly is engine/search.Service.SemanticSearch over the
	// name-only documents; on the default build it is the typed unavailable
	// response (AC-6).
	BaselineSemanticNameOnly Baseline = "semantic_name_only"
	// BaselineOracle ranks the judged spans themselves by grade: the ceiling
	// the metric code can reach, which proves the scorer rather than a
	// retriever.
	BaselineOracle Baseline = "oracle_upper_bound"
	// BaselineChunkOnly is the SW-263 retrieval module in ModeLexicalOnly:
	// lexical chunking only, no semantic candidate union, no RRF. The AC-9
	// "chunk-only" ablation (no embedder needed). The ranking is the same
	// integer sequence the HybridSearchBridge delegates to search_hybrid, so
	// chunk-only and hybrid_v1 carry identical hits in identical order.
	BaselineChunkOnly Baseline = "chunk_only"
	// BaselineFusion is the SW-263 retrieval module in ModeFusionNoGraph:
	// lexical+semantic union + integer RRF, NO bounded graph rerank. The
	// AC-9 "fusion" ablation. Requires an embedder (the runner wires one in
	// when -embedder is set); without one the baseline is unavailable with
	// the typed reason.
	BaselineFusion Baseline = "fusion"
	// BaselineFusionGraph is the SW-263 retrieval module in default ModeAuto:
	// full union + RRF + bounded rerank + classification + diversify. The
	// AC-9 "fusion+graph" ablation. Requires an embedder for the same
	// reason as fusion.
	BaselineFusionGraph Baseline = "fusion+graph"
)

// AllBaselines in report order. The three SW-263 retrieval ablations
// (chunk_only, fusion, fusion+graph) follow the SW-258 baselines so a
// reader of the report sees them as the addition this story is about.
var AllBaselines = []Baseline{BaselineLexical, BaselineHybridV1, BaselineSemanticNameOnly, BaselineOracle, BaselineChunkOnly, BaselineFusion, BaselineFusionGraph}

// ParseBaselines resolves names to baselines, refusing an unknown one.
func ParseBaselines(names []string) ([]Baseline, error) {
	if len(names) == 0 {
		return append([]Baseline(nil), AllBaselines...), nil
	}
	known := map[Baseline]bool{}
	for _, b := range AllBaselines {
		known[b] = true
	}
	var out []Baseline
	seen := map[Baseline]bool{}
	for _, n := range names {
		b := Baseline(strings.TrimSpace(n))
		if !known[b] {
			return nil, fmt.Errorf("retrieval: unknown baseline %q (have %s)", n, baselineNames())
		}
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	return out, nil
}

func baselineNames() string {
	names := make([]string, 0, len(AllBaselines))
	for _, b := range AllBaselines {
		names = append(names, string(b))
	}
	return strings.Join(names, ", ")
}

// DefaultRepeats is how many timed executions each query gets per baseline;
// the ranking is taken from the last one and every execution must agree.
const DefaultRepeats = 3

// Options is one run.
type Options struct {
	// RepoRoot is the checkout to index; RepoName and RepoSHA are what the
	// report says it was (the caller verifies the pin — see cmd/retrieval-eval).
	RepoRoot string
	RepoName string
	RepoSHA  string

	Dataset   *Loaded
	Baselines []Baseline

	RunnerClass  string
	CandidateSHA string

	// EmbedderSelector is the GRAPHI_EMBEDDER-style string (e.g.
	// "ollama:nomic-embed-text") the runner resolves into an Embedder
	// via embed.Constructor before indexing. Empty ⇒ no embedder (the
	// SW-258 default); the fusion / fusion+graph baselines then report
	// as unavailable with the typed reason and the semantic_name_only
	// baseline keeps its existing graceful-skip behavior.
	EmbedderSelector string

	// Repeats is the timed executions per query (DefaultRepeats when 0).
	Repeats int
	// WorkDir holds the SQLite store; a temp dir removed afterwards when empty.
	WorkDir string
	// Now supplies the environment timestamp; time.Now when nil.
	Now func() time.Time
	// Log receives progress lines; io.Discard when nil.
	Log io.Writer
}

// Result is a finished run: the report and the raw samples it derives from.
type Result struct {
	Report *Report
	Raw    *RawSamples
}

// RawSamples are the per-baseline measurements the report is recomputed
// from: the hits (the scorer's input) and the latency samples (the
// percentiles' input). They carry nothing derived.
type RawSamples struct {
	Hits    map[Baseline]RawHitSet
	Latency map[Baseline]RawLatencySet
}

// Raw series names.
const (
	RawSeriesHits    = "hits"
	RawSeriesLatency = "latency"
)

// RawHitSet is one baseline's rankings, query by query. Reason carries the
// typed reason when the baseline did not collect (AC-6); it is the only
// record that can justify a published `unavailable`.
type RawHitSet struct {
	FormatVersion  int            `json:"format_version"`
	HarnessVersion string         `json:"harness_version"`
	Series         string         `json:"series"`
	Baseline       Baseline       `json:"baseline"`
	Collected      bool           `json:"collected"`
	Reason         string         `json:"reason,omitempty"`
	Samples        int            `json:"samples"`
	Queries        []RawQueryHits `json:"queries"`
}

// RawQueryHits is one query's ranking.
type RawQueryHits struct {
	ID   string `json:"id"`
	Hits []Hit  `json:"hits"`
}

// RawLatencySet is one baseline's timings plus the single-sample measures
// (index time, peak RSS, vector-sidecar size) exactly as the run recorded
// them: the value when it was taken, the typed UNKNOWN / not_applicable
// status with its reason when it was not — so the aggregate checks an
// untaken measure against the record rather than against itself. Reason
// carries the typed reason when the baseline did not collect (AC-6).
type RawLatencySet struct {
	FormatVersion  int               `json:"format_version"`
	HarnessVersion string            `json:"harness_version"`
	Series         string            `json:"series"`
	Baseline       Baseline          `json:"baseline"`
	Collected      bool              `json:"collected"`
	Reason         string            `json:"reason,omitempty"`
	Samples        int               `json:"samples"`
	Queries        []RawQueryLatency `json:"queries"`

	IndexMS            Measure `json:"index_ms"`
	PeakRSSMB          Measure `json:"peak_rss_mb"`
	VectorSidecarBytes Measure `json:"vector_sidecar_bytes"`
}

// RawQueryLatency is one query's timed executions in microseconds.
type RawQueryLatency struct {
	ID        string  `json:"id"`
	SamplesUS []int64 `json:"samples_us"`
}

// Run indexes the repository once, executes every requested baseline over
// every dataset query, and returns the report with its raw samples. It fails
// closed: an unresolved judgement, a baseline that cannot execute, or a
// ranking that differs between repeated executions is an error.
func Run(ctx context.Context, o Options) (*Result, error) {
	if o.Dataset == nil || o.Dataset.Dataset == nil {
		return nil, fmt.Errorf("retrieval: no dataset")
	}
	if strings.TrimSpace(o.RepoRoot) == "" {
		return nil, fmt.Errorf("retrieval: no repository root")
	}
	if o.Repeats <= 0 {
		o.Repeats = DefaultRepeats
	}
	if len(o.Baselines) == 0 {
		o.Baselines = append([]Baseline(nil), AllBaselines...)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	ds := o.Dataset.Dataset

	// AC-9 inside the run as well as in the test: a judgement that does not
	// resolve makes the run an error, not a zero.
	if err := CheckSpanCoverage(o.RepoRoot, ds); err != nil {
		return nil, err
	}

	workDir := o.WorkDir
	if workDir == "" {
		dir, err := os.MkdirTemp("", "graphi-retrieval-eval")
		if err != nil {
			return nil, fmt.Errorf("retrieval: workdir: %w", err)
		}
		defer os.RemoveAll(dir)
		workDir = dir
	}

	idx, err := buildIndex(ctx, o.RepoRoot, workDir, o.EmbedderSelector, o.Log)
	if err != nil {
		return nil, err
	}
	defer idx.store.Close()
	// SW-260 AC-9: measured from the indexed files, not assumed.
	spanShare, err := spanMethodShare(ctx, o.RepoRoot, idx.filePaths)
	if err != nil {
		return nil, fmt.Errorf("retrieval: span method share: %w", err)
	}

	tokens := newTokenCounter(o.RepoRoot)
	minGrade := ds.MinGrade()
	report := &Report{
		FormatVersion:  FormatVersion,
		HarnessVersion: HarnessVersion,
		ScorerVersion:  ScorerVersion,
		Reproducible: Reproducible{
			CandidateSHA:          o.CandidateSHA,
			RunnerClass:           o.RunnerClass,
			Repo:                  RepoRef{Name: o.RepoName, SHA: o.RepoSHA, Nodes: idx.nodes, Edges: idx.edges, Files: idx.files},
			Dataset:               DatasetRefOf(o.Dataset, filepath.Base(o.Dataset.Path)),
			TokenizerID:           TokenizerID,
			TopK:                  TopK,
			TokenBudgets:          append([]int(nil), TokenBudgets...),
			HitContextWindowLines: HitContextWindowLines,
			RelevantMinGrade:      minGrade,
			MatchingRule:          MatchingRule,
			SpanMethodShare:       spanShare,
		},
		Environment: Environment{
			GeneratedAt: o.Now().UTC().Format(time.RFC3339),
			OS:          runtime.GOOS,
			Arch:        runtime.GOARCH,
			GoVersion:   runtime.Version(),
			CPUCount:    runtime.NumCPU(),
			Notes: "peak_rss_mb is the process-lifetime getrusage MAXRSS sampled after the baseline ran, so it is monotone across baselines within one run; " +
				"index_ms is the one cold IngestAll shared by every indexed baseline",
		},
	}
	raw := &RawSamples{Hits: map[Baseline]RawHitSet{}, Latency: map[Baseline]RawLatencySet{}}

	deps := resolve.Deps{Query: query.New(idx.store), Search: idx.search}
	for _, b := range o.Baselines {
		fmt.Fprintf(o.Log, "retrieval-eval: baseline %s over %d queries\n", b, len(ds.Queries))
		exec, method, err := executorFor(b, deps, idx, ds, minGrade)
		if err != nil {
			return nil, err
		}
		res, hits, lat, err := runBaseline(ctx, b, method, exec, ds, o.Repeats, tokens)
		if err != nil {
			return nil, err
		}
		if res.Status != BaselineStatusOK {
			// AC-6: nothing ran, so nothing was measured. The raw records say
			// so (collected: false) and carry the typed reason; the published
			// performance block is derived from that record alone, so every
			// figure reads UNKNOWN with the reason, never zero.
			hits.Collected, lat.Collected = false, false
			hits.Reason, lat.Reason = res.Reason, res.Reason
			u := Unknown("baseline unavailable: " + res.Reason)
			lat.IndexMS, lat.PeakRSSMB, lat.VectorSidecarBytes = u, u, u
		} else {
			switch b {
			case BaselineOracle:
				lat.IndexMS = NotApplicable("the oracle ranks the judged spans and builds no index")
				lat.VectorSidecarBytes = NotApplicable("the oracle ranks the judged spans and builds no index")
			default:
				lat.IndexMS = Measured(idx.indexMS, "ms")
				lat.VectorSidecarBytes = NotApplicable("no vector sidecar: this baseline ranks without vectors")
			}
			if rss, ok := peakRSSMB(); ok {
				lat.PeakRSSMB = Measured(rss, "MB")
			} else {
				lat.PeakRSSMB = Unknown("getrusage is not available on " + runtime.GOOS)
			}
		}
		report.Reproducible.Baselines = append(report.Reproducible.Baselines, res)
		report.Performance = append(report.Performance, PerformanceFromRaw(b, lat))
		raw.Hits[b] = hits
		raw.Latency[b] = lat
	}
	return &Result{Report: report, Raw: raw}, nil
}

// index is the one graph every indexed baseline shares.
type index struct {
	store   *graphstore.SQLiteStore
	search  *search.Service
	indexMS float64
	nodes   int
	edges   int
	files   int
	// filePaths are the indexed files (path order), the span-share input.
	filePaths []string
}

// buildIndex ingests root into a fresh SQLite store the way cmd/eval's full
// run does (engine/ingest over parse.NewDefaultRegistry, IngestAll timed),
// then wires the search service with the DEFAULT embedder registry — which on
// the default build registers nothing, so SemanticSearch is the typed
// unavailable response. When embedderSelector is non-empty the runner resolves
// it via embed.Constructor, generates + persists a GenerationStore under
// metaDir, reloads it into a fresh in-memory index, and wires the search
// service with WithSemantic + WithSemanticState(Ready) so the SW-263
// retrieval ablations (fusion, fusion+graph) and the semantic_name_only
// baseline can rank vectors end to end.
func buildIndex(ctx context.Context, root, workDir, embedderSelector string, log io.Writer) (*index, error) {
	dbPath := filepath.Join(workDir, "retrieval-eval.db")
	metaDir := filepath.Join(workDir, "retrieval-eval-meta")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return nil, fmt.Errorf("retrieval: open store: %w", err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("retrieval: ingest.New: %w", err)
	}
	fmt.Fprintf(log, "retrieval-eval: indexing %s\n", root)
	start := time.Now()
	err = ing.IngestAll(ctx, root)
	elapsed := time.Since(start)
	closeErr := ing.Close()
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("retrieval: index %s: %w", root, err)
	}
	if closeErr != nil {
		store.Close()
		return nil, fmt.Errorf("retrieval: close ingester: %w", closeErr)
	}
	agg, ok := any(store).(graphstore.BriefAggregatePort)
	if !ok {
		store.Close()
		return nil, fmt.Errorf("retrieval: store has no BriefAggregatePort")
	}
	stats, err := agg.BriefStats(ctx, 0)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("retrieval: inventory: %w", err)
	}
	if stats.TotalNodes == 0 {
		store.Close()
		return nil, fmt.Errorf("retrieval: index of %s produced no nodes", root)
	}
	svc := buildSearchService(ctx, store, metaDir, embedderSelector, log)
	fmt.Fprintf(log, "retrieval-eval: indexed %d nodes, %d edges, %d files in %dms\n", stats.TotalNodes, stats.TotalEdges, len(stats.Files), elapsed.Milliseconds())
	filePaths := make([]string, 0, len(stats.Files))
	for _, f := range stats.Files {
		filePaths = append(filePaths, f.Path)
	}
	return &index{
		store: store, search: svc,
		indexMS: float64(elapsed.Milliseconds()),
		nodes:   stats.TotalNodes, edges: stats.TotalEdges, files: len(stats.Files),
		filePaths: filePaths,
	}, nil
}

// buildSearchService wires the search service with or without a configured
// embedder per the runner's selector. Empty selector (the SW-258 default)
// keeps the unconfigured graceful-skip registry so SemanticSearch returns
// the typed Unavailable response and the fusion / fusion+graph ablations
// are unavailable with the typed reason. Non-empty selector loads the
// embedder, generates + persists a GenerationStore, reloads the in-memory
// index from the active generation, and wires WithSemanticState(Ready).
func buildSearchService(ctx context.Context, store graphstore.Graphstore, metaDir, selector string, log io.Writer) *search.Service {
	if strings.TrimSpace(selector) == "" {
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	emb, err := embed.Constructor(selector, embed.DefaultConstructors())
	if err != nil || emb == nil {
		fmt.Fprintf(log, "retrieval-eval: embedder %q unavailable: %v — semantic baselines will report unavailable\n", selector, err)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	fmt.Fprintf(log, "retrieval-eval: embedder %q active; generating vectors\n", emb.ID())

	reg := embed.NewRegistry()
	if rerr := reg.Register(emb); rerr != nil {
		fmt.Fprintf(log, "retrieval-eval: embedder register: %v\n", rerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	reg.Freeze()
	index := embed.NewIndex()
	// Enumerate every node in the graph so the generation pass covers
	// exactly what the index produced (one embedding per indexed node).
	nodes, nerr := store.Nodes(ctx, graphstore.Query{})
	if nerr != nil {
		fmt.Fprintf(log, "retrieval-eval: nodes enumerate: %v\n", nerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	genStore, gerr := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if gerr != nil {
		fmt.Fprintf(log, "retrieval-eval: generation store open: %v\n", gerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	res, err := embed.GenerateAndPersist(ctx, reg, nodes, embed.V1DocumentSource{}, index, genStore, embed.GraphGenerationPlaceholder)
	_ = genStore.Close()
	if err != nil {
		fmt.Fprintf(log, "retrieval-eval: generate+persist: %v\n", err)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	fmt.Fprintf(log, "retrieval-eval: generation pass embedded=%d reused=%d skipped=%d purged=%d (id=%s)\n",
		res.Embedded, res.Reused, res.Skipped, res.Purged, res.EmbedderID)
	// Reload the durable generation into a fresh in-memory index so the
	// search path serves from a stable, fingerprinted set (mirrors the
	// production runtime's reload pattern in cmd/internal/runtime).
	reloadStore, rerr := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if rerr != nil {
		fmt.Fprintf(log, "retrieval-eval: generation store reopen: %v\n", rerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	defer func() { _ = reloadStore.Close() }()
	fp := embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
	if fp.Dim == 0 {
		if d, ok, derr := reloadStore.DimForModel(ctx, emb.ID()); derr == nil && ok {
			fp.Dim = d
		}
	}
	gen, _, aerr := reloadStore.Active(ctx, fp, nil)
	if aerr != nil || gen.ID == "" {
		fmt.Fprintf(log, "retrieval-eval: active generation lookup: aerr=%v\n", aerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	rows, lerr := reloadStore.Load(ctx, gen.ID)
	if lerr != nil {
		fmt.Fprintf(log, "retrieval-eval: reload generation: %v\n", lerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	vecs := make([]embed.Vector, len(rows))
	for i, r := range rows {
		vecs[i] = embed.Vector{NodeID: r.NodeID, Values: r.Vector}
	}
	if rerr := index.Rebuild(ctx, vecs); rerr != nil {
		fmt.Fprintf(log, "retrieval-eval: index rebuild: %v\n", rerr)
		return search.New(store).WithSemantic(embed.NewDefaultRegistry(), nil, store)
	}
	svc := search.New(store).WithSemantic(reg, index, store).
		WithSemanticState(search.SemanticState{State: embed.StateReady})
	fmt.Fprintf(log, "retrieval-eval: semantic ready (model=%s, dim=%d, rows=%d)\n", emb.ID(), emb.Dim(), len(rows))
	return svc
}

// rawHit is what an executor returns before tokens are charged.
type rawHit struct {
	path, nodeID, kind, qn string
	line                   int
}

// executor runs one query. unavailable is the typed reason when the baseline
// cannot run on this build; it ends the baseline rather than yielding zeros.
type executor func(ctx context.Context, q Query) (hits []rawHit, unavailable string, err error)

// semanticReadyReason returns the typed reason when the SW-263 fusion
// ablations cannot run (no embedder configured), or "" when the semantic
// path is active and ready. It is the single source of truth so the
// chunk-only baseline stays lexical-only while fusion / fusion+graph refuse
// to answer without a configured embedder rather than silently degrading
// to a lexical-only ranking masquerading as fusion.
func semanticReadyReason(idx *index) string {
	resp, err := idx.search.SemanticSearch(context.Background(), "", 0)
	if err != nil {
		return fmt.Sprintf("semantic search probe failed: %v", err)
	}
	if !resp.Available {
		return resp.Reason
	}
	return ""
}

// unavailableExecutor returns an executor that responds with the typed
// unavailability reason on every query. It is the wrapper the fusion /
// fusion+graph cases use when no embedder is configured, so the baseline
// reports unavailable (the AC-6 posture) rather than yielding a silent
// lexical-only ranking.
func unavailableExecutor(reason string) executor {
	return func(ctx context.Context, q Query) ([]rawHit, string, error) {
		return nil, reason, nil
	}
}

// retrievalToRaws projects a retrieval.Result into the runner's rawHit
// shape so the same scoring + token-charging pipeline the existing
// baselines use sees the SW-263 ablations' rows. The path/line/node_id
// fields come from the retrieval's row; kind/qualified_name are looked up
// against the store so the metadata the matcher reads (token budget
// computation, the per-hit kind printed in the raw file) matches what the
// other baselines carry. Rows with no source path after the node lookup
// (package / external nodes that surface in the semantic index with
// empty SourcePath) are skipped — the matcher cannot credit them against
// any file-scoped judgement and the token counter's fileLines errors on
// a directory.
func retrievalToRaws(ctx context.Context, idx *index, res retrieval.Result) []rawHit {
	out := make([]rawHit, 0, len(res.Rows))
	for _, row := range res.Rows {
		path := row.Path
		line := 0
		node, err := idx.store.GetNode(ctx, model.NodeId(row.NodeID))
		if err == nil {
			if path == "" {
				path = node.SourcePath()
			}
			if row.Span != "" {
				if l, ok := parseSpanStart(row.Span); ok {
					line = l
				}
			}
			if line == 0 {
				line = node.Line()
			}
		}
		if path == "" {
			continue
		}
		var kind, qn string
		if err == nil {
			kind, qn = node.Kind(), node.QualifiedName()
		}
		out = append(out, rawHit{path: path, line: line, nodeID: row.NodeID, kind: kind, qn: qn})
	}
	return out
}

// parseSpanStart extracts the 1-based start line from a "start-end" span
// string. Returns ok=false on any parse failure so the caller falls back to
// the node's declared line. The retrieval's Span is engine-owned and
// always renders as "start-end" when both are known, so the parse is a
// strict substring split.
func parseSpanStart(span string) (int, bool) {
	for i := 0; i < len(span); i++ {
		if span[i] == '-' {
			n, ok := atoiStrict(span[:i])
			return n, ok
		}
	}
	return 0, false
}

func atoiStrict(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

func executorFor(b Baseline, deps resolve.Deps, idx *index, ds *Dataset, minGrade int) (executor, string, error) {
	switch b {
	case BaselineLexical:
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			resp, err := idx.search.Search(ctx, q.Text, TopK)
			if err != nil {
				return nil, "", err
			}
			out := make([]rawHit, 0, len(resp.Matches))
			for _, m := range resp.Matches {
				out = append(out, rawHit{path: m.SourcePath, line: m.Line, nodeID: m.NodeID, kind: m.Kind, qn: m.QualifiedName})
			}
			return out, "", nil
		}, "engine/search.Service.Search (sqlite fts5 bm25)", nil
	case BaselineHybridV1:
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			res, err := hybridsearch.Search(ctx, hybridsearch.Params{Query: q.Text, MaxItems: TopK, Deps: deps})
			if err != nil {
				return nil, "", err
			}
			if res.Outcome == contract.OutcomeUnavailable {
				return nil, res.Summary, nil
			}
			evidence := map[string]contract.Evidence{}
			for _, ev := range res.Evidence {
				evidence[ev.RefID] = ev
			}
			out := make([]rawHit, 0, len(res.Items))
			for _, it := range res.Items {
				if len(it.EvidenceRefIDs) == 0 {
					return nil, "", fmt.Errorf("retrieval: search_hybrid item %s cites no evidence", it.RefID)
				}
				ev, ok := evidence[it.EvidenceRefIDs[0]]
				if !ok {
					return nil, "", fmt.Errorf("retrieval: search_hybrid item %s cites unknown evidence %s", it.RefID, it.EvidenceRefIDs[0])
				}
				h := rawHit{path: ev.Path, line: ev.Line, nodeID: it.RefID}
				if n, gerr := idx.store.GetNode(ctx, model.NodeId(it.RefID)); gerr == nil {
					h.kind, h.qn = n.Kind(), n.QualifiedName()
				}
				out = append(out, h)
			}
			return out, "", nil
		}, hybridsearch.MethodVersion + " (engine/agenttools/hybridsearch, weights " + hybridsearch.WeightsHash() + ")", nil
	case BaselineSemanticNameOnly:
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			resp, err := idx.search.SemanticSearch(ctx, q.Text, TopK)
			if err != nil {
				return nil, "", err
			}
			if !resp.Available {
				return nil, resp.Reason, nil
			}
			out := make([]rawHit, 0, len(resp.Hits))
			for _, h := range resp.Hits {
				// Skip hits with no source path: package / external nodes
				// surface in the v2 index with empty SourcePath, the
				// matcher cannot credit them against a judgement, and
				// the token counter's fileLines errors on a directory.
				if h.SourcePath == "" {
					continue
				}
				out = append(out, rawHit{path: h.SourcePath, line: h.Line, nodeID: h.NodeID, kind: h.Kind, qn: h.QualifiedName})
			}
			return out, "", nil
		}, "engine/search.Service.SemanticSearch (name-only documents)", nil
	case BaselineChunkOnly:
		bridge := &retrieval.HybridSearchBridge{Deps: deps}
		bridge.WeightsHash = hybridsearch.WeightsHash()
		r := retrieval.New(bridge, &retrieval.SearchServiceBridge{Service: idx.search}, nil)
		method := "engine/retrieval (chunk-only, ModeLexicalOnly, weights " + retrieval.WeightsHash() + ")"
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			res, err := r.Retrieve(ctx, retrieval.Request{Query: q.Text, Limit: TopK, Mode: retrieval.ModeLexicalOnly})
			if err != nil {
				return nil, "", err
			}
			return retrievalToRaws(ctx, idx, res), "", nil
		}, method, nil
	case BaselineFusion:
		if reason := semanticReadyReason(idx); reason != "" {
			// The semantic path is not active: this baseline reports
			// unavailable with the typed reason rather than producing a
			// silent lexical-only ranking that masquerades as fusion.
			return unavailableExecutor(reason), "engine/retrieval (fusion, ModeFusionNoGraph) — requires configured embedder", nil
		}
		bridge := &retrieval.HybridSearchBridge{Deps: deps}
		bridge.WeightsHash = hybridsearch.WeightsHash()
		r := retrieval.New(bridge, &retrieval.SearchServiceBridge{Service: idx.search}, nil)
		method := "engine/retrieval (fusion, ModeFusionNoGraph, weights " + retrieval.WeightsHash() + ")"
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			res, err := r.Retrieve(ctx, retrieval.Request{Query: q.Text, Limit: TopK, Mode: retrieval.ModeFusionNoGraph})
			if err != nil {
				return nil, "", err
			}
			return retrievalToRaws(ctx, idx, res), "", nil
		}, method, nil
	case BaselineFusionGraph:
		if reason := semanticReadyReason(idx); reason != "" {
			return unavailableExecutor(reason), "engine/retrieval (fusion+graph, ModeAuto) — requires configured embedder", nil
		}
		bridge := &retrieval.HybridSearchBridge{Deps: deps}
		bridge.WeightsHash = hybridsearch.WeightsHash()
		r := retrieval.New(bridge, &retrieval.SearchServiceBridge{Service: idx.search}, nil)
		method := "engine/retrieval (fusion+graph, ModeAuto, weights " + retrieval.WeightsHash() + ")"
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			res, err := r.Retrieve(ctx, retrieval.Request{Query: q.Text, Limit: TopK, Mode: retrieval.ModeAuto})
			if err != nil {
				return nil, "", err
			}
			return retrievalToRaws(ctx, idx, res), "", nil
		}, method, nil
	case BaselineOracle:
		return func(ctx context.Context, q Query) ([]rawHit, string, error) {
			spans := append([]Judgement(nil), q.Judgements...)
			sort.SliceStable(spans, func(i, j int) bool {
				if spans[i].Grade != spans[j].Grade {
					return spans[i].Grade > spans[j].Grade
				}
				if spans[i].Path != spans[j].Path {
					return spans[i].Path < spans[j].Path
				}
				return spans[i].StartLine < spans[j].StartLine
			})
			var out []rawHit
			for _, s := range spans {
				if s.Grade < 1 || len(out) >= TopK {
					break
				}
				out = append(out, rawHit{path: s.Path, line: s.StartLine, kind: "span",
					qn: fmt.Sprintf("%s:%d-%d", s.Path, s.StartLine, s.EndLine)})
			}
			return out, "", nil
		}, "judged spans with grade >= 1 ranked by grade (the scorer's ceiling)", nil
	}
	return nil, "", fmt.Errorf("retrieval: unknown baseline %q", b)
}

// runBaseline executes every query Repeats times, checks the ranking is the
// same every time, scores it, and aggregates. Scoring runs over the canonical
// hits; the report and the raw record publish the bounded ones (boundHit).
func runBaseline(ctx context.Context, b Baseline, method string, exec executor, ds *Dataset, repeats int, tokens *tokenCounter) (BaselineResult, RawHitSet, RawLatencySet, error) {
	res := BaselineResult{Name: b, Status: BaselineStatusOK, Method: method, Queries: []QueryResult{},
		Strata: map[string]AggregateMetrics{}, Splits: map[string]AggregateMetrics{}}
	hitSet := RawHitSet{FormatVersion: FormatVersion, HarnessVersion: HarnessVersion, Series: RawSeriesHits, Baseline: b, Collected: true, Queries: []RawQueryHits{}}
	latSet := RawLatencySet{FormatVersion: FormatVersion, HarnessVersion: HarnessVersion, Series: RawSeriesLatency, Baseline: b, Collected: true, Queries: []RawQueryLatency{}}
	minGrade := ds.MinGrade()

	for _, q := range ds.Queries {
		var (
			ranking []rawHit
			samples = make([]int64, 0, repeats)
		)
		for i := 0; i < repeats; i++ {
			start := time.Now()
			hits, unavailable, err := exec(ctx, q)
			elapsed := time.Since(start)
			if err != nil {
				return res, hitSet, latSet, fmt.Errorf("retrieval: baseline %s query %s: %w", b, q.ID, err)
			}
			if unavailable != "" {
				return unavailableBaseline(b, method, unavailable), hitSet, latSet, nil
			}
			if i > 0 && !sameRanking(ranking, hits) {
				return res, hitSet, latSet, fmt.Errorf("retrieval: baseline %s query %s: ranking differs between executions %d and %d", b, q.ID, i, i+1)
			}
			ranking = hits
			samples = append(samples, elapsed.Microseconds())
		}
		scored := make([]Hit, 0, len(ranking))
		for i, h := range ranking {
			n, err := tokens.count(h.path, h.line)
			if err != nil {
				return res, hitSet, latSet, fmt.Errorf("retrieval: baseline %s query %s hit %d: %w", b, q.ID, i+1, err)
			}
			scored = append(scored, Hit{Rank: i + 1, Path: h.path, Line: h.line, NodeID: h.nodeID, Kind: h.kind, QualifiedName: h.qn, Tokens: n})
		}
		published := make([]Hit, 0, len(scored))
		for _, h := range scored {
			published = append(published, boundHit(h))
		}
		res.Queries = append(res.Queries, QueryResult{ID: q.ID, Stratum: q.Stratum, Split: q.Split, Hits: published,
			Metrics: Evaluate(scored, q, minGrade, TokenBudgets)})
		hitSet.Queries = append(hitSet.Queries, RawQueryHits{ID: q.ID, Hits: published})
		hitSet.Samples += len(published)
		latSet.Queries = append(latSet.Queries, RawQueryLatency{ID: q.ID, SamplesUS: samples})
		latSet.Samples += len(samples)
	}
	res.Overall, res.Strata, res.Splits = AggregateAll(res.Queries, TokenBudgets)
	return res, hitSet, latSet, nil
}

// PerformanceFromRaw is THE derivation of a baseline's performance block
// (AC-4) from its raw latency record: the runner publishes what it returns
// and the aggregate recomputes the block through the same function. A record
// that did not collect yields UNKNOWN with the typed reason for every
// measure; otherwise the percentiles are nearest-rank over every timed
// execution and index_ms / peak_rss_mb / vector_sidecar_bytes are the
// record's own measures, status and reason included. LatencySamples is
// always the count of timed executions the record carries, collected or
// not, so an uncollected record that nevertheless holds samples yields a
// block that no honest unavailable report (latency_samples 0) can match.
func PerformanceFromRaw(b Baseline, lat RawLatencySet) BaselinePerformance {
	var samples []int64
	for _, q := range lat.Queries {
		samples = append(samples, q.SamplesUS...)
	}
	if !lat.Collected {
		out := unavailablePerformance(b, lat.Reason)
		out.LatencySamples = len(samples)
		return out
	}
	out := BaselinePerformance{Baseline: b, IndexMS: lat.IndexMS, LatencySamples: len(samples),
		PeakRSSMB: lat.PeakRSSMB, VectorSidecarBytes: lat.VectorSidecarBytes}
	if len(samples) > 0 {
		out.QueryP50US = Measured(float64(PercentileInt64(samples, 50)), "us")
		out.QueryP95US = Measured(float64(PercentileInt64(samples, 95)), "us")
	} else {
		out.QueryP50US = Unknown("no timed executions")
		out.QueryP95US = Unknown("no timed executions")
	}
	return out
}

// AggregateAll computes the overall, per-stratum and per-split aggregates.
// Every stratum and split is present so a reader sees an empty one as
// UNKNOWN rather than as missing.
func AggregateAll(results []QueryResult, budgets []int) (overall AggregateMetrics, strata, splits map[string]AggregateMetrics) {
	strata = map[string]AggregateMetrics{}
	splits = map[string]AggregateMetrics{}
	byStratum := map[string][]QueryResult{}
	bySplit := map[string][]QueryResult{}
	for _, r := range results {
		byStratum[r.Stratum] = append(byStratum[r.Stratum], r)
		bySplit[r.Split] = append(bySplit[r.Split], r)
	}
	for _, s := range Strata {
		strata[s] = Aggregate(byStratum[s], budgets)
	}
	for _, s := range []string{SplitDev, SplitHoldout} {
		splits[s] = Aggregate(bySplit[s], budgets)
	}
	return Aggregate(results, budgets), strata, splits
}

func unavailablePerformance(b Baseline, reason string) BaselinePerformance {
	u := Unknown("baseline unavailable: " + reason)
	return BaselinePerformance{Baseline: b, IndexMS: u, QueryP50US: u, QueryP95US: u, PeakRSSMB: u, VectorSidecarBytes: u}
}

func unavailableBaseline(b Baseline, method, reason string) BaselineResult {
	res := BaselineResult{Name: b, Status: BaselineStatusUnavailable, Reason: reason, Method: method, Queries: []QueryResult{}}
	res.Overall, res.Strata, res.Splits = AggregateAll(nil, TokenBudgets)
	return res
}

func sameRanking(a, b []rawHit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// tokenCounter charges a hit the whitespace tokens of its read window
// (HitContextWindowLines from the hit's line), with the file cached.
type tokenCounter struct {
	root  string
	files map[string][]string
}

func newTokenCounter(root string) *tokenCounter {
	return &tokenCounter{root: root, files: map[string][]string{}}
}

func (t *tokenCounter) count(rel string, line int) (int, error) {
	lines, err := fileLines(t.root, t.files, rel)
	if err != nil {
		return 0, err
	}
	if line < 1 {
		line = 1
	}
	if line > len(lines) {
		return 0, fmt.Errorf("%s: hit line %d beyond %d lines", rel, line, len(lines))
	}
	end := min(line-1+HitContextWindowLines, len(lines))
	return eval.CountTokens(strings.Join(lines[line-1:end], "\n")), nil
}
