package model2vec

import (
	"encoding/json"
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
// implement, instead of approximating them. Each entry mutates one field of a
// synthetic tokenizer.json to a value the spike does not implement and asserts
// the load error names the offending field.
//
//   - normalizer/pre_tokenizer/model: switch to a type the spike does not implement
//   - post_processor: replace the absent section with a non-null one
//   - truncation: switch to an unsupported strategy / direction / stride / max_length
//   - model.unk_token / model.continuing_subword_prefix / model.max_input_chars_per_word: each
//     validated individually so a regression that drops the validation surfaces here
//   - added_tokens: refuse non-special and out-of-vocabulary added tokens
//   - padding (run against a synthetic that declares padding): refuse non-BatchLongest strategy,
//     non-Right direction, non-null pad_to_multiple_of, non-zero pad_type_id, out-of-range
//     pad_id, and a pad_token that does not match the vocabulary at pad_id
//
// Every case asserts the error names the offending field; a regression that
// silently accepts an unsupported setting would fail closed here.
func TestLoadTokenizer_RefusesUnsupportedComponents(t *testing.T) {
	base := writeSyntheticModel(t, 4, nil)
	raw, err := os.ReadFile(filepath.Join(base, FileTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, from, to, wantErr string
	}{
		{"version", `"version":"1.0"`, `"version":"2.0"`, "version"},
		{"normalizer_type", `"type":"BertNormalizer"`, `"type":"NFKC"`, "not the BertNormalizer"},
		{"pre_tokenizer_type", `"type":"BertPreTokenizer"`, `"type":"ByteLevel"`, "not the BertPreTokenizer"},
		{"post_processor_present", `"post_processor":null`, `"post_processor":{"type":"BertProcessing"}`, "post_processor"},
		{"model_type", `"type":"WordPiece"`, `"type":"BPE"`, "not WordPiece"},
		{"model_unk_token_not_in_vocab", `"unk_token":"[UNK]"`, `"unk_token":"[NOPE]"`, "model.unk_token"},
		{"model_continuing_subword_prefix_empty", `"continuing_subword_prefix":"##"`, `"continuing_subword_prefix":""`, "model.continuing_subword_prefix"},
		{"model_max_input_chars_per_word_zero", `"max_input_chars_per_word":100`, `"max_input_chars_per_word":0`, "model.max_input_chars_per_word"},
		{"truncation_direction", `"direction":"Right"`, `"direction":"Left"`, "truncation.direction"},
		{"truncation_strategy", `"strategy":"LongestFirst"`, `"strategy":"OnlyFirst"`, "truncation.strategy"},
		{"truncation_stride", `"stride":0`, `"stride":5`, "truncation.stride"},
		{"truncation_max_length", `"max_length":512`, `"max_length":0`, "truncation.max_length"},
		{"added_tokens_non_special", `"special":true`, `"special":false`, "not special"},
		{"added_tokens_unk_not_in_vocab", `"content":"[PAD]"`, `"content":"[NOPE]"`, "added_tokens[0]"},
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

// Padding-specific refusals run against a synthetic that DOES declare padding,
// so the mutation has somewhere to land. Several padding fields (direction,
// pad_id, pad_token, pad_type_id, pad_to_multiple_of) share their name with
// fields in the truncation or added_tokens sections, so this test parses the
// JSON, mutates the padding object as a map, and re-serialises — the
// truncation and added_tokens sections stay byte-identical, the padding
// section's offending field changes, and the load error must name the padding
// field that was mutated.
func TestLoadTokenizer_RefusesUnsupportedPadding(t *testing.T) {
	base := writeSyntheticModel(t, 4, syntheticPaddingBatchLongest)
	raw, err := os.ReadFile(filepath.Join(base, FileTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	mutatePadding := func(t *testing.T, raw []byte, mutate func(map[string]any)) []byte {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		pad, ok := doc["padding"].(map[string]any)
		if !ok {
			t.Fatalf("synthetic padding section is not an object: %T", doc["padding"])
		}
		mutate(pad)
		out, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{"padding_strategy", func(p map[string]any) { p["strategy"] = "Fixed" }, "padding.strategy"},
		{"padding_direction", func(p map[string]any) { p["direction"] = "Left" }, "padding.direction"},
		{"padding_pad_type_id", func(p map[string]any) { p["pad_type_id"] = float64(1) }, "padding.pad_type_id"},
		{"padding_pad_to_multiple_of", func(p map[string]any) { p["pad_to_multiple_of"] = float64(8) }, "padding.pad_to_multiple_of"},
		{"padding_pad_id_out_of_range", func(p map[string]any) { p["pad_id"] = float64(99999) }, "padding.pad_id"},
		{"padding_pad_token_mismatch", func(p map[string]any) { p["pad_token"] = "[UNK]" }, "padding.pad_token"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mutated := mutatePadding(t, raw, c.mutate)
			if string(mutated) == string(raw) {
				t.Fatalf("mutation not applied")
			}
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileTokenizer), mutated, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := LoadTokenizer(filepath.Join(dir, FileTokenizer))
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("LoadTokenizer error = %v, want it to mention %q", err, c.wantErr)
			}
		})
	}
}

// The decoder section is intentionally accepted as irrelevant (it renders ids
// back to text, which neither model2vec nor this spike does); mutate it
// arbitrarily and assert the load still succeeds.
func TestLoadTokenizer_AcceptsAnyDecoder(t *testing.T) {
	base := writeSyntheticModel(t, 4, nil)
	raw, err := os.ReadFile(filepath.Join(base, FileTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.ReplaceAll(string(raw), `"decoder":{"cleanup":true,"prefix":"##","type":"WordPiece"}`, `"decoder":{"cleanup":false,"suffix":"</w>","type":"BPEDecoder"}`)
	if mutated == string(raw) {
		t.Fatal("decoder mutation not applied")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileTokenizer), []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTokenizer(filepath.Join(dir, FileTokenizer)); err != nil {
		t.Fatalf("LoadTokenizer with an arbitrary decoder must still succeed: %v", err)
	}
}

// An unknown top-level section is refused (it would mean a setting this spike
// does not model can pass silently through).
func TestLoadTokenizer_RefusesUnknownTopLevelSection(t *testing.T) {
	base := writeSyntheticModel(t, 4, nil)
	raw, err := os.ReadFile(filepath.Join(base, FileTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(raw), `"version":"1.0"`, `"version":"1.0","chat_template":"{% for m in messages %}{{m}}{% endfor %}"`, 1)
	if mutated == string(raw) {
		t.Fatal("chat_template mutation not applied")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileTokenizer), []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadTokenizer(filepath.Join(dir, FileTokenizer))
	if err == nil || !strings.Contains(err.Error(), "chat_template") {
		t.Fatalf("LoadTokenizer error = %v, want it to name the unknown top-level section", err)
	}
}

func TestNormalizer_HangulDecomposes(t *testing.T) {
	m := loadSyntheticModel(t)
	got := m.Tokenizer().normalize("한")
	if want := []rune{0x1112, 0x1161, 0x11AB}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize(한) = %U, want %U", got, want)
	}
}
