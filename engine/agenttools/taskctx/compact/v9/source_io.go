package v9

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// GrepReadOperation records one bounded source read and binds it to the exact
// payload captured in the query-only discovery transcript.
type GrepReadOperation struct {
	Path             string `json:"path"`
	StartLine        int    `json:"start_line"`
	EndLine          int    `json:"end_line"`
	ResponseSequence int    `json:"response_sequence"`
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
