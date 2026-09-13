package v9

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"unicode/utf8"

	evaltokenizer "github.com/samibel/graphi/core/tokenizer"
	"github.com/samibel/graphi/engine/agenttools/contract"
)

const (
	SavingsCandidateBudget = 1200
	GrepReadWindowLines    = 40
	TokenizerID            = "whitespace-fields-v1"

	SavingsStopExhausted = "exhausted"
	SavingsStopMaxReads  = "max_reads"
	SavingsStopScanLimit = "scan_limit"

	PayloadOperationTaskContext = "task_context/2"
	PayloadOperationGrep        = "grep"
	PayloadOperationRead        = "read"
)

var ErrRetrievalNotReady = errors.New("compact task_context: retrieval is not ready")

type PayloadBoundary string

const (
	PayloadBoundaryCandidate PayloadBoundary = "mcp_jsonrpc_response_bytes"
	PayloadBoundaryGrepRead  PayloadBoundary = "grepread_response_bytes"
)

type PayloadTokenCount struct {
	TokenizerID      string `json:"tokenizer_id"`
	VocabularySHA256 string `json:"vocabulary_sha256,omitempty"`
	Tokens           int    `json:"tokens"`
}

type PreservedPayload struct {
	Sequence    int                 `json:"sequence"`
	Boundary    PayloadBoundary     `json:"boundary"`
	Operation   string              `json:"operation"`
	Bytes       []byte              `json:"bytes"`
	SHA256      string              `json:"sha256"`
	ByteCount   int                 `json:"byte_count"`
	TokenCounts []PayloadTokenCount `json:"token_counts"`
}

type PayloadCounter struct {
	TokenizerID      string
	VocabularySHA256 string
	Count            func([]byte) (int, error)
}

type CapturedPayload struct {
	Sequence  int             `json:"sequence"`
	Boundary  PayloadBoundary `json:"boundary"`
	Operation string          `json:"operation"`
	Bytes     []byte          `json:"bytes"`
	SHA256    string          `json:"sha256"`
	ByteCount int             `json:"byte_count"`
}

type PayloadLedger struct {
	Responses []CapturedPayload `json:"responses"`
}

func (l *PayloadLedger) capture(boundary PayloadBoundary, operation string, response []byte) int {
	copyOfResponse := make([]byte, len(response))
	copy(copyOfResponse, response)
	l.Responses = append(l.Responses, CapturedPayload{
		Sequence: len(l.Responses) + 1, Boundary: boundary, Operation: operation,
		Bytes: copyOfResponse, SHA256: SHA256Hex(copyOfResponse), ByteCount: len(copyOfResponse),
	})
	return len(l.Responses)
}

func (l PayloadLedger) Validate() error {
	if len(l.Responses) == 0 {
		return fmt.Errorf("compact task context: empty source-discovery ledger")
	}
	for i, response := range l.Responses {
		if response.Sequence != i+1 || response.Operation == "" || response.Bytes == nil || !utf8.Valid(response.Bytes) || response.SHA256 != SHA256Hex(response.Bytes) || response.ByteCount != len(response.Bytes) {
			return fmt.Errorf("compact task context: invalid source-discovery response %d", i+1)
		}
	}
	return nil
}

func SHA256Hex(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func isLowerHexDigest(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

type legacyEnvelope struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
}

func taskContextBundleFromCandidateBytes(raw []byte) (contract.Result, error) {
	var envelope legacyEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return contract.Result{}, fmt.Errorf("decode MCP response: %w", err)
	}
	if envelope.Result == nil || envelope.Result.IsError || len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" {
		return contract.Result{}, fmt.Errorf("MCP response does not contain exactly one successful text block")
	}
	var bundle contract.Result
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &bundle); err != nil {
		return contract.Result{}, fmt.Errorf("decode task_context/2 result: %w", err)
	}
	return bundle, nil
}

func validatePayloadCostInput(_ string, payload PreservedPayload, counter PayloadCounter) error {
	if payload.Sequence != 1 || payload.Boundary != PayloadBoundaryCandidate || payload.Operation != PayloadOperationTaskContext || payload.Bytes == nil {
		return fmt.Errorf("input is not one preserved task_context/2 response")
	}
	if payload.SHA256 != SHA256Hex(payload.Bytes) || payload.ByteCount != len(payload.Bytes) {
		return fmt.Errorf("input digest or byte count drift")
	}
	bundle, err := taskContextBundleFromCandidateBytes(payload.Bytes)
	if err != nil {
		return err
	}
	if err := contract.ValidateResult(&bundle); err != nil {
		return err
	}
	if !strings.HasPrefix(bundle.Summary, "task_context/2:") {
		return fmt.Errorf("input does not attest task_context/2 retrieval")
	}
	if !strings.Contains(bundle.Summary, "degradation: ready") {
		return ErrRetrievalNotReady
	}
	if counter.Count == nil || counter.TokenizerID == "" {
		return fmt.Errorf("missing executable payload counter")
	}
	return nil
}

func exactEvidenceSpan(span string) (int, int, error) {
	left, right, ok := strings.Cut(span, "-")
	if !ok || strings.Contains(right, "-") {
		return 0, 0, fmt.Errorf("invalid source span %q", span)
	}
	start, startErr := strconv.Atoi(left)
	end, endErr := strconv.Atoi(right)
	if startErr != nil || endErr != nil || start < 1 || end < start {
		return 0, 0, fmt.Errorf("invalid source span %q", span)
	}
	return start, end, nil
}

// Build runs the preregistered V9 query-only discovery and coherent-region
// selector over one canonical legacy task_context/2 result. The response
// envelope used internally is deterministic and exists only to preserve the
// V9 input-digest semantics; callers receive the transport-neutral fields.
func Build(ctx context.Context, query string, legacy []byte, repository fs.FS, sourceBudget int) (string, CompactTaskContextStructured, error) {
	if sourceBudget <= 0 {
		sourceBudget = 250
	}
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	encoder.SetEscapeHTML(false)
	legacyResponse := legacyEnvelope{JSONRPC: "2.0", ID: 1}
	legacyResponse.Result = &struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}{Content: []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: string(legacy)}}}
	if err := encoder.Encode(legacyResponse); err != nil {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task context: encode input: %w", err)
	}
	raw := bytes.TrimSuffix(content.Bytes(), []byte{'\n'})
	tokenizer, err := evaltokenizer.LoadEmbedded()
	if err != nil {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task context: load embedded tokenizer: %w", err)
	}
	counter := PayloadCounter{TokenizerID: evaltokenizer.TokenizerID, VocabularySHA256: evaltokenizer.PinnedVocabularySHA256, Count: tokenizer.Count}
	input := PreservedPayload{
		Sequence: 1, Boundary: PayloadBoundaryCandidate, Operation: PayloadOperationTaskContext,
		Bytes: raw, SHA256: SHA256Hex(raw), ByteCount: len(raw),
	}
	transcript, err := GrepReadV2(ctx, repository, query)
	if err != nil {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task context: source discovery: %w", err)
	}
	payload, err := BuildCompactTaskContextWithRepository(query, input, &transcript, repository, sourceBudget, counter)
	if err != nil {
		return "", CompactTaskContextStructured{}, err
	}
	return ParseCompactTaskContext(payload.Bytes)
}
