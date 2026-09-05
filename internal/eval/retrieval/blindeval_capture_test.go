package retrieval

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/taskctx"
)

// encodeLikeTheStdioTransport reproduces exactly how surfaces/mcp writes a
// response: json.Encoder with HTML escaping off, one object per line, struct
// field order jsonrpc, id, result.
func encodeLikeTheStdioTransport(t *testing.T, summary string, isError bool, withError bool) []byte {
	t.Helper()
	inner := contract.Result{
		Outcome: "ok",
		Summary: summary,
		Items:   []contract.Item{},
	}
	innerBytes, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	type rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	type response struct {
		JSONRPC string  `json:"jsonrpc"`
		ID      int     `json:"id"`
		Result  any     `json:"result,omitempty"`
		Error   *rpcErr `json:"error,omitempty"`
	}
	out := response{JSONRPC: "2.0", ID: 1}
	if withError {
		out.Error = &rpcErr{Code: -32602, Message: "tool not available: task_context"}
	} else {
		out.Result = map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(innerBytes)}},
			"isError": isError,
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readyV2Summary() string {
	return taskctx.MethodVersionV2 + ": 4 seeds; degradation: ready; weights h0"
}

// AC-2: every substitute for the transport bytes is refused, and each row below
// mutates a real capture into that substitute rather than asserting the rule.
func TestQrelBlindSmoke_CandidateBundleRefusesEverySubstitute(t *testing.T) {
	good := encodeLikeTheStdioTransport(t, readyV2Summary(), false, false)
	if _, err := ValidateCandidateBundleBytes("cb-1", good); err != nil {
		t.Fatalf("the transport bytes must be accepted: %v", err)
	}

	for _, tc := range []struct {
		name    string
		bytes   func() []byte
		wantSub string
	}{
		{
			name: "re-marshaled through map[string]any (keys reordered)",
			bytes: func() []byte {
				var generic map[string]any
				if err := json.Unmarshal(good, &generic); err != nil {
					t.Fatal(err)
				}
				out, err := json.Marshal(generic)
				if err != nil {
					t.Fatal(err)
				}
				return append(out, '\n')
			},
			wantSub: "does not begin with the stdio encoder's envelope",
		},
		{
			// Caught by the envelope clause rather than the newline clause,
			// because json.Indent breaks the line immediately after the opening
			// brace. Either refusal is the right answer; the expectation names
			// which one actually fires so a future change to the ordering shows
			// up here rather than passing silently.
			name: "pretty-printed",
			bytes: func() []byte {
				var indented bytes.Buffer
				if err := json.Indent(&indented, bytes.TrimSpace(good), "", "  "); err != nil {
					t.Fatal(err)
				}
				return append(indented.Bytes(), '\n')
			},
			wantSub: "does not begin with the stdio encoder's envelope",
		},
		{
			name:    "a trailing blank line appended",
			bytes:   func() []byte { return append(append([]byte{}, good...), '\n') },
			wantSub: "newlines",
		},
		{
			name: "the extracted inner bundle text with no envelope",
			bytes: func() []byte {
				var envelope struct {
					Result struct {
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if err := json.Unmarshal(good, &envelope); err != nil {
					t.Fatal(err)
				}
				return append([]byte(envelope.Result.Content[0].Text), '\n')
			},
			wantSub: "does not begin with the stdio encoder's envelope",
		},
		{
			name:    "the terminating newline stripped",
			bytes:   func() []byte { return bytes.TrimRight(good, "\n") },
			wantSub: "missing the transport's terminating newline",
		},
		{
			name:    "two responses concatenated",
			bytes:   func() []byte { return append(append([]byte{}, good...), good...) },
			wantSub: "newlines",
		},
		{
			name:    "an empty capture",
			bytes:   func() []byte { return nil },
			wantSub: "produced no response bytes",
		},
		{
			name:    "a JSON-RPC error response",
			bytes:   func() []byte { return encodeLikeTheStdioTransport(t, readyV2Summary(), false, true) },
			wantSub: "carries a JSON-RPC error",
		},
		{
			name:    "a tool result flagged isError",
			bytes:   func() []byte { return encodeLikeTheStdioTransport(t, readyV2Summary(), true, false) },
			wantSub: "reported isError",
		},
		{
			name:    "a task_context/1 bundle",
			bytes:   func() []byte { return encodeLikeTheStdioTransport(t, "task_context/1: 4 seeds", false, false) },
			wantSub: "does not attest task_context/2",
		},
		{
			name: "a /2 bundle assembled from degraded retrieval",
			bytes: func() []byte {
				return encodeLikeTheStdioTransport(t, taskctx.MethodVersionV2+": 4 seeds; degradation: stale", false, false)
			},
			wantSub: "does not attest ready retrieval",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidateCandidateBundleBytes("cb-1", tc.bytes())
			if err == nil {
				t.Fatalf("%s was accepted as the measured object", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// AC-2: the bytes handed to a rater are byte-identical to the preserved
// payload. The check reads the bundle back OUT of the assembled prompt at the
// recorded offset instead of trusting that it was embedded unchanged.
func TestQrelBlindSmoke_RaterPromptCarriesThePreservedBundleByteIdentically(t *testing.T) {
	raw := encodeLikeTheStdioTransport(t, readyV2Summary(), false, false)
	counter := PayloadCounter{
		TokenizerID:      "tiktoken:cl100k_base:ordinary",
		VocabularySHA256: fixtureVocabSHA,
		Count:            func(b []byte) (int, error) { return len(b) / 4, nil },
	}
	payload, err := preserveCandidatePayload("cb-1", raw, counter)
	if err != nil {
		t.Fatal(err)
	}
	if payload.Boundary != PayloadBoundaryCandidate || payload.Operation != PayloadOperationTaskContext {
		t.Fatalf("preserved payload = %s/%s", payload.Boundary, payload.Operation)
	}
	if !bytes.Equal(payload.Bytes, raw) || payload.SHA256 != SHA256Hex(raw) || payload.ByteCount != len(raw) {
		t.Fatal("the preserved payload is not the captured bytes")
	}
	if len(payload.TokenCounts) != 2 {
		t.Fatalf("%d token counts, want the whitespace counter and the pinned real tokenizer", len(payload.TokenCounts))
	}

	prompt, err := BuildRaterPrompt("cb-1", "where is the flag parsed?", payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPromptCarriesPreservedBundle(prompt, payload); err != nil {
		t.Fatalf("the assembled prompt does not carry the preserved bundle: %v", err)
	}
	embedded, err := prompt.EmbeddedBundleBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(embedded, raw) {
		t.Fatal("the prompt's embedded bundle differs from the captured transport bytes")
	}
	if !bytes.Contains(prompt.Bytes, []byte("where is the flag parsed?")) {
		t.Error("the prompt does not carry the query text")
	}
	// The prompt names nothing the rater must not see.
	for _, forbidden := range []string{"judgement", "qrel", "grade-3", "expected answer", "rubric", "spf13/cobra"} {
		if bytes.Contains(bytes.ToLower(prompt.Bytes), []byte(forbidden)) {
			t.Errorf("the rater prompt mentions %q", forbidden)
		}
	}
}

// Refusal: a pretty-printed or otherwise altered bundle is caught by the
// byte-identity check rather than passing as "the same JSON".
func TestQrelBlindSmoke_APrettyPrintedBundleFailsTheByteIdentityCheck(t *testing.T) {
	raw := encodeLikeTheStdioTransport(t, readyV2Summary(), false, false)
	counter := PayloadCounter{
		TokenizerID:      "tiktoken:cl100k_base:ordinary",
		VocabularySHA256: fixtureVocabSHA,
		Count:            func(b []byte) (int, error) { return len(b) / 4, nil },
	}
	payload, err := preserveCandidatePayload("cb-1", raw, counter)
	if err != nil {
		t.Fatal(err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, bytes.TrimSpace(raw), "", "  "); err != nil {
		t.Fatal(err)
	}
	altered := payload
	altered.Bytes = append(indented.Bytes(), '\n')
	altered.SHA256 = SHA256Hex(altered.Bytes)
	altered.ByteCount = len(altered.Bytes)

	prompt, err := BuildRaterPrompt("cb-1", "where is the flag parsed?", altered)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPromptCarriesPreservedBundle(prompt, payload); err == nil {
		t.Fatal("a prompt built from a pretty-printed bundle passed the byte-identity check")
	}
}

// Refusal: BuildRaterPrompt will not build from a payload whose digest does not
// recompute, so the byte-identity claim can never rest on an unchecked slice.
func TestQrelBlindSmoke_RaterPromptRefusesAnUncheckedPayload(t *testing.T) {
	raw := encodeLikeTheStdioTransport(t, readyV2Summary(), false, false)
	for _, tc := range []struct {
		name    string
		payload PreservedPayload
		wantSub string
	}{
		{
			name:    "no bytes at all",
			payload: PreservedPayload{Boundary: PayloadBoundaryCandidate},
			wantSub: "no preserved bundle bytes",
		},
		{
			name: "a digest that does not recompute",
			payload: PreservedPayload{Boundary: PayloadBoundaryCandidate, Bytes: raw,
				SHA256: SHA256Hex([]byte("something else")), ByteCount: len(raw)},
			wantSub: "does not recompute",
		},
		{
			name: "a comparator payload",
			payload: PreservedPayload{Boundary: PayloadBoundaryGrepRead, Bytes: raw,
				SHA256: SHA256Hex(raw), ByteCount: len(raw)},
			wantSub: "not \"mcp_jsonrpc_response_bytes\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := BuildRaterPrompt("cb-1", "a question", tc.payload)
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("refusal %q does not mention %q", err.Error(), tc.wantSub)
			}
		})
	}
}
