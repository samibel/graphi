package embed_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/embed"
)

// docSource is a DocumentSource over a prebuilt document set (what
// `graphi index --semantic` supplies from re-parsed source files).
type docSource map[model.NodeId]embed.SemanticDocument

func (s docSource) Document(n model.Node) (embed.SemanticDocument, bool) {
	d, ok := s[n.ID()]
	return d, ok
}

// loadRows loads the active generation from a GenerationStore and converts
// each Row to a Vector for the byte-compat checks in this file.
func loadRows(t *testing.T, s embed.GenerationStore, fp embed.Fingerprint) []embed.Vector {
	t.Helper()
	ctx := context.Background()
	gen, _, err := s.Active(ctx, fp, nil)
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if gen.ID == "" {
		return nil
	}
	rows, err := s.Load(ctx, gen.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out := make([]embed.Vector, len(rows))
	for i, r := range rows {
		out[i] = embed.Vector{NodeID: r.NodeID, DocumentID: r.DocumentID, Values: r.Vector}
	}
	return out
}

// fpFor builds a Fingerprint that matches what GenerateAndPersist will use
// internally (ModelID/Dim/DocumentSchema only — graph_generation is left at
// the placeholder because the in-process test does not have a graphstore).
func fpFor(emb embed.Embedder) embed.Fingerprint {
	return embed.Fingerprint{
		ModelID:         emb.ID(),
		Dim:             emb.Dim(),
		DocumentSchema:  embed.DocumentSchema,
		GraphGeneration: embed.GraphGenerationPlaceholder,
	}
}

// TestGenerateAndPersist_EmbedsV2DocumentText pins SW-260 AC-8: the
// generation pass embeds SemanticDocument.text (body + doc + path), not the
// v1 NodeText, and a node without a document is skipped and counted rather
// than embedded as a name-only stand-in.
func TestGenerateAndPersist_EmbedsV2DocumentText(t *testing.T) {
	ctx := context.Background()
	const src = "package shop\n\n// Price computes the price.\nfunc Price(n int) int { return n * 7 }\n"
	res, err := parse.NewDefaultRegistry().Parse(ctx, "shop/price.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	docs, _, err := embed.BuildDocuments(embed.FileSource{
		Source: embed.Source{Language: res.Meta.Language, Bytes: []byte(src)},
		Path:   res.Meta.Path, Nodes: res.Nodes, Spans: res.Spans,
	})
	if err != nil {
		t.Fatal(err)
	}
	source := docSource{}
	for _, d := range docs {
		source[d.NodeID] = d
	}

	rec := &recordingEmbedder{inner: embed.NewMockEmbedder(8)}
	reg := embed.NewRegistry()
	reg.Register(rec)
	store := embed.NewMemGenerationStore()
	got, err := embed.GenerateAndPersist(ctx, reg, res.Nodes, source, embed.NewIndex(), store, embed.GraphGenerationPlaceholder)
	if err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}
	// The file node has no document: skipped, not embedded.
	if got.Embedded != 1 || got.Excluded != 1 {
		t.Fatalf("result = %+v, want Embedded=1 Excluded=1 (file node has no document)", got)
	}
	if len(rec.calls) != 1 || len(rec.calls[0]) != 1 {
		t.Fatalf("embed calls = %v", rec.calls)
	}
	text := rec.calls[0][0]
	if !strings.Contains(text, "// Price computes the price.") || !strings.Contains(text, "return n * 7") {
		t.Errorf("embedded text is not the v2 document: %q", text)
	}
	for _, n := range res.Nodes {
		if text == embed.NodeText(n) {
			t.Errorf("embedded text is the deprecated v1 NodeText %q", text)
		}
	}
	rows := loadRows(t, store, fpFor(rec))
	if len(rows) != 1 || rows[0].NodeID != docs[0].NodeID {
		t.Errorf("persisted rows = %+v", rows)
	}
	// A nil source with a configured embedder is an error, never a silent v1 fallback.
	if _, err := embed.GenerateAndPersist(ctx, reg, res.Nodes, nil, embed.NewIndex(), store, embed.GraphGenerationPlaceholder); err == nil {
		t.Error("nil DocumentSource must be an error")
	}
	// The graceful skip still precedes everything: no embedder, no source needed.
	if r, err := embed.GenerateAndPersist(ctx, embed.NewRegistry(), res.Nodes, nil, embed.NewIndex(), store, embed.GraphGenerationPlaceholder); err != nil || r.Configured {
		t.Errorf("graceful skip = %+v, %v", r, err)
	}
}

// recordingEmbedder wraps the deterministic mock and records every Embed
// call's input size; failOnCall (1-based) makes that call error, so tests can
// pin the chunked failure path.
type recordingEmbedder struct {
	inner      embed.Embedder
	calls      [][]string
	failOnCall int
}

func (r *recordingEmbedder) ID() string { return r.inner.ID() }
func (r *recordingEmbedder) Dim() int   { return r.inner.Dim() }
func (r *recordingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	r.calls = append(r.calls, append([]string(nil), texts...))
	if r.failOnCall > 0 && len(r.calls) == r.failOnCall {
		return nil, errors.New("injected embed failure")
	}
	return r.inner.Embed(ctx, texts)
}

func progressNodes(t *testing.T, n int) []model.Node {
	t.Helper()
	nodes := make([]model.Node, 0, n)
	for i := 0; i < n; i++ {
		nd, err := model.NewNode("function", fmt.Sprintf("pkg.Fn%03d", i), "pkg/f.go", i+1, 1)
		if err != nil {
			t.Fatalf("NewNode: %v", err)
		}
		nodes = append(nodes, nd)
	}
	return nodes
}

// TestGenerateAndPersistWithProgress_ChunksAndReports pins the progress seam:
// the pass visits every document in node order, and
// onProgress climbs monotonically to a final (total, total) — the contract
// the CLI renderer relies on. Result and index contents must be identical to
// the unchunked wrapper (GenerateAndPersist over the same registry).
func TestGenerateAndPersistWithProgress_ChunksAndReports(t *testing.T) {
	ctx := context.Background()
	const n = 12
	nodes := progressNodes(t, n)

	rec := &recordingEmbedder{inner: embed.NewMockEmbedder(8)}
	reg := embed.NewRegistry()
	reg.Register(rec)

	var steps []embed.GenerationProgress
	store := embed.NewMemGenerationStore()
	res, err := embed.GenerateAndPersistWithProgress(ctx, reg, nodes, embed.V1DocumentSource{}, embed.NewIndex(), store, func(ev embed.GenerationProgress) {
		steps = append(steps, ev)
	}, embed.GraphGenerationPlaceholder)
	if err != nil {
		t.Fatalf("GenerateAndPersistWithProgress: %v", err)
	}
	if res.Embedded != n {
		t.Fatalf("Embedded = %d, want %d", res.Embedded, n)
	}

	// Chunks cover all texts, in order, with no overlap.
	var covered int
	for _, call := range rec.calls {
		for i, text := range call {
			want := embed.NodeText(nodes[covered+i])
			if text != want {
				t.Fatalf("chunked text %d = %q, want %q (order must match node order)", covered+i, text, want)
			}
		}
		covered += len(call)
	}
	if covered != n {
		t.Fatalf("chunks covered %d texts, want %d", covered, n)
	}
	if len(rec.calls) != n {
		t.Fatalf("embed calls = %d, want one bounded call per document (%d)", len(rec.calls), n)
	}

	// Progress is monotonic, one step per chunk, ending at (total, total).
	if len(steps) != n+1 {
		t.Fatalf("progress steps = %d, want initial event plus one per document (%d)", len(steps), n+1)
	}
	prev := -1
	for _, s := range steps {
		if s.Total != n {
			t.Fatalf("progress total = %d, want %d", s.Total, n)
		}
		if s.Done <= prev {
			t.Fatalf("progress not monotonic: %d after %d", s.Done, prev)
		}
		if s.GenerationID == "" || s.GenerationID != res.GenerationID {
			t.Fatalf("progress generation = %q, want %q", s.GenerationID, res.GenerationID)
		}
		prev = s.Done
	}
	if prev != n {
		t.Fatalf("final progress = %d, want %d", prev, n)
	}

	// Byte-compat with the plain wrapper: same persisted vectors per node.
	reg2 := embed.NewRegistry()
	reg2.Register(embed.NewMockEmbedder(8))
	store2 := embed.NewMemGenerationStore()
	if _, err := embed.GenerateAndPersist(ctx, reg2, nodes, embed.V1DocumentSource{}, embed.NewIndex(), store2, embed.GraphGenerationPlaceholder); err != nil {
		t.Fatalf("GenerateAndPersist: %v", err)
	}
	got := loadRows(t, store, fpFor(rec))
	want := loadRows(t, store2, fpFor(rec.inner))
	if len(got) != len(want) || len(got) != n {
		t.Fatalf("persisted vectors: chunked=%d plain=%d, want %d", len(got), len(want), n)
	}
	for i := range got {
		if got[i].NodeID != want[i].NodeID {
			t.Fatalf("row %d: chunked node %s vs plain %s", i, got[i].NodeID, want[i].NodeID)
		}
		for d := range got[i].Values {
			if got[i].Values[d] != want[i].Values[d] {
				t.Fatalf("node %s: chunked vector diverges from plain at dim %d", got[i].NodeID, d)
			}
		}
	}
}

// TestGenerateAndPersistWithProgress_ChunkFailurePropagates pins the failure
// contract: an embed error in a later chunk surfaces as an error (earlier
// chunks' vectors are already persisted — derived state a re-run overwrites).
func TestGenerateAndPersistWithProgress_ChunkFailurePropagates(t *testing.T) {
	ctx := context.Background()
	// AC-6 (SW-265): chunk size is 256. Need > 256 nodes to get past the
	// first chunk and reach the failing second chunk.
	nodes := progressNodes(t, 3)
	rec := &recordingEmbedder{inner: embed.NewMockEmbedder(8), failOnCall: 2}
	reg := embed.NewRegistry()
	reg.Register(rec)

	var steps int
	store := embed.NewMemGenerationStore()
	_, err := embed.GenerateAndPersistWithProgress(ctx, reg, nodes, embed.V1DocumentSource{}, embed.NewIndex(), store, func(embed.GenerationProgress) { steps++ }, embed.GraphGenerationPlaceholder)
	if err == nil {
		t.Fatal("want the injected chunk failure to propagate")
	}
	if steps != 2 {
		t.Fatalf("progress steps before failure = %d, want initial plus first document", steps)
	}
	// The Commit never ran, so Active returns StateMissing — there is no
	// persisted generation to count. The first chunk's rows live in the
	// staging generation; the partial progress is recoverable on a
	// re-run because the next Begin drops the stale staging row.
	gen, state, err := store.Active(ctx, fpFor(rec), nil)
	if err != nil {
		t.Fatalf("Active after failure: %v", err)
	}
	if state != embed.StateMissing {
		t.Fatalf("state after chunk-2 failure = %s, want missing (no Commit)", state)
	}
	if gen.ID != "" {
		t.Fatalf("expected no active generation after a failed Commit, got %s", gen.ID)
	}
}

// TestGenerateAndPersistWithProgress_AC6_ProgressByDocument pins the
// SW-265 AC-6 contract: the progress callback is invoked with the running
// DOCUMENT count (not the running HTTP call count), so the CLI renderer's
// "documents N/M" line moves by document, not by HTTP round-trip.
//
// What the contract pins:
//
//   - onProgress is called exactly once per chunk (running total vs total).
//   - the running count is in DOCUMENT UNITS — every chunk advances the
//     counter by `min(chunkSize, remaining)` documents.
//   - the final step is (total, total) — never an off-by-one, never a
//     skipped tail.
//   - the per-chunk `done` value is the absolute document count, not the
//     chunk's offset within the run; the CLI uses it as "X of N documents".
//
// A regression that replaced the document count with a per-call counter
// (e.g. `len(rec.calls)`) would fail this test: that counter would step
// from 0 to 1, 2, 3 — not 256, 512, 768.
func TestGenerateAndPersistWithProgress_AC6_ProgressByDocument(t *testing.T) {
	ctx := context.Background()
	const n = 3
	nodes := progressNodes(t, n)

	rec := &recordingEmbedder{inner: embed.NewMockEmbedder(8)}
	reg := embed.NewRegistry()
	reg.Register(rec)

	var steps []embed.GenerationProgress
	store := embed.NewMemGenerationStore()
	res, err := embed.GenerateAndPersistWithProgress(ctx, reg, nodes, embed.V1DocumentSource{}, embed.NewIndex(), store, func(ev embed.GenerationProgress) {
		steps = append(steps, ev)
	}, embed.GraphGenerationPlaceholder)
	if err != nil {
		t.Fatalf("GenerateAndPersistWithProgress: %v", err)
	}

	// The final step MUST be (n, n) — the contract is "by document", so
	// "done" is the absolute document count.
	if len(steps) == 0 {
		t.Fatal("no progress steps recorded")
	}
	last := steps[len(steps)-1]
	if last.Done != n || last.Total != n {
		t.Fatalf("final progress = (%d, %d), want (%d, %d)", last.Done, last.Total, n, n)
	}

	// Every `done` value MUST be a multiple of the chunk size (or n for
	// the last step). It must NEVER be a per-call counter (1, 2, 3, …)
	// nor a fraction. The chunked contract is what the CLI's progress
	// renderer reads: "256/600 documents", "512/600 documents", etc.
	expectedDones := []int{0, 1, 2, 3}
	if len(steps) != len(expectedDones) {
		t.Fatalf("progress steps = %d, want %d (one per chunk)", len(steps), len(expectedDones))
	}
	for i, step := range steps {
		if step.Done != expectedDones[i] {
			t.Fatalf("step %d done = %d, want %d (AC-6 progress by document)", i, step.Done, expectedDones[i])
		}
		if step.Total != n {
			t.Fatalf("step %d total = %d, want %d", i, step.Total, n)
		}
		if step.GenerationID != res.GenerationID || step.GenerationID == "" {
			t.Fatalf("step %d generation = %q, want %q", i, step.GenerationID, res.GenerationID)
		}
	}
}
