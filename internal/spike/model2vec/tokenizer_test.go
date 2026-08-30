package model2vec

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The tokenizer over the synthetic vocabulary: every BertNormalizer step, the
// BertPreTokenizer, WordPiece, added tokens and truncation, each pinned by
// the id sequence it must produce. (The pinned artifact is exercised by the
// oracle replay; this table runs without it.)
func TestTokenizer_Synthetic(t *testing.T) {
	m := loadSyntheticModel(t)
	tok := m.Tokenizer()
	id := func(s string) int {
		for i, v := range syntheticVocab {
			if v == s {
				return i
			}
		}
		t.Fatalf("no synthetic token %q", s)
		return -1
	}
	unk := id("[UNK]")
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"empty", "", []int{}},
		{"whitespace only", "  \t\n ", []int{}},
		{"lowercase", "Hello WORLD", []int{id("hello"), id("world")}},
		{"wordpiece continuation", "hellos", []int{id("hello"), id("##s")}},
		{"greedy longest first", "unaffable", []int{id("un"), id("##affable")}},
		{"unmatched remainder is one unk", "helloq", []int{unk}},
		{"unknown word", "zzz", []int{unk}},
		{"punctuation isolated", "hello,world", []int{id("hello"), id(","), id("world")}},
		{"ascii symbol counts as punctuation", "hello_world", []int{id("hello"), id("_"), id("world")}},
		{"strip accents via NFD (ß has no decomposition)", "Größe", []int{id("große")}},
		{"NFC and NFD agree", "Größe", []int{id("große")}},
		{"cjk ideograph padded", "hello認world", []int{id("hello"), id("認"), id("world")}},
		{"control character (Cc) dropped", "hello\x01 world", []int{id("hello"), id("world")}},
		{"format character (Cf) dropped", "hello ​world", []int{id("hello"), id("world")}},
		{"nbsp is whitespace", "hello world", []int{id("hello"), id("world")}},
		{"unk literal is an added token", "hello [UNK] world", []int{id("hello"), unk, id("world")}},
		{"unk literal is not lowercased away", "x[UNK]x", []int{id("x"), unk, id("x")}},
		{"pad literal single word", "hello [PAD] world", []int{id("hello"), id("[PAD]"), id("world")}},
		{"pad literal lowercase input", "hello [pad] world", []int{id("hello"), id("[PAD]"), id("world")}},
		{"pad literal glued to a word is not special", "x[PAD]y", []int{id("x"), id("["), id("pad"), id("]"), unk}},
		{"long word is unk", strings.Repeat("a", 101), []int{unk}},
		{"unmatched continuation is unk", "aaa", []int{unk}}, // "a" matches, "##aa"/"##a" do not
		{"digits", "12", []int{id("1"), id("##2")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := tok.Encode(c.text); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Encode(%q) = %v %v, want %v %v", c.text, got, tok.Tokens(got), c.want, tok.Tokens(c.want))
			}
		})
	}
}

func TestTokenizer_Truncation(t *testing.T) {
	m := loadSyntheticModel(t)
	text := strings.Repeat("hello ", 600)
	ids := m.TokenIDs(text)
	if len(ids) != 512 {
		t.Fatalf("TokenIDs of 600 words has %d ids, want 512 (tokenizer truncation)", len(ids))
	}
	// The model cut: 512 × median token length code points, then ≤ 512 tokens.
	if got := m.InferenceIDs(text); len(got) > 512 {
		t.Fatalf("InferenceIDs has %d ids, want ≤ 512", len(got))
	}
}

// InferenceIDs drops unk ids (model2vec's tokenize) while TokenIDs keeps them.
func TestInferenceIDs_DropsUnk(t *testing.T) {
	m := loadSyntheticModel(t)
	raw := m.TokenIDs("hello zzz world")
	if !reflect.DeepEqual(raw, []int{2, 1, 3}) {
		t.Fatalf("TokenIDs = %v, want [2 1 3]", raw)
	}
	if got := m.InferenceIDs("hello zzz world"); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("InferenceIDs = %v, want [2 3]", got)
	}
	if got := m.InferenceIDs("zzz"); len(got) != 0 {
		t.Fatalf("InferenceIDs(all unk) = %v, want empty", got)
	}
}

// The character cut happens BEFORE tokenisation, in code points, at
// max_length × median token length.
func TestInferenceIDs_CharacterCutPrecedesTokenisation(t *testing.T) {
	m := loadSyntheticModel(t)
	limit := m.maxLength * m.MedianTokenLength()
	if limit <= 0 {
		t.Fatalf("limit %d", limit)
	}
	// "認" is one code point and one id; a word placed beyond the cut must
	// never appear.
	text := strings.Repeat("認", limit) + " hello"
	for _, id := range m.InferenceIDs(text) {
		if id == 2 {
			t.Fatalf("token beyond the %d-code-point cut survived", limit)
		}
	}
}

// The loader refuses tokenizer.json files whose components the spike does not
// implement, instead of approximating them.
func TestLoadTokenizer_RefusesUnsupportedComponents(t *testing.T) {
	base := writeSyntheticModel(t, 4)
	raw, err := os.ReadFile(filepath.Join(base, FileTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, from, to, wantErr string
	}{
		{"normalizer", `"BertNormalizer"`, `"NFKC"`, "not the BertNormalizer"},
		{"pre_tokenizer", `"BertPreTokenizer"`, `"ByteLevel"`, "not the BertPreTokenizer"},
		{"post_processor", `"post_processor":null`, `"post_processor":{"type":"BertProcessing"}`, "post_processor"},
		{"model", `"type":"WordPiece"`, `"type":"BPE"`, "not WordPiece"},
		{"truncation", `"strategy":"LongestFirst"`, `"strategy":"OnlyFirst"`, "truncation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := strings.ReplaceAll(string(raw), c.from, c.to)
			if mutated == string(raw) {
				t.Fatalf("mutation %q not applied", c.from)
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileTokenizer), []byte(mutated), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadTokenizer(filepath.Join(dir, FileTokenizer))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("LoadTokenizer error = %v, want it to mention %q", err, c.wantErr)
			}
		})
	}
}

func TestNormalizer_HangulDecomposes(t *testing.T) {
	m := loadSyntheticModel(t)
	got := m.Tokenizer().normalize("한")
	if want := []rune{0x1112, 0x1161, 0x11AB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize(한) = %U, want %U", got, want)
	}
}
