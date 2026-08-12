package context

import (
	"fmt"
	"os"
	"strings"
)

// LocalReader is the production SourceReader. It reads file spans from local
// disk ONLY. It rejects remote sources (http/https URLs) so the local-first
// contract is enforced at the only I/O boundary in the engine. It performs no
// network I/O of any kind.
type LocalReader struct{}

// NewLocalReader returns a disk-backed SourceReader.
func NewLocalReader() LocalReader { return LocalReader{} }

// ReadSpan reads lines [want.Start, want.End] (1-based, inclusive) from the file
// at path and returns the joined text plus got, the ACTUAL (clamped) span read.
// Clamping keeps the citation honest: a want window past EOF or before line 1 is
// narrowed to the available lines, and got reflects exactly what was read, so
// re-reading got always reproduces text.
//
// Remote sources are rejected with an explicit error (local-first contract).
func (LocalReader) ReadSpan(path string, want Span) (string, Span, error) {
	if isRemoteSource(path) {
		return "", Span{}, fmt.Errorf("context: remote source rejected (%q) — SourceReader is local-disk only", path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // local-first engine reading a graph-tracked source path
	if err != nil {
		return "", Span{}, fmt.Errorf("context: read %s: %w", path, err)
	}
	lines := splitKeepLines(string(data))
	return extractSpan(path, lines, want)
}

// RootedReader resolves repo-relative candidate paths against a fixed root
// before delegating to the disk-backed LocalReader. Graph paths are stored
// repo-relative; surfaces that know the repository root wrap LocalReader in a
// RootedReader so snippet reads work regardless of the process working
// directory. An empty Root is a passthrough (cwd-relative, today's CLI
// behavior when run from the repo root). Absolute paths are passed through
// untouched. The remote-source rejection stays in LocalReader.
type RootedReader struct {
	Root string
}

// NewRootedReader returns a SourceReader that resolves paths against root.
func NewRootedReader(root string) RootedReader { return RootedReader{Root: root} }

// ReadSpan resolves path against Root and reads via LocalReader.
func (r RootedReader) ReadSpan(path string, want Span) (string, Span, error) {
	p := path
	if r.Root != "" && !strings.HasPrefix(p, "/") && !isRemoteSource(p) {
		p = strings.TrimSuffix(r.Root, "/") + "/" + p
	}
	return LocalReader{}.ReadSpan(p, want)
}

// FilterReadable returns the candidates whose first span line is readable via
// reader, preserving order. Assemble is deliberately fail-closed (one read
// error aborts the whole bundle); callers that prefer to drop unreadable
// candidates — e.g. attach-mode surfaces whose working directory is not the
// repo root — pre-filter with this helper. Deterministic: a candidate is kept
// or dropped purely by the reader's success on its probe span.
func FilterReadable(reader Reader, cands []Candidate) []Candidate {
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		if _, _, err := reader.ReadSpan(c.Path, Span{Start: c.StartLine, End: c.StartLine}); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// extractSpan is the pure, testable core of ReadSpan: given the file's lines and
// a want window, it returns the clamped text + actual span. It is shared with
// the in-memory test reader so clamping behavior is identical everywhere.
func extractSpan(path string, lines []string, want Span) (string, Span, error) {
	n := len(lines)
	if n == 0 {
		return "", Span{Start: 0, End: 0}, nil
	}
	start := want.Start
	end := want.End
	if start < 1 {
		start = 1
	}
	if end > n {
		end = n
	}
	if end < start {
		// Empty/negative window after clamping → no bytes.
		return "", Span{Start: start, End: start - 1}, nil
	}
	return strings.Join(lines[start-1:end], "\n"), Span{Start: start, End: end}, nil
}

// splitKeepLines splits file content into lines WITHOUT a trailing newline per
// element (so joining with "" round-trips the cited bytes exactly). A trailing
// newline produces no extra empty element (mirrors how a citation should map to
// source bytes).
func splitKeepLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Trim exactly one trailing newline so the final line is not reported as empty.
	hadTrailing := strings.HasSuffix(s, "\n")
	if hadTrailing {
		s = s[:len(s)-1]
	}
	return strings.Split(s, "\n")
}

// isRemoteSource reports whether path looks like a remote URL. The engine only
// reads local files; this guard keeps the local-first contract honest.
func isRemoteSource(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
