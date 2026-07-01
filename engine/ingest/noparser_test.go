package ingest_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// goOnlyParser mimics the real parse.Registry's behavior of returning
// ErrNoParser for any file whose extension isn't registered — unlike
// stubParser, which "parses" every file it's handed regardless of name.
type goOnlyParser struct {
	parseCount int
}

func (p *goOnlyParser) Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error) {
	if !strings.HasSuffix(path, ".go") {
		return nil, parse.ErrNoParser
	}
	p.parseCount++
	n, err := model.NewNode("function", "pkg/fn"+filepath.Base(path), path, 1, 1)
	if err != nil {
		return nil, err
	}
	return &parse.ParseResult{
		Meta:  parse.SourceMeta{Path: path, Language: "stub", Size: len(src)},
		Nodes: []model.Node{n},
	}, nil
}

// TestIngest_FailsClosed_OnUnsupportedFileType is a regression test: a repo
// containing files with no registered parser (e.g. a macOS .DS_Store, an
// image, a lockfile — the overwhelming majority of non-source files in any
// real-world repository) used to abort the ENTIRE ingest with
// "ingest: parse .DS_Store: parse: no parser registered for file type",
// because parse.ErrNoParser fell through parseUnit's skip-sentinel switch
// into a hard error. It must now be silently treated as "not source code" —
// no error, no diagnostic (this is the expected common case, not a
// resource-bound breach) — and ingestion of the rest of the repo must proceed.
func TestIngest_FailsClosed_OnUnsupportedFileType(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &goOnlyParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{
		"real.go":    "package a\n",
		".DS_Store":  "\x00\x01binary junk",
		"logo.png":   "\x89PNG\r\n",
		"go.sum.lck": "not a real lockfile format",
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll should not abort on a file type with no registered parser: %v", err)
	}
	if parser.parseCount != 1 {
		t.Fatalf("expected only real.go to reach the parser, got %d parses", parser.parseCount)
	}
	if skips := i.SkippedDiagnostics(); len(skips) != 0 {
		t.Fatalf("expected zero skip diagnostics (no-parser is silent, not diagnostic-worthy), got %v", skips)
	}
}
