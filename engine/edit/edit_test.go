package edit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
)

// --- test parser -----------------------------------------------------------
//
// stubParser mirrors the ingest test's parser: it emits one node per file whose
// IDENTITY (kind + qualifiedName + sourcePath) is derived from the file path
// only, NOT from the file contents. That is exactly the identity-preserving
// property SW-035 is scoped to: editing the bytes of a file changes the node's
// (non-identity) line/column or nothing at all, but never its identity, so
// incremental and full re-index converge. extractRefs lets a file declare
// dependents via "use:other.go" so the reverse-dep cascade can be exercised.

type stubParser struct{ failOn string }

func (p *stubParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	if p.failOn != "" && path == p.failOn {
		return nil, errors.New("stub parser: forced parse failure on " + path)
	}
	name := "fn" + filepath.Base(path)
	n, err := model.NewNode("function", "pkg/"+name, path, 1, 1)
	if err != nil {
		return nil, err
	}
	return &parse.ParseResult{
		Meta:       parse.SourceMeta{Path: path, Language: "stub", Size: len(src)},
		Nodes:      []model.Node{n},
		Edges:      []model.Edge{},
		References: extractRefs(src),
	}, nil
}

func extractRefs(src []byte) []string {
	var refs []string
	for _, line := range bytes.Split(src, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if bytes.HasPrefix(line, []byte("use:")) {
			refs = append(refs, string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("use:")))))
		}
	}
	return refs
}

// --- harness ---------------------------------------------------------------

type harness struct {
	root    string
	store   graphstore.Graphstore
	ing     *ingest.Ingester
	parser  *stubParser
	applier *edit.Applier
}

func newHarness(t *testing.T, files map[string]string) *harness {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	parser := &stubParser{}
	ing, err := ingest.New(store, parser, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("initial IngestAll: %v", err)
	}

	// Consistency checker: build a throwaway full re-index store each Check, using
	// a fresh parser that shares the same identity rules.
	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, ierr := ingest.New(fs, &stubParser{}, t.TempDir())
		if ierr != nil {
			return nil, nil, nil, ierr
		}
		cleanup := func() { _ = fi.Close(); _ = fs.Close() }
		return fs, fi, cleanup, nil
	})

	applier, err := edit.NewApplier(store, ing, root, checker)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	return &harness{root: root, store: store, ing: ing, parser: parser, applier: applier}
}

func (h *harness) read(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func (h *harness) digest(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	nodes, err := h.store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	edges, err := h.store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	b, err := model.NewGraph(nodes, edges).Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// --- AC-1: success apply + reindex -----------------------------------------

func TestApply_SuccessAppliesAndReindexes(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{
		"a.go": "package a\nconst X = 1\n",
		"b.go": "package b\nuse:a.go\n",
	})

	before := h.read(t, "a.go")
	span := edit.Span{Start: bytesIndex(before, "1"), End: bytesIndex(before, "1") + 1}
	res, err := h.applier.Apply(ctx, edit.EditOp{
		TargetNodeID: "node-a",
		FilePath:     "a.go",
		ByteSpan:     span,
		Replacement:  []byte("42"),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", res.Outcome)
	}
	if got := string(h.read(t, "a.go")); got != "package a\nconst X = 42\n" {
		t.Fatalf("source after edit = %q", got)
	}
	if len(res.TouchedFiles) != 1 || res.TouchedFiles[0] != "a.go" {
		t.Fatalf("touched files = %v, want [a.go]", res.TouchedFiles)
	}
}

// --- AC-3: incremental == full byte-identical (via Marshal) ----------------

func TestApply_ByteIdenticalInvariant(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{
		"a.go": "package a\nconst X = 1\n",
		"b.go": "package b\nuse:a.go\n",
		"c.go": "package c\n",
	})

	// Independent full re-index of the SAME repo into a fresh store; capture its
	// canonical marshalled graph as the reference.
	res, err := h.applier.Apply(ctx, edit.EditOp{
		FilePath:    "a.go",
		ByteSpan:    edit.Span{Start: bytesIndex(h.read(t, "a.go"), "1"), End: bytesIndex(h.read(t, "a.go"), "1") + 1},
		Replacement: []byte("999"),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q", res.Outcome)
	}

	// Build a brand-new store and full-index the post-edit repo; assert the
	// incremental store (h.store) marshals byte-identically. This is the same
	// invariant the embedded ConsistencyChecker enforced, asserted here over the
	// canonical Marshal bytes (NOT the FNV content cache).
	fresh := graphstore.NewMemStore()
	defer fresh.Close()
	fi, err := ingest.New(fresh, &stubParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer fi.Close()
	if err := fi.IngestAll(ctx, h.root); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}

	freshNodes, _ := fresh.Nodes(ctx, graphstore.Query{})
	freshEdges, _ := fresh.Edges(ctx, graphstore.Query{})
	freshGraph, err := model.NewGraph(freshNodes, freshEdges).Marshal()
	if err != nil {
		t.Fatalf("marshal fresh: %v", err)
	}
	if h.digest(t) != string(freshGraph) {
		t.Fatalf("incremental graph not byte-identical to full re-index")
	}
}

// --- AC-2: atomic rollback on each fault mode ------------------------------

func TestApply_RollbackOnEachFaultMode(t *testing.T) {
	faults := []struct {
		name  string
		hook  string
		brk   string // parser failure target (real parse failure path)
		wantE error
	}{
		{name: "write_error", hook: edit.FaultAfterSourceWrite, wantE: edit.ErrWrite},
		{name: "parse_failure_injected", hook: edit.FaultDuringReindex, wantE: edit.ErrReindex},
		{name: "parse_failure_real", brk: "a.go", wantE: edit.ErrReindex},
		{name: "index_inconsistency", hook: edit.FaultConsistencyCheck, wantE: edit.ErrInconsistent},
	}

	for _, f := range faults {
		t.Run(f.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, map[string]string{
				"a.go": "package a\nconst X = 1\n",
				"b.go": "package b\nuse:a.go\n",
			})
			srcBefore := h.read(t, "a.go")
			graphBefore := h.digest(t)

			if f.hook != "" {
				h.applier.SetFaultHook(f.hook)
			}
			if f.brk != "" {
				h.parser.failOn = f.brk
			}

			res, err := h.applier.Apply(ctx, edit.EditOp{
				FilePath:    "a.go",
				ByteSpan:    edit.Span{Start: bytesIndex(srcBefore, "1"), End: bytesIndex(srcBefore, "1") + 1},
				Replacement: []byte("42"),
			})
			if !errors.Is(err, f.wantE) {
				t.Fatalf("err = %v, want wrap of %v", err, f.wantE)
			}
			if res.Outcome != edit.OutcomeRolledBack {
				t.Fatalf("outcome = %q, want rolled_back", res.Outcome)
			}

			// Source rolled back byte-for-byte.
			if got := h.read(t, "a.go"); !bytes.Equal(got, srcBefore) {
				t.Fatalf("source not rolled back: %q != %q", got, srcBefore)
			}
			// Graph rolled back byte-for-byte (marshalled).
			if got := h.digest(t); got != graphBefore {
				t.Fatalf("graph not rolled back to pre-edit state")
			}

			// Idempotency: clear faults and retry the SAME edit — it must now
			// succeed, proving rollback left no poisoned state.
			h.parser.failOn = ""
			h.applier.SetFaultHook("")
			res2, err2 := h.applier.Apply(ctx, edit.EditOp{
				FilePath:    "a.go",
				ByteSpan:    edit.Span{Start: bytesIndex(srcBefore, "1"), End: bytesIndex(srcBefore, "1") + 1},
				Replacement: []byte("42"),
			})
			if err2 != nil || res2.Outcome != edit.OutcomeApplied {
				t.Fatalf("retry after rollback failed: outcome=%q err=%v", res2.Outcome, err2)
			}
			if got := string(h.read(t, "a.go")); got != "package a\nconst X = 42\n" {
				t.Fatalf("retry source = %q", got)
			}
		})
	}
}

// --- crash recovery via dirty-flag + RecoverWithRoot -----------------------

func TestApply_CrashRecoveryViaDirtyFlag(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package a\nconst X = 1\n"})
	srcBefore := h.read(t, "a.go")

	// Simulate a crash DURING re-index: arm ingest's own fail-after-dirty-mark
	// hook so the re-index errors after dirty flags are durable. The edit saga
	// observes a re-index failure, rolls source + graph back; then a recovery pass
	// converges the graph.
	h.ing.SetFailAfterDirtyMarkHook(errors.New("simulated crash mid-reindex"))
	res, err := h.applier.Apply(ctx, edit.EditOp{
		FilePath:    "a.go",
		ByteSpan:    edit.Span{Start: bytesIndex(srcBefore, "1"), End: bytesIndex(srcBefore, "1") + 1},
		Replacement: []byte("7"),
	})
	if !errors.Is(err, edit.ErrReindex) {
		t.Fatalf("err = %v, want ErrReindex", err)
	}
	if res.Outcome != edit.OutcomeRolledBack {
		t.Fatalf("outcome = %q, want rolled_back", res.Outcome)
	}
	if got := h.read(t, "a.go"); !bytes.Equal(got, srcBefore) {
		t.Fatalf("source not rolled back after simulated crash")
	}

	// Recovery converges: dirty units reprocess against the (rolled-back) source.
	if err := h.ing.RecoverWithRoot(ctx, h.root); err != nil {
		t.Fatalf("RecoverWithRoot: %v", err)
	}
}

// --- span edge cases -------------------------------------------------------

func TestApply_SpanEdgeCases(t *testing.T) {
	cases := []struct {
		name        string
		initial     string
		mkSpan      func([]byte) edit.Span
		replacement string
		want        string
	}{
		{
			name:    "deletion_empty_replacement",
			initial: "package a\nconst X = 1\n",
			mkSpan: func(b []byte) edit.Span {
				i := bytesIndex(b, "const X = 1\n")
				return edit.Span{Start: i, End: i + len("const X = 1\n")}
			},
			replacement: "",
			want:        "package a\n",
		},
		{
			name:        "insertion_at_start",
			initial:     "package a\n",
			mkSpan:      func(b []byte) edit.Span { return edit.Span{Start: 0, End: 0} },
			replacement: "// header\n",
			want:        "// header\npackage a\n",
		},
		{
			name:        "insertion_at_eof",
			initial:     "package a\n",
			mkSpan:      func(b []byte) edit.Span { return edit.Span{Start: len(b), End: len(b)} },
			replacement: "const Y = 2\n",
			want:        "package a\nconst Y = 2\n",
		},
		{
			name:    "multibyte_utf8",
			initial: "package a\n// δοκιμή\n",
			mkSpan: func(b []byte) edit.Span {
				i := bytesIndex(b, "δοκιμή")
				return edit.Span{Start: i, End: i + len("δοκιμή")}
			},
			replacement: "тест",
			want:        "package a\n// тест\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := newHarness(t, map[string]string{"a.go": c.initial})
			res, err := h.applier.Apply(ctx, edit.EditOp{
				FilePath:    "a.go",
				ByteSpan:    c.mkSpan([]byte(c.initial)),
				Replacement: []byte(c.replacement),
			})
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if res.Outcome != edit.OutcomeApplied {
				t.Fatalf("outcome = %q", res.Outcome)
			}
			if got := string(h.read(t, "a.go")); got != c.want {
				t.Fatalf("source = %q, want %q", got, c.want)
			}
		})
	}
}

// --- cascade to dependents -------------------------------------------------

func TestApply_CascadesToDependents(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{
		"a.go": "package a\nconst X = 1\n",
		"b.go": "package b\nuse:a.go\n", // b depends on a
	})
	src := h.read(t, "a.go")
	res, err := h.applier.Apply(ctx, edit.EditOp{
		FilePath:    "a.go",
		ByteSpan:    edit.Span{Start: bytesIndex(src, "1"), End: bytesIndex(src, "1") + 1},
		Replacement: []byte("2"),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	// The cascade re-indexes a.go (edited) and b.go (dependent). The consistency
	// gate already proved the result equals a full re-index; here we assert the
	// dependent node still resolves in the live store.
	nodes, _ := h.store.Nodes(ctx, graphstore.Query{})
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes (a,b) after cascade, got %d", len(nodes))
	}
}

// --- validation / security -------------------------------------------------

func TestApply_RejectsInvalidOps(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, map[string]string{"a.go": "package a\n"})

	cases := []struct {
		name string
		op   edit.EditOp
	}{
		{"empty_path", edit.EditOp{FilePath: "", ByteSpan: edit.Span{0, 0}}},
		{"path_escape", edit.EditOp{FilePath: "../escape.go", ByteSpan: edit.Span{0, 0}}},
		{"span_out_of_range", edit.EditOp{FilePath: "a.go", ByteSpan: edit.Span{Start: 0, End: 9999}}},
		{"negative_span", edit.EditOp{FilePath: "a.go", ByteSpan: edit.Span{Start: 5, End: 2}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := h.applier.Apply(ctx, c.op)
			if !errors.Is(err, edit.ErrInvalidOp) {
				t.Fatalf("err = %v, want ErrInvalidOp", err)
			}
			if res.Outcome != edit.OutcomeRolledBack {
				t.Fatalf("outcome = %q", res.Outcome)
			}
			// Source must be untouched (no write attempted on invalid op).
			if got := string(h.read(t, "a.go")); got != "package a\n" {
				t.Fatalf("source mutated on invalid op: %q", got)
			}
		})
	}
}

// bytesIndex returns the byte offset of the first occurrence of sub in b.
func bytesIndex(b []byte, sub string) int {
	return bytes.Index(b, []byte(sub))
}
