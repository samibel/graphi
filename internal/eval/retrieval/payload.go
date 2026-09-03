package retrieval

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// CapturedPayload is one response body at the point where GrepRead returns it
// to its actor. Bytes is never reconstructed from source coordinates.
type CapturedPayload struct {
	Sequence  int             `json:"sequence"`
	Boundary  PayloadBoundary `json:"boundary"`
	Operation string          `json:"operation"`
	Bytes     []byte          `json:"bytes"`
	SHA256    string          `json:"sha256"`
	ByteCount int             `json:"byte_count"`
}

// PayloadLedger is the ordered, lossless response side of a tool transcript.
// Its entries are appended only from the bytes an operation is about to
// return. In particular, it has no API that accepts a file window or a count.
type PayloadLedger struct {
	Responses []CapturedPayload `json:"responses"`
}

func (l *PayloadLedger) capture(boundary PayloadBoundary, operation string, response []byte) int {
	if response == nil {
		response = make([]byte, 0)
	}
	raw := make([]byte, len(response))
	copy(raw, response)
	l.Responses = append(l.Responses, CapturedPayload{
		Sequence:  len(l.Responses) + 1,
		Boundary:  boundary,
		Operation: operation,
		Bytes:     raw,
		SHA256:    SHA256Hex(raw),
		ByteCount: len(raw),
	})
	return len(l.Responses)
}

// ConcatenatedBytes returns the complete measured payload in call order.
// Empty and error responses occupy entries even when they add zero bytes.
func (l PayloadLedger) ConcatenatedBytes() []byte {
	all := make([]byte, 0, l.TotalByteCount())
	for _, response := range l.Responses {
		all = append(all, response.Bytes...)
	}
	return all
}

// TotalByteCount recomputes cost from the captured slices, not ByteCount.
func (l PayloadLedger) TotalByteCount() int {
	total := 0
	for _, response := range l.Responses {
		total += len(response.Bytes)
	}
	return total
}

// DigestSHA256 identifies the complete concatenated payload byte stream.
func (l PayloadLedger) DigestSHA256() string {
	return SHA256Hex(l.ConcatenatedBytes())
}

// Validate rejects any ledger whose descriptive fields cannot be recomputed
// from its exact response bytes.
func (l PayloadLedger) Validate() error {
	if len(l.Responses) == 0 {
		return fmt.Errorf("retrieval payload ledger: no responses")
	}
	for i, response := range l.Responses {
		sequence := i + 1
		if response.Sequence != sequence {
			return fmt.Errorf("retrieval payload ledger: response sequence=%d, want %d", response.Sequence, sequence)
		}
		if response.Boundary != PayloadBoundaryCandidate && response.Boundary != PayloadBoundaryGrepRead {
			return fmt.Errorf("retrieval payload ledger: response %d has unknown boundary %q", sequence, response.Boundary)
		}
		if response.Operation == "" {
			return fmt.Errorf("retrieval payload ledger: response %d has no operation", sequence)
		}
		if response.Bytes == nil {
			return fmt.Errorf("retrieval payload ledger: response %d has no preserved bytes", sequence)
		}
		if !utf8.Valid(response.Bytes) {
			return fmt.Errorf("retrieval payload ledger: response %d is not valid UTF-8", sequence)
		}
		if response.SHA256 != SHA256Hex(response.Bytes) {
			return fmt.Errorf("retrieval payload ledger: response %d sha256 does not match its preserved bytes", sequence)
		}
		if response.ByteCount != len(response.Bytes) {
			return fmt.Errorf("retrieval payload ledger: response %d byte_count=%d, recomputed=%d", sequence, response.ByteCount, len(response.Bytes))
		}
	}
	return nil
}

// PreservedPayloads recomputes the two contract token counts from each
// captured response. The real counter is supplied by the later tokenizer
// slice; its identity and vocabulary digest are recorded beside every count.
func (l PayloadLedger) PreservedPayloads(real PayloadCounter) ([]PreservedPayload, error) {
	if err := l.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(real.TokenizerID) == "" || real.TokenizerID == TokenizerID || real.Count == nil {
		return nil, fmt.Errorf("retrieval payload ledger: a distinct executable real tokenizer is required")
	}
	if !isLowerHexDigest(real.VocabularySHA256, 64) {
		return nil, fmt.Errorf("retrieval payload ledger: real tokenizer vocabulary sha256 must be 64 lowercase hex characters")
	}

	out := make([]PreservedPayload, 0, len(l.Responses))
	for _, response := range l.Responses {
		counterInput := make([]byte, len(response.Bytes))
		copy(counterInput, response.Bytes)
		realTokens, err := real.Count(counterInput)
		if err != nil {
			return nil, fmt.Errorf("retrieval payload ledger: response %d tokenizer %q: %w", response.Sequence, real.TokenizerID, err)
		}
		if realTokens < 0 {
			return nil, fmt.Errorf("retrieval payload ledger: response %d tokenizer %q returned negative count %d", response.Sequence, real.TokenizerID, realTokens)
		}
		preservedBytes := make([]byte, len(response.Bytes))
		copy(preservedBytes, response.Bytes)
		out = append(out, PreservedPayload{
			Sequence:  response.Sequence,
			Boundary:  response.Boundary,
			Operation: response.Operation,
			Bytes:     preservedBytes,
			SHA256:    SHA256Hex(response.Bytes),
			ByteCount: len(response.Bytes),
			TokenCounts: []PayloadTokenCount{
				{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(response.Bytes)))},
				{TokenizerID: real.TokenizerID, VocabularySHA256: real.VocabularySHA256, Tokens: realTokens},
			},
		})
	}
	return out, nil
}
