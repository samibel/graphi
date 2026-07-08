package ingest_test

// WP-08: a deterministic storage budget that hard-gates WP-06's storage diet.
// The field test's 2.3 GB DB came from ~500 bytes/edge (edge reason inline +
// edge FTS index). WP-06 cut that; this gate keeps it cut. It measures real
// on-disk bytes-per-edge over an EDGE-DENSE fixture (many same-package calls, so
// edge storage — not node storage — dominates the per-edge figure, unlike the
// node-balanced fan-out fixture). edges/node itself is gated separately by
// TestLinkFanout_EdgeExplosionBudget (WP-01).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
)

// maxBytesPerEdge is the armed budget. WP-06 measures ~250 bytes/edge on
// edge-dense real code (graphi's own engine/, edges/node 5.5); the pre-WP-06
// inline-reason + edge-FTS layout was ~2x that. The budget guards against a
// regression back toward the field test's ~500 bytes/edge.
const maxBytesPerEdge = 360.0

// genEdgeDenseRepo builds a single Go package of `files` files, each with `fns`
// functions; every function calls several others in the package, so the linker
// resolves many same-package `calls`/`references` edges — an edge-dense graph
// where per-edge storage dominates the DB size.
func genEdgeDenseRepo(files, fns int) map[string]string {
	out := map[string]string{"go.mod": "module dense\n\ngo 1.21\n"}
	total := files * fns
	for f := 0; f < files; f++ {
		src := "package dense\n\n"
		for k := 0; k < fns; k++ {
			id := f*fns + k
			body := ""
			// Call the next 6 functions (mod total) — dense intra-package calls.
			for j := 1; j <= 6; j++ {
				body += fmt.Sprintf("\tFn%d()\n", (id+j)%total)
			}
			src += fmt.Sprintf("func Fn%d() {\n%s}\n\n", id, body)
		}
		out[fmt.Sprintf("f%d.go", f)] = src
	}
	return out
}

func TestStorageBudget_BytesPerEdge(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	repo := writeRepo(t, genEdgeDenseRepo(20, 20)) // 400 fns, ~2400 edges
	ing := newIngester(t, store, parse.NewDefaultRegistry())
	if err := ing.IngestAll(ctx, repo); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if err := store.WALCheckpoint(ctx, "TRUNCATE"); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	fi, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	var bytesPerEdge float64
	if len(edges) > 0 {
		bytesPerEdge = float64(fi.Size()) / float64(len(edges))
	}
	t.Logf("[GATE WP-08] storage: edges=%d DB=%d bytes = %.1f bytes/edge (budget %.1f)",
		len(edges), fi.Size(), bytesPerEdge, maxBytesPerEdge)

	if bytesPerEdge > maxBytesPerEdge {
		t.Errorf("bytes/edge = %.1f exceeds budget %.1f — storage diet (WP-06) regressed",
			bytesPerEdge, maxBytesPerEdge)
	}
}
