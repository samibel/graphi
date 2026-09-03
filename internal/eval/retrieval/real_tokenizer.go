package retrieval

import (
	"errors"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// NewPinnedRealPayloadCounter adapts an already verified cl100k_base
// tokenizer to the executable SW-274 counter contract. Keeping the adapter in
// retrieval lets the tokenizer remain a deep, reusable module with no upward
// dependency on the measurement package.
func NewPinnedRealPayloadCounter(tok *evaltokenizer.Tokenizer) (PayloadCounter, error) {
	if tok == nil {
		return PayloadCounter{}, errors.New("retrieval measurement contract: pinned real tokenizer is nil")
	}
	return PayloadCounter{
		TokenizerID:      evaltokenizer.TokenizerID,
		VocabularySHA256: evaltokenizer.PinnedVocabularySHA256,
		Count:            tok.Count,
	}, nil
}

// LoadPinnedRealPayloadCounter is the measurement entry point. Missing or
// corrupt artifact bytes are returned as fatal errors; there is deliberately
// no whitespace fallback.
func LoadPinnedRealPayloadCounter() (PayloadCounter, error) {
	tok, err := evaltokenizer.LoadPinned()
	if err != nil {
		return PayloadCounter{}, err
	}
	return NewPinnedRealPayloadCounter(tok)
}
