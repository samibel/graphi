// Package static's tokenizer: a strict, fail-closed loader of the pinned
// BertNormalizer → BertPreTokenizer → WordPiece pipeline, with the two added
// tokens [PAD]/[UNK], right truncation at 512 tokens, and no post-processor.
//
// The loader validates every top-level section of tokenizer.json: an
// unexpected value or section fails the load naming the field. This is the
// SW-259 carry-forward "validate the tokenizer's `padding` section
// fail-closed" requirement: a BatchLongest padding section is REQUIRED, with
// the pad id read from the file (never hardcoded), and the loader refuses
// any other padding strategy / direction / pad type / pad-to-multiple-of
// value. Without this guard, chunk composition could silently change a
// vector's mean-pooled ids.
package static

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// tokenizerSections are the top-level keys of a HuggingFace tokenizers JSON
// file this package knows. Every encoding-relevant section is parsed with
// DisallowUnknownFields and validated against the component set the pinned
// tokenizer.json declares; any other top-level key is refused. "decoder" is
// the one section that is read but not validated: it configures ids → text
// rendering, which neither model2vec's encode nor this package ever performs,
// so its value cannot affect a token id or a vector.
var tokenizerSections = map[string]bool{
	"version":        true,
	"truncation":     true,
	"padding":        true,
	"added_tokens":   true,
	"normalizer":     true,
	"pre_tokenizer":  true,
	"post_processor": true,
	"decoder":        true,
	"model":          true,
}

// supportedTokenizerVersion is the tokenizers JSON format version of the
// pinned file.
const supportedTokenizerVersion = "1.0"

type truncationSpec struct {
	Direction string `json:"direction"`
	MaxLength int    `json:"max_length"`
	Strategy  string `json:"strategy"`
	Stride    int    `json:"stride"`
}

// paddingSpec is tokenizers' PaddingParams. strategy is either the string
// "BatchLongest" or an object {"Fixed": n}; only the former is implemented.
type paddingSpec struct {
	Strategy        json.RawMessage `json:"strategy"`
	Direction       string          `json:"direction"`
	PadToMultipleOf *int            `json:"pad_to_multiple_of"`
	PadID           int             `json:"pad_id"`
	PadTypeID       int             `json:"pad_type_id"`
	PadToken        string          `json:"pad_token"`
}

type addedToken struct {
	ID         int    `json:"id"`
	Content    string `json:"content"`
	SingleWord bool   `json:"single_word"`
	LStrip     bool   `json:"lstrip"`
	RStrip     bool   `json:"rstrip"`
	Normalized bool   `json:"normalized"`
	Special    bool   `json:"special"`
}

type bertNormalizerSpec struct {
	Type               string `json:"type"`
	CleanText          bool   `json:"clean_text"`
	HandleChineseChars bool   `json:"handle_chinese_chars"`
	StripAccents       *bool  `json:"strip_accents"`
	Lowercase          bool   `json:"lowercase"`
}

type typedSpec struct {
	Type string `json:"type"`
}

type modelSpec struct {
	Type                    string         `json:"type"`
	UnkToken                string         `json:"unk_token"`
	ContinuingSubwordPrefix string         `json:"continuing_subword_prefix"`
	MaxInputCharsPerWord    int            `json:"max_input_chars_per_word"`
	Vocab                   map[string]int `json:"vocab"`
}

// Tokenizer is the BertNormalizer → BertPreTokenizer → WordPiece pipeline of
// the pinned tokenizer.json, with its added tokens and right truncation. The
// production embedder does NOT apply BatchLongest padding (EmbedEach is
// batch-invariant) — but the loader still validates the padding block
// fail-closed so a different pinned revision whose tokenizer.json declares
// no padding (or declares a different strategy) is rejected at load time.
// Otherwise the chunk composition could silently change a vector.
type Tokenizer struct {
	vocab         map[string]int
	tokens        []string // id → token string
	unkID         int
	prefix        string
	maxInputChars int
	maxLength     int // truncation length; 0 = none

	// padEnabled mirrors a non-null "padding" section. The production
	// embedder never pools pad ids (EmbedEach is batch-invariant), but the
	// loader still validates the block fail-closed so a tokenizer.json
	// whose padding strategy is not BatchLongest/Right (or whose pad_id is
	// out of range) is refused at load time.
	padEnabled bool
	padID      int

	cleanText    bool
	chineseChars bool
	stripAccents bool
	lowercase    bool

	rawAdded  []addedMatch // matched on the raw text (normalized: false)
	normAdded []addedMatch // matched on the normalized text (normalized: true)
}

// addedMatch is an added token prepared for matching.
type addedMatch struct {
	content    []rune
	id         int
	singleWord bool
	lstrip     bool
	rstrip     bool
}

// LoadTokenizer parses a tokenizer.json and validates that every
// encoding-relevant section uses exactly the settings this embedder
// implements. A section or value outside that set is a load error naming
// the field; nothing is approximated.
func LoadTokenizer(path string) (*Tokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sections map[string]json.RawMessage
	if err := json.Unmarshal(raw, &sections); err != nil {
		return nil, fmt.Errorf("static: tokenizer %s: %w", path, err)
	}
	fail := func(field, format string, args ...any) error {
		return fmt.Errorf("static: tokenizer %s: %s: %s", path, field, fmt.Sprintf(format, args...))
	}
	keys := make([]string, 0, len(sections))
	for k := range sections {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !tokenizerSections[k] {
			return nil, fail(k, "unsupported top-level section (value %s); this embedder implements only %v", compact(sections[k]), sortedKeys(tokenizerSections))
		}
	}

	// version
	var version string
	if err := decodeStrict(sections["version"], &version); err != nil {
		return nil, fail("version", "%v", err)
	}
	if version != supportedTokenizerVersion {
		return nil, fail("version", "%q is not the tokenizers JSON version %q this embedder implements", version, supportedTokenizerVersion)
	}

	// normalizer
	if isNull(sections["normalizer"]) {
		return nil, fail("normalizer", "missing; the pinned file declares a BertNormalizer")
	}
	var norm bertNormalizerSpec
	if err := decodeStrict(sections["normalizer"], &norm); err != nil {
		return nil, fail("normalizer", "%v (value %s)", err, compact(sections["normalizer"]))
	}
	if norm.Type != "BertNormalizer" {
		return nil, fail("normalizer.type", "%q is not the BertNormalizer this embedder implements", norm.Type)
	}

	// pre_tokenizer
	if isNull(sections["pre_tokenizer"]) {
		return nil, fail("pre_tokenizer", "missing; the pinned file declares a BertPreTokenizer")
	}
	var pre typedSpec
	if err := decodeStrict(sections["pre_tokenizer"], &pre); err != nil {
		return nil, fail("pre_tokenizer", "%v (value %s)", err, compact(sections["pre_tokenizer"]))
	}
	if pre.Type != "BertPreTokenizer" {
		return nil, fail("pre_tokenizer.type", "%q is not the BertPreTokenizer this embedder implements", pre.Type)
	}

	// post_processor: must be absent — the ids are used raw, without special
	// tokens, and model2vec encodes with add_special_tokens=False.
	if !isNull(sections["post_processor"]) {
		return nil, fail("post_processor", "%s is not supported; the pinned file has none", compact(sections["post_processor"]))
	}

	// decoder: accepted as irrelevant. It describes how ids are rendered
	// back to text; neither model2vec's encode nor this embedder ever
	// decodes, so its value cannot affect a token id or a vector. It is
	// deliberately not validated (see tokenizerSections).
	_ = sections["decoder"]

	// model
	if isNull(sections["model"]) {
		return nil, fail("model", "missing")
	}
	var model modelSpec
	if err := decodeStrict(sections["model"], &model); err != nil {
		return nil, fail("model", "%v", err)
	}
	if model.Type != "WordPiece" {
		return nil, fail("model.type", "%q is not WordPiece", model.Type)
	}
	if model.ContinuingSubwordPrefix == "" {
		return nil, fail("model.continuing_subword_prefix", "empty; WordPiece continuation pieces need a prefix")
	}
	if model.MaxInputCharsPerWord <= 0 {
		return nil, fail("model.max_input_chars_per_word", "%d is not a positive word length", model.MaxInputCharsPerWord)
	}
	unkID, ok := model.Vocab[model.UnkToken]
	if !ok {
		return nil, fail("model.unk_token", "%q is not in the vocabulary", model.UnkToken)
	}
	tokens := make([]string, len(model.Vocab))
	seen := make([]bool, len(model.Vocab))
	for tok, id := range model.Vocab {
		if id < 0 || id >= len(tokens) || seen[id] {
			return nil, fail("model.vocab", "ids are not the contiguous range 0..%d (token %q has id %d)", len(tokens)-1, tok, id)
		}
		seen[id] = true
		tokens[id] = tok
	}

	t := &Tokenizer{
		vocab:         model.Vocab,
		tokens:        tokens,
		unkID:         unkID,
		prefix:        model.ContinuingSubwordPrefix,
		maxInputChars: model.MaxInputCharsPerWord,
		cleanText:     norm.CleanText,
		chineseChars:  norm.HandleChineseChars,
		lowercase:     norm.Lowercase,
	}
	// tokenizers: strip_accents unset ⇒ follows lowercase, as in the original BERT.
	if norm.StripAccents != nil {
		t.stripAccents = *norm.StripAccents
	} else {
		t.stripAccents = norm.Lowercase
	}

	// truncation: null means none; otherwise exactly the right/LongestFirst/
	// stride-0 truncation Encode implements.
	if !isNull(sections["truncation"]) {
		var tr truncationSpec
		if err := decodeStrict(sections["truncation"], &tr); err != nil {
			return nil, fail("truncation", "%v", err)
		}
		if tr.Direction != "Right" {
			return nil, fail("truncation.direction", "%q is not the Right truncation this embedder implements", tr.Direction)
		}
		if tr.Strategy != "LongestFirst" {
			return nil, fail("truncation.strategy", "%q is not the LongestFirst truncation this embedder implements", tr.Strategy)
		}
		if tr.Stride != 0 {
			return nil, fail("truncation.stride", "%d; only stride 0 is implemented", tr.Stride)
		}
		if tr.MaxLength <= 0 {
			return nil, fail("truncation.max_length", "%d is not a positive length", tr.MaxLength)
		}
		t.maxLength = tr.MaxLength
	}

	// padding (FAIL-CLOSED): must be either null (no padding section) or
	// exactly the BatchLongest/Right padding EncodeBatch implements. The
	// production embedder does not use padding (EmbedEach is
	// batch-invariant), but the loader still validates the block so a
	// tokenizer.json whose padding strategy is not BatchLongest/Right, or
	// whose pad_id is out of range, is refused at load time. Without this
	// guard the chunk composition could silently change a vector (the
	// SW-259 requirement "validate the tokenizer's padding section
	// fail-closed").
	if !isNull(sections["padding"]) {
		var pad paddingSpec
		if err := decodeStrict(sections["padding"], &pad); err != nil {
			return nil, fail("padding", "%v", err)
		}
		var strategy string
		if err := json.Unmarshal(pad.Strategy, &strategy); err != nil || strategy != "BatchLongest" {
			return nil, fail("padding.strategy", "%s is not the BatchLongest padding this embedder validates (the production path is batch-invariant and never pools pad ids; the tokenizer section is still rejected so chunk composition cannot silently change a vector)", compact(pad.Strategy))
		}
		if pad.Direction != "Right" {
			return nil, fail("padding.direction", "%q is not the Right padding this embedder validates", pad.Direction)
		}
		if pad.PadToMultipleOf != nil {
			return nil, fail("padding.pad_to_multiple_of", "%d; only null (no rounding up) is implemented", *pad.PadToMultipleOf)
		}
		if pad.PadTypeID != 0 {
			return nil, fail("padding.pad_type_id", "%d; only 0 is implemented (type ids are never used)", pad.PadTypeID)
		}
		if pad.PadID < 0 || pad.PadID >= len(tokens) {
			return nil, fail("padding.pad_id", "%d is outside the vocabulary 0..%d", pad.PadID, len(tokens)-1)
		}
		if tokens[pad.PadID] != pad.PadToken {
			return nil, fail("padding.pad_token", "%q is not the vocabulary token of pad_id %d (%q)", pad.PadToken, pad.PadID, tokens[pad.PadID])
		}
		t.padEnabled = true
		t.padID = pad.PadID
	}

	// added_tokens: each must be a special token already in the vocabulary
	// with the same id (out-of-vocabulary added tokens would need an id
	// space the embedding table does not have).
	if !isNull(sections["added_tokens"]) {
		var added []addedToken
		if err := decodeStrict(sections["added_tokens"], &added); err != nil {
			return nil, fail("added_tokens", "%v", err)
		}
		for i, a := range added {
			field := fmt.Sprintf("added_tokens[%d]", i)
			if a.Content == "" {
				return nil, fail(field, "empty content")
			}
			if id, ok := model.Vocab[a.Content]; !ok || id != a.ID {
				return nil, fail(field, "%q (id %d) is not in the vocabulary with that id; out-of-vocabulary added tokens are not implemented", a.Content, a.ID)
			}
			if !a.Special {
				return nil, fail(field, "%q is not special; only special added tokens are implemented", a.Content)
			}
			m := addedMatch{id: a.ID, singleWord: a.SingleWord, lstrip: a.LStrip, rstrip: a.RStrip}
			if a.Normalized {
				m.content = t.normalize(a.Content)
				t.normAdded = append(t.normAdded, m)
			} else {
				m.content = []rune(a.Content)
				t.rawAdded = append(t.rawAdded, m)
			}
		}
	}
	return t, nil
}

// decodeStrict unmarshals raw into v, refusing unknown object keys so that
// a setting this embedder does not model cannot pass silently.
func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// isNull reports whether a section is absent or JSON null.
func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return b.String()
}

// VocabSize is the number of ids in the WordPiece vocabulary.
func (t *Tokenizer) VocabSize() int { return len(t.tokens) }

// Token returns the vocabulary string for id, or "" when out of range.
func (t *Tokenizer) Token(id int) string {
	if id < 0 || id >= len(t.tokens) {
		return ""
	}
	return t.tokens[id]
}

// UnkID is the id of the unknown token.
func (t *Tokenizer) UnkID() int { return t.unkID }

// MaxLength is the truncation length (0 = none).
func (t *Tokenizer) MaxLength() int { return t.maxLength }

// Padding reports whether tokenizer.json declares a padding section and, if
// so, the pad id EncodeBatch pads with (read from the file, never assumed).
// The production embedder never pools pad ids (EmbedEach is
// batch-invariant); this method is preserved so the loader's fail-closed
// validation is observable.
func (t *Tokenizer) Padding() (enabled bool, padID int) { return t.padEnabled, t.padID }

// Encode is tokenizers' Tokenizer.encode(text, add_special_tokens=False).ids:
// added-token extraction → normalisation → pre-tokenisation → WordPiece →
// right truncation. Unknown words yield the unk id (they are NOT removed
// here; Model2Vec drops them one stage later, see Model.InferenceIDs). No
// padding is applied: a single encoding is its own longest.
func (t *Tokenizer) Encode(text string) []int {
	ids := []int{}
	for _, seg := range splitAdded([]rune(text), t.rawAdded) {
		if seg.id >= 0 {
			ids = append(ids, seg.id)
			continue
		}
		for _, nseg := range splitAdded(t.normalizeRunes(seg.runes), t.normAdded) {
			if nseg.id >= 0 {
				ids = append(ids, nseg.id)
				continue
			}
			for _, word := range preTokenize(nseg.runes) {
				ids = t.wordPiece(ids, word)
			}
		}
	}
	if t.maxLength > 0 && len(ids) > t.maxLength {
		ids = ids[:t.maxLength]
	}
	return ids
}

// Tokens renders ids back to their vocabulary strings (diagnostics only).
func (t *Tokenizer) Tokens(ids []int) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = t.Token(id)
	}
	return out
}

// segment is a piece of input: either literal text to tokenize (id < 0) or
// an extracted added token.
type segment struct {
	runes []rune
	id    int
}

// splitAdded extracts added tokens from text the way tokenizers'
// AddedVocabulary does: leftmost, non-overlapping matches; a single_word
// token must not touch an alphanumeric character on either side; lstrip/
// rstrip absorb adjacent whitespace into the match.
func splitAdded(text []rune, toks []addedMatch) []segment {
	if len(toks) == 0 {
		return []segment{{runes: text, id: -1}}
	}
	var out []segment
	prev := 0
	for i := 0; i < len(text); {
		matched := false
		for _, tk := range toks {
			end := i + len(tk.content)
			if end > len(text) || !runesEqual(text[i:end], tk.content) {
				continue
			}
			if tk.singleWord {
				if (i > 0 && isWordRune(text[i-1])) || (end < len(text) && isWordRune(text[end])) {
					continue
				}
			}
			start := i
			if tk.lstrip {
				for start > prev && isWhitespace(text[start-1]) {
					start--
				}
			}
			if tk.rstrip {
				for end < len(text) && isWhitespace(text[end]) {
					end++
				}
			}
			if start > prev {
				out = append(out, segment{runes: text[prev:start], id: -1})
			}
			out = append(out, segment{id: tk.id})
			prev, i = end, end
			matched = true
			break
		}
		if !matched {
			i++
		}
	}
	if prev < len(text) {
		out = append(out, segment{runes: text[prev:], id: -1})
	}
	return out
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// normalize applies the BertNormalizer to a string.
func (t *Tokenizer) normalize(s string) []rune { return t.normalizeRunes([]rune(s)) }

// normalizeRunes is tokenizers' BertNormalizer, step for step and in its
// order: clean_text (drop NUL, U+FFFD and control characters; fold every
// whitespace to a space), handle_chinese_chars (pad each CJK ideograph
// with spaces), strip_accents (NFD, then drop nonspacing marks),
// lowercase.
func (t *Tokenizer) normalizeRunes(in []rune) []rune {
	out := make([]rune, 0, len(in)+8)
	for _, r := range in {
		if t.cleanText {
			if r == 0 || r == 0xFFFD || isControl(r) {
				continue
			}
			if isWhitespace(r) {
				r = ' '
			}
		}
		if t.chineseChars && isChineseChar(r) {
			out = append(out, ' ', r, ' ')
			continue
		}
		out = append(out, r)
	}
	if t.stripAccents {
		stripped := make([]rune, 0, len(out))
		var scratch [8]rune
		for _, r := range out {
			for _, d := range appendNFD(scratch[:0], r) {
				if !unicode.Is(unicode.Mn, d) {
					stripped = append(stripped, d)
				}
			}
		}
		out = stripped
	}
	if t.lowercase {
		for i, r := range out {
			out[i] = unicode.ToLower(r)
		}
	}
	return out
}

// isControl is tokenizers' is_control: tab, newline and carriage return
// count as whitespace; everything in the "Other" categories (Cc, Cf, Co,
// Cs, and unassigned Cn) is control.
func isControl(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return false
	}
	if unicode.Is(unicode.C, r) {
		return true
	}
	return !unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S, unicode.Z)
}

// isWhitespace is tokenizers' is_whitespace: tab/newline/CR plus the
// Unicode White_Space property.
func isWhitespace(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return true
	}
	return unicode.IsSpace(r)
}

// isChineseChar is tokenizers' is_chinese_char: the CJK Unified Ideograph
// blocks (not kana, not Hangul), exactly the ranges the original BERT used.
func isChineseChar(r rune) bool {
	switch {
	case r >= 0x4E00 && r <= 0x9FFF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x20000 && r <= 0x2A6DF,
		r >= 0x2A700 && r <= 0x2B73F,
		r >= 0x2B740 && r <= 0x2B81F,
		r >= 0x2B920 && r <= 0x2CEAF,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0x2F800 && r <= 0x2FA1F:
		return true
	}
	return false
}

// isBertPunc is tokenizers' is_bert_punc: ASCII punctuation (which includes
// the ASCII symbols $+<=>^`|~) or any Unicode punctuation category.
func isBertPunc(r rune) bool {
	if r < 0x80 {
		return (r >= '!' && r <= '/') || (r >= ':' && r <= '@') || (r >= '[' && r <= '`') || (r >= '{' && r <= '~')
	}
	return unicode.IsPunct(r)
}

// preTokenize is the BertPreTokenizer: split on whitespace (removed) and on
// punctuation (each punctuation character becomes its own word).
func preTokenize(text []rune) [][]rune {
	var words [][]rune
	start := -1
	flush := func(end int) {
		if start >= 0 {
			words = append(words, text[start:end])
			start = -1
		}
	}
	for i, r := range text {
		switch {
		case isWhitespace(r):
			flush(i)
		case isBertPunc(r):
			flush(i)
			words = append(words, text[i:i+1])
		default:
			if start < 0 {
				start = i
			}
		}
	}
	flush(len(text))
	return words
}

// wordPiece appends the WordPiece ids of one word: greedy longest-match-first
// over characters, continuation pieces carrying the prefix; a word longer
// than max_input_chars_per_word, or one with any unmatched remainder, is the
// single unk id.
func (t *Tokenizer) wordPiece(ids []int, word []rune) []int {
	if len(word) > t.maxInputChars {
		return append(ids, t.unkID)
	}
	var buf strings.Builder
	var pieces []int
	start := 0
	for start < len(word) {
		cur := -1
		end := len(word)
		for start < end {
			buf.Reset()
			if start > 0 {
				buf.WriteString(t.prefix)
			}
			for _, r := range word[start:end] {
				buf.WriteRune(r)
			}
			if id, ok := t.vocab[buf.String()]; ok {
				cur = id
				break
			}
			end--
		}
		if cur < 0 {
			return append(ids, t.unkID)
		}
		pieces = append(pieces, cur)
		start = end
	}
	return append(ids, pieces...)
}
