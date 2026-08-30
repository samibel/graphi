package model2vec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// The HuggingFace tokenizers JSON sections this spike reads. Only the component
// types the pinned tokenizer.json declares are modelled; LoadTokenizer refuses
// anything else rather than approximating it.
type tokenizerFile struct {
	Version       string          `json:"version"`
	Truncation    *truncationSpec `json:"truncation"`
	AddedTokens   []addedToken    `json:"added_tokens"`
	Normalizer    json.RawMessage `json:"normalizer"`
	PreTokenizer  json.RawMessage `json:"pre_tokenizer"`
	PostProcessor json.RawMessage `json:"post_processor"`
	Model         modelSpec       `json:"model"`
}

type truncationSpec struct {
	Direction string `json:"direction"`
	MaxLength int    `json:"max_length"`
	Strategy  string `json:"strategy"`
	Stride    int    `json:"stride"`
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

// Tokenizer is the BertNormalizer → BertPreTokenizer → WordPiece pipeline of the
// pinned tokenizer.json, with its added tokens and right truncation.
type Tokenizer struct {
	vocab         map[string]int
	tokens        []string // id → token string
	unkID         int
	prefix        string
	maxInputChars int
	maxLength     int // truncation length; 0 = none

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

// LoadTokenizer parses a tokenizer.json and validates that it uses exactly the
// components this spike implements.
func LoadTokenizer(path string) (*Tokenizer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f tokenizerFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("tokenizer %s: %w", path, err)
	}

	var norm bertNormalizerSpec
	if err := json.Unmarshal(f.Normalizer, &norm); err != nil || norm.Type != "BertNormalizer" {
		return nil, fmt.Errorf("tokenizer %s: normalizer %s is not the BertNormalizer this spike implements", path, compact(f.Normalizer))
	}
	var pre typedSpec
	if err := json.Unmarshal(f.PreTokenizer, &pre); err != nil || pre.Type != "BertPreTokenizer" {
		return nil, fmt.Errorf("tokenizer %s: pre_tokenizer %s is not the BertPreTokenizer this spike implements", path, compact(f.PreTokenizer))
	}
	if post := compact(f.PostProcessor); post != "" && post != "null" {
		return nil, fmt.Errorf("tokenizer %s: post_processor %s is not supported; the pinned file has none", path, post)
	}
	if f.Model.Type != "WordPiece" {
		return nil, fmt.Errorf("tokenizer %s: model type %q is not WordPiece", path, f.Model.Type)
	}
	if f.Model.ContinuingSubwordPrefix == "" {
		return nil, fmt.Errorf("tokenizer %s: WordPiece model has no continuing_subword_prefix", path)
	}
	if f.Model.MaxInputCharsPerWord <= 0 {
		return nil, fmt.Errorf("tokenizer %s: WordPiece model has no max_input_chars_per_word", path)
	}
	unkID, ok := f.Model.Vocab[f.Model.UnkToken]
	if !ok {
		return nil, fmt.Errorf("tokenizer %s: unk_token %q is not in the vocabulary", path, f.Model.UnkToken)
	}
	tokens := make([]string, len(f.Model.Vocab))
	seen := make([]bool, len(f.Model.Vocab))
	for tok, id := range f.Model.Vocab {
		if id < 0 || id >= len(tokens) || seen[id] {
			return nil, fmt.Errorf("tokenizer %s: vocabulary ids are not the contiguous range 0..%d (token %q has id %d)", path, len(tokens)-1, tok, id)
		}
		seen[id] = true
		tokens[id] = tok
	}

	t := &Tokenizer{
		vocab:         f.Model.Vocab,
		tokens:        tokens,
		unkID:         unkID,
		prefix:        f.Model.ContinuingSubwordPrefix,
		maxInputChars: f.Model.MaxInputCharsPerWord,
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
	if f.Truncation != nil {
		tr := f.Truncation
		if tr.Strategy != "LongestFirst" || tr.Direction != "Right" || tr.Stride != 0 || tr.MaxLength <= 0 {
			return nil, fmt.Errorf("tokenizer %s: truncation %+v is not the right/LongestFirst/stride-0 truncation this spike implements", path, *tr)
		}
		t.maxLength = tr.MaxLength
	}
	for _, a := range f.AddedTokens {
		if id, ok := f.Model.Vocab[a.Content]; !ok || id != a.ID {
			return nil, fmt.Errorf("tokenizer %s: added token %q (id %d) is not in the vocabulary with that id; out-of-vocabulary added tokens are not implemented", path, a.Content, a.ID)
		}
		m := addedMatch{id: a.ID, singleWord: a.SingleWord, lstrip: a.LStrip, rstrip: a.RStrip}
		if a.Normalized {
			// tokenizers normalises the CONTENT of a normalized added token with
			// the same normalizer before matching it against normalized text.
			m.content = t.normalize(a.Content)
			t.normAdded = append(t.normAdded, m)
		} else {
			m.content = []rune(a.Content)
			t.rawAdded = append(t.rawAdded, m)
		}
	}
	return t, nil
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

// Encode is tokenizers' Tokenizer.encode(text, add_special_tokens=False).ids:
// added-token extraction → normalisation → pre-tokenisation → WordPiece →
// right truncation. Unknown words yield the unk id (they are NOT removed
// here; Model2Vec drops them one stage later, see Model.InferenceIDs).
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

// segment is a piece of input: either literal text to tokenize (id < 0) or an
// extracted added token.
type segment struct {
	runes []rune
	id    int
}

// splitAdded extracts added tokens from text the way tokenizers'
// AddedVocabulary does: leftmost, non-overlapping matches; a single_word token
// must not touch an alphanumeric character on either side; lstrip/rstrip
// absorb adjacent whitespace into the match.
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
// whitespace to a space), handle_chinese_chars (pad each CJK ideograph with
// spaces), strip_accents (NFD, then drop nonspacing marks), lowercase.
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

// isControl is tokenizers' is_control: tab, newline and carriage return count
// as whitespace; everything in the "Other" categories (Cc, Cf, Co, Cs, and
// unassigned Cn) is control.
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

// isWhitespace is tokenizers' is_whitespace: tab/newline/CR plus the Unicode
// White_Space property (Rust char::is_whitespace, Go unicode.IsSpace).
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

// isBertPunc is tokenizers' is_bert_punc: ASCII punctuation (which includes the
// ASCII symbols $+<=>^`|~) or any Unicode punctuation category.
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
// over characters, continuation pieces carrying the prefix; a word longer than
// max_input_chars_per_word, or one with any unmatched remainder, is the single
// unk id.
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
