package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	evaltokenizer "github.com/samibel/graphi/core/tokenizer"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
	"github.com/samibel/graphi/surfaces/client"
)

type compactTaskContextClient struct{ allToolsClient }

func useCheckedInTaskContextTokenizer(t *testing.T) {
	t.Helper()
	t.Setenv("GRAPHI_EVAL_TOKENIZER_DIR", filepath.Join("..", "..", "core", "tokenizer", "testdata", "artifact"))
}

func (compactTaskContextClient) TaskContext(context.Context, client.TaskContextParams) ([]byte, error) {
	return contract.Serialize(&contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: `task_context/2: 1 seed(s) for "where is required input validated before command runs" — 0 related, 0 callers, 0 callees, 0 tests, 0 configs, 0 files, risk low (task_context/2; retrieval/3; weights abcdef12; model fixture-model; 3/1200 snippet tokens; context-definitions/3; strategy semantic_first; degradation: ready)`,
		Items: []contract.Item{{
			RefID: "decoy", Rank: 1, Reason: "primary: func decoy (decoy.go:3)", EvidenceRefIDs: []string{"source-1", "snippet-1"},
		}},
		Evidence: []contract.Evidence{
			{RefID: "source-1", Path: "decoy.go", Line: 3, Span: "3-3", Role: "primary", ClaimType: "source_match"},
			{RefID: "snippet-1", Path: "decoy.go", Line: 3, Span: "3-3", Role: "snippet", Snippet: "func decoy() {}", TextHash: shape.TextHash("func decoy() {}")},
		},
		Confidence: contract.Confidence{Distribution: map[string]float64{"heuristic": 1}, Top: "heuristic", Method: "fixture"},
		Limits:     contract.Limits{CapApplied: 40, TotalAvailable: 1},
	})
}

type compactFallbackClient struct{ allToolsClient }

func (compactFallbackClient) TaskContext(context.Context, client.TaskContextParams) ([]byte, error) {
	raw, err := (compactTaskContextClient{}).TaskContext(context.Background(), client.TaskContextParams{})
	if err != nil {
		return nil, err
	}
	var result contract.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	result.Summary = strings.Replace(result.Summary, "degradation: ready", "degradation: lexical_only", 1)
	return contract.Serialize(&result)
}

func TestTaskContextV2_PublicMCPPreservesNonReadyFallback(t *testing.T) {
	server := NewServerWithClient(compactFallbackClient{}, WithLabs(), WithRepository(client.Repository{Root: t.TempDir()}))
	defer server.Close()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_context","arguments":{"task":"missing","version":2,"token_budget":1200}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(t.Context(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent json.RawMessage `json:"structuredContent"`
			IsError           bool            `json:"isError"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Error) != 0 || response.Result.IsError || len(response.Result.Content) != 1 {
		t.Fatalf("fallback became an RPC/tool error: %s", output.String())
	}
	if !strings.Contains(response.Result.Content[0].Text, "degradation: lexical_only") || len(response.Result.StructuredContent) != 0 {
		t.Fatalf("canonical fallback was not preserved: %s", output.String())
	}
}

func TestTaskContextV2_EvaluationLexicalControlUsesCompactWireAndTruthfulState(t *testing.T) {
	useCheckedInTaskContextTokenizer(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "decoy.go"), []byte("package fixture\n\nfunc decoy() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithClient(compactFallbackClient{}, WithLabs(), WithRepository(client.Repository{Root: root}), WithEvaluationLexicalCompactControl())
	defer server.Close()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_context","arguments":{"task":"missing","version":2,"token_budget":1200}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(t.Context(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			StructuredContent taskcompact.Structured `json:"structuredContent"`
			IsError           bool                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.IsError || response.Result.StructuredContent.Version != taskcompact.Version {
		t.Fatalf("lexical control did not use compact/17 wire: %s", output.String())
	}
	if got := response.Result.StructuredContent.Provenance.RetrievalState; got != "lexical_only" {
		t.Fatalf("lexical control state = %q, want lexical_only", got)
	}
}

func TestTaskContextV2_PublicMCPNegativeBudgetDoesNotReactivateSourceReads(t *testing.T) {
	root := t.TempDir()
	const sourceOnly = "SHOULD_NOT_BE_DISCOVERED"
	if err := os.WriteFile(filepath.Join(root, "answer.go"), []byte("package fixture\n// "+sourceOnly+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithClient(compactTaskContextClient{}, WithLabs(), WithRepository(client.Repository{Root: root}))
	defer server.Close()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_context","arguments":{"task":"SHOULD_NOT_BE_DISCOVERED","version":2,"token_budget":-1}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(t.Context(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte(sourceOnly)) || bytes.Contains(output.Bytes(), []byte(`"structuredContent"`)) {
		t.Fatalf("negative token budget reactivated compact source discovery: %s", output.String())
	}
}

func TestTaskContextV2_PublicMCPDoesNotFollowSourceSymlinkOutsideRepository(t *testing.T) {
	useCheckedInTaskContextTokenizer(t)
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	const secret = "OUTSIDE_REPOSITORY_SECRET"
	if err := os.WriteFile(outside, []byte("package stolen\n// "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "decoy.go"), []byte("package fixture\n\nfunc decoy() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "command.go")); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithClient(compactTaskContextClient{}, WithLabs(), WithRepository(client.Repository{Root: root}))
	defer server.Close()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_context","arguments":{"task":"command.go","version":2,"token_budget":1200}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(t.Context(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte(secret)) {
		t.Fatalf("task_context exposed bytes through a root-escaping source symlink: %s", output.String())
	}
	if bytes.Contains(output.Bytes(), []byte(`"error"`)) {
		t.Fatalf("supplemental source rejection should remain a successful task_context result: %s", output.String())
	}
}

func TestTaskContextV2_PublicMCPEnforcesFrozenRealTokenizerCeiling(t *testing.T) {
	useCheckedInTaskContextTokenizer(t)
	root := t.TempDir()
	largeBody := "package fixture\n\n// required input validated before command runs\nfunc validateRequired() {\n" + strings.Repeat("\tvalue_0123456789 += another_0123456789 // required validated command input\n", 800) + "}\n"
	if err := os.WriteFile(filepath.Join(root, "command.go"), []byte(largeBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "decoy.go"), []byte("package fixture\n\nfunc decoy() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := NewServerWithClient(compactTaskContextClient{}, WithLabs(), WithRepository(client.Repository{Root: root}))
	defer server.Close()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_context","arguments":{"task":"where is required input validated before command runs","version":2,"token_budget":1200}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(t.Context(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}
	tokenizer, err := evaltokenizer.LoadEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := tokenizer.Count(output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if tokens > 1200 {
		t.Fatalf("serialized MCP response = %d cl100k_base tokens, want <= 1200", tokens)
	}
}

func TestTaskContextV2_PublicMCPRecoversMissingAnswerIntoCompactStructuredContent(t *testing.T) {
	useCheckedInTaskContextTokenizer(t)
	root := t.TempDir()
	for name, body := range map[string]string{
		"decoy.go":   "package fixture\n\nfunc decoy() {}\n",
		"command.go": "package fixture\n\n// validateRequired is where required input is validated before command runs.\nfunc validateRequired(input string) error {\n\tif input == \"\" { return errRequired }\n\treturn nil\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	server := NewServerWithClient(compactTaskContextClient{}, WithLabs(), WithRepository(client.Repository{Root: root}))
	defer server.Close()
	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"task_context","arguments":{"task":"where is required input validated before command runs","version":2,"token_budget":1200}}}` + "\n")
	var output bytes.Buffer
	if err := server.Serve(t.Context(), bytes.NewReader(request), &output); err != nil {
		t.Fatal(err)
	}

	var response struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent struct {
				Version string `json:"version"`
				Sources []struct {
					Path string `json:"path"`
					Text string `json:"text"`
				} `json:"sources"`
			} `json:"structuredContent"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v\n%s", err, output.String())
	}
	if response.Result.IsError || len(response.Result.Content) != 1 || response.Result.Content[0].Type != "text" {
		t.Fatalf("invalid MCP tool result: %#v\n%s", response.Result, output.String())
	}
	if response.Result.StructuredContent.Version != taskcompact.Version {
		t.Fatalf("structured version = %q", response.Result.StructuredContent.Version)
	}
	joined := ""
	for _, source := range response.Result.StructuredContent.Sources {
		joined += source.Path + "\n" + source.Text + "\n"
	}
	if !strings.Contains(joined, "command.go") || !strings.Contains(joined, "func validateRequired") {
		t.Fatalf("missing locally discovered answer source:\n%s", joined)
	}
	if strings.Contains(response.Result.Content[0].Text, `\"outcome\"`) {
		t.Fatalf("v2 still double-encodes the legacy contract in text: %q", response.Result.Content[0].Text)
	}
}
