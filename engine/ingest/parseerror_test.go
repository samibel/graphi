package ingest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// wiremockBody is a WireMock __files response body using Handlebars
// response-templating (`{{...}}` at a structural, object-key position). WireMock
// renders it at runtime; strict encoding/json rejects it with
// "invalid character '{' looking for beginning of object key string".
const wiremockBody = "{\n  {{#each request.body.licenses}}\n  \"id\": \"{{randomValue type='UUID'}}\"\n  {{/each}}\n}\n"

// TestIngest_FailsClosed_OnMalformedJSON is a regression test for the reported
// crash: a repo containing a single non-strict `.json` file — the canonical
// case being a WireMock __files response body — used to abort the ENTIRE full
// ingest with
//
//	ingest: parse wiremock/__files/license/createLicenseBatch_201_response.json:
//	parse: json syntax error in "...": invalid character '{' looking for
//	beginning of object key string
//
// because a genuine parse/syntax error fell through parseUnit's skip-sentinel
// switch into a hard error. The malformed file must now be SKIPPED fail-closed
// with a structured SkipParseError diagnostic, and indexing of the rest of the
// repo must proceed. It asserts BOTH sides: exactly one skip is recorded (a
// malformed source file the user asked to index IS diagnostic-worthy), AND a
// valid file after it is genuinely indexed (proving ingestion did not silently
// no-op the whole repo — recall on the positive case).
func TestIngest_FailsClosed_OnMalformedJSON(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	// The real default registry: `.json` dispatches to the strict encoding/json
	// JSONParser (reproducing the user's exact failure), `.go` to the GoParser
	// (which emits real symbol nodes we can assert on).
	parser := ingest.NewNotebookParser(parse.NewDefaultRegistry())
	i := newIngester(t, store, parser)

	const badPath = "wiremock/__files/license/createLicenseBatch_201_response.json"
	root := writeRepo(t, map[string]string{
		badPath:        wiremockBody,
		"pkg/ok.go":    "package pkg\n\nfunc Ok() {}\n",
		"data/ok.json": "{\"ok\": true}\n",
	})

	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll must not abort on a single malformed JSON file: %v", err)
	}

	// Precision: exactly one skip, for the malformed file, with the right reason.
	skips := i.SkippedDiagnostics()
	if len(skips) != 1 {
		t.Fatalf("expected exactly one skip diagnostic, got %d: %v", len(skips), skips)
	}
	if got := skips[0]; got.Path != badPath || got.Reason != ingest.SkipParseError {
		t.Fatalf("expected SkipParseError for %q, got %+v", badPath, got)
	}

	// Recall: the valid Go file after the malformed JSON was actually indexed —
	// the ingest continued rather than silently skipping everything.
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("Nodes: %v", err)
	}
	var indexedOk bool
	for _, n := range nodes {
		if n.SourcePath() == "pkg/ok.go" {
			indexedOk = true
			break
		}
	}
	if !indexedOk {
		t.Fatalf("expected pkg/ok.go to be indexed after the malformed JSON was skipped; nodes=%v", nodes)
	}
}

// TestIngestChanged_ElevatesReprocessedParseErrorToHardError guards the
// incremental-path invariant that keeps the edit saga's two stores consistent.
// A full index tolerates a malformed file (skip + continue), but the INCREMENTAL
// path only ever reparses files it was explicitly asked to process. If one of
// those is now unparseable, IngestChanged MUST return a hard error so its
// metadata transaction rolls back atomically — otherwise the meta cache commits
// "0 nodes / clean" while the graphstore keeps the file's stale nodes, and the
// edit applier's compensate (which restores the graphstore snapshot but not the
// meta DB) would permanently poison the sidecar. This is the transaction-boundary
// fix for that divergence.
func TestIngestChanged_ElevatesReprocessedParseErrorToHardError(t *testing.T) {
	ctx := context.Background()
	store := graphstore.NewMemStore()
	defer store.Close()

	parser := ingest.NewNotebookParser(parse.NewDefaultRegistry())
	i := newIngester(t, store, parser)

	const jsonPath = "wiremock/__files/license/createLicenseBatch_201_response.json"
	root := writeRepo(t, map[string]string{
		jsonPath: "{\"id\": \"seed\"}\n", // starts valid
	})
	if err := i.IngestAll(ctx, root); err != nil {
		t.Fatalf("initial IngestAll: %v", err)
	}

	// The file is edited to the non-strict WireMock body and reprocessed
	// explicitly — exactly what the edit applier / watcher do.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(jsonPath)), []byte(wiremockBody), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if err := i.IngestChanged(ctx, root, []string{jsonPath}); err == nil {
		t.Fatalf("IngestChanged must return a hard error when a reprocessed file no longer parses, so the meta transaction rolls back; got nil")
	}
}
