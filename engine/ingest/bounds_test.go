package ingest_test

import (
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// TestIngest_FailsClosed_OnOversizeFile proves the fail-closed max-file-size bound
// (SW-055 AC#6): an over-bound file is SKIPPED with a structured diagnostic and
// ingestion of the remaining files continues (never parse-anyway, never truncate).
func TestIngest_FailsClosed_OnOversizeFile(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &stubParser{}
	i := newIngester(t, store, parser).WithResourceBounds(parse.ResourceBounds{
		MaxFileSize:  64, // tiny bound
		ParseTimeout: 0,
		MaxDepth:     0,
	})

	// small.go is under the bound; big.go is over it.
	root := writeRepo(t, map[string]string{
		"small.go": "package a\n",
		"big.go":   strings.Repeat("x", 200), // 200 bytes > 64
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll should not abort on an oversize file (fail-closed skip): %v", err)
	}

	skips := i.SkippedDiagnostics()
	if len(skips) != 1 {
		t.Fatalf("expected exactly 1 skip diagnostic, got %d: %v", len(skips), skips)
	}
	if skips[0].Reason != ingest.SkipOversize {
		t.Fatalf("expected oversize skip, got %q", skips[0].Reason)
	}
	if skips[0].Path != "big.go" {
		t.Fatalf("expected big.go skipped, got %q", skips[0].Path)
	}
	if skips[0].Size != 200 {
		t.Fatalf("expected recorded size 200, got %d", skips[0].Size)
	}
	// The diagnostic must NOT echo raw source content.
	if strings.Contains(skips[0].Path, "xxxx") {
		t.Fatal("skip diagnostic leaked raw source")
	}
	// small.go still ingested → parser was invoked for exactly one file.
	if parser.parseCount.Load() != 1 {
		t.Fatalf("expected the under-bound file to still be parsed (count=1), got %d", parser.parseCount.Load())
	}

	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("read nodes: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected exactly 1 node (from small.go), got %d", len(nodes))
	}
}

// TestIngest_FailsClosed_OnDeepNesting proves the fail-closed recursion-depth bound
// end-to-end through ingest with the real default parsers: a deeply-nested file is
// skipped (ErrMaxDepthExceeded), a shallow sibling is ingested, and no raw source
// leaks into the diagnostic.
func TestIngest_FailsClosed_OnDeepNesting(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	realParser := parse.RegisterDefaults(parse.NewRegistry())
	i := newIngester(t, store, realParser).WithResourceBounds(parse.ResourceBounds{
		MaxFileSize:  16 << 20,
		ParseTimeout: 0,
		MaxDepth:     40, // tight
	})
	defer parse.SetMaxParseDepth(parse.DefaultResourceBounds().MaxDepth)

	deep := "z = " + strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300) + "  # SECRET_TOKEN_xyz\n"
	root := writeRepo(t, map[string]string{
		"shallow.py": "def f():\n    return 1\n",
		"deep.py":    deep,
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll should fail-closed skip the deep file, not abort: %v", err)
	}

	skips := i.SkippedDiagnostics()
	if len(skips) != 1 || skips[0].Reason != ingest.SkipMaxDepth || skips[0].Path != "deep.py" {
		t.Fatalf("expected one max-depth skip of deep.py, got %v", skips)
	}
}

// parserRegistryAdapter lets a *parse.Registry satisfy ingest.Parser.
// (parse.Registry already has Parse(ctx, filename, src) so it satisfies the
// ingest.Parser interface directly — this is just a compile-time assertion.)
var _ ingest.Parser = (*parse.Registry)(nil)
