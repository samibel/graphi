package main

// SW-278: task_context/2 is a Labs operation, so its hero scenario cannot
// enter corpus/hero without breaking that suite's exactly-twenty, Stable-only
// contract. This file owns the separate corpus/labs-hero suite and makes the
// semantic requirement fail closed: no embedder, missing model bytes, or any
// non-ready generation is a test failure, never a skip or accepted fallback.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/embed"
	staticembed "github.com/samibel/graphi/engine/embed/static"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/coverage"
	"github.com/samibel/graphi/internal/embedsource"
	evalretrieval "github.com/samibel/graphi/internal/eval/retrieval"
)

const labsHeroDisableEmbedderEnv = "GRAPHI_LABS_HERO_DISABLE_EMBEDDER"

type labsHeroProvenance struct {
	SchemaVersion        int                  `json:"schema_version"`
	ScenarioID           string               `json:"scenario_id"`
	FixtureRef           string               `json:"fixture_ref"`
	FixtureRoot          string               `json:"fixture_root"`
	Dataset              labsHeroDatasetRef   `json:"dataset"`
	AnswerSpan           labsHeroAnswerSpan   `json:"answer_span"`
	RequiredSnippetPaths []string             `json:"required_snippet_paths"`
	Embedder             labsHeroEmbedderRef  `json:"embedder"`
	Files                []labsHeroFileDigest `json:"files"`
}

type labsHeroDatasetRef struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	QueryID string `json:"query_id"`
}

type labsHeroAnswerSpan struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	SHA256    string `json:"sha256"`
}

type labsHeroEmbedderRef struct {
	Selector string            `json:"selector"`
	Revision string            `json:"revision"`
	Files    map[string]string `json:"files"`
}

type labsHeroFileDigest struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type labsHeroEngine struct {
	deps               resolve.Deps
	reader             enginecontext.Reader
	retrieval          *evalretrieval.TaskContextRetriever
	taskContextV2Calls int
	bundle             *contract.Result
}

var (
	_ scenario.Engine          = (*labsHeroEngine)(nil)
	_ scenario.ContractInvoker = (*labsHeroEngine)(nil)
)

func (e *labsHeroEngine) Invoke(string, map[string]string) ([]string, *float64, error) {
	return nil, nil, fmt.Errorf("labs hero: only the contract operation task_context/2 is supported")
}

func (e *labsHeroEngine) InvokeContract(ctx context.Context, operation string, args map[string]string) (*contract.Result, error) {
	if operation != scenario.OpTaskContext || args["version"] != "2" {
		return nil, fmt.Errorf("labs hero: operation is %s/%s, want task_context/2", operation, args["version"])
	}
	budget, err := strconv.Atoi(args["token_budget"])
	if err != nil {
		return nil, fmt.Errorf("labs hero: invalid token_budget %q: %w", args["token_budget"], err)
	}
	e.taskContextV2Calls++
	e.bundle, err = taskctx.AssembleV2(ctx, taskctx.Params{
		Task:        args["task"],
		TokenBudget: budget,
		Deps:        e.deps,
		Reader:      e.reader,
	})
	return e.bundle, err
}

// TestLabsHeroSuite_RelationOnlyBundleRejected is AC-4's bite test. The
// synthetic bundle cites a line inside the reviewed answer interval but
// carries no source bytes; the same assertion used by the live suite must
// reject it.
func TestLabsHeroSuite_RelationOnlyBundleRejected(t *testing.T) {
	root := repoRoot(t)
	p := loadLabsHeroProvenance(t, root)
	answerText := readLabsHeroSpan(t, filepath.Join(root, filepath.FromSlash(p.FixtureRoot)), p.AnswerSpan)
	relationOnly := &contract.Result{Evidence: []contract.Evidence{{
		RefID: "relation-only", Path: p.AnswerSpan.Path, Line: p.AnswerSpan.StartLine,
		Role: "relation", ClaimType: "graph_relation", EdgeTier: "confirmed",
	}}}
	err := requireAnswerBearingSnippet(relationOnly, p.AnswerSpan, answerText)
	if err == nil {
		t.Fatal("relation-only bundle passed the answer-bearing assertion")
	}
	t.Logf("relation-only negative control rejected: %v", err)
}

// TestLabsHeroSuite_StableTwentyRemainStable is AC-2's self-protection:
// corpus/hero remains exactly twenty scenarios and every operation in it is
// from the frozen Stable set. The Labs scenario is also checked to be outside
// that set, so moving it into corpus/hero cannot be papered over by relaxing
// a count alone.
func TestLabsHeroSuite_StableTwentyRemainStable(t *testing.T) {
	heroes := loadHeroScenarios(t)
	if len(heroes) != 20 {
		t.Fatalf("corpus/hero has %d scenarios, want exactly 20", len(heroes))
	}
	stable := map[string]bool{}
	for _, op := range coverage.CanonicalStableOps() {
		stable[op] = true
	}
	for _, hero := range heroes {
		if !stable[hero.Operation.Name] {
			t.Errorf("corpus/hero scenario %s uses Labs/non-Stable operation %q", hero.ID, hero.Operation.Name)
		}
	}
	labs := loadSingleLabsHeroScenario(t, repoRoot(t))
	if stable[labs.Operation.Name] {
		t.Fatalf("Labs hero operation %q unexpectedly belongs to the frozen Stable set", labs.Operation.Name)
	}
}

// TestLabsHeroSuite_CIGateUsesVerifiedArtifact keeps the model-backed hero
// from becoming a machine-local test. The ordinary suite deliberately omits
// the labs_hero build tag; this dedicated job receives SW-271's single
// producer artifact, verifies the pins again, and runs the tagged gate.
func TestLabsHeroSuite_CIGateUsesVerifiedArtifact(t *testing.T) {
	workflowPath := filepath.Join(repoRoot(t), ".github", "workflows", "static-embedder-cross-arch.yml")
	raw, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read Labs hero workflow: %v", err)
	}
	const header = "\n  labs-hero:\n"
	start := strings.Index(string(raw), header)
	if start < 0 {
		t.Fatal("static-embedder-cross-arch workflow has no labs-hero job")
	}
	job := string(raw[start:])
	for _, required := range []string{
		"needs: prepare-model",
		"name: static-model-${{ github.run_id }}",
		`go run ./cmd/static-embedder-archcheck verify -model-dir "$RUNNER_TEMP/static-model"`,
		"GRAPHI_STATIC_MODEL_DIR: ${{ runner.temp }}/static-model",
		`go test -tags labs_hero ./cmd/eval -run '^TestLabsHeroSuite_' -count=1 -v`,
	} {
		if !strings.Contains(job, required) {
			t.Errorf("Labs hero CI job is missing %q", required)
		}
	}
	if strings.Contains(job, "continue-on-error") {
		t.Error("Labs hero CI job must not tolerate failure")
	}
}

func loadSingleLabsHeroScenario(t *testing.T, root string) scenario.Scenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "corpus", "labs-hero", "*.yaml"))
	if err != nil {
		t.Fatalf("glob corpus/labs-hero: %v", err)
	}
	sort.Strings(files)
	if len(files) != 1 {
		t.Fatalf("corpus/labs-hero has %d scenarios, want exactly one", len(files))
	}
	s, err := scenario.LoadScenario(files[0])
	if err != nil {
		t.Fatalf("load Labs hero scenario: %v", err)
	}
	return s
}

func loadLabsHeroProvenance(t *testing.T, root string) labsHeroProvenance {
	t.Helper()
	path := filepath.Join(root, "corpus", "labs-hero", "provenance", "fixture.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Labs hero provenance: %v", err)
	}
	var p labsHeroProvenance
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("parse Labs hero provenance: %v", err)
	}
	return p
}

func verifyLabsHeroProvenance(t *testing.T, root string, s scenario.Scenario, p labsHeroProvenance) (evalretrieval.Query, string) {
	t.Helper()
	if p.SchemaVersion != 1 {
		t.Fatalf("Labs hero provenance schema_version=%d, want 1", p.SchemaVersion)
	}
	if p.ScenarioID != s.ID || p.FixtureRef != s.FixtureRef {
		t.Fatalf("Labs hero provenance identifies %s/%s, scenario identifies %s/%s", p.ScenarioID, p.FixtureRef, s.ID, s.FixtureRef)
	}
	if s.Operation.Name != scenario.OpTaskContext || s.Operation.Args["version"] != "2" {
		t.Fatalf("Labs hero operation is %s/%s, want task_context/2", s.Operation.Name, s.Operation.Args["version"])
	}
	if s.Operation.Args["token_budget"] != strconv.Itoa(evalretrieval.TaskContextTokenBudget) {
		t.Fatalf("Labs hero token_budget=%q, want %d", s.Operation.Args["token_budget"], evalretrieval.TaskContextTokenBudget)
	}

	if p.Embedder.Selector != staticembed.PinnedSelector || p.Embedder.Revision != staticembed.PinnedRevision {
		t.Fatalf("Labs hero embedder provenance is %s@%s, want %s@%s", p.Embedder.Selector, p.Embedder.Revision, staticembed.PinnedSelector, staticembed.PinnedRevision)
	}
	if len(p.Embedder.Files) != len(staticembed.PinnedSHA256) {
		t.Fatalf("Labs hero records %d model files, pin table has %d", len(p.Embedder.Files), len(staticembed.PinnedSHA256))
	}
	for name, want := range staticembed.PinnedSHA256 {
		if got := p.Embedder.Files[name]; got != want {
			t.Fatalf("Labs hero model pin %s=%q, want %q", name, got, want)
		}
	}

	datasetPath := filepath.Join(root, filepath.FromSlash(p.Dataset.Path))
	loaded, err := evalretrieval.LoadDataset(datasetPath)
	if err != nil {
		t.Fatalf("load Labs hero dataset provenance: %v", err)
	}
	if loaded.SHA256 != p.Dataset.SHA256 {
		t.Fatalf("Labs hero dataset sha256=%s, want %s", loaded.SHA256, p.Dataset.SHA256)
	}
	var q *evalretrieval.Query
	for i := range loaded.Dataset.Queries {
		if loaded.Dataset.Queries[i].ID == p.Dataset.QueryID {
			q = &loaded.Dataset.Queries[i]
			break
		}
	}
	if q == nil {
		t.Fatalf("Labs hero dataset has no query %q", p.Dataset.QueryID)
	}
	wantTask := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s.Operation.Args["task"]), "?"))
	if strings.ToLower(strings.TrimSpace(q.Text)) != wantTask {
		t.Fatalf("Labs hero task %q does not match provenanced query %q", s.Operation.Args["task"], q.Text)
	}
	if q.Stratum != evalretrieval.StratumNLBehaviour || q.Split != evalretrieval.SplitDev {
		t.Fatalf("Labs hero query %s is %s/%s, want %s/%s", q.ID, q.Stratum, q.Split, evalretrieval.StratumNLBehaviour, evalretrieval.SplitDev)
	}
	var answerJudgement bool
	relevant := map[string]bool{}
	for _, path := range p.RequiredSnippetPaths {
		relevant[path] = false
	}
	for _, j := range q.Judgements {
		if j.Grade == evalretrieval.GradeMax && j.Path == p.AnswerSpan.Path && j.StartLine <= p.AnswerSpan.StartLine && j.EndLine >= p.AnswerSpan.EndLine {
			answerJudgement = true
		}
		if _, ok := relevant[j.Path]; ok {
			relevant[j.Path] = true
		}
	}
	if !answerJudgement {
		t.Fatalf("Labs hero answer %s:%d-%d is not inside a reviewed grade-3 judgement", p.AnswerSpan.Path, p.AnswerSpan.StartLine, p.AnswerSpan.EndLine)
	}
	for path, found := range relevant {
		if !found {
			t.Fatalf("Labs hero required context %s is not a provenanced judgement for query %s", path, q.ID)
		}
	}

	fixtureRoot := filepath.Join(root, filepath.FromSlash(p.FixtureRoot))
	verifyLabsHeroFixtureFiles(t, fixtureRoot, p.Files)
	answerText := readLabsHeroSpan(t, fixtureRoot, p.AnswerSpan)
	if got := sha256Hex([]byte(answerText)); got != p.AnswerSpan.SHA256 {
		t.Fatalf("Labs hero answer span sha256=%s, want %s", got, p.AnswerSpan.SHA256)
	}
	return *q, answerText
}

func verifyLabsHeroFixtureFiles(t *testing.T, fixtureRoot string, pinned []labsHeroFileDigest) {
	t.Helper()
	want := make([]string, 0, len(pinned))
	seen := map[string]bool{}
	for _, file := range pinned {
		clean := filepath.ToSlash(filepath.Clean(file.Path))
		if clean != file.Path || clean == "." || strings.HasPrefix(clean, "../") || seen[clean] {
			t.Fatalf("Labs hero provenance has invalid or duplicate fixture path %q", file.Path)
		}
		seen[clean] = true
		want = append(want, clean)
		raw, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(clean)))
		if err != nil {
			t.Fatalf("read Labs hero fixture %s: %v", clean, err)
		}
		if got := sha256Hex(raw); got != file.SHA256 {
			t.Fatalf("Labs hero fixture %s sha256=%s, want %s", clean, got, file.SHA256)
		}
	}
	sort.Strings(want)
	var got []string
	err := filepath.WalkDir(fixtureRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		got = append(got, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("enumerate Labs hero fixture: %v", err)
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Labs hero fixture file set drifted\n got: %v\nwant: %v", got, want)
	}
}

func readLabsHeroSpan(t *testing.T, fixtureRoot string, span labsHeroAnswerSpan) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureRoot, filepath.FromSlash(span.Path)))
	if err != nil {
		t.Fatalf("read Labs hero answer source: %v", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	if span.StartLine < 1 || span.EndLine < span.StartLine || span.EndLine > len(lines) {
		t.Fatalf("Labs hero answer span %s:%d-%d is outside %d lines", span.Path, span.StartLine, span.EndLine, len(lines))
	}
	return strings.Join(lines[span.StartLine-1:span.EndLine], "\n")
}

func buildLabsHeroEngine(ctx context.Context, root string, p labsHeroProvenance, disableEmbedder bool) (*labsHeroEngine, func(), error) {
	store := graphstore.NewMemStore()
	cleanup := func() { _ = store.Close() }
	fixtureRoot := filepath.Join(root, filepath.FromSlash(p.FixtureRoot))
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), "")
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if err := ing.IngestAll(ctx, fixtureRoot); err != nil {
		_ = ing.Close()
		cleanup()
		return nil, func() {}, fmt.Errorf("index fixture: %w", err)
	}
	if err := ing.Close(); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("close fixture ingester: %w", err)
	}

	searchService := search.New(store)
	if !disableEmbedder {
		emb, err := embed.Constructor(p.Embedder.Selector, embed.DefaultConstructors())
		if err != nil || emb == nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("construct pinned embedder %q: %v", p.Embedder.Selector, err)
		}
		reg := embed.NewRegistry()
		if err := reg.Register(emb); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("register pinned embedder: %w", err)
		}
		reg.Freeze()
		nodes, err := store.Nodes(ctx, graphstore.Query{})
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("enumerate fixture nodes: %w", err)
		}
		embedsource.SortNodesByPath(nodes)
		index := embed.NewIndex()
		generations := embed.NewMemGenerationStore()
		graphGeneration := "labs-hero:" + p.Dataset.SHA256
		generated, err := embed.GenerateAndPersist(ctx, reg, nodes, embedsource.NewFileDocumentSource(ctx, fixtureRoot, emb), index, generations, graphGeneration)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("pinned embedder unavailable or generation failed: %w", err)
		}
		if generated.Embedded == 0 || generated.Failed != 0 || generated.GenerationID == "" {
			cleanup()
			return nil, func() {}, fmt.Errorf("semantic generation embedded=%d failed=%d generation=%q", generated.Embedded, generated.Failed, generated.GenerationID)
		}
		fp := labsHeroFingerprint(emb, graphGeneration)
		generation, state, err := generations.Active(ctx, fp, embed.NodeReferencerFromGraphLookup(store.GetNode))
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("validate semantic generation: %w", err)
		}
		if state != embed.StateReady || generation.ID == "" {
			cleanup()
			return nil, func() {}, fmt.Errorf("semantic generation state=%s id=%q, want ready", state, generation.ID)
		}
		rows, err := generations.Load(ctx, generation.ID)
		if err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("reload semantic generation: %w", err)
		}
		vectors := make([]embed.Vector, len(rows))
		for i, row := range rows {
			vectors[i] = embed.Vector{NodeID: row.NodeID, DocumentID: row.DocumentID, Values: row.Vector}
		}
		reloaded := embed.NewIndex()
		if err := reloaded.Rebuild(ctx, vectors); err != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("rebuild semantic index: %w", err)
		}
		searchService.WithSemantic(reg, reloaded, store).WithSemanticState(search.SemanticState{
			State: embed.StateReady, Requested: fp, Reason: search.ReasonForState(embed.StateReady),
		})
	}

	deps := resolve.Deps{Query: query.New(store), Search: searchService}
	realRetrieval := engineretrieval.New(deps, searchService, store)
	adapter := evalretrieval.NewTaskContextRetriever(realRetrieval)
	deps.Retrieval = adapter
	return &labsHeroEngine{
		deps: deps, reader: enginecontext.NewRootedReader(fixtureRoot), retrieval: adapter,
	}, cleanup, nil
}

func labsHeroFingerprint(emb embed.Embedder, graphGeneration string) embed.Fingerprint {
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

func assertLabsHeroRun(s scenario.Scenario, p labsHeroProvenance, q evalretrieval.Query, answerText string, run scenario.Result, eng *labsHeroEngine) error {
	if eng.taskContextV2Calls != 1 {
		return fmt.Errorf("labs hero task_context/2 calls=%d, want exactly 1", eng.taskContextV2Calls)
	}
	if eng.retrieval.Called() != 1 {
		return fmt.Errorf("labs hero retrieval calls=%d, want exactly 1 inside the single task_context/2 call", eng.retrieval.Called())
	}
	retrieved := eng.retrieval.LastResult()
	if retrieved.Degradation != string(engineretrieval.StateReady) {
		return fmt.Errorf("labs hero semantic state %q, want %q; refusing lexical fallback (this suite fails, it does not skip)", retrieved.Degradation, engineretrieval.StateReady)
	}
	if run.Outcome != "pass" {
		return fmt.Errorf("labs hero scenario %s outcome=%q evidence=%v", s.ID, run.Outcome, run.Evidence)
	}
	if eng.bundle == nil {
		return fmt.Errorf("labs hero task_context/2 returned a nil bundle")
	}
	if err := contract.ValidateResult(eng.bundle); err != nil {
		return fmt.Errorf("labs hero invalid bundle: %w", err)
	}
	if retrieved.Summary.RetrievalVersion != engineretrieval.Version || retrieved.Summary.Strategy != "semantic_first" {
		return fmt.Errorf("labs hero retrieval method=%s/%s, want %s/semantic_first", retrieved.Summary.RetrievalVersion, retrieved.Summary.Strategy, engineretrieval.Version)
	}
	fingerprints := []struct {
		name  string
		value string
	}{
		{"weights", retrieved.Summary.WeightsHash},
		{"model", retrieved.Summary.ModelFingerprint},
		{"index", retrieved.Summary.IndexFingerprint},
	}
	for _, fingerprint := range fingerprints {
		if fingerprint.value == "" {
			return fmt.Errorf("labs hero %s fingerprint is empty", fingerprint.name)
		}
		if !strings.Contains(eng.bundle.Summary, fingerprint.value) {
			return fmt.Errorf("labs hero bundle summary omits %s fingerprint %q: %q", fingerprint.name, fingerprint.value, eng.bundle.Summary)
		}
	}
	if retrieved.Summary.WeightsHash != engineretrieval.WeightsHash() {
		return fmt.Errorf("labs hero weights fingerprint=%q, want %q", retrieved.Summary.WeightsHash, engineretrieval.WeightsHash())
	}
	used, budget, ok := labsHeroTokenCounts(eng.bundle.Summary)
	if !ok {
		return fmt.Errorf("labs hero bundle summary has no exact Bundle.Tokens/Budget accounting: %q", eng.bundle.Summary)
	}
	if budget != evalretrieval.TaskContextTokenBudget || used > budget {
		return fmt.Errorf("labs hero Bundle.Tokens=%d Budget=%d, want Tokens <= %d", used, budget, evalretrieval.TaskContextTokenBudget)
	}
	if err := requireAnswerBearingSnippet(eng.bundle, p.AnswerSpan, answerText); err != nil {
		return err
	}
	for _, path := range p.RequiredSnippetPaths {
		if !bundleHasSnippetPath(eng.bundle, path) {
			return fmt.Errorf("labs hero bundle has no answer-context snippet from %s (query %s)", path, q.ID)
		}
	}
	if !bundleHasProvenancedRelation(eng.bundle) {
		return fmt.Errorf("labs hero bundle has no graph_relation citation with an edge provenance tier")
	}
	return nil
}

func requireAnswerBearingSnippet(bundle *contract.Result, answer labsHeroAnswerSpan, answerText string) error {
	relationOnly := 0
	if bundle != nil {
		for _, ev := range bundle.Evidence {
			if ev.Path != answer.Path {
				continue
			}
			if ev.ClaimType == "graph_relation" && ev.Line >= answer.StartLine && ev.Line <= answer.EndLine {
				relationOnly++
			}
			if ev.Snippet == "" || ev.ClaimType != "" {
				continue
			}
			start, end, ok := parseLabsHeroSpan(ev.Span)
			if ok && start <= answer.StartLine && end >= answer.EndLine && strings.Contains(ev.Snippet, answerText) {
				return nil
			}
		}
	}
	return fmt.Errorf("labs hero answer-bearing assertion: no snippet text covers %s:%d-%d; ignored %d graph_relation citation(s) in the judged interval", answer.Path, answer.StartLine, answer.EndLine, relationOnly)
}

func parseLabsHeroSpan(span string) (int, int, bool) {
	left, right, ok := strings.Cut(span, "-")
	if !ok {
		return 0, 0, false
	}
	start, err := strconv.Atoi(left)
	if err != nil {
		return 0, 0, false
	}
	end, err := strconv.Atoi(right)
	return start, end, err == nil && start > 0 && end >= start
}

func bundleHasSnippetPath(bundle *contract.Result, path string) bool {
	for _, ev := range bundle.Evidence {
		if ev.Path == path && ev.Snippet != "" && ev.ClaimType == "" {
			return true
		}
	}
	return false
}

func bundleHasProvenancedRelation(bundle *contract.Result) bool {
	for _, ev := range bundle.Evidence {
		if ev.ClaimType == "graph_relation" && ev.EdgeTier != "" && ev.Path != "" && ev.Line > 0 {
			return true
		}
	}
	return false
}

func labsHeroTokenCounts(summary string) (int, int, bool) {
	const suffix = " snippet tokens"
	end := strings.Index(summary, suffix)
	if end < 0 {
		return 0, 0, false
	}
	prefix := summary[:end]
	start := strings.LastIndex(prefix, "; ")
	if start < 0 {
		return 0, 0, false
	}
	parts := strings.Split(prefix[start+2:], "/")
	if len(parts) != 2 {
		return 0, 0, false
	}
	used, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	budget, err := strconv.Atoi(parts[1])
	return used, budget, err == nil
}

func sha256Hex(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Compile-time check for the fixture service used above.
var _ graphstore.BoundedGraphLookup = (*graphstore.MemStore)(nil)
