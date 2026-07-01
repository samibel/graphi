package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// flakyJSONParser mimics core/parse.JSONParser's behavior: it rejects any file
// whose content contains "{{" (Handlebars-style templating, as used by WireMock
// stub files — syntactically invalid JSON) with a plain, non-sentinel error, and
// otherwise "parses" successfully. It also honors ctx cancellation like a real
// parser would, to exercise the outer-cancellation-must-not-be-swallowed path.
type flakyJSONParser struct {
	parseCount int
}

func (p *flakyJSONParser) Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.Contains(string(src), "{{") {
		return nil, fmt.Errorf("parse: json syntax error in %q: invalid character '{' looking for beginning of object key string", path)
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

// TestIngest_FailsClosed_OnGenuineParseError is a regression test: a repo
// containing a file that is recognized as a language (has a registered parser)
// but whose content doesn't actually parse — e.g. a WireMock stub `.json` file
// using Handlebars templating — used to abort the ENTIRE ingest with an error
// like `ingest: parse foo.json: parse: json syntax error in "foo.json": ...`.
// It must now be recorded as a SkipParseError diagnostic (unlike SkipUnreadable
// or ErrNoParser's silent skip, a genuine syntax error IS worth surfacing) and
// ingestion of the rest of the repo must proceed.
func TestIngest_FailsClosed_OnGenuineParseError(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &flakyJSONParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{
		"real.json":     `{"ok": true}`,
		"wiremock.json": `{{randomValue type='UUID'}}`,
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll should not abort on a genuine parse error in one file: %v", err)
	}
	if parser.parseCount != 1 {
		t.Fatalf("expected only real.json to parse successfully, got %d", parser.parseCount)
	}

	skips := i.SkippedDiagnostics()
	if len(skips) != 1 {
		t.Fatalf("expected exactly 1 skip diagnostic, got %d: %v", len(skips), skips)
	}
	if skips[0].Path != "wiremock.json" {
		t.Fatalf("expected wiremock.json skipped, got %q", skips[0].Path)
	}
	if skips[0].Reason != ingest.SkipParseError {
		t.Fatalf("expected SkipParseError, got %q", skips[0].Reason)
	}
}

// TestIngest_PropagatesOuterContextCancellation guards against a regression in
// the SkipParseError fix: a caller-cancelled/expired context must still abort
// IngestAll promptly, not be swallowed as "just another file to skip and keep
// going" — that would grind through the rest of a possibly huge repo after the
// caller already asked to stop.
func TestIngest_PropagatesOuterContextCancellation(t *testing.T) {
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := &flakyJSONParser{}
	i := newIngester(t, store, parser)

	root := writeRepo(t, map[string]string{
		"a.json": `{"ok": true}`,
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before IngestAll even starts

	err := i.IngestAll(ctx, root)
	if err == nil {
		t.Fatal("expected IngestAll to return an error when the context is already cancelled, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the error to wrap context.Canceled, got: %v", err)
	}
}
