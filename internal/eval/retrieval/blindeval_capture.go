package retrieval

// The candidate MCP capture.
//
// docs/eval/retrieval/methodology.md records "Candidate MCP capture remains
// UNENFORCED until the release-composition slice owns the actual transport
// call". This file is that instrument. It drives the REAL MCP stdio surface —
// surfaces/mcp.Server.Serve, the same JSON-RPC read/dispatch/write loop the
// shipped server runs — over one `tools/call task_context` request per query,
// and preserves the exact response bytes the loop's encoder wrote, terminating
// newline included.
//
// What makes the bytes trustworthy is what is NOT here: no re-marshaling, no
// pretty printer, no envelope reconstruction and no extraction of the inner
// text. The writer's buffer is the artifact. ValidateCandidateBundle then
// refuses the three substitute shapes AC-2 names, and each refusal has a test
// that breaks a real capture to prove it bites.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/query"
	engineretrieval "github.com/samibel/graphi/engine/retrieval"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/mcp"
)

// CandidateCaptureVersion identifies the capture instrument. It travels into
// the run directory so a later change to how bytes are captured cannot be
// mistaken for the same measurement.
const CandidateCaptureVersion = "sw280-candidate-mcp-capture/1"

// candidateJSONRPCPrefix is the exact opening the stdio encoder produces for a
// response: encoding/json writes struct fields in declaration order, and
// surfaces/mcp declares jsonrpc, then id, then result.
//
// Requiring it is a cheap, decisive refusal of a re-marshaled envelope: a
// bundle round-tripped through map[string]any comes back with alphabetically
// ordered keys and cannot start this way.
const candidateJSONRPCPrefix = `{"jsonrpc":"2.0","id":`

// CapturedCandidateBundle is one query's preserved task_context/2 response,
// together with the request that produced it. The request is recorded for
// provenance only; it is outside the payload boundary and enters no count.
type CapturedCandidateBundle struct {
	QueryID      string           `json:"query_id"`
	RequestBytes []byte           `json:"request_bytes"`
	Payload      PreservedPayload `json:"payload"`
	// RetrievalStrategy and RetrievalState are the observed engine facts that
	// prove this was the ready task_context/2 path and not the /1 fallback.
	RetrievalStrategy string `json:"retrieval_strategy"`
	RetrievalState    string `json:"retrieval_state"`
	BundleSummary     string `json:"bundle_summary"`
}

// CandidateCaptureProvenance records the composition the bytes came out of.
type CandidateCaptureProvenance struct {
	CaptureVersion    string `json:"capture_version"`
	Transport         string `json:"transport"`
	Surface           string `json:"surface"`
	Boundary          string `json:"boundary"`
	RepoName          string `json:"repo_name"`
	RepoSHA           string `json:"repo_sha"`
	DatasetSHA256     string `json:"dataset_sha256"`
	EmbedderSelector  string `json:"embedder_selector"`
	ModelFingerprint  string `json:"model_fingerprint"`
	IndexFingerprint  string `json:"index_fingerprint"`
	GenerationID      string `json:"generation_id"`
	PersistedVectors  int    `json:"persisted_vectors"`
	SemanticState     string `json:"semantic_state"`
	TokenBudget       int    `json:"token_budget"`
	MethodVersion     string `json:"method_version"`
	TokenizerID       string `json:"tokenizer_id"`
	TokenizerVocabSHA string `json:"tokenizer_vocabulary_sha256"`
	QueryCount        int    `json:"query_count"`
}

// CandidateCaptureOptions is one fail-closed capture run.
type CandidateCaptureOptions struct {
	RepoRoot         string
	RepoName         string
	RepoSHA          string
	Dataset          *Loaded
	Queries          []Query
	EmbedderSelector string
	WorkDir          string
	RealCounter      PayloadCounter
	Log              io.Writer
}

// CaptureCandidateBundles builds the production index over the pinned checkout
// and captures exactly one complete MCP JSON-RPC `task_context/2` response per
// query at the frozen 1200-token budget.
//
// It fails closed on every degradation the measurement would otherwise absorb
// silently: a non-ready semantic generation, a retrieval fallback, a /1 bundle,
// more or fewer than one retrieval call, or a response that is not a
// well-formed single-line JSON-RPC result.
func CaptureCandidateBundles(ctx context.Context, o CandidateCaptureOptions) ([]CapturedCandidateBundle, CandidateCaptureProvenance, error) {
	var provenance CandidateCaptureProvenance
	if o.Dataset == nil || o.Dataset.Dataset == nil {
		return nil, provenance, fmt.Errorf("retrieval %s capture: no dataset", QrelBlindSmokeEvaluationName)
	}
	if len(o.Queries) == 0 {
		return nil, provenance, fmt.Errorf("retrieval %s capture: no queries", QrelBlindSmokeEvaluationName)
	}
	if strings.TrimSpace(o.RepoRoot) == "" || strings.TrimSpace(o.RepoSHA) == "" {
		return nil, provenance, fmt.Errorf("retrieval %s capture: repository root and sha are required", QrelBlindSmokeEvaluationName)
	}
	if strings.TrimSpace(o.EmbedderSelector) == "" {
		return nil, provenance, fmt.Errorf("retrieval %s capture: a production embedder selector is required", QrelBlindSmokeEvaluationName)
	}
	if o.RealCounter.Count == nil || o.RealCounter.TokenizerID == "" || o.RealCounter.TokenizerID == TokenizerID {
		return nil, provenance, fmt.Errorf("retrieval %s capture: the pinned real tokenizer counter is required", QrelBlindSmokeEvaluationName)
	}
	if o.Log == nil {
		o.Log = io.Discard
	}

	head, err := CheckoutHEAD(ctx, o.RepoRoot)
	if err != nil {
		return nil, provenance, err
	}
	if !strings.EqualFold(head, o.RepoSHA) || !strings.EqualFold(head, o.Dataset.Dataset.RepoSHA) {
		return nil, provenance, fmt.Errorf("retrieval %s capture: checkout is at %s, option pins %s and dataset pins %s", QrelBlindSmokeEvaluationName, head, o.RepoSHA, o.Dataset.Dataset.RepoSHA)
	}

	workDir := o.WorkDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "graphi-qrel-blind-capture")
		if err != nil {
			return nil, provenance, fmt.Errorf("retrieval %s capture: workdir: %w", QrelBlindSmokeEvaluationName, err)
		}
		defer os.RemoveAll(workDir)
	}
	idx, err := buildTaskContextIndex(ctx, o.RepoRoot, workDir, o.EmbedderSelector, o.Log)
	if err != nil {
		return nil, provenance, err
	}
	defer idx.store.Close()

	semanticState := idx.search.SemanticState()
	if semanticState.State != embed.StateReady {
		return nil, provenance, fmt.Errorf("retrieval %s capture: semantic state is %s, want ready; refusing a lexical-fallback bundle", QrelBlindSmokeEvaluationName, semanticState.State)
	}
	if semanticState.Requested.Canonical() != idx.fingerprint.Canonical() {
		return nil, provenance, fmt.Errorf("retrieval %s capture: the search service's requested fingerprint does not equal the independently verified generation fingerprint", QrelBlindSmokeEvaluationName)
	}

	querySvc := query.New(idx.store)
	realEngine := engineretrieval.New(resolve.Deps{Query: querySvc, Search: idx.search}, idx.search, idx.store)
	if realEngine == nil {
		return nil, provenance, fmt.Errorf("retrieval %s capture: retrieval.New returned nil", QrelBlindSmokeEvaluationName)
	}

	provenance = CandidateCaptureProvenance{
		CaptureVersion:    CandidateCaptureVersion,
		Transport:         "MCP stdio JSON-RPC 2.0 (surfaces/mcp.Server.Serve, line-delimited)",
		Surface:           "surfaces/mcp tools/call " + mcp.ToolTaskContext,
		Boundary:          string(PayloadBoundaryCandidate),
		RepoName:          o.RepoName,
		RepoSHA:           head,
		DatasetSHA256:     o.Dataset.SHA256,
		EmbedderSelector:  o.EmbedderSelector,
		ModelFingerprint:  idx.embedderID,
		IndexFingerprint:  idx.fingerprint.Canonical(),
		GenerationID:      string(idx.generationID),
		PersistedVectors:  idx.persistedVectors,
		SemanticState:     semanticState.State.String(),
		TokenBudget:       SavingsCandidateBudget,
		MethodVersion:     taskctx.MethodVersionV2,
		TokenizerID:       o.RealCounter.TokenizerID,
		TokenizerVocabSHA: o.RealCounter.VocabularySHA256,
		QueryCount:        len(o.Queries),
	}

	captured := make([]CapturedCandidateBundle, 0, len(o.Queries))
	for _, q := range o.Queries {
		bundle, err := captureOneCandidateBundle(ctx, o, q, querySvc, idx, realEngine)
		if err != nil {
			return nil, provenance, err
		}
		captured = append(captured, bundle)
	}
	return captured, provenance, nil
}

func captureOneCandidateBundle(ctx context.Context, o CandidateCaptureOptions, q Query, querySvc *query.Service, idx *taskContextIndex, realEngine TaskContextEngine) (CapturedCandidateBundle, error) {
	// One adapter per query, so "exactly one task_context/2 call" is observed
	// per query rather than inferred from a running total.
	adapter := NewTaskContextRetriever(realEngine)
	direct := client.NewDirect(querySvc, idx.search).
		WithRetrieval(adapter).
		WithRepoRoot(o.RepoRoot)
	server := mcp.NewServerWithClient(direct, mcp.WithLabs(), mcp.WithRepository(client.Repository{Root: o.RepoRoot}))
	defer server.Close()

	request, err := candidateToolCallRequest(q.Text)
	if err != nil {
		return CapturedCandidateBundle{}, err
	}

	// The writer's buffer IS the artifact: whatever Serve's encoder emits is
	// what a client reads off the pipe, and nothing here reformats it.
	var out bytes.Buffer
	if err := server.Serve(ctx, bytes.NewReader(request), &out); err != nil {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s MCP serve: %w", QrelBlindSmokeEvaluationName, q.ID, err)
	}
	responseBytes := out.Bytes()

	if adapter.Called() != 1 {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s called the real retrieval instance %d times, want exactly 1", QrelBlindSmokeEvaluationName, q.ID, adapter.Called())
	}
	if adapter.LastErr() != nil {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s retrieval errored (%v); the bundle would be a fallback", QrelBlindSmokeEvaluationName, q.ID, adapter.LastErr())
	}
	last := adapter.LastResult()
	if last.Degradation != string(engineretrieval.StateReady) {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s retrieval state is %q, want ready", QrelBlindSmokeEvaluationName, q.ID, last.Degradation)
	}
	if last.Summary.RetrievalVersion != engineretrieval.Version || last.Summary.Strategy != "semantic_first" {
		return CapturedCandidateBundle{}, fmt.Errorf("retrieval %s capture: query %s method is %s/%s, want %s/semantic_first", QrelBlindSmokeEvaluationName, q.ID, last.Summary.RetrievalVersion, last.Summary.Strategy, engineretrieval.Version)
	}

	summary, err := ValidateCandidateBundleBytes(q.ID, responseBytes)
	if err != nil {
		return CapturedCandidateBundle{}, err
	}

	payload, err := preserveCandidatePayload(q.ID, responseBytes, o.RealCounter)
	if err != nil {
		return CapturedCandidateBundle{}, err
	}
	return CapturedCandidateBundle{
		QueryID:           q.ID,
		RequestBytes:      request,
		Payload:           payload,
		RetrievalStrategy: last.Summary.Strategy,
		RetrievalState:    last.Degradation,
		BundleSummary:     summary,
	}, nil
}

// candidateToolCallRequest builds the one request line. token_budget and
// version are literal constants here: there is no flag, option or environment
// variable that can change what the candidate was asked for.
func candidateToolCallRequest(task string) ([]byte, error) {
	budget := SavingsCandidateBudget
	version := 2
	request := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": mcp.ToolTaskContext,
			"arguments": map[string]any{
				"task":         task,
				"version":      version,
				"token_budget": budget,
			},
		},
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("retrieval %s capture: encode request: %w", QrelBlindSmokeEvaluationName, err)
	}
	return append(encoded, '\n'), nil
}

// candidateResponseEnvelope is the shape a captured response must decode to. It
// is parsed for VALIDATION only; the preserved bytes are never re-serialized
// from it.
type candidateResponseEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error json.RawMessage `json:"error"`
}

// ValidateCandidateBundleBytes refuses everything that is not the exact
// transport bytes of one complete, successful, ready `task_context/2` response.
//
// Each clause below corresponds to a substitute AC-2 forbids, and each has a
// test that mutates a real capture into that shape and asserts the refusal:
//
//   - a reconstructed or re-marshaled envelope loses the encoder's field order
//     and fails the prefix check;
//   - a pretty-printed envelope carries interior newlines and fails the
//     single-line check;
//   - an extracted-text bundle has no envelope at all and fails both;
//   - a /1 bundle, or one assembled from a degraded retrieval, fails the
//     method-version and degradation checks.
//
// It returns the bundle summary so the caller can record what it validated.
func ValidateCandidateBundleBytes(queryID string, raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("retrieval %s capture: query %s produced no response bytes", QrelBlindSmokeEvaluationName, queryID)
	}
	if !bytes.HasPrefix(raw, []byte(candidateJSONRPCPrefix)) {
		return "", fmt.Errorf("retrieval %s capture: query %s response does not begin with the stdio encoder's envelope %s; a reconstructed, re-marshaled or extracted-text bundle is not the measured object", QrelBlindSmokeEvaluationName, queryID, candidateJSONRPCPrefix)
	}
	if raw[len(raw)-1] != '\n' {
		return "", fmt.Errorf("retrieval %s capture: query %s response is missing the transport's terminating newline", QrelBlindSmokeEvaluationName, queryID)
	}
	if n := bytes.Count(raw, []byte{'\n'}); n != 1 {
		return "", fmt.Errorf("retrieval %s capture: query %s response holds %d newlines, want exactly the one that terminates a single line-delimited message; a pretty-printed or concatenated capture is not the measured object", QrelBlindSmokeEvaluationName, queryID, n)
	}
	var envelope candidateResponseEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s response is not a JSON-RPC message: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if envelope.JSONRPC != "2.0" {
		return "", fmt.Errorf("retrieval %s capture: query %s response jsonrpc=%q", QrelBlindSmokeEvaluationName, queryID, envelope.JSONRPC)
	}
	if len(envelope.Error) > 0 && string(envelope.Error) != "null" {
		return "", fmt.Errorf("retrieval %s capture: query %s response carries a JSON-RPC error: %s", QrelBlindSmokeEvaluationName, queryID, string(envelope.Error))
	}
	if envelope.Result == nil {
		return "", fmt.Errorf("retrieval %s capture: query %s response carries no result", QrelBlindSmokeEvaluationName, queryID)
	}
	if envelope.Result.IsError {
		return "", fmt.Errorf("retrieval %s capture: query %s tool call reported isError", QrelBlindSmokeEvaluationName, queryID)
	}
	if len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" {
		return "", fmt.Errorf("retrieval %s capture: query %s response content is not exactly one text block", QrelBlindSmokeEvaluationName, queryID)
	}
	var result contract.Result
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &result); err != nil {
		return "", fmt.Errorf("retrieval %s capture: query %s inner bundle is not a contract result: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if !strings.HasPrefix(result.Summary, taskctx.MethodVersionV2+":") {
		return "", fmt.Errorf("retrieval %s capture: query %s bundle summary %q does not attest %s; a /1 bundle is a different candidate", QrelBlindSmokeEvaluationName, queryID, result.Summary, taskctx.MethodVersionV2)
	}
	if !strings.Contains(result.Summary, "degradation: ready") {
		return "", fmt.Errorf("retrieval %s capture: query %s bundle summary %q does not attest ready retrieval", QrelBlindSmokeEvaluationName, queryID, result.Summary)
	}
	return result.Summary, nil
}

// preserveCandidatePayload stores the captured bytes through the SAME
// PayloadLedger the comparator uses, so both arms are preserved and recounted
// by one implementation, and then re-checks the result through the measurement
// contract's own payload validation.
func preserveCandidatePayload(queryID string, raw []byte, real PayloadCounter) (PreservedPayload, error) {
	ledger := &PayloadLedger{}
	ledger.capture(PayloadBoundaryCandidate, PayloadOperationTaskContext, raw)
	payloads, err := ledger.PreservedPayloads(real)
	if err != nil {
		return PreservedPayload{}, fmt.Errorf("retrieval %s capture: query %s: %w", QrelBlindSmokeEvaluationName, queryID, err)
	}
	if len(payloads) != 1 {
		return PreservedPayload{}, fmt.Errorf("retrieval %s capture: query %s preserved %d payloads, want exactly 1", QrelBlindSmokeEvaluationName, queryID, len(payloads))
	}
	payload := payloads[0]
	requiredCounters := map[string]string{
		TokenizerID:      "",
		real.TokenizerID: real.VocabularySHA256,
	}
	counters := map[string]PayloadCounter{real.TokenizerID: real}
	if _, err := validatePreservedPayload(queryID, "candidate", payload, 1, PayloadBoundaryCandidate, requiredCounters, counters); err != nil {
		return PreservedPayload{}, err
	}
	return payload, nil
}
