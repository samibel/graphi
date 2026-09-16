package retrieval

import (
	"errors"
	"os"

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

// LoadPinnedRealPayloadCounter is the measurement entry point. By default it
// verifies and loads the governed vocabulary embedded in the binary, keeping
// clean and offline evaluators hermetic. An explicit artifact-directory
// override remains fail-closed so conformance checks can detect absent or
// corrupt external bytes. There is deliberately no whitespace fallback.
func LoadPinnedRealPayloadCounter() (PayloadCounter, error) {
	var (
		tok *evaltokenizer.Tokenizer
		err error
	)
	if os.Getenv("GRAPHI_EVAL_TOKENIZER_DIR") != "" {
		tok, err = evaltokenizer.LoadPinned()
	} else {
		tok, err = evaltokenizer.LoadEmbedded()
	}
	if err != nil {
		return PayloadCounter{}, err
	}
	return NewPinnedRealPayloadCounter(tok)
}
