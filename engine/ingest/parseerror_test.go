package ingest_test

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// TestIngest_FailsClosed_OnMalformedJSON is a regression test for the reported
// crash: a repo containing a single non-strict `.json` file — the canonical
// case being a WireMock __files response body that uses Handlebars
// response-templating and is therefore not valid strict JSON — used to abort
// the ENTIRE ingest with
//
//	ingest: parse wiremock/__files/license/createLicenseBatch_201_response.json:
//	parse: json syntax error in "...": invalid character '{' looking for
//	beginning of object key string
//
// because a genuine parse/syntax error fell through parseUnit's skip-sentinel
// switch into a hard error. The malformed file must now be SKIPPED fail-closed
// with a structured SkipParseError diagnostic, and indexing of the rest of the
// repo must proceed. Unlike a no-parser asset (silently untracked), a malformed
// source file the user asked to index IS diagnostic-worthy, so exactly one skip
// is recorded.
func TestIngest_FailsClosed_OnMalformedJSON(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	// The real default registry: `.json` dispatches to the strict encoding/json
	// JSONParser, reproducing the user's exact failure rather than a stub.
	parser := ingest.NewNotebookParser(parse.NewDefaultRegistry())
	i := newIngester(t, store, parser)

	const badPath = "wiremock/__files/license/createLicenseBatch_201_response.json"
	root := writeRepo(t, map[string]string{
		// Handlebars template at a structural (object-key) position — accepted by
		// WireMock, rejected by strict JSON with the reported error.
		badPath: "{\n  {{#each request.body.licenses}}\n  \"id\": \"{{randomValue type='UUID'}}\"\n  {{/each}}\n}\n",
		// A valid JSON file that must still be indexed after the bad one is skipped.
		"data/ok.json": "{\"ok\": true}\n",
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll must not abort on a single malformed JSON file: %v", err)
	}

	skips := i.SkippedDiagnostics()
	if len(skips) != 1 {
		t.Fatalf("expected exactly one skip diagnostic, got %d: %v", len(skips), skips)
	}
	if got := skips[0]; got.Path != badPath || got.Reason != ingest.SkipParseError {
		t.Fatalf("expected SkipParseError for %q, got %+v", badPath, got)
	}
}
