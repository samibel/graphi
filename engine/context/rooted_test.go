package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootedReader_ResolvesRelativeAgainstRoot(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "a.go"), []byte("l1\nl2\nl3\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := NewRootedReader(dir)
	text, got, err := r.ReadSpan("pkg/a.go", Span{Start: 2, End: 3})
	if err != nil {
		t.Fatal(err)
	}
	if text != "l2\nl3" || got != (Span{Start: 2, End: 3}) {
		t.Fatalf("unexpected read: %q %+v", text, got)
	}

	// Empty root is a passthrough; a path relative to cwd fails cleanly when absent.
	if _, _, err := (RootedReader{}).ReadSpan("does/not/exist.go", Span{Start: 1, End: 1}); err == nil {
		t.Fatal("expected error for missing file under empty root")
	}

	// Remote sources stay rejected through the rooted path.
	if _, _, err := r.ReadSpan("https://example.com/x.go", Span{Start: 1, End: 1}); err == nil ||
		!strings.Contains(err.Error(), "remote source rejected") {
		t.Fatalf("expected remote rejection, got %v", err)
	}
}

func TestFilterReadable_DropsUnreadableKeepsOrder(t *testing.T) {
	reader := memReader{
		"a.go": "one\ntwo\n",
		"c.go": "three\n",
	}
	in := []Candidate{
		{Path: "a.go", StartLine: 1, EndLine: 2},
		{Path: "missing.go", StartLine: 1, EndLine: 1},
		{Path: "c.go", StartLine: 1, EndLine: 1},
	}
	out := FilterReadable(reader, in)
	if len(out) != 2 || out[0].Path != "a.go" || out[1].Path != "c.go" {
		t.Fatalf("unexpected filter result: %+v", out)
	}
	// Deterministic: same input, same output.
	again := FilterReadable(reader, in)
	if len(again) != 2 || again[0] != out[0] || again[1] != out[1] {
		t.Fatalf("filter not deterministic: %+v vs %+v", again, out)
	}
}
