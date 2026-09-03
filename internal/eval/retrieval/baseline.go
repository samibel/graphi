package retrieval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// GrepReadVersion changes whenever any comparator behavior or response
	// serialization changes.
	GrepReadVersion = "1"

	// GrepReadSearchLimit is the maximum number of distinct matching source
	// lines serialized by the initial grep response.
	GrepReadSearchLimit = 20

	// GrepReadMaxReads bounds the follow-up read operations. It is deliberately
	// not a recall-dependent stop condition.
	GrepReadMaxReads = 8
)

// GrepReadOperation records one follow-up read request. ResponseSequence binds
// it to the exact payload captured at the operation boundary.
type GrepReadOperation struct {
	Path             string `json:"path"`
	StartLine        int    `json:"start_line"`
	EndLine          int    `json:"end_line"`
	ResponseSequence int    `json:"response_sequence"`
}

// GrepReadTranscript is the complete deterministic comparator run. It has no
// judgement, qrel, answer-span or recall-target field.
type GrepReadTranscript struct {
	Version       string              `json:"version"`
	Query         string              `json:"query"`
	Patterns      []string            `json:"patterns"`
	IncludedFiles []string            `json:"included_files"`
	Reads         []GrepReadOperation `json:"reads"`
	StopReason    string              `json:"stop_reason"`
	Ledger        PayloadLedger       `json:"ledger"`
}

// DigestSHA256 identifies the complete transcript, including operation
// metadata, boundaries, exact response bytes and per-response digests.
func (t GrepReadTranscript) DigestSHA256() string {
	raw, err := json.Marshal(t)
	if err != nil {
		panic(fmt.Sprintf("retrieval: marshal GrepRead transcript: %v", err))
	}
	return SHA256Hex(raw)
}

// Validate checks that a transcript still contains every response captured by
// the fixed operation sequence.
func (t GrepReadTranscript) Validate() error {
	if t.Version != GrepReadVersion {
		return fmt.Errorf("retrieval GrepRead: version=%q, want %q", t.Version, GrepReadVersion)
	}
	wantPatterns := grepReadPatterns(t.Query)
	if !equalStrings(t.Patterns, wantPatterns) {
		return fmt.Errorf("retrieval GrepRead: patterns do not match the pinned query transformation")
	}
	if !sort.StringsAreSorted(t.IncludedFiles) {
		return fmt.Errorf("retrieval GrepRead: included files are not in canonical order")
	}
	for i, name := range t.IncludedFiles {
		if !grepReadIncludes(name) {
			return fmt.Errorf("retrieval GrepRead: included file %q violates the pinned inclusion rule", name)
		}
		if i > 0 && name == t.IncludedFiles[i-1] {
			return fmt.Errorf("retrieval GrepRead: included file %q is duplicated", name)
		}
	}
	if t.StopReason != SavingsStopExhausted && t.StopReason != SavingsStopMaxReads {
		return fmt.Errorf("retrieval GrepRead: stop reason %q is not exhausted or max_reads", t.StopReason)
	}
	if len(t.Reads) > GrepReadMaxReads {
		return fmt.Errorf("retrieval GrepRead: %d reads exceed max %d", len(t.Reads), GrepReadMaxReads)
	}
	if err := t.Ledger.Validate(); err != nil {
		return err
	}
	if len(t.Ledger.Responses) != len(t.Reads)+1 {
		return fmt.Errorf("retrieval GrepRead: ledger has %d responses for grep plus %d reads", len(t.Ledger.Responses), len(t.Reads))
	}
	for i, response := range t.Ledger.Responses {
		if response.Boundary != PayloadBoundaryGrepRead {
			return fmt.Errorf("retrieval GrepRead: response %d boundary=%q, want %q", response.Sequence, response.Boundary, PayloadBoundaryGrepRead)
		}
		wantOperation := PayloadOperationRead
		if i == 0 {
			wantOperation = PayloadOperationGrep
		}
		if response.Operation != wantOperation {
			return fmt.Errorf("retrieval GrepRead: response %d operation=%q, want %q", response.Sequence, response.Operation, wantOperation)
		}
	}
	for i, read := range t.Reads {
		if read.Path == "" || read.StartLine < 1 || read.EndLine < read.StartLine-1 {
			return fmt.Errorf("retrieval GrepRead: read %d has invalid coordinates %+v", i+1, read)
		}
		if read.ResponseSequence != i+2 {
			return fmt.Errorf("retrieval GrepRead: read %d response sequence=%d, want %d", i+1, read.ResponseSequence, i+2)
		}
	}
	return nil
}

// GrepRead executes the fixed baseline over repository. Its complete input
// surface is an fs.FS and query text: scoring data cannot be passed to it. All
// operation failures are serialized as response payloads and therefore remain
// part of the measured cost.
func GrepRead(repository fs.FS, query string) GrepReadTranscript {
	transcript := GrepReadTranscript{
		Version:  GrepReadVersion,
		Query:    query,
		Patterns: grepReadPatterns(query),
	}

	files, matches, searchResponse := grepReadSearch(repository, transcript.Patterns)
	transcript.IncludedFiles = files
	transcript.Ledger.capture(PayloadBoundaryGrepRead, PayloadOperationGrep, searchResponse)

	covered := make([]grepReadWindow, 0, GrepReadMaxReads)
	for _, match := range matches {
		if grepReadCovered(match.Path, match.Line, covered) {
			continue
		}
		if len(transcript.Reads) == GrepReadMaxReads {
			transcript.StopReason = SavingsStopMaxReads
			break
		}

		window := grepReadWindow{Path: match.Path, StartLine: match.Line, EndLine: match.Line + GrepReadWindowLines - 1}
		covered = append(covered, window)
		response, endLine := grepReadRead(repository, window)
		sequence := transcript.Ledger.capture(PayloadBoundaryGrepRead, PayloadOperationRead, response)
		transcript.Reads = append(transcript.Reads, GrepReadOperation{
			Path:             match.Path,
			StartLine:        match.Line,
			EndLine:          endLine,
			ResponseSequence: sequence,
		})
	}
	if transcript.StopReason == "" {
		transcript.StopReason = SavingsStopExhausted
	}
	return transcript
}

type grepReadFile struct {
	Path      string
	Bytes     []byte
	ErrorKind string
}

type grepReadMatch struct {
	Path   string
	Line   int
	Column int
	Text   []byte
}

type grepReadWindow struct {
	Path      string
	StartLine int
	EndLine   int
}

func grepReadPatterns(query string) []string {
	var all []string
	seen := map[string]bool{}
	start := -1
	flush := func(end int) {
		if start < 0 {
			return
		}
		term := strings.ToLower(query[start:end])
		if !seen[term] {
			seen[term] = true
			all = append(all, term)
		}
		start = -1
	}
	for offset, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if start < 0 {
				start = offset
			}
			continue
		}
		flush(offset)
	}
	flush(len(query))

	var substantial []string
	for _, term := range all {
		if utf8.RuneCountInString(term) >= 3 {
			substantial = append(substantial, term)
		}
	}
	if len(substantial) > 0 {
		return substantial
	}
	return all
}

func grepReadSearch(repository fs.FS, patterns []string) ([]string, []grepReadMatch, []byte) {
	if len(patterns) == 0 {
		return []string{}, nil, []byte("grep:error:query:no_searchable_pattern\n")
	}
	if repository == nil {
		return []string{}, nil, []byte("grep:error:.:walk_failed\n")
	}

	var files []grepReadFile
	walkErr := fs.WalkDir(repository, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			files = append(files, grepReadFile{Path: cleanGrepReadPath(name), ErrorKind: "walk_failed"})
			return nil
		}
		if entry.IsDir() {
			if name != "." && grepReadExcludedDirectory(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !grepReadIncludes(name) || !entry.Type().IsRegular() {
			return nil
		}
		file := grepReadFile{Path: cleanGrepReadPath(name)}
		file.Bytes, err = fs.ReadFile(repository, name)
		if err != nil {
			file.Bytes = nil
			file.ErrorKind = "read_failed"
		} else if !utf8.Valid(file.Bytes) {
			file.Bytes = nil
			file.ErrorKind = "invalid_utf8"
		}
		files = append(files, file)
		return nil
	})
	if walkErr != nil && len(files) == 0 {
		files = append(files, grepReadFile{Path: ".", ErrorKind: "walk_failed"})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	included := make([]string, 0, len(files))
	var matches []grepReadMatch
	var errors []grepReadFile
	regexps := make([]*regexp.Regexp, len(patterns))
	for i, pattern := range patterns {
		regexps[i] = regexp.MustCompile("(?i:" + regexp.QuoteMeta(pattern) + ")")
	}
	for _, file := range files {
		if grepReadIncludes(file.Path) {
			included = append(included, file.Path)
		}
		if file.ErrorKind != "" {
			errors = append(errors, file)
			continue
		}
		for lineIndex, line := range splitGrepReadLines(file.Bytes) {
			column := 0
			for _, re := range regexps {
				location := re.FindIndex(line.Text)
				if location != nil && (column == 0 || location[0]+1 < column) {
					column = location[0] + 1
				}
			}
			if column > 0 {
				matches = append(matches, grepReadMatch{Path: file.Path, Line: lineIndex + 1, Column: column, Text: append([]byte(nil), line.Text...)})
			}
		}
	}
	if len(matches) > GrepReadSearchLimit {
		matches = matches[:GrepReadSearchLimit]
	}

	var response bytes.Buffer
	for _, file := range errors {
		fmt.Fprintf(&response, "grep:error:%s:%s\n", file.Path, file.ErrorKind)
	}
	for _, match := range matches {
		fmt.Fprintf(&response, "%s:%d:%d:", match.Path, match.Line, match.Column)
		response.Write(match.Text)
		response.WriteByte('\n')
	}
	return included, matches, response.Bytes()
}

type grepReadLine struct {
	Start int
	End   int
	Text  []byte
}

func splitGrepReadLines(raw []byte) []grepReadLine {
	var lines []grepReadLine
	for start := 0; start < len(raw); {
		newline := bytes.IndexByte(raw[start:], '\n')
		end := len(raw)
		textEnd := end
		if newline >= 0 {
			end = start + newline + 1
			textEnd = end - 1
		}
		if textEnd > start && raw[textEnd-1] == '\r' {
			textEnd--
		}
		lines = append(lines, grepReadLine{Start: start, End: end, Text: raw[start:textEnd]})
		start = end
	}
	return lines
}

func grepReadRead(repository fs.FS, window grepReadWindow) ([]byte, int) {
	raw, err := fs.ReadFile(repository, window.Path)
	if err != nil {
		return []byte(fmt.Sprintf("read:error:%s:read_failed\n", window.Path)), window.StartLine - 1
	}
	if !utf8.Valid(raw) {
		return []byte(fmt.Sprintf("read:error:%s:invalid_utf8\n", window.Path)), window.StartLine - 1
	}
	lines := splitGrepReadLines(raw)
	if window.StartLine > len(lines) {
		return make([]byte, 0), window.StartLine - 1
	}
	endLine := window.EndLine
	if endLine > len(lines) {
		endLine = len(lines)
	}
	startOffset := lines[window.StartLine-1].Start
	endOffset := lines[endLine-1].End
	return append([]byte(nil), raw[startOffset:endOffset]...), endLine
}

func grepReadCovered(name string, line int, windows []grepReadWindow) bool {
	for _, window := range windows {
		if name == window.Path && line >= window.StartLine && line <= window.EndLine {
			return true
		}
	}
	return false
}

func grepReadIncludes(name string) bool {
	clean := cleanGrepReadPath(name)
	if clean == "." || path.Ext(clean) != ".go" {
		return false
	}
	for _, part := range strings.Split(clean, "/") {
		if grepReadExcludedDirectory(part) {
			return false
		}
	}
	return true
}

func grepReadExcludedDirectory(name string) bool {
	return name == "vendor" || strings.HasPrefix(name, ".")
}

func cleanGrepReadPath(name string) string {
	clean := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if clean == "" {
		return "."
	}
	return clean
}

func equalStrings(a, b []string) bool {
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
