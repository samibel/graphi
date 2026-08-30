package model2vec

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// syntheticVocab is a tiny WordPiece vocabulary in the shape of the pinned
// one: [PAD]=0, [UNK]=1, then whole words and "##" continuations. It lets the
// loader, tokenizer and pooling arithmetic be tested without the 32 MB
// artifact, so those tests never skip.
var syntheticVocab = []string{
	"[PAD]", "[UNK]", "hello", "world", "##s", "un", "##affable", ",", "認", "große",
	"a", "##b", "validate", "##token", "token", "1", "##2", "x", "foo", "_", "##pad",
	"[", "]", "pad", "!", "?", ".", "the", "is", "##er",
}

// writeSyntheticModel writes a complete Model2Vec directory into a temp dir:
// the pinned tokenizer.json component set over syntheticVocab, a config.json
// with normalize=true, and an F16 safetensors table whose row i is
// [i, -i/2, 0.25, i*0.001] (exact in binary16 where it matters).
func writeSyntheticModel(t testing.TB, dim int) string {
	t.Helper()
	dir := t.TempDir()
	vocab := map[string]int{}
	for i, tok := range syntheticVocab {
		vocab[tok] = i
	}
	tokenizer := map[string]any{
		"version": "1.0",
		"truncation": map[string]any{
			"direction": "Right", "max_length": 512, "strategy": "LongestFirst", "stride": 0,
		},
		"padding": nil,
		"added_tokens": []map[string]any{
			{"id": 0, "content": "[PAD]", "single_word": true, "lstrip": true, "rstrip": true, "normalized": true, "special": true},
			{"id": 1, "content": "[UNK]", "single_word": false, "lstrip": false, "rstrip": false, "normalized": false, "special": true},
		},
		"normalizer": map[string]any{
			"type": "BertNormalizer", "clean_text": true, "handle_chinese_chars": true, "strip_accents": nil, "lowercase": true,
		},
		"pre_tokenizer":  map[string]any{"type": "BertPreTokenizer"},
		"post_processor": nil,
		"decoder":        map[string]any{"type": "WordPiece", "prefix": "##", "cleanup": true},
		"model": map[string]any{
			"type": "WordPiece", "unk_token": "[UNK]", "continuing_subword_prefix": "##",
			"max_input_chars_per_word": 100, "vocab": vocab,
		},
	}
	writeJSON(t, filepath.Join(dir, FileTokenizer), tokenizer)
	writeJSON(t, filepath.Join(dir, FileConfig), map[string]any{"normalize": true, "embedding_dtype": "float16"})
	writeJSON(t, filepath.Join(dir, FileModules), []map[string]any{
		{"idx": 0, "name": "0", "path": ".", "type": "sentence_transformers.models.StaticEmbedding"},
		{"idx": 1, "name": "1", "path": "1_Normalize", "type": "sentence_transformers.models.Normalize"},
	})

	rows := len(syntheticVocab)
	data := make([]byte, rows*dim*2)
	for i := 0; i < rows; i++ {
		for j := 0; j < dim; j++ {
			var v float32
			switch j % 4 {
			case 0:
				v = float32(i)
			case 1:
				v = -float32(i) / 2
			case 2:
				v = 0.25
			case 3:
				v = float32(i) * 0.001
			}
			binary.LittleEndian.PutUint16(data[(i*dim+j)*2:], f32ToF16(v))
		}
	}
	header, err := json.Marshal(map[string]any{
		tensorName: map[string]any{"dtype": "F16", "shape": []int{rows, dim}, "data_offsets": []int{0, len(data)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var file []byte
	file = binary.LittleEndian.AppendUint64(file, uint64(len(header)))
	file = append(file, header...)
	file = append(file, data...)
	if err := os.WriteFile(filepath.Join(dir, FileSafetensors), file, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeJSON(t testing.TB, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func loadSyntheticModel(t testing.TB) *Model {
	t.Helper()
	m, err := LoadModel(writeSyntheticModel(t, 8))
	if err != nil {
		t.Fatalf("LoadModel(synthetic): %v", err)
	}
	return m
}
