package tokenizer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	envArtifactDir       = "GRAPHI_EVAL_TOKENIZER_DIR"
	maxVocabularyBytes   = 2 << 20
	wantVocabularyTokens = 100256
)

// PinMismatchError is returned before vocabulary parsing when a required file
// is absent or its bytes do not match the pin. Expected and Actual are always
// populated so truncation, corruption, and absence all have one loud shape.
type PinMismatchError struct {
	File     string
	Path     string
	Expected string
	Actual   string
}

func (e *PinMismatchError) Error() string {
	return fmt.Sprintf("eval tokenizer: %s: SHA-256 mismatch: expected %s, actual %s (path=%s)", e.File, e.Expected, e.Actual, e.Path)
}

// Tokenizer is a pure-Go implementation of cl100k_base ordinary-text
// tokenization: the frozen cl100k pre-tokenizer followed by byte-pair encoding
// against the verified mergeable-rank vocabulary.
type Tokenizer struct {
	ranks map[string]int
}

// LoadPinned resolves the immutable local artifact directory and loads it. It
// never downloads and never falls back to the whitespace counter.
func LoadPinned() (*Tokenizer, error) {
	dir := ArtifactDir()
	if dir == "" {
		return nil, fmt.Errorf("eval tokenizer: artifact directory is unavailable; set %s or run `go run ./cmd/retrieval-eval -setup-tokenizer`", envArtifactDir)
	}
	tok, err := Load(dir)
	if err != nil {
		return nil, fmt.Errorf("eval tokenizer: pinned %s artifact unavailable: %w; run `go run ./cmd/retrieval-eval -setup-tokenizer` or set %s", TokenizerID, err, envArtifactDir)
	}
	return tok, nil
}

// ArtifactDir returns the environment override or the revision-addressed
// default cache path. The vocabulary digest in the directory name prevents a
// pin rotation from silently reusing an older artifact.
func ArtifactDir() string {
	if dir := os.Getenv(envArtifactDir); dir != "" {
		return dir
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			cache = filepath.Join(home, ".cache")
		}
	}
	if cache == "" {
		return ""
	}
	return filepath.Join(cache, "graphi", "tokenizers", "cl100k_base@"+PinnedVocabularySHA256)
}

// Load verifies every pinned file before parsing the vocabulary. The loader
// accepts only regular, non-symlink files and a complete contiguous rank set.
func Load(dir string) (*Tokenizer, error) {
	if dir == "" {
		return nil, errors.New("eval tokenizer: artifact directory is empty")
	}
	if len(PinnedFileNames) != 1 || PinnedFileNames[0] != PinnedVocabularyFile {
		return nil, fmt.Errorf("eval tokenizer: pin table must name exactly %s", PinnedVocabularyFile)
	}
	want, ok := PinnedSHA256[PinnedVocabularyFile]
	if !ok {
		return nil, fmt.Errorf("eval tokenizer: no SHA-256 pin recorded for %s", PinnedVocabularyFile)
	}
	path := filepath.Join(dir, PinnedVocabularyFile)
	raw, actual, err := readPinnedFile(path, maxVocabularyBytes)
	if err != nil {
		return nil, &PinMismatchError{File: PinnedVocabularyFile, Path: path, Expected: want, Actual: "unavailable (" + err.Error() + ")"}
	}
	if actual != want {
		return nil, &PinMismatchError{File: PinnedVocabularyFile, Path: path, Expected: want, Actual: actual}
	}

	ranks, err := parseVocabulary(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("eval tokenizer: parse verified %s: %w", PinnedVocabularyFile, err)
	}
	return &Tokenizer{ranks: ranks}, nil
}

func readPinnedFile(path string, limit int64) ([]byte, string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, "", err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, "", errors.New("pinned file is a symlink; want a regular file")
	}
	if !st.Mode().IsRegular() {
		return nil, "", fmt.Errorf("pinned file is not regular: %s", st.Mode())
	}
	if st.Size() > limit {
		return nil, "", fmt.Errorf("pinned file size %d exceeds limit %d", st.Size(), limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(raw)) != st.Size() {
		return nil, "", fmt.Errorf("read %d bytes, stat reported %d", len(raw), st.Size())
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func parseVocabulary(r io.Reader) (map[string]int, error) {
	ranks := make(map[string]int, wantVocabularyTokens)
	seenRanks := make([]bool, wantVocabularyTokens)
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		fields := bytes.Fields(scanner.Bytes())
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: want base64-token and rank, got %d fields", line, len(fields))
		}
		token, err := base64.StdEncoding.Strict().DecodeString(string(fields[0]))
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid base64 token: %w", line, err)
		}
		if len(token) == 0 || base64.StdEncoding.EncodeToString(token) != string(fields[0]) {
			return nil, fmt.Errorf("line %d: token is empty or not canonical base64", line)
		}
		rank, err := strconv.Atoi(string(fields[1]))
		if err != nil || rank < 0 || rank >= wantVocabularyTokens {
			return nil, fmt.Errorf("line %d: rank %q is outside 0..%d", line, fields[1], wantVocabularyTokens-1)
		}
		key := string(token)
		if _, duplicate := ranks[key]; duplicate {
			return nil, fmt.Errorf("line %d: duplicate token %q", line, fields[0])
		}
		if seenRanks[rank] {
			return nil, fmt.Errorf("line %d: duplicate rank %d", line, rank)
		}
		ranks[key] = rank
		seenRanks[rank] = true
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(ranks) != wantVocabularyTokens {
		return nil, fmt.Errorf("vocabulary has %d tokens, want %d", len(ranks), wantVocabularyTokens)
	}
	for rank, seen := range seenRanks {
		if !seen {
			return nil, fmt.Errorf("vocabulary is missing rank %d", rank)
		}
	}
	for b := 0; b < 256; b++ {
		if _, ok := ranks[string([]byte{byte(b)})]; !ok {
			return nil, fmt.Errorf("vocabulary is missing byte token 0x%02x", b)
		}
	}
	return ranks, nil
}

// Encode returns cl100k_base token IDs for ordinary UTF-8 text. It rejects
// invalid UTF-8 rather than counting replacement characters that were not in
// the preserved payload.
func (t *Tokenizer) Encode(payload []byte) ([]int, error) {
	if t == nil || len(t.ranks) == 0 {
		return nil, errors.New("eval tokenizer: tokenizer is not loaded")
	}
	if !utf8.Valid(payload) {
		return nil, errors.New("eval tokenizer: payload is not valid UTF-8")
	}
	pieces := preTokenize(string(payload))
	ids := make([]int, 0, len(pieces))
	for _, piece := range pieces {
		ids = t.appendBPE(ids, []byte(piece))
	}
	return ids, nil
}

// Count recomputes the count from the supplied bytes on every call.
func (t *Tokenizer) Count(payload []byte) (int, error) {
	ids, err := t.Encode(payload)
	return len(ids), err
}

func (t *Tokenizer) appendBPE(ids []int, piece []byte) []int {
	if len(piece) == 0 {
		return ids
	}
	if rank, ok := t.ranks[string(piece)]; ok {
		return append(ids, rank)
	}
	boundaries := make([]int, len(piece)+1)
	for i := range boundaries {
		boundaries[i] = i
	}
	for len(boundaries) > 2 {
		bestRank, bestAt := int(^uint(0)>>1), -1
		for i := 0; i+2 < len(boundaries); i++ {
			if rank, ok := t.ranks[string(piece[boundaries[i]:boundaries[i+2]])]; ok && rank < bestRank {
				bestRank, bestAt = rank, i
			}
		}
		if bestAt < 0 {
			break
		}
		boundaries = append(boundaries[:bestAt+1], boundaries[bestAt+2:]...)
	}
	for i := 0; i+1 < len(boundaries); i++ {
		// The vocabulary is required to contain every byte, so every final
		// piece has a rank. Treat a missing rank as an impossible loader bug.
		rank, ok := t.ranks[string(piece[boundaries[i]:boundaries[i+1]])]
		if !ok {
			panic("eval tokenizer: BPE produced a piece absent from the verified vocabulary")
		}
		ids = append(ids, rank)
	}
	return ids
}

// preTokenize implements cl100k_base's frozen pattern without a regex engine:
//
//	(?i:'s|'t|'re|'ve|'m|'ll|'d)|[^\r\n\p{L}\p{N}]?\p{L}+|\p{N}{1,3}|
//	?[^\s\p{L}\p{N}]+[\r\n]*|\s*[\r\n]+|\s+(?!\S)|\s+
//
// Working over rune boundaries preserves the pattern's Unicode Letter,
// Number, and White_Space semantics; BPE then operates on the matched UTF-8
// bytes exactly as tiktoken does.
func preTokenize(text string) []string {
	runes := []rune(text)
	pieces := make([]string, 0, len(runes)/4+1)
	appendRunes := func(start, end int) {
		if end > start {
			pieces = append(pieces, string(runes[start:end]))
		}
	}
	for i := 0; i < len(runes); {
		if end := contractionEnd(runes, i); end > i {
			appendRunes(i, end)
			i = end
			continue
		}

		// [^\r\n\p{L}\p{N}]?\p{L}+
		letterStart := i
		if !isLetter(runes[i]) && !isNumber(runes[i]) && runes[i] != '\r' && runes[i] != '\n' && i+1 < len(runes) && isLetter(runes[i+1]) {
			letterStart++
		}
		if isLetter(runes[letterStart]) {
			end := letterStart + 1
			for end < len(runes) && isLetter(runes[end]) {
				end++
			}
			appendRunes(i, end)
			i = end
			continue
		}

		// \p{N}{1,3}
		if isNumber(runes[i]) {
			end := i + 1
			for end < len(runes) && end-i < 3 && isNumber(runes[end]) {
				end++
			}
			appendRunes(i, end)
			i = end
			continue
		}

		//  ?[^\s\p{L}\p{N}]+[\r\n]*
		punctStart := i
		if runes[i] == ' ' && i+1 < len(runes) && isNonSpaceSymbol(runes[i+1]) {
			punctStart++
		}
		if punctStart < len(runes) && isNonSpaceSymbol(runes[punctStart]) {
			end := punctStart + 1
			for end < len(runes) && isNonSpaceSymbol(runes[end]) {
				end++
			}
			for end < len(runes) && (runes[end] == '\r' || runes[end] == '\n') {
				end++
			}
			appendRunes(i, end)
			i = end
			continue
		}

		// \s*[\r\n]+ | \s+(?!\S) | \s+
		if unicode.IsSpace(runes[i]) {
			end := i + 1
			lastLineBreak := -1
			if runes[i] == '\r' || runes[i] == '\n' {
				lastLineBreak = end
			}
			for end < len(runes) && unicode.IsSpace(runes[end]) {
				end++
				if runes[end-1] == '\r' || runes[end-1] == '\n' {
					lastLineBreak = end
				}
			}
			if lastLineBreak > 0 {
				end = lastLineBreak
			} else if end < len(runes) && end-i > 1 {
				// The trailing-whitespace alternative, \s+(?!\S), backtracks
				// one rune when a non-space follows. That leaves the final
				// whitespace rune available to prefix the following word or
				// punctuation through an earlier alternative.
				end--
			}
			appendRunes(i, end)
			i = end
			continue
		}

		// The alternatives are exhaustive. Keep the failure local if a future
		// Unicode classification change disproves that assumption.
		appendRunes(i, i+1)
		i++
	}
	return pieces
}

func contractionEnd(text []rune, start int) int {
	if text[start] != '\'' {
		return start
	}
	for _, suffix := range []string{"s", "t", "re", "ve", "m", "ll", "d"} {
		want := []rune(suffix)
		if start+1+len(want) > len(text) {
			continue
		}
		matched := true
		for i, r := range want {
			if unicode.ToLower(text[start+1+i]) != r {
				matched = false
				break
			}
		}
		if matched {
			return start + 1 + len(want)
		}
	}
	return start
}

func isLetter(r rune) bool { return unicode.IsLetter(r) }
func isNumber(r rune) bool { return unicode.IsNumber(r) }
func isNonSpaceSymbol(r rune) bool {
	return !unicode.IsSpace(r) && !isLetter(r) && !isNumber(r)
}

// Identity returns the fields every report must stamp beside real-token counts.
func Identity() (tokenizerID, vocabularySHA256 string) {
	return TokenizerID, PinnedVocabularySHA256
}

// DescribePin is a compact operator-facing identity used by setup output.
func DescribePin() string {
	return strings.Join([]string{TokenizerID, PinnedVocabularyFile, PinnedVocabularySHA256}, " ")
}
