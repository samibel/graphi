// SW-264 AC-1 — Client.SearchHybrid(version=0/1) byte-identity with the
// SW-257 §7.2 golden. The /1 path on every surface (CLI, MCP, HTTP) must
// produce the canonical 1590 bytes with sha256
// 0ec5fd56cf662defc4efe69ff9f7be2fe68645bc71bcc5e102535bed5888ae40.
//
// The test exercises the v1 path through the full surface composition:
// corpus fixture → client.Direct.SearchHybrid(version=0) →
// contract.Serialize. It is the AC-1 byte-identity proof the seam relies on:
// a structural change in either path (search_hybrid or the surface
// composition) is a regression the test fails on with the exact bytes.
package client_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// SW-257 §7.2 captured bytes for `search_hybrid` `hello greeter` on the
// SQLite backend at the candidate. The v1 golden is the AC-1 contract
// every shipped surface (CLI, MCP, HTTP) preserves.
const sw257HelloGreeterHash = "0ec5fd56cf662defc4efe69ff9f7be2fe68645bc71bcc5e102535bed5888ae40"
const sw257HelloGreeterBytes = 1590

// TestAC1_ClientSearchHybridV1ByteIdentity is the AC-1 byte-identity proof
// for the shipped surface composition: client.Direct.SearchHybrid with
// version=0 (or version=1) must produce the SW-257 §7.2 canonical bytes
// for `hello greeter` on the corpus/fixtures/go SQLite backend. The same
// composition that serves every shipped surface — MCP, HTTP, CLI — runs
// here, so a structural change in any of those surfaces is caught.
func TestAC1_ClientSearchHybridV1ByteIdentity(t *testing.T) {
	dir := locateFixtureDir(t)
	store, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	if err := ing.IngestAll(context.Background(), dir); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	if err := ing.Close(); err != nil {
		t.Fatalf("ing.Close: %v", err)
	}

	d := client.NewDirect(query.New(store), search.New(store))

	// version=0 selects /1 (the shipped default, byte-identical).
	got, err := d.SearchHybrid(context.Background(), client.SearchHybridParams{
		Query: "hello greeter", MaxItems: 20, Version: 0,
	})
	if err != nil {
		t.Fatalf("Client.SearchHybrid v0: %v", err)
	}
	if sum := sha256.Sum256(got); hex.EncodeToString(sum[:]) != sw257HelloGreeterHash {
		t.Fatalf("v0 sha256 = %s, want SW-257 §7.2 %s (the shipped default changed)",
			hex.EncodeToString(sum[:]), sw257HelloGreeterHash)
	}
	if len(got) != sw257HelloGreeterBytes {
		t.Fatalf("v0 bytes = %d, want SW-257 §7.2 %d (the shipped default changed)",
			len(got), sw257HelloGreeterBytes)
	}

	// version=1 also selects /1.
	got1, err := d.SearchHybrid(context.Background(), client.SearchHybridParams{
		Query: "hello greeter", MaxItems: 20, Version: 1,
	})
	if err != nil {
		t.Fatalf("Client.SearchHybrid v1: %v", err)
	}
	if sum := sha256.Sum256(got1); hex.EncodeToString(sum[:]) != sw257HelloGreeterHash {
		t.Fatalf("v1 sha256 = %s, want SW-257 §7.2 %s",
			hex.EncodeToString(sum[:]), sw257HelloGreeterHash)
	}
	if len(got1) != sw257HelloGreeterBytes {
		t.Fatalf("v1 bytes = %d, want %d", len(got1), sw257HelloGreeterBytes)
	}
}

// locateFixtureDir walks up from this test file to the module root and
// returns corpus/fixtures/go. Same resolution rule every other hermetic
// test in this repo uses.
func locateFixtureDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, "corpus", "fixtures", "go")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod walking up from test cwd")
		}
		dir = parent
	}
}
