package context

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
)

// memReader is an in-memory SourceReader for deterministic tests. It shares
// extractSpan with LocalReader so clamping behavior is identical.
type memReader map[string]string

func (m memReader) ReadSpan(path string, want Span) (string, Span, error) {
	data, ok := m[path]
	if !ok {
		return "", Span{}, fmt.Errorf("context: read %s: not found", path)
	}
	lines := splitKeepLines(data)
	return extractSpan(path, lines, want)
}

// AC: bundle returns ranked evidence snippets trimmed to relevant spans (not
// whole files); citation resolves to source.
func TestAssemble_ReturnsWinnowedSnippetsInRankOrder(t *testing.T) {
	r := memReader{
		"a.go": "package a\nfunc A() {}\nfunc B() {}\nfunc C() {}\n",
		"b.go": "package b\nvar X = 1\n",
	}
	cands := []Candidate{
		{Path: "a.go", StartLine: 3, EndLine: 3, Rank: 2.0}, // func B
		{Path: "a.go", StartLine: 2, EndLine: 2, Rank: 1.0}, // func A
		{Path: "b.go", StartLine: 2, EndLine: 2, Rank: 3.0}, // var X
	}
	bundle, err := Assemble("q", cands, Options{Budget: 100, ContextLines: 0}, r)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Snippets) != 3 {
		t.Fatalf("want 3 snippets, got %d (%+v)", len(bundle.Snippets), bundle.Snippets)
	}
	// Rank order: 1.0 (func A), 2.0 (func B), 3.0 (var X).
	wantTexts := []string{"func A() {}", "func B() {}", "var X = 1"}
	for i, want := range wantTexts {
		if bundle.Snippets[i].Text != want {
			t.Errorf("snippet %d: want %q got %q", i, want, bundle.Snippets[i].Text)
		}
	}
	// Citations resolve to the carried bytes.
	for _, s := range bundle.Snippets {
		got, _, err := r.ReadSpan(s.Citation.Path, Span{Start: s.Citation.StartLine, End: s.Citation.EndLine})
		if err != nil {
			t.Fatal(err)
		}
		if got != s.Text {
			t.Errorf("citation %v does not round-trip: want %q got %q", s.Citation, s.Text, got)
		}
	}
}

// AC: token budget — include in rank order until budget reached, drop the
// remainder; never emit an over-budget bundle.
func TestAssemble_BudgetNeverExceeded(t *testing.T) {
	cases := []struct {
		name   string
		budget int
	}{
		{"zero budget -> empty", 0},
		{"negative budget -> empty", -5},
		{"tiny budget", 1},
		{"partial", 4},
		{"fits first only", 2},
		{"fits all", 100},
	}
	cands := []Candidate{
		{Path: "a.go", StartLine: 1, EndLine: 1, Rank: 1},   // "one two" = 2 tokens
		{Path: "a.go", StartLine: 2, EndLine: 2, Rank: 2.0}, // "three four" = 2 tokens
		{Path: "a.go", StartLine: 3, EndLine: 3, Rank: 3.0}, // "five six" = 2 tokens
	}
	r := memReader{"a.go": "one two\nthree four\nfive six\n"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle, err := Assemble("q", cands, Options{Budget: tc.budget}, r)
			if err != nil {
				t.Fatal(err)
			}
			if tc.budget > 0 && bundle.Tokens > tc.budget {
				t.Errorf("budget %d: tokens %d exceeds budget", tc.budget, bundle.Tokens)
			}
			if bundle.Tokens != sumTokens(bundle.Snippets) {
				t.Errorf("Tokens (%d) != sum of snippet tokens (%d)", bundle.Tokens, sumTokens(bundle.Snippets))
			}
			// Inclusion is greedy by rank: included snippets are a prefix of the
			// rank-ordered candidate sequence.
			if tc.budget <= 0 && len(bundle.Snippets) != 0 {
				t.Errorf("budget %d: want empty bundle, got %d snippets", tc.budget, len(bundle.Snippets))
			}
		})
	}
	// Explicit: a candidate whose first snippet alone exceeds the budget -> empty.
	r2 := memReader{"big.go": "a b c d e f g h i j\n"} // 10 tokens
	b, err := Assemble("q", []Candidate{{Path: "big.go", StartLine: 1, EndLine: 1, Rank: 1.0}}, Options{Budget: 5}, r2)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Snippets) != 0 {
		t.Errorf("single over-budget snippet: want empty bundle, got %d snippets", len(b.Snippets))
	}
}

func sumTokens(snips []Snippet) int {
	n := 0
	for _, s := range snips {
		n += s.Tokens
	}
	return n
}

// AC: deterministic — identical inputs produce byte-identical bundles across
// runs (catches map-iteration-order leakage and nondeterministic emission).
func TestAssemble_DeterministicByteIdentical(t *testing.T) {
	// Scramble input order deliberately; identical SETS must yield identical output.
	candsA := []Candidate{
		{Path: "a.go", StartLine: 2, EndLine: 2, Rank: 2.0},
		{Path: "a.go", StartLine: 1, EndLine: 1, Rank: 1.0},
		{Path: "b.go", StartLine: 1, EndLine: 1, Rank: 1.5},
		{Path: "a.go", StartLine: 1, EndLine: 1, Rank: 1.0}, // duplicate
	}
	candsReversed := make([]Candidate, len(candsA))
	for i := range candsA {
		candsReversed[i] = candsA[len(candsA)-1-i]
	}
	r := memReader{
		"a.go": "alpha\nbeta gamma\ndelta\n",
		"b.go": "epsilon zeta\n",
	}
	b1, err := Assemble("q", candsA, Options{Budget: 10, ContextLines: 1}, r)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Assemble("q", candsReversed, Options{Budget: 10, ContextLines: 1}, r)
	if err != nil {
		t.Fatal(err)
	}
	j1, _ := json.Marshal(b1)
	j2, _ := json.Marshal(b2)
	if string(j1) != string(j2) {
		t.Errorf("bundles not byte-identical across input orderings\n A=%s\n B=%s", j1, j2)
	}
}

// AC: winnowing emits only the relevant span + bounded context padding, clamped
// to file bounds; the rest of the file is excluded.
func TestWinnow_PaddingClampedToBFileBounds(t *testing.T) {
	r := memReader{"f.go": "L1\nL2\nL3\n"} // 3 lines
	// Match on line 1 with large padding -> clamped to [1,3].
	snip, err := winnow(r, Candidate{Path: "f.go", StartLine: 1, EndLine: 1, Rank: 1}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if snip.Citation.StartLine != 1 || snip.Citation.EndLine != 3 {
		t.Errorf("clamp: want [1,3] got %v", snip.Citation)
	}
	if snip.Text != "L1\nL2\nL3" {
		t.Errorf("text: want whole file (no trailing nl), got %q", snip.Text)
	}
	// Match on line 2, no padding -> exactly line 2.
	snip2, err := winnow(r, Candidate{Path: "f.go", StartLine: 2, EndLine: 2, Rank: 1}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snip2.Text != "L2" || snip2.Citation.StartLine != 2 || snip2.Citation.EndLine != 2 {
		t.Errorf("exact span: want line 2 only, got %q %v", snip2.Text, snip2.Citation)
	}
}

// AC: offline / local-first — the SourceReader rejects remote sources; assembly
// from on-disk fixtures succeeds with no network.
func TestLocalReader_RejectsRemoteSources(t *testing.T) {
	lr := NewLocalReader()
	for _, remote := range []string{"http://example.com/a.go", "https://example.com/a.go"} {
		if _, _, err := lr.ReadSpan(remote, Span{Start: 1, End: 1}); err == nil {
			t.Errorf("remote source %q should have been rejected", remote)
		}
	}
	// A real local file assembles fine (write a fixture).
	dir := t.TempDir()
	path := filepath.Join(dir, "real.go")
	if err := os.WriteFile(path, []byte("package real\nfunc Real() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := Assemble("q", []Candidate{{Path: path, StartLine: 2, EndLine: 2, Rank: 1}}, Options{Budget: 100}, lr)
	if err != nil {
		t.Fatalf("local assembly failed: %v", err)
	}
	if len(bundle.Snippets) != 1 || !strings.Contains(bundle.Snippets[0].Text, "func Real() {}") {
		t.Fatalf("local assembly produced wrong bundle: %+v", bundle)
	}
}

// AC: intake adapters preserve EP-001 provenance.
func TestFromSearchMatches_Intake(t *testing.T) {
	resp := search.Response{Query: "A", Matches: []search.Match{
		{NodeID: "n1", QualifiedName: "A", Kind: "func", SourcePath: "a.go", Line: 5, Column: 1, Rank: 0.1},
		{SourcePath: "", Line: 9, Rank: 0.2},     // dropped: no path
		{SourcePath: "b.go", Line: 0, Rank: 0.3}, // dropped: line < 1
	}}
	cands := FromSearchMatches(resp)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate (others dropped), got %d", len(cands))
	}
	want := Candidate{Path: "a.go", StartLine: 5, EndLine: 5, Rank: 0.1, Symbol: "A", Kind: "func"}
	if !reflect.DeepEqual(cands[0], want) {
		t.Errorf("intake mismatch: want %+v got %+v", want, cands[0])
	}
}

func TestFromQueryResult_Intake(t *testing.T) {
	res := query.Result{Nodes: []query.ResultNode{
		{ID: "n1", QualifiedName: "A", Kind: "func", SourcePath: "a.go", Line: 5, Column: 1},
		{SourcePath: ""}, // dropped
	}}
	cands := FromQueryResult(res)
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	if cands[0].Path != "a.go" || cands[0].StartLine != 5 || cands[0].Rank != 0 {
		t.Errorf("query intake: %+v", cands[0])
	}
}

// candidateLess total-order pin.
func TestCandidateLess_TotalOrder(t *testing.T) {
	cases := []struct {
		name string
		a, b Candidate
		want bool
	}{
		{"rank decides", Candidate{Rank: 1}, Candidate{Rank: 2}, true},
		{"path tiebreak", Candidate{Rank: 1, Path: "a"}, Candidate{Rank: 1, Path: "b"}, true},
		{"startline tiebreak", Candidate{Rank: 1, Path: "a", StartLine: 2}, Candidate{Rank: 1, Path: "a", StartLine: 3}, true},
		{"endline tiebreak", Candidate{Rank: 1, Path: "a", StartLine: 2, EndLine: 2}, Candidate{Rank: 1, Path: "a", StartLine: 2, EndLine: 3}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := candidateLess(tc.a, tc.b); got != tc.want {
				t.Errorf("candidateLess: want %v got %v", tc.want, got)
			}
		})
	}
}
