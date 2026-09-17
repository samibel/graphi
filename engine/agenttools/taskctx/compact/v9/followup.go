package v9

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"strconv"
	"strings"
)

// FollowupMaxLines bounds the one follow-up read a compact response may
// designate. It is the cap under which the second-response contract was
// measured; a longer unit is designated from its start up to this many lines.
const FollowupMaxLines = 120

// CompactTaskContextFollowup names the one exact span a reader should fetch
// next when the lead source is a cut window into a larger unit: the whole
// declaration (doc comment to closing brace) for Go, the whole section for
// Markdown. It is a designation, not source bytes; the response stays within
// its own ceiling and the read is charged separately by whoever performs it.
type CompactTaskContextFollowup struct {
	Path  string
	Start int
	End   int
}

// MarshalJSON writes the designation as one citation, `path:start-end`, the
// same shape a reader would type into a file read. The object form costs a
// dozen more tokens on every response that carries a hint and says nothing
// the citation does not.
func (f CompactTaskContextFollowup) MarshalJSON() ([]byte, error) {
	return json.Marshal(FormatFollowupCitation(f.Path, f.Start, f.End))
}

// UnmarshalJSON accepts the citation form only.
func (f *CompactTaskContextFollowup) UnmarshalJSON(raw []byte) error {
	var citation string
	if err := json.Unmarshal(raw, &citation); err != nil {
		return fmt.Errorf("compact task_context: followup: %w", err)
	}
	path, start, end, err := ParseFollowupCitation(citation)
	if err != nil {
		return err
	}
	*f = CompactTaskContextFollowup{Path: path, Start: start, End: end}
	return nil
}

// FormatFollowupCitation renders `path:start-end`.
func FormatFollowupCitation(path string, start, end int) string {
	return path + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

// ParseFollowupCitation is the inverse of FormatFollowupCitation. The path
// may itself contain colons; the citation is split at the last one.
func ParseFollowupCitation(citation string) (path string, start, end int, err error) {
	colon := strings.LastIndex(citation, ":")
	if colon <= 0 {
		return "", 0, 0, fmt.Errorf("compact task_context: followup %q is not path:start-end", citation)
	}
	lines := strings.Split(citation[colon+1:], "-")
	if len(lines) != 2 {
		return "", 0, 0, fmt.Errorf("compact task_context: followup %q is not path:start-end", citation)
	}
	start, err = strconv.Atoi(lines[0])
	if err == nil {
		end, err = strconv.Atoi(lines[1])
	}
	if err != nil || start < 1 || end < start {
		return "", 0, 0, fmt.Errorf("compact task_context: followup %q is not path:start-end", citation)
	}
	return citation[:colon], start, end, nil
}

// compactTaskContextFollowup applies the lead policy: only the first emitted
// source is considered, and only repository bytes decide its unit. Anything
// that cannot be read or parsed yields no hint; the first response then
// stands alone exactly as it did before compact/13.
func compactTaskContextFollowup(repository fs.FS, snapshot *grepReadSnapshot, sources []CompactTaskContextSource) *CompactTaskContextFollowup {
	if repository == nil || len(sources) == 0 {
		return nil
	}
	lead := sources[0]
	raw, err := readScannedSource(repository, snapshot, lead.Path)
	if err != nil {
		return nil
	}
	start, end, ok := 0, 0, false
	switch {
	case strings.HasSuffix(lead.Path, ".go"):
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, lead.Path, raw, parser.ParseComments)
		if err != nil {
			return nil
		}
		start, end, ok = compactTaskContextDeclarationSpan(set, file, lead.Start)
	case strings.HasSuffix(lead.Path, ".md"):
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		start, end, ok = compactTaskContextMarkdownSection(lines, lead.Start)
	}
	if !ok {
		return nil
	}
	if end-start+1 > FollowupMaxLines {
		end = start + FollowupMaxLines - 1
	}
	if start >= lead.Start && end <= lead.End {
		return nil
	}
	return &CompactTaskContextFollowup{Path: lead.Path, Start: start, End: end}
}
