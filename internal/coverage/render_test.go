package coverage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMarkdownIsFresh asserts the checked-in docs/coverage-matrix.md is exactly
// RenderMarkdown(LoadMatrix(docs/coverage-matrix.yaml)), so the human-readable
// table can never drift from the YAML source of truth (AC-6). If this fails, run
// `go run ./cmd/coverage -generate`.
func TestMarkdownIsFresh(t *testing.T) {
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("ModuleRoot: %v", err)
	}
	caps, err := LoadMatrix(filepath.Join(root, filepath.FromSlash(MatrixYAMLPath)))
	if err != nil {
		t.Fatalf("LoadMatrix: %v", err)
	}
	want := RenderMarkdown(caps)

	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(MatrixMDPath)))
	if err != nil {
		t.Fatalf("read %s: %v", MatrixMDPath, err)
	}
	if string(got) != want {
		t.Errorf("%s is stale — run `go run ./cmd/coverage -generate`", MatrixMDPath)
	}
}
