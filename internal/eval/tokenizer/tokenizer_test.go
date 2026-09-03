package tokenizer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGoldenTokenVectors_DifferFromWhitespace(t *testing.T) {
	tok := loadRealArtifact(t)
	tests := []struct {
		name       string
		text       string
		real       int
		whitespace int
		ids        []int
	}{
		{
			name: "identifier", text: "ValidateSavingsAggregateInput",
			real: 5, whitespace: 1,
			ids: []int{18409, 50, 46851, 65680, 2566},
		},
		{
			name: "punctuation-dense code", text: `if err := validate(foo.Bar()); err != nil { return fmt.Errorf("boom: %w", err) }`,
			real: 23, whitespace: 13,
			ids: []int{333, 1886, 1703, 9788, 72980, 41620, 13732, 1886, 976, 2139, 314, 471, 9055, 13380, 446, 96416, 25, 1034, 86, 498, 1886, 8, 335},
		},
		{
			name: "long path", text: "docs/eval/retrieval/runs/2026-09-03-sw277-tokenizer/raw/task_context.jsonl",
			real: 24, whitespace: 1,
			ids: []int{14452, 14, 14504, 10991, 9104, 838, 49485, 82, 14, 2366, 21, 12, 2545, 12, 2839, 62979, 16367, 35941, 3213, 77009, 59286, 8634, 4421, 75},
		},
		{
			name: "UTF-8 outside ASCII", text: "Grüße, 世界 — café ☕️",
			real: 13, whitespace: 5,
			ids: []int{6600, 2448, 24352, 11, 220, 3574, 244, 98220, 2001, 53050, 26182, 243, 31643},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.real == tt.whitespace {
				t.Fatalf("golden is vacuous: real=%d whitespace=%d", tt.real, tt.whitespace)
			}
			ids, err := tok.Encode([]byte(tt.text))
			if err != nil {
				t.Fatal(err)
			}
			whitespace := len(strings.Fields(tt.text))
			if !reflect.DeepEqual(ids, tt.ids) || len(ids) != tt.real || whitespace != tt.whitespace {
				t.Fatalf("%s counts: real=%d (ids=%v), whitespace=%d; want real=%d (ids=%v), whitespace=%d",
					tt.name, len(ids), ids, whitespace, tt.real, tt.ids, tt.whitespace)
			}
			t.Logf("golden %s: real=%d whitespace=%d ids=%v", tt.name, len(ids), whitespace, ids)
		})
	}
}

func TestLoad_RejectsOneByteCorruptionWithBothHashes(t *testing.T) {
	dir := realArtifactDir(t)
	body, err := os.ReadFile(filepath.Join(dir, PinnedVocabularyFile))
	if err != nil {
		t.Fatal(err)
	}
	body[len(body)/2] ^= 0x01
	corruptDir := t.TempDir()
	path := filepath.Join(corruptDir, PinnedVocabularyFile)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	actual := sha256.Sum256(body)
	want := fmt.Sprintf("eval tokenizer: %s: SHA-256 mismatch: expected %s, actual %s (path=%s)",
		PinnedVocabularyFile, PinnedVocabularySHA256, hex.EncodeToString(actual[:]), path)
	_, err = Load(corruptDir)
	if err == nil {
		t.Fatal("Load accepted a one-byte-corrupted real artifact")
	}
	var mismatch *PinMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Load error type = %T, want *PinMismatchError: %v", err, err)
	}
	if err.Error() != want {
		t.Fatalf("corruption diagnostic:\n got %q\nwant %q", err, want)
	}
	t.Logf("observed diagnostic: %s", err)
}

func TestLoadPinned_MissingArtifactFailsLoudlyWithoutFallback(t *testing.T) {
	t.Setenv(envArtifactDir, t.TempDir())
	_, err := LoadPinned()
	if err == nil {
		t.Fatal("LoadPinned accepted an absent artifact or silently fell back")
	}
	for _, want := range []string{
		TokenizerID,
		PinnedVocabularyFile,
		"expected " + PinnedVocabularySHA256,
		"actual unavailable",
		"-setup-tokenizer",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing-artifact diagnostic does not contain %q: %v", want, err)
		}
	}
	t.Logf("observed diagnostic: %s", err)
}

func TestEncode_RejectsInvalidUTF8(t *testing.T) {
	tok := testTokenizer()
	if _, err := tok.Count([]byte{0xff}); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("Count invalid UTF-8 = %v", err)
	}
}

func TestPreTokenize_FrozenCl100kPattern(t *testing.T) {
	text := "we're ValidateSavingsAggregateInput 123456 !!\r\n  tail  "
	want := []string{"we", "'re", " ValidateSavingsAggregateInput", " ", "123", "456", " !!\r\n", " ", " tail", "  "}
	if got := preTokenize(text); !reflect.DeepEqual(got, want) {
		t.Fatalf("preTokenize(%q) = %#v, want %#v", text, got, want)
	}
}

func TestIdentity_StampsTokenizerAndVocabularyTogether(t *testing.T) {
	id, vocabulary := Identity()
	if id != TokenizerID || vocabulary != PinnedSHA256[PinnedVocabularyFile] {
		t.Fatalf("Identity() = (%q, %q), pin table = (%q, %q)", id, vocabulary, TokenizerID, PinnedSHA256[PinnedVocabularyFile])
	}
}

func loadRealArtifact(t *testing.T) *Tokenizer {
	t.Helper()
	dir := realArtifactDir(t)
	tok, err := Load(dir)
	if err != nil {
		t.Fatalf("real artifact at %s is present but unusable: %v", dir, err)
	}
	return tok
}

func realArtifactDir(t *testing.T) string {
	t.Helper()
	if os.Getenv(envArtifactDir) != "" {
		dir := ArtifactDir()
		if _, err := os.Stat(filepath.Join(dir, PinnedVocabularyFile)); err != nil {
			t.Fatalf("explicit real-tokenizer test artifact is absent at %s: %v", dir, err)
		}
		return dir
	}
	return filepath.Join("testdata", "artifact")
}

func testTokenizer() *Tokenizer {
	ranks := make(map[string]int, 258)
	for i := 0; i < 256; i++ {
		ranks[string([]byte{byte(i)})] = i
	}
	ranks["hello"] = 256
	ranks[" world"] = 257
	return &Tokenizer{ranks: ranks}
}
