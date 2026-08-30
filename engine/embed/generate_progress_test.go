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
		out[i] = embed.Vector{NodeID: r.NodeID, Values: r.Vector}
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
	docs, _ := embed.BuildDocuments(embed.FileSource{
		Source: embed.Source{Language: res.Meta.Language, Bytes: []byte(src)},
		Path:   res.Meta.Path, Nodes: res.Nodes, Spans: res.Spans,
	})
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
	if got.Embedded != 1 || got.Skipped != 1 {
		t.Fatalf("result = %+v, want Embedded=1 Skipped=1 (file node has no document)", got)
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
// the pass embeds in chunks that cover every text in node order, and
// onProgress climbs monotonically to a final (total, total) — the contract
// the CLI renderer relies on. Result and index contents must be identical to
// the unchunked wrapper (GenerateAndPersist over the same registry).
func TestGenerateAndPersistWithProgress_ChunksAndReports(t *testing.T) {
	ctx := context.Background()
	const n = 150 // > 2 chunks at the internal chunk size
	nodes := progressNodes(t, n)

	rec := &recordingEmbedder{inner: embed.NewMockEmbedder(8)}
	reg := embed.NewRegistry()
	reg.Register(rec)

	var steps [][2]int
	store := embed.NewMemGenerationStore()
	res, err := embed.GenerateAndPersistWithProgress(ctx, reg, nodes, embed.V1DocumentSource{}, embed.NewIndex(), store, func(done, total int) {
		steps = append(steps, [2]int{done, total})
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
	if len(rec.calls) < 2 {
		t.Fatalf("expected multiple chunks for %d nodes, got %d call(s)", n, len(rec.calls))
	}

	// Progress is monotonic, one step per chunk, ending at (total, total).
	if len(steps) != len(rec.calls) {
		t.Fatalf("progress steps = %d, want one per chunk (%d)", len(steps), len(rec.calls))
	}
	prev := 0
	for _, s := range steps {
		if s[1] != n {
			t.Fatalf("progress total = %d, want %d", s[1], n)
		}
		if s[0] <= prev {
			t.Fatalf("progress not monotonic: %d after %d", s[0], prev)
		}
		prev = s[0]
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
	nodes := progressNodes(t, 150)
	rec := &recordingEmbedder{inner: embed.NewMockEmbedder(8), failOnCall: 2}
	reg := embed.NewRegistry()
	reg.Register(rec)

	var steps int
	store := embed.NewMemGenerationStore()
	_, err := embed.GenerateAndPersistWithProgress(ctx, reg, nodes, embed.V1DocumentSource{}, embed.NewIndex(), store, func(done, total int) { steps++ }, embed.GraphGenerationPlaceholder)
	if err == nil {
		t.Fatal("want the injected chunk failure to propagate")
	}
	if steps != 1 {
		t.Fatalf("progress steps before failure = %d, want exactly 1 (the successful first chunk)", steps)
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
