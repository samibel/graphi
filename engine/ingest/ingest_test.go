package ingest_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

type stubParser struct {
	parseCount int
}

func (p *stubParser) Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error) {
	p.parseCount++
	name := "fn" + filepath.Base(path)
	n, err := model.NewNode("function", "pkg/"+name, path, 1, 1)
	if err != nil {
		return nil, err
	}
	refs := extractRefs(path, string(src))
	return &parse.ParseResult{
		Meta: parse.SourceMeta{
			Path:        path,
			Language:    "stub",
			ContentHash: "",
			Size:        len(src),
		},
		Nodes:      []model.Node{n},
		Edges:      []model.Edge{},
		References: refs,
	}, nil
}

// extractRefs parses simple directives like "use:other.go" to build the import set.
func extractRefs(path, src string) []string {
	var refs []string
	for _, line := range bytes.Split([]byte(src), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("use:")) {
			refs = append(refs, string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("use:")))))
		}
	}
	return refs
}

func newIngester(t *testing.T, store graphstore.Graphstore, parser ingest.Parser) *ingest.Ingester {
	t.Helper()
	i, err := ingest.New(store, parser, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = i.Close() })
	return i
}

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestIngest_FullThenUnchangedIsNoOp(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	parser := &stubParser{}
	i := newIngester(t, store, parser)

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go",
	})

	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	firstCount := parser.parseCount

	if err := i.IngestChanged(ctx, repo, nil); err != nil {
		t.Fatalf("IngestChanged no-op: %v", err)
	}
	if parser.parseCount != firstCount {
		t.Fatalf("expected no parse calls, got %d more", parser.parseCount-firstCount)
	}
}

func TestIngest_SingleFileChange(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	parser := &stubParser{}
	i := newIngester(t, store, parser)

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go",
		"c.go": "package c\n",
	})

	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	parser.parseCount = 0

	// Change a.go -> b.go depends on a.go and must be re-parsed.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n//changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite a.go: %v", err)
	}
	if err := i.IngestChanged(ctx, repo, []string{"a.go"}); err != nil {
		t.Fatalf("IngestChanged: %v", err)
	}
	if parser.parseCount != 2 {
		t.Fatalf("expected 2 re-parses (a.go + b.go), got %d", parser.parseCount)
	}
}

func TestIngest_CrashRecovery(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()
	parser := &stubParser{}
	i := newIngester(t, store, parser)

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
	})

	if err := i.IngestAll(ctx, repo); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	parser.parseCount = 0

	// Rewrite a.go and inject a fault after dirty-mark.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n//changed\n"), 0o600); err != nil {
		t.Fatalf("rewrite a.go: %v", err)
	}
	injected := fmt.Errorf("simulated crash after dirty-mark")
	i.SetFailAfterDirtyMarkHook(injected)
	if err := i.IngestChanged(ctx, repo, []string{"a.go"}); !isError(err, injected) {
		t.Fatalf("expected injected error, got %v", err)
	}

	// Recover should reprocess a.go exactly and clear the dirty set.
	parser.parseCount = 0
	if err := i.RecoverWithRoot(ctx, repo); err != nil {
		t.Fatalf("RecoverWithRoot: %v", err)
	}
	if parser.parseCount != 1 {
		t.Fatalf("expected 1 re-parse during recovery, got %d", parser.parseCount)
	}
}

func TestIngest_GoldenIncrementalVsFull(t *testing.T) {
	ctx := context.Background()

	// Full reindex store.
	storeFull := graphstore.NewMemStore()
	parserFull := &stubParser{}
	iFull := newIngester(t, storeFull, parserFull)

	// Incremental store.
	storeInc := graphstore.NewMemStore()
	parserInc := &stubParser{}
	iInc := newIngester(t, storeInc, parserInc)

	repo := writeRepo(t, map[string]string{
		"a.go": "package a\n",
		"b.go": "package b\nuse:a.go",
		"c.go": "package c\n",
	})

	if err := iFull.IngestAll(ctx, repo); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}
	if err := iInc.IngestAll(ctx, repo); err != nil {
		t.Fatalf("inc IngestAll: %v", err)
	}

	// Mutate repo: edit a.go, add d.go, delete c.go, change b.go import.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a\n//changed\n"), 0o600); err != nil {
		t.Fatalf("edit a.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "d.go"), []byte("package d\nuse:b.go\n"), 0o600); err != nil {
		t.Fatalf("add d.go: %v", err)
	}
	if err := os.Remove(filepath.Join(repo, "c.go")); err != nil {
		t.Fatalf("remove c.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "b.go"), []byte("package b\nuse:a.go\n//changed\n"), 0o600); err != nil {
		t.Fatalf("edit b.go: %v", err)
	}

	// Full reindex on mutated repo.
	if err := iFull.IngestAll(ctx, repo); err != nil {
		t.Fatalf("full reindex: %v", err)
	}
	// Incremental on mutated repo.
	if err := iInc.IngestChanged(ctx, repo, []string{"a.go", "b.go", "d.go"}); err != nil {
		t.Fatalf("incremental: %v", err)
	}

	fullSnap := filepath.Join(t.TempDir(), "full.snapshot")
	incSnap := filepath.Join(t.TempDir(), "inc.snapshot")
	if err := storeFull.Snapshot(ctx, fullSnap); err != nil {
		t.Fatalf("full snapshot: %v", err)
	}
	if err := storeInc.Snapshot(ctx, incSnap); err != nil {
		t.Fatalf("inc snapshot: %v", err)
	}
	b1, err := os.ReadFile(fullSnap)
	if err != nil {
		t.Fatalf("read full: %v", err)
	}
	b2, err := os.ReadFile(incSnap)
	if err != nil {
		t.Fatalf("read inc: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("incremental and full snapshots differ:\n%s\n%s", b1, b2)
	}
}

func isError(err, target error) bool {
	if err == nil {
		return target == nil
	}
	return err.Error() == target.Error()
}
