package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixtureFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckSpanCoverage(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "auth/token.go", "package auth\n\n// ValidateToken checks.\nfunc ValidateToken() {}\n\nfunc other() {}\n")

	good := Judgement{Path: "auth/token.go", StartLine: 3, EndLine: 4, Anchor: "func ValidateToken", Grade: 3,
		Reason: "r", Annotator: "a", Reviewer: "o"}
	ds := func(js ...Judgement) *Dataset {
		return &Dataset{Queries: []Query{{ID: "q1", Stratum: StratumExactIdentifier, Judgements: js}}}
	}

	if err := CheckSpanCoverage(root, ds(good)); err != nil {
		t.Fatalf("a resolvable span must pass: %v", err)
	}

	cases := []struct {
		name  string
		mut   func(*Judgement)
		wants string
	}{
		{"missing file", func(j *Judgement) { j.Path = "auth/gone.go" }, "auth/gone.go"},
		{"directory instead of file", func(j *Judgement) { j.Path = "auth" }, "not a regular file"},
		{"end line past EOF", func(j *Judgement) { j.EndLine = 40 }, "has 6 lines"},
		{"anchor moved out of the range", func(j *Judgement) { j.StartLine, j.EndLine = 5, 6 }, "anchor"},
		{"anchor text no longer in the file", func(j *Judgement) { j.Anchor = "func ValidateJWT" }, "anchor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stale := good
			tc.mut(&stale)
			err := CheckSpanCoverage(root, ds(good, stale))
			if err == nil {
				t.Fatal("a stale span must fail the coverage check")
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wants) || !strings.Contains(msg, "q1") {
				t.Errorf("error = %q, want it to name query q1 and mention %q", msg, tc.wants)
			}
		})
	}

	t.Run("every unresolved span is listed, not only the first", func(t *testing.T) {
		a, b := good, good
		a.Path = "a.go"
		b.Path = "b.go"
		err := CheckSpanCoverage(root, ds(a, b))
		if err == nil || !strings.Contains(err.Error(), "a.go") || !strings.Contains(err.Error(), "b.go") {
			t.Errorf("error = %v, want both a.go and b.go named", err)
		}
	})
}

func TestReadSpan_ReturnsTheJudgedLines(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "f.go", "l1\nl2\nl3\nl4\n")
	text, err := ReadSpan(root, "f.go", 2, 3)
	if err != nil || text != "l2\nl3" {
		t.Errorf("ReadSpan = %q, %v", text, err)
	}
	if _, err := ReadSpan(root, "f.go", 3, 9); err == nil {
		t.Error("a range past EOF must be an error")
	}
}
