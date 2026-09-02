package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/embedsource"
)

// TaskContext measurement versions are independent of the general retrieval
// report versions. This harness measures the assembled task_context/2 bundle,
// not a top-k retrieval ranking.
const (
	TaskContextFormatVersion  = 2
	TaskContextHarnessVersion = "task-context-v2-eval/2"
	TaskContextScorerVersion  = "span-matches-grade3-any/2"
	TaskContextTokenBudget    = 1200
)

// TaskContextMatchingRule documents exactly how ScoreTaskContextBundle uses
// SpanMatches. Keeping the function call as the implementation (rather than
// copying its interval arithmetic) prevents the two scorers from drifting.
const TaskContextMatchingRule = "a query is covered when any task_context/2 bundle evidence citation satisfies retrieval.SpanMatches(evidence.path, evidence.line, judgement) for any grade-3 judgement"

// TaskContextOptions is one fail-closed task_context/2 measurement run.
type TaskContextOptions struct {
	RepoRoot string
	RepoName string
	RepoSHA  string

	Dataset          *Loaded
	DatasetSHA       string
	EmbedderSelector string
	Candidate        TaskContextCandidate

	WorkDir string
	Log     io.Writer
}

// TaskContextCandidate identifies an uncommitted candidate without presenting
// its base commit as if it were clean. SourceFiles bind the exact measurement
// implementation bytes; DiffSHA256 binds the tracked patch plus those files.
type TaskContextCandidate struct {
	SHA         string                  `json:"sha"`
	BaseSHA     string                  `json:"base_sha"`
	Dirty       bool                    `json:"dirty"`
	DiffSHA256  string                  `json:"diff_sha256"`
	SourceFiles []TaskContextFileDigest `json:"source_files"`
}

// TaskContextFileDigest is a content-addressed run file.
type TaskContextFileDigest struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

// TaskContextMeasurement is the published aggregate and its complete
// provenance. Raw bundles and matches live in the files named by Queries.
type TaskContextMeasurement struct {
	FormatVersion  int    `json:"format_version"`
	HarnessVersion string `json:"harness_version"`
	ScorerVersion  string `json:"scorer_version"`
	Story          string `json:"story"`
	AC             string `json:"ac"`

	EligibleForThreshold bool                          `json:"eligible_for_threshold"`
	Eligibility          []TaskContextEligibilityCheck `json:"eligibility"`
	Candidate            TaskContextCandidate          `json:"candidate"`
	Dataset              TaskContextDatasetRef         `json:"dataset"`
	Repo                 TaskContextRepoRef            `json:"repo"`
	Embedder             TaskContextEmbedderRef        `json:"embedder"`
	Retrieval            TaskContextRetrievalRef       `json:"retrieval"`
	Bundle               TaskContextBundleRef          `json:"bundle"`
	Aggregate            TaskContextAggregate          `json:"aggregate"`
	Queries              []TaskContextQueryResult      `json:"queries"`
	RecomputeCommand     string                        `json:"recompute_command"`
}

// TaskContextEligibilityCheck is one condition that must hold before the
// measurement may set eligible_for_threshold=true.
type TaskContextEligibilityCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

// TaskContextDatasetRef records both the immutable source dataset and the
// derived dev/nl_behaviour slice copied into the run directory.
type TaskContextDatasetRef struct {
	ID                  string   `json:"id"`
	SourceFile          string   `json:"source_file"`
	SourceSHA256        string   `json:"source_sha256"`
	MeasuredSliceFile   string   `json:"measured_slice_file"`
	MeasuredSliceSHA256 string   `json:"measured_slice_sha256"`
	Stratum             string   `json:"stratum"`
	Split               string   `json:"split"`
	QueryCount          int      `json:"query_count"`
	QueryIDs            []string `json:"query_ids"`
	Grade               int      `json:"grade"`
	SelectionRule       string   `json:"selection_rule"`
}

// TaskContextRepoRef records the pinned checkout and indexed inventory.
type TaskContextRepoRef struct {
	Name  string `json:"name"`
	SHA   string `json:"sha"`
	Nodes int    `json:"nodes"`
	Edges int    `json:"edges"`
	Files int    `json:"files"`
}

// TaskContextEmbedderRef records the configured model and the generation that
// was independently reloaded and verified ready.
type TaskContextEmbedderRef struct {
	Selector         string `json:"selector"`
	ModelFingerprint string `json:"model_fingerprint"`
	IndexFingerprint string `json:"index_fingerprint"`
	GenerationID     string `json:"generation_id"`
	PersistedVectors int    `json:"persisted_vectors"`
	Dimension        int    `json:"dimension"`
	SemanticState    string `json:"semantic_state"`
}

// TaskContextRetrievalRef identifies the real retrieval instance and the
// ready-path method observed from every request.
type TaskContextRetrievalRef struct {
	Construction string `json:"construction"`
	NonNil       bool   `json:"non_nil"`
	State        string `json:"state"`
	Version      string `json:"version"`
	Strategy     string `json:"strategy"`
	Method       string `json:"method"`
}

// TaskContextBundleRef records the surface, reader and real token dimension.
type TaskContextBundleRef struct {
	MethodVersion string `json:"method_version"`
	Reader        string `json:"reader"`
	TokenBudget   int    `json:"token_budget"`
	Scorer        string `json:"scorer"`
	MatchingRule  string `json:"matching_rule"`
}

// TaskContextAggregate is the binary per-query grade-3 coverage aggregate.
type TaskContextAggregate struct {
	CoveredQueries             int     `json:"covered_queries"`
	TotalQueries               int     `json:"total_queries"`
	Coverage                   float64 `json:"grade3_span_coverage"`
	CoverageResolutionFraction string  `json:"coverage_resolution_fraction"`
	CoverageResolution         float64 `json:"coverage_resolution"`
	TruncatedQueries           int     `json:"truncated_queries"`
	CoverageCostNote           string  `json:"coverage_cost_note"`
}

// TaskContextQueryResult is the aggregate's per-query index. RawFile holds the
// exact bundle, ready retrieval result, grade-3 judgements and SpanMatches.
type TaskContextQueryResult struct {
	ID                              string `json:"id"`
	Stratum                         string `json:"stratum"`
	Split                           string `json:"split"`
	Covered                         bool   `json:"covered"`
	ItemCount                       int    `json:"item_count"`
	EvidenceCitationCount           int    `json:"evidence_citation_count"`
	SnippetCount                    int    `json:"snippet_count"`
	EmittedSnippetWhitespaceTokens  int    `json:"emitted_snippet_whitespace_tokens"`
	EngineReportedSnippetTokens     int    `json:"engine_reported_snippet_tokens"`
	EngineReportedTokenBudget       int    `json:"engine_reported_token_budget"`
	EngineMinusEmittedSnippetTokens int    `json:"engine_minus_emitted_snippet_tokens"`
	ItemCapApplied                  int    `json:"item_cap_applied"`
	ItemsAvailable                  int    `json:"items_available"`
	ItemsDropped                    int    `json:"items_dropped"`
	Truncated                       bool   `json:"truncated"`
	Grade3Judgements                int    `json:"grade3_judgements"`
	MatchCount                      int    `json:"match_count"`
	RawFile                         string `json:"raw_file"`
	RawSHA256                       string `json:"raw_sha256"`
}

// TaskContextScore is the scorer's raw result for one bundle.
type TaskContextScore struct {
	Covered                         bool               `json:"covered"`
	ItemCount                       int                `json:"item_count"`
	EvidenceCitationCount           int                `json:"evidence_citation_count"`
	SnippetCount                    int                `json:"snippet_count"`
	EmittedSnippetWhitespaceTokens  int                `json:"emitted_snippet_whitespace_tokens"`
	EngineReportedSnippetTokens     int                `json:"engine_reported_snippet_tokens"`
	EngineReportedTokenBudget       int                `json:"engine_reported_token_budget"`
	EngineMinusEmittedSnippetTokens int                `json:"engine_minus_emitted_snippet_tokens"`
	ItemCapApplied                  int                `json:"item_cap_applied"`
	ItemsAvailable                  int                `json:"items_available"`
	ItemsDropped                    int                `json:"items_dropped"`
	Truncated                       bool               `json:"truncated"`
	Grade3Judgements                int                `json:"grade3_judgements"`
	Matches                         []TaskContextMatch `json:"matches"`
}

// TaskContextMatch records the evidence/judgement pair for which SpanMatches
// returned true.
type TaskContextMatch struct {
	EvidenceRefID  string `json:"evidence_ref_id"`
	EvidencePath   string `json:"evidence_path"`
	EvidenceLine   int    `json:"evidence_line"`
	EvidenceSpan   string `json:"evidence_span,omitempty"`
	EvidenceRole   string `json:"evidence_role"`
	EvidenceClaim  string `json:"evidence_claim_type,omitempty"`
	JudgementPath  string `json:"judgement_path"`
	JudgementStart int    `json:"judgement_start_line"`
	JudgementEnd   int    `json:"judgement_end_line"`
	JudgementGrade int    `json:"judgement_grade"`
}

// TaskContextRawQuery is the complete raw input/output record for one query.
type TaskContextRawQuery struct {
	FormatVersion  int                     `json:"format_version"`
	HarnessVersion string                  `json:"harness_version"`
	ScorerVersion  string                  `json:"scorer_version"`
	Query          Query                   `json:"query"`
	Grade3         []Judgement             `json:"grade3_judgements"`
	Retrieval      resolve.RetrieverResult `json:"retrieval"`
	Bundle         *contract.Result        `json:"bundle"`
	Score          TaskContextScore        `json:"score"`
}

// TaskContextRun is the in-memory result before WriteTaskContextRunDir adds
// file names and digests.
type TaskContextRun struct {
	Measurement  *TaskContextMeasurement
	DatasetSlice *Dataset
	Raw          []TaskContextRawQuery
}

// SelectTaskContextDevNLBehaviour returns the complete dev nl_behaviour
// population. There is deliberately no split parameter: this measurement
// cannot be widened to holdout by a caller. The post-selection assertions
// fail closed if this function is later edited incorrectly.
func SelectTaskContextDevNLBehaviour(ds *Dataset) ([]Query, error) {
	if ds == nil {
		return nil, fmt.Errorf("task-context eval: nil dataset")
	}
	queries := make([]Query, 0)
	for _, q := range ds.Queries {
		if q.Stratum == StratumNLBehaviour && q.Split == SplitDev {
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("task-context eval: dataset %s has no %s queries in split %s", ds.ID, StratumNLBehaviour, SplitDev)
	}
	for _, q := range queries {
		if q.Stratum != StratumNLBehaviour || q.Split != SplitDev {
			return nil, fmt.Errorf("task-context eval: selection widened to query %s (%s/%s); only %s/%s is eligible", q.ID, q.Stratum, q.Split, StratumNLBehaviour, SplitDev)
		}
		grade3 := 0
		for _, j := range q.Judgements {
			if j.Grade == GradeMax {
				grade3++
			}
		}
		if grade3 == 0 {
			return nil, fmt.Errorf("task-context eval: query %s has no grade-3 judgement", q.ID)
		}
	}
	return queries, nil
}

// ScoreTaskContextBundle applies the established SpanMatches rule to every
// emitted evidence citation and every grade-3 judgement. It does not inspect
// item ranks and does not parse evidence.Span: SpanMatches intentionally uses
// the citation's canonical path and declaration/start line.
func ScoreTaskContextBundle(q Query, bundle *contract.Result) (TaskContextScore, error) {
	if q.Stratum != StratumNLBehaviour || q.Split != SplitDev {
		return TaskContextScore{}, fmt.Errorf("task-context eval: refusing to score query %s from %s/%s", q.ID, q.Stratum, q.Split)
	}
	if bundle == nil {
		return TaskContextScore{}, fmt.Errorf("task-context eval: query %s returned a nil bundle", q.ID)
	}
	if err := contract.ValidateResult(bundle); err != nil {
		return TaskContextScore{}, fmt.Errorf("task-context eval: query %s invalid bundle: %w", q.ID, err)
	}
	grade3 := make([]Judgement, 0)
	for _, j := range q.Judgements {
		if j.Grade == GradeMax {
			grade3 = append(grade3, j)
		}
	}
	if len(grade3) == 0 {
		return TaskContextScore{}, fmt.Errorf("task-context eval: query %s has no grade-3 judgement", q.ID)
	}
	score := TaskContextScore{
		ItemCount:             len(bundle.Items),
		EvidenceCitationCount: len(bundle.Evidence),
		ItemCapApplied:        bundle.Limits.CapApplied,
		ItemsAvailable:        bundle.Limits.TotalAvailable,
		ItemsDropped:          bundle.Limits.Dropped,
		Truncated:             bundle.Limits.Truncated,
		Grade3Judgements:      len(grade3),
		Matches:               []TaskContextMatch{},
	}
	score.EngineReportedSnippetTokens, score.EngineReportedTokenBudget, _ = taskContextSummaryTokenCounts(bundle.Summary)
	for _, ev := range bundle.Evidence {
		if ev.Snippet != "" {
			score.SnippetCount++
			score.EmittedSnippetWhitespaceTokens += len(strings.Fields(ev.Snippet))
		}
		for _, j := range grade3 {
			if !SpanMatches(ev.Path, ev.Line, j) {
				continue
			}
			score.Matches = append(score.Matches, TaskContextMatch{
				EvidenceRefID:  ev.RefID,
				EvidencePath:   ev.Path,
				EvidenceLine:   ev.Line,
				EvidenceSpan:   ev.Span,
				EvidenceRole:   ev.Role,
				EvidenceClaim:  ev.ClaimType,
				JudgementPath:  j.Path,
				JudgementStart: j.StartLine,
				JudgementEnd:   j.EndLine,
				JudgementGrade: j.Grade,
			})
		}
	}
	score.EngineMinusEmittedSnippetTokens = score.EngineReportedSnippetTokens - score.EmittedSnippetWhitespaceTokens
	score.Covered = len(score.Matches) > 0
	return score, nil
}

// taskContextSummaryTokenCounts reads taskctx's own pre-shaping context-bundle
// accounting from its summary. That count is deliberately kept separate from
// the independent whitespace recount over snippets that remain in the emitted
// contract.Result evidence: evidence citation deduplication can make the two
// views disagree even though both use whitespace tokenization.
func taskContextSummaryTokenCounts(summary string) (used, budget int, ok bool) {
	const suffix = " snippet tokens"
	end := strings.Index(summary, suffix)
	if end < 0 {
		return 0, 0, false
	}
	prefix := summary[:end]
	start := strings.LastIndex(prefix, "; ")
	if start >= 0 {
		prefix = prefix[start+2:]
	}
	parts := strings.Split(prefix, "/")
	if len(parts) != 2 {
		return 0, 0, false
	}
	used, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, false
	}
	budget, err = strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, false
	}
	return used, budget, true
}

func taskContextAllPassed(checks []TaskContextEligibilityCheck) bool {
	if len(checks) == 0 {
		return false
	}
	for _, check := range checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

func taskContextRecomputeCommand() string {
	return `: "${SW264_AC9_MODEL_DIR:?set to your potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b model dir}"
: "${SW264_AC9_COBRA_ROOT:?set to the cobra checkout at a0a6ae020bb3899ff0276067863e50523f897370}"
GRAPHI_STATIC_MODEL_DIR="$SW264_AC9_MODEL_DIR" \
SW264_AC9_COBRA_ROOT="$SW264_AC9_COBRA_ROOT" \
SW264_AC9_MEASURE=1 CGO_ENABLED=0 \
go test ./internal/eval/retrieval -run '^TestSW264_AC9Measurement$' -count=1 -v`
}

type taskContextIndex struct {
	store            *graphstore.SQLiteStore
	search           *search.Service
	nodes            int
	edges            int
	files            int
	embedderID       string
	fingerprint      embed.Fingerprint
	generationID     embed.GenerationID
	persistedVectors int
}

type taskContextEngine interface {
	Retrieve(context.Context, engineretrieval.Request) (engineretrieval.Result, error)
}

// taskContextRetriever is the measurement's mechanical composition adapter.
// It records the concrete engine's last response so the harness can reject
// taskctx's intentional fallback after the call instead of mistaking it for a
// ready-path measurement.
type taskContextRetriever struct {
	engine taskContextEngine
	called int
	result resolve.RetrieverResult
	err    error
}

func (a *taskContextRetriever) Retrieve(ctx context.Context, req resolve.RetrieverRequest) (resolve.RetrieverResult, error) {
	a.called++
	res, err := a.engine.Retrieve(ctx, engineretrieval.Request{
		Query:      req.Query,
		Limit:      req.Limit,
		BudgetHint: req.Budget,
		Mode:       taskContextProductionMode(req.Mode),
	})
	if err != nil {
		a.err = err
		return resolve.RetrieverResult{}, err
	}
	out := resolve.RetrieverResult{
		Degradation: string(res.Degradation),
		Reason:      res.Reason,
		Rows:        make([]resolve.RetrieverRow, len(res.Rows)),
		Summary: resolve.RetrieverSummary{
			RetrievalVersion: res.Summary.RetrievalVersion,
			Strategy:         res.Summary.Strategy,
			WeightsHash:      res.Summary.WeightsHash,
			ModelFingerprint: res.Summary.ModelFingerprint,
			IndexFingerprint: res.Summary.IndexFingerprint,
			Query:            res.Summary.Query,
			Limit:            res.Summary.Limit,
			CandidateK:       res.Summary.CandidateK,
			RRFk:             res.Summary.RRFk,
			RRFScale:         res.Summary.RRFScale,
			MaxPerFile:       res.Summary.MaxPerFile,
		},
	}
	for i, row := range res.Rows {
		out.Rows[i] = resolve.RetrieverRow{
			NodeID:     row.NodeID,
			DocumentID: row.DocumentID,
			Path:       row.Path,
			Line:       taskContextLineFromSpan(row.Span),
			Span:       row.Span,
			Region:     row.Region,
			Explain: resolve.RetrieverExplain{
				LexicalRank:    row.Explain.LexicalRank,
				SemanticRank:   row.Explain.SemanticRank,
				RRF:            row.Explain.RRF,
				Graph:          row.Explain.Graph,
				Classification: row.Explain.Classification,
				Final:          row.Explain.Final,
			},
			Final: row.Explain.Final,
		}
	}
	a.result = out
	return out, nil
}

func taskContextProductionMode(mode int) engineretrieval.Mode {
	switch mode {
	case int(engineretrieval.ModeLexicalOnly):
		return engineretrieval.ModeLexicalOnly
	case int(engineretrieval.ModeSemanticRequired):
		return engineretrieval.ModeSemanticRequired
	default:
		return engineretrieval.ModeAuto
	}
}

func taskContextLineFromSpan(span string) int {
	for i := 0; i < len(span); i++ {
		if span[i] == '-' {
			n := 0
			for j := 0; j < i; j++ {
				if span[j] < '0' || span[j] > '9' {
					return 0
				}
				n = n*10 + int(span[j]-'0')
			}
			return n
		}
	}
	return 0
}

// RunTaskContextV2 indexes the pinned repository, generates and reloads a
// real semantic generation, composes engine/retrieval, and measures exactly
// the dev nl_behaviour population through taskctx.AssembleV2.
func RunTaskContextV2(ctx context.Context, o TaskContextOptions) (*TaskContextRun, error) {
	if o.Dataset == nil || o.Dataset.Dataset == nil {
		return nil, fmt.Errorf("task-context eval: no dataset")
	}
	datasetPinned := o.Dataset.SHA256 == o.DatasetSHA
	if !datasetPinned {
		return nil, fmt.Errorf("task-context eval: dataset sha256 %s, want pinned %s", o.Dataset.SHA256, o.DatasetSHA)
	}
	if strings.TrimSpace(o.RepoRoot) == "" || strings.TrimSpace(o.RepoSHA) == "" {
		return nil, fmt.Errorf("task-context eval: repository root and sha are required")
	}
	if strings.TrimSpace(o.EmbedderSelector) == "" {
		return nil, fmt.Errorf("task-context eval: a production embedder selector is required")
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	queries, err := SelectTaskContextDevNLBehaviour(o.Dataset.Dataset)
	if err != nil {
		return nil, err
	}
	selectedPopulation := len(queries) > 0
	for _, q := range queries {
		selectedPopulation = selectedPopulation && q.Stratum == StratumNLBehaviour && q.Split == SplitDev
	}
	slice := &Dataset{
		SchemaVersion:    o.Dataset.Dataset.SchemaVersion,
		ID:               o.Dataset.Dataset.ID + "-dev-nl-behaviour",
		Repo:             o.Dataset.Dataset.Repo,
		RepoSHA:          o.Dataset.Dataset.RepoSHA,
		Language:         o.Dataset.Dataset.Language,
		EvidenceClass:    o.Dataset.Dataset.EvidenceClass,
		RelevantMinGrade: o.Dataset.Dataset.RelevantMinGrade,
		Notes:            "Derived measurement population: every and only split=dev, stratum=nl_behaviour query from the cited source dataset; holdout is excluded.",
		Queries:          append([]Query(nil), queries...),
	}
	if err := slice.Validate(); err != nil {
		return nil, fmt.Errorf("task-context eval: measured slice: %w", err)
	}
	if err := CheckSpanCoverage(o.RepoRoot, slice); err != nil {
		return nil, fmt.Errorf("task-context eval: measured slice span coverage: %w", err)
	}
	head, err := CheckoutHEAD(ctx, o.RepoRoot)
	if err != nil {
		return nil, err
	}
	repoPinned := strings.EqualFold(head, o.RepoSHA) && strings.EqualFold(head, o.Dataset.Dataset.RepoSHA)
	if !repoPinned {
		return nil, fmt.Errorf("task-context eval: checkout is at %s, option pins %s and dataset pins %s", head, o.RepoSHA, o.Dataset.Dataset.RepoSHA)
	}

	workDir := o.WorkDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "graphi-task-context-eval")
		if err != nil {
			return nil, fmt.Errorf("task-context eval: workdir: %w", err)
		}
		defer os.RemoveAll(workDir)
	}
	idx, err := buildTaskContextIndex(ctx, o.RepoRoot, workDir, o.EmbedderSelector, o.Log)
	if err != nil {
		return nil, err
	}
	defer idx.store.Close()
	productionStaticEmbedder := strings.HasPrefix(o.EmbedderSelector, "static:") && strings.HasPrefix(idx.embedderID, o.EmbedderSelector+":")
	semanticState := idx.search.SemanticState()
	semanticReady := semanticState.State == embed.StateReady
	if !semanticReady {
		return nil, fmt.Errorf("task-context eval: semantic state is %s, want ready; refusing lexical fallback", semanticState.State)
	}
	generationFingerprintMatched := semanticState.Requested.Canonical() == idx.fingerprint.Canonical()
	if !generationFingerprintMatched {
		return nil, fmt.Errorf("task-context eval: search service requested fingerprint does not equal the independently verified generation fingerprint")
	}
	generationReloadedReady := semanticReady && generationFingerprintMatched && idx.persistedVectors > 0 && idx.generationID != ""

	deps := resolve.Deps{Query: query.New(idx.store), Search: idx.search}
	realEngine := engineretrieval.New(deps, idx.search, idx.store)
	realRetrievalConstructed := realEngine != nil
	if !realRetrievalConstructed {
		return nil, fmt.Errorf("task-context eval: retrieval.New returned nil")
	}
	reader := enginecontext.NewRootedReader(o.RepoRoot)
	run := &TaskContextRun{DatasetSlice: slice, Raw: make([]TaskContextRawQuery, 0, len(queries))}
	measurement := &TaskContextMeasurement{
		FormatVersion:  TaskContextFormatVersion,
		HarnessVersion: TaskContextHarnessVersion,
		ScorerVersion:  TaskContextScorerVersion,
		Story:          "SW-264",
		AC:             "AC-9",
		Candidate:      o.Candidate,
		Dataset: TaskContextDatasetRef{
			ID:            o.Dataset.Dataset.ID,
			SourceFile:    filepath.ToSlash(o.Dataset.Path),
			SourceSHA256:  o.Dataset.SHA256,
			Stratum:       StratumNLBehaviour,
			Split:         SplitDev,
			QueryCount:    len(queries),
			Grade:         GradeMax,
			SelectionRule: "SelectTaskContextDevNLBehaviour: q.stratum == nl_behaviour AND q.split == dev; no caller-selectable split",
		},
		Repo: TaskContextRepoRef{Name: o.RepoName, SHA: head, Nodes: idx.nodes, Edges: idx.edges, Files: idx.files},
		Embedder: TaskContextEmbedderRef{
			Selector:         o.EmbedderSelector,
			ModelFingerprint: idx.embedderID,
			IndexFingerprint: idx.fingerprint.Canonical(),
			GenerationID:     string(idx.generationID),
			PersistedVectors: idx.persistedVectors,
			Dimension:        idx.fingerprint.Dim,
			SemanticState:    semanticState.State.String(),
		},
		Retrieval: TaskContextRetrievalRef{
			Construction: "engine/retrieval.New(resolve.Deps{Query, Search}, readySearchService, graphstore)",
			NonNil:       realRetrievalConstructed,
			Method:       "task_context/2 -> engine/retrieval ModeAuto (production adapter)",
		},
		Bundle: TaskContextBundleRef{
			MethodVersion: taskctx.MethodVersionV2,
			Reader:        "engine/context.RootedReader over the pinned cobra checkout",
			TokenBudget:   TaskContextTokenBudget,
			Scorer:        "internal/eval/retrieval.SpanMatches",
			MatchingRule:  TaskContextMatchingRule,
		},
		RecomputeCommand: taskContextRecomputeCommand(),
	}
	run.Measurement = measurement

	covered := 0
	truncated := 0
	state, version, strategy := "", "", ""
	observed := struct {
		Queries                int
		RetrievalCalledOnce    int
		RetrievalReady         int
		SemanticFirst          int
		RetrievalFingerprinted int
		ReadyBundle            int
		ReaderSnippets         int
		EngineBudgetReported   int
		EngineWithinBudget     int
		EmittedWithinBudget    int
		Scored                 int
		RawRecords             int
	}{}
	for _, q := range queries {
		observed.Queries++
		adapter := &taskContextRetriever{engine: realEngine}
		queryDeps := deps
		queryDeps.Retrieval = adapter
		bundle, err := taskctx.AssembleV2(ctx, taskctx.Params{
			Task:        q.Text,
			TokenBudget: TaskContextTokenBudget,
			Deps:        queryDeps,
			Reader:      reader,
		})
		if err != nil {
			return nil, fmt.Errorf("task-context eval: query %s AssembleV2: %w", q.ID, err)
		}
		calledOnce := adapter.called == 1
		if calledOnce {
			observed.RetrievalCalledOnce++
		}
		if !calledOnce {
			return nil, fmt.Errorf("task-context eval: query %s called the real retrieval instance %d times, want exactly 1", q.ID, adapter.called)
		}
		if adapter.err != nil {
			return nil, fmt.Errorf("task-context eval: query %s retrieval errored (%v); AssembleV2 would have fallen back", q.ID, adapter.err)
		}
		retrievalReady := adapter.result.Degradation == string(engineretrieval.StateReady)
		if retrievalReady {
			observed.RetrievalReady++
		}
		if !retrievalReady {
			return nil, fmt.Errorf("task-context eval: query %s retrieval state is %q, want ready; refusing fallback bundle", q.ID, adapter.result.Degradation)
		}
		semanticFirst := adapter.result.Summary.RetrievalVersion == engineretrieval.Version && adapter.result.Summary.Strategy == "semantic_first"
		if semanticFirst {
			observed.SemanticFirst++
		}
		if !semanticFirst {
			return nil, fmt.Errorf("task-context eval: query %s method is %s/%s, want %s/semantic_first", q.ID, adapter.result.Summary.RetrievalVersion, adapter.result.Summary.Strategy, engineretrieval.Version)
		}
		retrievalFingerprinted := adapter.result.Summary.ModelFingerprint != "" && adapter.result.Summary.IndexFingerprint != ""
		if retrievalFingerprinted {
			observed.RetrievalFingerprinted++
		}
		if !retrievalFingerprinted {
			return nil, fmt.Errorf("task-context eval: query %s ready retrieval omitted model/index fingerprint", q.ID)
		}
		readyBundle := strings.HasPrefix(bundle.Summary, taskctx.MethodVersionV2+":") && strings.Contains(bundle.Summary, "degradation: ready")
		if readyBundle {
			observed.ReadyBundle++
		}
		if !readyBundle {
			return nil, fmt.Errorf("task-context eval: query %s bundle summary does not attest %s with ready retrieval: %q", q.ID, taskctx.MethodVersionV2, bundle.Summary)
		}
		score, err := ScoreTaskContextBundle(q, bundle)
		if err != nil {
			return nil, err
		}
		observed.Scored++
		engineBudgetReported := score.EngineReportedTokenBudget == TaskContextTokenBudget
		if engineBudgetReported {
			observed.EngineBudgetReported++
		}
		if !engineBudgetReported {
			return nil, fmt.Errorf("task-context eval: query %s engine summary reported token budget %d, want %d", q.ID, score.EngineReportedTokenBudget, TaskContextTokenBudget)
		}
		engineWithinBudget := score.EngineReportedSnippetTokens <= score.EngineReportedTokenBudget
		if engineWithinBudget {
			observed.EngineWithinBudget++
		}
		if !engineWithinBudget {
			return nil, fmt.Errorf("task-context eval: query %s engine reported %d snippet tokens over budget %d", q.ID, score.EngineReportedSnippetTokens, score.EngineReportedTokenBudget)
		}
		emittedWithinBudget := score.EmittedSnippetWhitespaceTokens <= TaskContextTokenBudget
		if emittedWithinBudget {
			observed.EmittedWithinBudget++
		}
		if !emittedWithinBudget {
			return nil, fmt.Errorf("task-context eval: query %s emitted evidence recount is %d whitespace tokens over budget %d", q.ID, score.EmittedSnippetWhitespaceTokens, TaskContextTokenBudget)
		}
		readerExercised := score.SnippetCount > 0
		if readerExercised {
			observed.ReaderSnippets++
		}
		if !readerExercised {
			return nil, fmt.Errorf("task-context eval: query %s emitted no snippets; the Reader/token-budget dimension was not exercised", q.ID)
		}
		if score.Covered {
			covered++
		}
		if score.Truncated {
			truncated++
		}
		grade3 := make([]Judgement, 0)
		for _, j := range q.Judgements {
			if j.Grade == GradeMax {
				grade3 = append(grade3, j)
			}
		}
		run.Raw = append(run.Raw, TaskContextRawQuery{
			FormatVersion: TaskContextFormatVersion, HarnessVersion: TaskContextHarnessVersion,
			ScorerVersion: TaskContextScorerVersion, Query: q, Grade3: grade3,
			Retrieval: adapter.result, Bundle: bundle, Score: score,
		})
		observed.RawRecords++
		measurement.Dataset.QueryIDs = append(measurement.Dataset.QueryIDs, q.ID)
		measurement.Queries = append(measurement.Queries, TaskContextQueryResult{
			ID: q.ID, Stratum: q.Stratum, Split: q.Split, Covered: score.Covered,
			ItemCount: score.ItemCount, EvidenceCitationCount: score.EvidenceCitationCount,
			SnippetCount: score.SnippetCount, EmittedSnippetWhitespaceTokens: score.EmittedSnippetWhitespaceTokens,
			EngineReportedSnippetTokens: score.EngineReportedSnippetTokens, EngineReportedTokenBudget: score.EngineReportedTokenBudget,
			EngineMinusEmittedSnippetTokens: score.EngineMinusEmittedSnippetTokens,
			ItemCapApplied:                  score.ItemCapApplied, ItemsAvailable: score.ItemsAvailable,
			ItemsDropped: score.ItemsDropped, Truncated: score.Truncated, Grade3Judgements: score.Grade3Judgements,
			MatchCount: len(score.Matches),
		})
		version, strategy = adapter.result.Summary.RetrievalVersion, adapter.result.Summary.Strategy
		state = adapter.result.Degradation
	}
	measurement.Retrieval.State = state
	measurement.Retrieval.Version = version
	measurement.Retrieval.Strategy = strategy
	measurement.Aggregate = TaskContextAggregate{
		CoveredQueries:             covered,
		TotalQueries:               len(queries),
		Coverage:                   float64(covered) / float64(len(queries)),
		CoverageResolutionFraction: fmt.Sprintf("1/%d", len(queries)),
		CoverageResolution:         1 / float64(len(queries)),
		TruncatedQueries:           truncated,
		CoverageCostNote:           "grade3_span_coverage is binary hit coverage and excludes item, citation, token, and truncation cost; inspect per-query cost fields separately",
	}
	allQueries := len(queries)
	realRetrievalObserved := realRetrievalConstructed && observed.Queries == allQueries && observed.RetrievalCalledOnce == allQueries && observed.RetrievalReady == allQueries && observed.SemanticFirst == allQueries && observed.RetrievalFingerprinted == allQueries && observed.ReadyBundle == allQueries
	readerAndBudgetObserved := observed.ReaderSnippets == allQueries && observed.EngineBudgetReported == allQueries && observed.EngineWithinBudget == allQueries && observed.EmittedWithinBudget == allQueries
	scorerObserved := observed.Scored == allQueries && len(measurement.Queries) == allQueries
	rawObserved := observed.RawRecords == allQueries && len(run.Raw) == allQueries
	candidateProvenance := o.Candidate.SHA != "" && o.Candidate.BaseSHA != "" && o.Candidate.DiffSHA256 != "" && len(o.Candidate.SourceFiles) > 0 && measurement.RecomputeCommand != ""
	for _, source := range o.Candidate.SourceFiles {
		candidateProvenance = candidateProvenance && source.File != "" && source.SHA256 != ""
	}
	measurement.Eligibility = []TaskContextEligibilityCheck{
		{Name: "pinned_dataset", Passed: datasetPinned, Detail: fmt.Sprintf("observed source sha256 %s; required %s", o.Dataset.SHA256, o.DatasetSHA)},
		{Name: "dev_nl_behaviour_only", Passed: selectedPopulation, Detail: fmt.Sprintf("observed %d/%d selected records with split=dev and stratum=nl_behaviour", len(queries), len(queries))},
		{Name: "pinned_repo", Passed: repoPinned, Detail: fmt.Sprintf("observed checkout HEAD %s; option and dataset both require it", head)},
		{Name: "production_static_embedder", Passed: productionStaticEmbedder, Detail: fmt.Sprintf("selector %s produced model fingerprint %s", o.EmbedderSelector, idx.embedderID)},
		{Name: "generation_reloaded_ready", Passed: generationReloadedReady, Detail: fmt.Sprintf("observed %d persisted vectors; generation %s; state %s; requested fingerprint matched=%t", idx.persistedVectors, idx.generationID, semanticState.State, generationFingerprintMatched)},
		{Name: "real_retrieval_instance", Passed: realRetrievalObserved, Detail: fmt.Sprintf("constructed non-nil=%t; called once %d/%d; ready %d/%d; %s/semantic_first %d/%d; fingerprinted %d/%d; ready bundle %d/%d", realRetrievalConstructed, observed.RetrievalCalledOnce, allQueries, observed.RetrievalReady, allQueries, engineretrieval.Version, observed.SemanticFirst, allQueries, observed.RetrievalFingerprinted, allQueries, observed.ReadyBundle, allQueries)},
		{Name: "rooted_reader_and_1200_token_budget", Passed: readerAndBudgetObserved, Detail: fmt.Sprintf("snippets %d/%d; engine reported budget 1200 for %d/%d and stayed within it %d/%d; emitted-evidence whitespace recount stayed within 1200 for %d/%d", observed.ReaderSnippets, allQueries, observed.EngineBudgetReported, allQueries, observed.EngineWithinBudget, allQueries, observed.EmittedWithinBudget, allQueries)},
		{Name: "established_span_scorer", Passed: scorerObserved, Detail: fmt.Sprintf("%d/%d queries scored by ScoreTaskContextBundle, which calls SpanMatches for every evidence/grade-3 pair", observed.Scored, allQueries)},
		{Name: "raw_bundles_and_matches", Passed: rawObserved, Detail: fmt.Sprintf("observed %d/%d raw bundle, judgement, and match records prepared for content-addressed export", observed.RawRecords, allQueries)},
		{Name: "complete_provenance", Passed: candidateProvenance, Detail: fmt.Sprintf("candidate sha=%s; base sha=%s; diff sha256=%s; source digests=%d; exact recomputation command present=%t", o.Candidate.SHA, o.Candidate.BaseSHA, o.Candidate.DiffSHA256, len(o.Candidate.SourceFiles), measurement.RecomputeCommand != "")},
	}
	measurement.EligibleForThreshold = taskContextAllPassed(measurement.Eligibility)
	return run, nil
}

func buildTaskContextIndex(ctx context.Context, root, workDir, selector string, log io.Writer) (*taskContextIndex, error) {
	dbPath := filepath.Join(workDir, "task-context-eval.db")
	metaDir := filepath.Join(workDir, "task-context-eval-meta")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: open store: %w", err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = store.Close()
		}
	}()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: ingest.New: %w", err)
	}
	fmt.Fprintf(log, "task-context eval: indexing %s\n", root)
	if err := ing.IngestAll(ctx, root); err != nil {
		_ = ing.Close()
		return nil, fmt.Errorf("task-context eval: index %s: %w", root, err)
	}
	if err := ing.Close(); err != nil {
		return nil, fmt.Errorf("task-context eval: close ingester: %w", err)
	}
	stats, err := store.BriefStats(ctx, 0)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: inventory: %w", err)
	}
	if stats.TotalNodes == 0 {
		return nil, fmt.Errorf("task-context eval: index produced no nodes")
	}

	emb, err := embed.Constructor(selector, embed.DefaultConstructors())
	if err != nil || emb == nil {
		return nil, fmt.Errorf("task-context eval: embedder %q unavailable: %v", selector, err)
	}
	reg := embed.NewRegistry()
	if err := reg.Register(emb); err != nil {
		return nil, fmt.Errorf("task-context eval: embedder register: %w", err)
	}
	reg.Freeze()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("task-context eval: enumerate nodes: %w", err)
	}
	embedsource.SortNodesByPath(nodes)
	docs := embedsource.NewFileDocumentSource(ctx, root, emb)
	graphGen, err := graphGenerationFromStore(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: graph identity: %w", err)
	}
	genStore, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: generation store open: %w", err)
	}
	generated, genErr := embed.GenerateAndPersist(ctx, reg, nodes, docs, embed.NewIndex(), genStore, graphGen)
	closeErr := genStore.Close()
	if genErr != nil {
		return nil, fmt.Errorf("task-context eval: generate+persist: %w", genErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("task-context eval: generation store close: %w", closeErr)
	}
	if generated.Failed != 0 || generated.Embedded+generated.Reused == 0 {
		return nil, fmt.Errorf("task-context eval: invalid generation result embedded=%d reused=%d excluded=%d failed=%d", generated.Embedded, generated.Reused, generated.Excluded, generated.Failed)
	}

	fp := taskContextFingerprint(emb, graphGen)
	reloadStore, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: generation store reopen: %w", err)
	}
	defer reloadStore.Close()
	gen, state, err := reloadStore.Active(ctx, fp, embed.NodeReferencerFromGraphLookup(store.GetNode))
	if err != nil {
		return nil, fmt.Errorf("task-context eval: active generation validation: %w", err)
	}
	if state != embed.StateReady || gen.ID == "" {
		return nil, fmt.Errorf("task-context eval: active generation state is %s (id=%s), want ready; refusing fallback", state, gen.ID)
	}
	if gen.Fingerprint.Canonical() != fp.Canonical() {
		return nil, fmt.Errorf("task-context eval: active generation fingerprint differs from independently reconstructed fingerprint")
	}
	rows, err := reloadStore.Load(ctx, gen.ID)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: reload generation: %w", err)
	}
	if len(rows) != gen.RowCount {
		return nil, fmt.Errorf("task-context eval: loaded %d vectors, generation records %d", len(rows), gen.RowCount)
	}
	index := embed.NewIndex()
	vecs := make([]embed.Vector, len(rows))
	for i, row := range rows {
		vecs[i] = embed.Vector{NodeID: row.NodeID, DocumentID: row.DocumentID, Values: row.Vector}
	}
	if err := index.Rebuild(ctx, vecs); err != nil {
		return nil, fmt.Errorf("task-context eval: vector index rebuild: %w", err)
	}
	svc := search.New(store).WithSemantic(reg, index, store).WithSemanticState(search.SemanticState{
		State: embed.StateReady, Requested: fp, Reason: search.ReasonForState(embed.StateReady),
	})
	probe, err := svc.SemanticSearch(ctx, "task context readiness probe", 1)
	if err != nil {
		return nil, fmt.Errorf("task-context eval: semantic readiness probe: %w", err)
	}
	if !probe.Available {
		return nil, fmt.Errorf("task-context eval: semantic readiness probe unavailable: %s", probe.Reason)
	}
	fmt.Fprintf(log, "task-context eval: semantic ready (model=%s, dim=%d, persisted_vectors=%d, generation=%s)\n", emb.ID(), fp.Dim, len(rows), gen.ID)
	closeOnError = false
	return &taskContextIndex{
		store: store, search: svc, nodes: stats.TotalNodes, edges: stats.TotalEdges, files: len(stats.Files),
		embedderID: emb.ID(), fingerprint: fp, generationID: gen.ID, persistedVectors: len(rows),
	}, nil
}

func taskContextFingerprint(emb embed.Embedder, graphGeneration string) embed.Fingerprint {
	fp := embed.Fingerprint{
		ModelID: emb.ID(), Dim: emb.Dim(), DocumentSchema: embed.DocumentSchema,
		GraphGeneration: graphGeneration,
	}
	if v, ok := emb.(interface{ Revision() string }); ok {
		fp.Revision = v.Revision()
	}
	if v, ok := emb.(interface{ ModelSHA256() string }); ok {
		fp.ModelSHA256 = v.ModelSHA256()
	}
	if v, ok := emb.(interface{ TokenizerSHA256() string }); ok {
		fp.TokenizerSHA256 = v.TokenizerSHA256()
	}
	if v, ok := emb.(interface{ ChunkerConfig() string }); ok {
		fp.ChunkerConfig = v.ChunkerConfig()
	}
	return fp
}

// WriteTaskContextRunDir writes the checked-in run directory: a published
// measurement, the measured dev slice, raw per-query bundles+matches, README,
// and a content-addressed run.json index.
func WriteTaskContextRunDir(dir string, run *TaskContextRun) error {
	if run == nil || run.Measurement == nil || run.DatasetSlice == nil || len(run.Raw) == 0 {
		return fmt.Errorf("task-context eval: nothing to export")
	}
	derivedEligibility := taskContextAllPassed(run.Measurement.Eligibility)
	if run.Measurement.EligibleForThreshold != derivedEligibility {
		return fmt.Errorf("task-context eval: eligible_for_threshold=%t disagrees with all(eligibility.passed)=%t", run.Measurement.EligibleForThreshold, derivedEligibility)
	}
	if err := os.MkdirAll(filepath.Join(dir, "raw"), 0o755); err != nil {
		return fmt.Errorf("task-context eval: create run dir: %w", err)
	}
	datasetBytes, err := taskContextMarshal(run.DatasetSlice)
	if err != nil {
		return err
	}
	datasetFile := "dataset-dev-nl-behaviour.json"
	if err := os.WriteFile(filepath.Join(dir, datasetFile), datasetBytes, 0o644); err != nil {
		return fmt.Errorf("task-context eval: write dataset slice: %w", err)
	}
	run.Measurement.Dataset.MeasuredSliceFile = datasetFile
	run.Measurement.Dataset.MeasuredSliceSHA256 = SHA256Hex(datasetBytes)

	files := []TaskContextFileDigest{{File: datasetFile, SHA256: SHA256Hex(datasetBytes)}}
	if len(run.Raw) != len(run.Measurement.Queries) {
		return fmt.Errorf("task-context eval: %d raw queries for %d aggregate queries", len(run.Raw), len(run.Measurement.Queries))
	}
	for i := range run.Raw {
		name := filepath.ToSlash(filepath.Join("raw", run.Raw[i].Query.ID+".json"))
		raw, err := taskContextMarshal(run.Raw[i])
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), raw, 0o644); err != nil {
			return fmt.Errorf("task-context eval: write %s: %w", name, err)
		}
		digest := SHA256Hex(raw)
		run.Measurement.Queries[i].RawFile = name
		run.Measurement.Queries[i].RawSHA256 = digest
		files = append(files, TaskContextFileDigest{File: name, SHA256: digest})
	}
	measurementBytes, err := taskContextMarshal(run.Measurement)
	if err != nil {
		return err
	}
	measurementFile := "measurement.json"
	if err := os.WriteFile(filepath.Join(dir, measurementFile), measurementBytes, 0o644); err != nil {
		return fmt.Errorf("task-context eval: write measurement: %w", err)
	}
	files = append(files, TaskContextFileDigest{File: measurementFile, SHA256: SHA256Hex(measurementBytes)})

	readme := taskContextREADME(run.Measurement)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		return fmt.Errorf("task-context eval: write README: %w", err)
	}
	files = append(files, TaskContextFileDigest{File: "README.md", SHA256: SHA256Hex([]byte(readme))})
	sort.Slice(files, func(i, j int) bool { return files[i].File < files[j].File })
	index := struct {
		FormatVersion  int                     `json:"format_version"`
		HarnessVersion string                  `json:"harness_version"`
		ScorerVersion  string                  `json:"scorer_version"`
		Measurement    string                  `json:"measurement"`
		Dataset        string                  `json:"dataset"`
		Files          []TaskContextFileDigest `json:"files"`
		Notes          string                  `json:"notes"`
	}{
		FormatVersion: TaskContextFormatVersion, HarnessVersion: TaskContextHarnessVersion,
		ScorerVersion: TaskContextScorerVersion, Measurement: measurementFile, Dataset: datasetFile,
		Files: files,
		Notes: "SW-264 task_context/2 AC-9 run directory. measurement.json is the aggregate and provenance record; dataset-dev-nl-behaviour.json contains only the measured dev stratum; raw/<query>.json contains the exact bundle and SpanMatches pairs. File digests are over the bytes on disk.",
	}
	indexBytes, err := taskContextMarshal(index)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), indexBytes, 0o644); err != nil {
		return fmt.Errorf("task-context eval: write run index: %w", err)
	}
	return nil
}

func taskContextMarshal(v any) ([]byte, error) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("task-context eval: marshal artifact: %w", err)
	}
	return append(raw, '\n'), nil
}

func taskContextREADME(m *TaskContextMeasurement) string {
	var costs strings.Builder
	for _, q := range m.Queries {
		fmt.Fprintf(&costs, "| %s | %d | %d | %d | %d | %d | %d | %t |\n",
			q.ID, q.ItemCount, q.EvidenceCitationCount, q.EmittedSnippetWhitespaceTokens,
			q.EngineReportedSnippetTokens, q.ItemCapApplied, q.ItemsDropped, q.Truncated)
	}
	return fmt.Sprintf(`# SW-264 AC-9 — task_context/2 production-static measurement

This run measures every dev query in the pinned cobra-v1 nl_behaviour stratum. It does not use or copy holdout queries. The measured population contains %d queries and the observed grade-3 span coverage is %d/%d (%.6f).

The coverage resolution is %s (%.6f): one query changes the aggregate by that amount. Coverage is a hit metric, not a cost metric. The bundle cost observed alongside it was:

| Query | Items | Evidence citations | Emitted snippet whitespace tokens | Engine-reported snippet tokens | Item cap | Items dropped | Truncated |
|---|---:|---:|---:|---:|---:|---:|:---:|
%s
The two token columns are intentionally distinct. The emitted count independently recounts whitespace-separated fields in snippet evidence remaining in the final contract.Result. The engine-reported count is taskctx's context-bundle accounting parsed from its summary before snippet citations are deduplicated into the final evidence set. Both use whitespace tokenization, but they count different populations and can disagree. Neither count is substituted for the other.

The run used a non-nil instance returned by engine/retrieval.New, the production static embedder %s, a persisted generation reloaded and independently validated as ready (%d vectors), a RootedReader over cobra at %s, and task_context/2 with TokenBudget=1200. Each per-query record contains the exact contract.Result bundle and every evidence/judgement pair for which internal/eval/retrieval.SpanMatches returned true.

eligible_for_threshold is %t because every eligibility check in measurement.json passed. This is measurement evidence for SW-266; it does not set or instruct a threshold.

## Recompute

Set SW264_AC9_MODEL_DIR to the pinned static model directory and SW264_AC9_COBRA_ROOT to the pinned cobra checkout. The command fails before running if either input is missing.

~~~bash
%s
~~~

run.json content-addresses measurement.json, the dev-only dataset slice, this README, and each raw query record.
`, m.Dataset.QueryCount, m.Aggregate.CoveredQueries, m.Aggregate.TotalQueries, m.Aggregate.Coverage,
		m.Aggregate.CoverageResolutionFraction, m.Aggregate.CoverageResolution, costs.String(),
		m.Embedder.ModelFingerprint, m.Embedder.PersistedVectors, m.Repo.SHA, m.EligibleForThreshold, m.RecomputeCommand)
}
