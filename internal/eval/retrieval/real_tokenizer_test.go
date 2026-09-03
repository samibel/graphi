package retrieval

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

func TestPinnedRealPayloadCounter_RecomputesPreservedBytesWithoutMutation(t *testing.T) {
	dir := filepath.Join("..", "tokenizer", "testdata", "artifact")
	tok, err := evaltokenizer.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	counter, err := NewPinnedRealPayloadCounter(tok)
	if err != nil {
		t.Fatal(err)
	}
	if counter.TokenizerID != evaltokenizer.TokenizerID || counter.VocabularySHA256 != evaltokenizer.PinnedVocabularySHA256 || counter.Count == nil {
		t.Fatalf("PayloadCounter identity = %+v", counter)
	}
	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"text":"ValidateSavingsAggregateInput"}}` + "\n")
	before := append([]byte(nil), payload...)
	first, err := counter.Count(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, before) {
		t.Fatal("counter mutated the preserved payload")
	}
	payload = append(payload, []byte("// changed after the first count\n")...)
	second, err := counter.Count(payload)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("counter cached a prior result: before=%d after=%d", first, second)
	}
}

func TestLoadPinnedRealPayloadCounter_MissingArtifactFailsWithoutFallback(t *testing.T) {
	t.Setenv("GRAPHI_EVAL_TOKENIZER_DIR", t.TempDir())
	_, err := LoadPinnedRealPayloadCounter()
	if err == nil {
		t.Fatal("measurement counter accepted an absent artifact or silently fell back")
	}
	for _, want := range []string{
		evaltokenizer.TokenizerID,
		evaltokenizer.PinnedVocabularyFile,
		"expected " + evaltokenizer.PinnedVocabularySHA256,
		"actual unavailable",
		"-setup-tokenizer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing-artifact diagnostic does not contain %q: %v", want, err)
		}
	}
	t.Logf("observed measurement failure: %s", err)
}

func TestNewPinnedRealPayloadCounter_RejectsNil(t *testing.T) {
	if _, err := NewPinnedRealPayloadCounter(nil); err == nil {
		t.Fatal("nil tokenizer accepted")
	}
}

func TestPinnedRealPayloadCounter_TestArtifactExists(t *testing.T) {
	path := filepath.Join("..", "tokenizer", "testdata", "artifact", evaltokenizer.PinnedVocabularyFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("checked-in real tokenizer artifact missing: %v", err)
	}
}
