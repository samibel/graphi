package v9

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
)

// followupGoFile is a Go file whose only declaration is one documented
// function with bodyLines statements: doc comment on line 1, signature on
// line 4, closing brace on line bodyLines+6.
func followupGoFile(bodyLines int) []byte {
	lines := []string{"package p", "", "// Run executes the command.", "func Run() error {"}
	for i := 0; i < bodyLines; i++ {
		lines = append(lines, fmt.Sprintf("\tstep%d := resolve(step%d)", i, i))
	}
	lines = append(lines, "\treturn nil", "}", "")
	return []byte(strings.Join(lines, "\n"))
}

func followupSource(repository fstest.MapFS, path string, start, end int) CompactTaskContextSource {
	lines := strings.Split(string(repository[path].Data), "\n")
	return CompactTaskContextSource{Path: path, Start: start, End: end, Text: strings.Join(lines[start-1:end], "\n")}
}

// TestFollowupNamesTheLeadDeclarationWhenItsWindowIsCut pins the compact/13
// rule: when the first emitted source is a cut window into a declaration, the
// response designates that whole declaration — doc comment to closing brace —
// as the one follow-up read. The designation is computed from repository
// bytes and the emitted sources only.
func TestFollowupNamesTheLeadDeclarationWhenItsWindowIsCut(t *testing.T) {
	repository := fstest.MapFS{"cmd/run.go": {Data: followupGoFile(30)}}
	sources := []CompactTaskContextSource{followupSource(repository, "cmd/run.go", 5, 12)}
	got := compactTaskContextFollowup(repository, nil, sources)
	if got == nil || got.Path != "cmd/run.go" || got.Start != 3 || got.End != 36 {
		t.Fatalf("followup = %#v, want cmd/run.go:3-36", got)
	}
}

// TestFollowupIsAbsentWhenTheLeadIsComplete: a complete lead needs no second
// read, and the wire must not carry an empty hint.
func TestFollowupIsAbsentWhenTheLeadIsComplete(t *testing.T) {
	repository := fstest.MapFS{"cmd/run.go": {Data: followupGoFile(10)}}
	sources := []CompactTaskContextSource{followupSource(repository, "cmd/run.go", 3, 16)}
	if got := compactTaskContextFollowup(repository, nil, sources); got != nil {
		t.Fatalf("complete lead produced a followup %#v", got)
	}
	raw, err := json.Marshal(CompactTaskContextStructured{Version: CompactTaskContextVersion, Sources: sources})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "followup") {
		t.Fatalf("wire carries an empty followup: %s", raw)
	}
}

// TestFollowupFollowsTheLeadOnly: the policy is one read of the lead's unit.
// A later cut source does not earn a hint; the measured ceiling showed the
// lead policy reaches the same spans with a fifth of the reads.
func TestFollowupFollowsTheLeadOnly(t *testing.T) {
	repository := fstest.MapFS{
		"cmd/run.go":  {Data: followupGoFile(10)},
		"cmd/exec.go": {Data: followupGoFile(40)},
	}
	sources := []CompactTaskContextSource{
		followupSource(repository, "cmd/run.go", 3, 16),
		followupSource(repository, "cmd/exec.go", 10, 20),
	}
	if got := compactTaskContextFollowup(repository, nil, sources); got != nil {
		t.Fatalf("non-lead source earned a followup %#v", got)
	}
}

// TestFollowupIsCappedAtTheReadLimit: the hint never asks for more than
// FollowupMaxLines from the unit's start, and a cap that adds nothing beyond
// the emitted window yields no hint at all.
func TestFollowupIsCappedAtTheReadLimit(t *testing.T) {
	repository := fstest.MapFS{"cmd/run.go": {Data: followupGoFile(300)}}
	cut := []CompactTaskContextSource{followupSource(repository, "cmd/run.go", 5, 20)}
	got := compactTaskContextFollowup(repository, nil, cut)
	if got == nil || got.Start != 3 || got.End != 3+FollowupMaxLines-1 {
		t.Fatalf("followup = %#v, want cmd/run.go:3-%d", got, 3+FollowupMaxLines-1)
	}
	already := []CompactTaskContextSource{followupSource(repository, "cmd/run.go", 3, 3+FollowupMaxLines-1)}
	if got := compactTaskContextFollowup(repository, nil, already); got != nil {
		t.Fatalf("window already equal to the capped unit produced %#v", got)
	}
}

// TestFollowupNamesTheMarkdownSection: for documentation the unit is the
// section from its heading to the next heading of the same or higher level.
func TestFollowupNamesTheMarkdownSection(t *testing.T) {
	repository := fstest.MapFS{
		"site/content/completions/_index.md": {Data: []byte("# Completions\n\nintro\n\n### Descriptions\n\nCobra supports descriptions.\n\nAdd `--no-descriptions` to hide them.\n\n## Bash completions\n\nbash\n")},
	}
	sources := []CompactTaskContextSource{followupSource(repository, "site/content/completions/_index.md", 7, 8)}
	got := compactTaskContextFollowup(repository, nil, sources)
	if got == nil || got.Start != 5 || got.End != 10 {
		t.Fatalf("followup = %#v, want the heading's section 5-10", got)
	}
}

// TestFollowupIsSupplemental: no repository, an unreadable path, a file kind
// without units, or an unparsable Go file give no hint and no error — the
// first response stands alone, as it did before compact/13.
func TestFollowupIsSupplemental(t *testing.T) {
	repository := fstest.MapFS{
		"cmd/broken.go":   {Data: []byte("package p\nfunc (\n")},
		"cmd/notes.txt":   {Data: []byte("a\nb\nc\n")},
		"cmd/complete.go": {Data: followupGoFile(10)},
	}
	cases := map[string][]CompactTaskContextSource{
		"broken":  {{Path: "cmd/broken.go", Start: 1, End: 1, Text: "package p"}},
		"text":    {{Path: "cmd/notes.txt", Start: 1, End: 1, Text: "a"}},
		"missing": {{Path: "cmd/missing.go", Start: 1, End: 1, Text: "package p"}},
		"nothing": {},
	}
	for name, sources := range cases {
		if got := compactTaskContextFollowup(repository, nil, sources); got != nil {
			t.Fatalf("%s: unexpected followup %#v", name, got)
		}
	}
	cut := []CompactTaskContextSource{followupSource(repository, "cmd/complete.go", 5, 8)}
	if got := compactTaskContextFollowup(nil, nil, cut); got != nil {
		t.Fatalf("nil repository produced %#v", got)
	}
}

// TestFollowupWireIsOneCitation: the hint is serialized as `path:start-end`
// and read back exactly; malformed citations are rejected rather than
// silently dropped.
func TestFollowupWireIsOneCitation(t *testing.T) {
	raw, err := json.Marshal(CompactTaskContextStructured{
		Version: CompactTaskContextVersion, Followup: &CompactTaskContextFollowup{Path: "site/a:b.md", Start: 3, End: 36},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"followup":"site/a:b.md:3-36"`) {
		t.Fatalf("wire = %s", raw)
	}
	var back CompactTaskContextStructured
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Followup == nil || *back.Followup != (CompactTaskContextFollowup{Path: "site/a:b.md", Start: 3, End: 36}) {
		t.Fatalf("round trip = %#v", back.Followup)
	}
	for _, bad := range []string{`"cmd/run.go"`, `"cmd/run.go:3"`, `"cmd/run.go:0-3"`, `"cmd/run.go:9-3"`, `{"path":"cmd/run.go"}`} {
		if err := json.Unmarshal([]byte(bad), &CompactTaskContextFollowup{}); err == nil {
			t.Fatalf("accepted malformed followup %s", bad)
		}
	}
}
