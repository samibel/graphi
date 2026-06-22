package edit_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
)

// --- refactor test parser --------------------------------------------------
//
// refactorParser models the non-identity-preserving shape SW-036 must handle. A
// file's node IDENTITY is derived from its CONTENT (the `def:Name` directive), so
// renaming the symbol mints a NEW NodeId and the old one must be deleted by the
// incremental re-index (Slice 1) for the graph to stay byte-identical with a full
// re-index.
//
// Directives (one per line):
//
//	def:Name            — this file defines symbol pkg/Name (the file's node).
//	ref:Path:Name       — this file references the node (function, pkg/Name, Path),
//	                      emitting a `references` edge def-node -> target so the
//	                      EP-004 forward-impact analyzer can discover this file as
//	                      a dependent of the target. Path is also listed in
//	                      References for the reverse-dependency cascade.
//
// Fixtures order files so a referenced definition file sorts BEFORE its referrers
// (ingest walks sorted), guaranteeing PutEdge endpoints exist.
type refactorParser struct{ failOn string }

func (p *refactorParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	if p.failOn != "" && path == p.failOn {
		return nil, errors.New("refactor parser: forced failure on " + path)
	}
	name := "fn" + filepath.Base(path)
	var refs []string
	var edges []model.Edge

	for _, raw := range bytes.Split(src, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if strings.HasPrefix(line, "def:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "def:"))
		}
	}
	defNode, err := model.NewNode("function", "pkg/"+name, path, 1, 1)
	if err != nil {
		return nil, err
	}

	for _, raw := range bytes.Split(src, []byte("\n")) {
		line := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(line, "ref:") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "ref:"))
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			continue
		}
		targetPath, targetName := parts[0], parts[1]
		refs = append(refs, targetPath)
		target, err := model.NewNode("function", "pkg/"+targetName, targetPath, 1, 1)
		if err != nil {
			return nil, err
		}
		e, err := model.NewEdge(defNode.ID(), target.ID(), "references",
			model.TierDerived, 0.9, "references pkg/"+targetName, []string{path + ":1"})
		if err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}

	return &parse.ParseResult{
		Meta:       parse.SourceMeta{Path: path, Language: "stub", Size: len(src)},
		Nodes:      []model.Node{defNode},
		Edges:      edges,
		References: refs,
	}, nil
}

// --- harness ---------------------------------------------------------------

type rharness struct {
	root    string
	store   graphstore.Graphstore
	ing     *ingest.Ingester
	parser  *refactorParser
	applier *edit.Applier
}

func newRefactorHarness(t *testing.T, files map[string]string) *rharness {
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
	parser := &refactorParser{}
	ing, err := ingest.New(store, parser, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("initial IngestAll: %v", err)
	}

	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, ierr := ingest.New(fs, &refactorParser{}, t.TempDir())
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
	return &rharness{root: root, store: store, ing: ing, parser: parser, applier: applier}
}

func (h *rharness) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// liveDigest marshals the live store canonically (the AC-1 comparison target).
func (h *rharness) liveDigest(t *testing.T) string {
	t.Helper()
	return marshalStore(t, h.store)
}

// fullReindexDigest builds a fresh store, full-indexes the CURRENT on-disk repo,
// and returns its canonical marshalled bytes — the byte-identical oracle (AC-1).
func (h *rharness) fullReindexDigest(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	fresh := graphstore.NewMemStore()
	defer fresh.Close()
	fi, err := ingest.New(fresh, &refactorParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	defer fi.Close()
	if err := fi.IngestAll(ctx, h.root); err != nil {
		t.Fatalf("full IngestAll: %v", err)
	}
	return marshalStore(t, fresh)
}

func marshalStore(t *testing.T, store graphstore.Graphstore) string {
	t.Helper()
	ctx := context.Background()
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	b, err := model.NewGraph(nodes, edges).Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func (h *rharness) symbolID(name, path string) string {
	n, _ := model.NewNode("function", "pkg/"+name, path, 1, 1)
	return string(n.ID())
}

// nodeQNames returns the sorted qualified names currently in the live store.
func (h *rharness) nodeQNames(t *testing.T) []string {
	t.Helper()
	nodes, err := h.store.Nodes(context.Background(), graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.QualifiedName())
	}
	return out
}

// --- AC-1: graph-aware rename across multiple files, byte-identical ---------

func TestApplyRefactor_RenameAcrossFilesByteIdentical(t *testing.T) {
	ctx := context.Background()
	// def.go defines Widget; two files reference it (the blast radius).
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
		"c_use.go": "def:UseC\nref:a_def.go:Widget\nWidget()\n",
	})

	target := h.symbolID("Widget", "a_def.go")
	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: target,
		OldName:      "Widget",
		NewName:      "Gadget",
	})
	if err != nil {
		t.Fatalf("ApplyRefactor: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q, want applied", res.Outcome)
	}
	// Every file containing a reference was rewritten.
	if got := h.read(t, "a_def.go"); !strings.Contains(got, "def:Gadget") {
		t.Fatalf("definition not renamed: %q", got)
	}
	for _, f := range []string{"b_use.go", "c_use.go"} {
		got := h.read(t, f)
		if strings.Contains(got, "Widget") {
			t.Fatalf("reference in %s not rewritten: %q", f, got)
		}
		if !strings.Contains(got, "Gadget()") {
			t.Fatalf("reference in %s missing new name: %q", f, got)
		}
	}
	// AC-1: incremental graph is byte-identical to a full re-index, AND the old
	// (pkg/Widget) node was deleted, not orphaned.
	if h.liveDigest(t) != h.fullReindexDigest(t) {
		t.Fatalf("incremental graph not byte-identical to full re-index after rename")
	}
	for _, qn := range h.nodeQNames(t) {
		if qn == "pkg/Widget" {
			t.Fatalf("old node pkg/Widget orphaned after rename")
		}
	}
}

// --- AC-1 dry-run preview: no mutation, impact set surfaced -----------------

func TestApplyRefactor_DryRunPreviewsWithoutMutating(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	})
	before := h.liveDigest(t)
	srcBefore := h.read(t, "b_use.go")

	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("ApplyRefactor dry-run: %v", err)
	}
	if !res.DryRun || len(res.PlannedOps) == 0 {
		t.Fatalf("dry-run did not surface planned ops: %+v", res)
	}
	if len(res.ImpactFiles) < 2 {
		t.Fatalf("dry-run impact files = %v, want >=2", res.ImpactFiles)
	}
	// No mutation occurred.
	if h.liveDigest(t) != before {
		t.Fatalf("dry-run mutated the graph")
	}
	if h.read(t, "b_use.go") != srcBefore {
		t.Fatalf("dry-run mutated source")
	}
}

// --- AC-3: signature change updates every call site + atomic rollback -------

func TestApplyRefactor_SignatureChangeAllCallSites(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Compute\n",
		"b_use.go": "def:UseB\nref:a_def.go:Compute\nCompute()\n",
		"c_use.go": "def:UseC\nref:a_def.go:Compute\nCompute()\n",
	})
	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorSignatureChange,
		TargetSymbol: h.symbolID("Compute", "a_def.go"),
		OldName:      "Compute",
		NewName:      "ComputeV2",
	})
	if err != nil {
		t.Fatalf("ApplyRefactor: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	// Every call site rewritten; no edge points at the old signature.
	for _, qn := range h.nodeQNames(t) {
		if qn == "pkg/Compute" {
			t.Fatalf("old signature node still present")
		}
	}
	edges, _ := h.store.Edges(ctx, graphstore.Query{})
	oldID := h.symbolID("Compute", "a_def.go")
	for _, e := range edges {
		if string(e.To()) == oldID {
			t.Fatalf("edge still points at old signature node")
		}
	}
	if h.liveDigest(t) != h.fullReindexDigest(t) {
		t.Fatalf("signature change not byte-identical to full re-index")
	}
}

// --- AC-3: multi-file fail-on-K rollback (no partial graph state) -----------

func TestApplyRefactor_FailOnFileKRollsBackAll(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
		"c_use.go": "def:UseC\nref:a_def.go:Widget\nWidget()\n",
	})
	graphBefore := h.liveDigest(t)
	srcBefore := map[string]string{
		"a_def.go": h.read(t, "a_def.go"),
		"b_use.go": h.read(t, "b_use.go"),
		"c_use.go": h.read(t, "c_use.go"),
	}

	// Fail while writing the 2nd touched file: files written before it must be
	// restored AND the graph must be unchanged.
	h.applier.SetBatchFaultHook(2)
	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	})
	if !errors.Is(err, edit.ErrWrite) {
		t.Fatalf("err = %v, want wrap of ErrWrite", err)
	}
	if res.Outcome != edit.OutcomeRolledBack {
		t.Fatalf("outcome = %q, want rolled_back", res.Outcome)
	}
	// All source files restored byte-for-byte.
	for f, want := range srcBefore {
		if got := h.read(t, f); got != want {
			t.Fatalf("file %s not rolled back: %q != %q", f, got, want)
		}
	}
	// Graph unchanged.
	if h.liveDigest(t) != graphBefore {
		t.Fatalf("graph not rolled back after fail-on-K")
	}

	// Idempotency: clear the fault and retry — it must now succeed.
	h.applier.SetBatchFaultHook(0)
	res2, err2 := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	})
	if err2 != nil || res2.Outcome != edit.OutcomeApplied {
		t.Fatalf("retry after rollback failed: outcome=%q err=%v", res2.Outcome, err2)
	}
	if h.liveDigest(t) != h.fullReindexDigest(t) {
		t.Fatalf("retried rename not byte-identical to full re-index")
	}
}

// --- AC-2: extract/move provenance + freshness restored <=2s ----------------

func TestApplyRefactor_ExtractByteIdentical(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Helper\n",
		"b_use.go": "def:UseB\nref:a_def.go:Helper\nHelper()\n",
	})
	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorExtract,
		TargetSymbol: h.symbolID("Helper", "a_def.go"),
		OldName:      "Helper",
		NewName:      "Helper2",
	})
	if err != nil {
		t.Fatalf("ApplyRefactor extract: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if h.liveDigest(t) != h.fullReindexDigest(t) {
		t.Fatalf("extract not byte-identical to full re-index")
	}
}

func TestApplyRefactor_MoveByteIdentical(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Mover\n",
		"b_use.go": "def:UseB\nref:a_def.go:Mover\nMover()\n",
	})
	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorMove,
		TargetSymbol: h.symbolID("Mover", "a_def.go"),
		OldName:      "Mover",
		NewName:      "Moved",
	})
	if err != nil {
		t.Fatalf("ApplyRefactor move: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if h.liveDigest(t) != h.fullReindexDigest(t) {
		t.Fatalf("move not byte-identical to full re-index")
	}
}

// AC-2 provenance: affected edges carry correct re-derived PARSE provenance
// (tier/confidence/reason/evidence) after the incremental re-index — NOT a
// per-edit-id record (that is SW-037, per the refinement scope clarification).
func TestApplyRefactor_AffectedEdgesCarryParseProvenance(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{
		"a_def.go": "def:Widget\n",
		"b_use.go": "def:UseB\nref:a_def.go:Widget\nWidget()\n",
	})
	if _, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Widget", "a_def.go"),
		OldName:      "Widget",
		NewName:      "Gadget",
	}); err != nil {
		t.Fatalf("ApplyRefactor: %v", err)
	}
	edges, _ := h.store.Edges(ctx, graphstore.Query{})
	if len(edges) == 0 {
		t.Fatalf("expected at least one repointed edge after rename")
	}
	newID := h.symbolID("Gadget", "a_def.go")
	var found bool
	for _, e := range edges {
		if string(e.To()) == newID {
			found = true
			if !e.Tier().Valid() {
				t.Fatalf("edge provenance tier invalid: %q", e.Tier())
			}
			if e.Confidence() <= 0 || e.Confidence() > 1 {
				t.Fatalf("edge confidence out of range: %v", e.Confidence())
			}
			if strings.TrimSpace(e.Reason()) == "" || len(e.Evidence()) == 0 {
				t.Fatalf("edge missing parse provenance reason/evidence")
			}
		}
	}
	if !found {
		t.Fatalf("no edge repointed to the renamed node pkg/Gadget")
	}
}

// --- AC-2: <=2s freshness budget over the incremental window ----------------
//
// Measures ONLY the ApplyRefactor incremental window. The full-IngestAll
// byte-identical verifier (the AC-1 correctness invariant) is asserted
// separately and is NOT included in the measured budget (refinement decision 5).
func TestApplyRefactor_FreshnessWithinBudget(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"a_def.go": "def:Hot\n"}
	// A representative multi-file fixture: many dependents referencing one symbol.
	for i := 0; i < 25; i++ {
		name := "dep_" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".go"
		files[name] = "def:Dep" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "\nref:a_def.go:Hot\nHot()\n"
	}
	h := newRefactorHarness(t, files)

	start := time.Now()
	res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: h.symbolID("Hot", "a_def.go"),
		OldName:      "Hot",
		NewName:      "Warm",
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ApplyRefactor: %v", err)
	}
	if res.Outcome != edit.OutcomeApplied {
		t.Fatalf("outcome = %q", res.Outcome)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("freshness budget exceeded: %v > 2s", elapsed)
	}
	// Correctness (excluded from the budget): byte-identical to full re-index.
	if h.liveDigest(t) != h.fullReindexDigest(t) {
		t.Fatalf("freshness fixture not byte-identical to full re-index")
	}
}

// --- validation -------------------------------------------------------------

func TestApplyRefactor_RejectsInvalidRequests(t *testing.T) {
	ctx := context.Background()
	h := newRefactorHarness(t, map[string]string{"a_def.go": "def:Widget\n"})
	cases := []struct {
		name string
		op   edit.RefactorOp
	}{
		{"empty_target", edit.RefactorOp{Kind: edit.RefactorRename, OldName: "Widget", NewName: "Gadget"}},
		{"unknown_kind", edit.RefactorOp{Kind: "frobnicate", TargetSymbol: "x", OldName: "a", NewName: "b"}},
		{"missing_new_name", edit.RefactorOp{Kind: edit.RefactorRename, TargetSymbol: "x", OldName: "Widget"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := h.applier.ApplyRefactor(ctx, c.op)
			if !errors.Is(err, edit.ErrInvalidOp) {
				t.Fatalf("err = %v, want ErrInvalidOp", err)
			}
			if res.Outcome != edit.OutcomeRolledBack {
				t.Fatalf("outcome = %q", res.Outcome)
			}
		})
	}
}

// SW-037 AC-1: an extract/move refactor applied through the existing incremental
// route records the saga-minted (edit_id, op_type, timestamp) in the
// edit_provenance side-channel for the affected nodes AND edges, with a single
// timestamp shared across the touched set, and surfaces edit_id on the Result.
func TestApplyRefactor_RecordsEditProvenance(t *testing.T) {
	cases := []struct {
		name   string
		kind   edit.RefactorKind
		opType ingest.EditOpType
	}{
		{"move", edit.RefactorMove, ingest.EditOpMove},
		{"extract", edit.RefactorExtract, ingest.EditOpExtract},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			h := newRefactorHarness(t, map[string]string{
				"a_def.go": "def:Sym\n",
				"b_use.go": "def:UseB\nref:a_def.go:Sym\nSym()\n",
			})
			fixed := time.Unix(1700001234, 0).UTC()
			h.applier.SetClock(func() time.Time { return fixed })

			res, err := h.applier.ApplyRefactor(ctx, edit.RefactorOp{
				Kind:         c.kind,
				TargetSymbol: h.symbolID("Sym", "a_def.go"),
				OldName:      "Sym",
				NewName:      "Sym2",
			})
			if err != nil {
				t.Fatalf("ApplyRefactor: %v", err)
			}
			if res.Outcome != edit.OutcomeApplied {
				t.Fatalf("outcome = %q", res.Outcome)
			}
			if res.EditID == "" {
				t.Fatal("expected surfaced edit id on RefactorResult")
			}
			// Byte-identical invariant still holds (side-channel excluded).
			if h.liveDigest(t) != h.fullReindexDigest(t) {
				t.Fatalf("%s not byte-identical to full re-index", c.name)
			}

			recs, err := h.ing.EditProvenance(ctx)
			if err != nil {
				t.Fatalf("EditProvenance: %v", err)
			}
			if len(recs) == 0 {
				t.Fatal("expected provenance rows after refactor")
			}
			var sawNode, sawEdge bool
			for _, r := range recs {
				if r.EditID != res.EditID {
					t.Fatalf("provenance edit id %q != surfaced %q", r.EditID, res.EditID)
				}
				if r.OpType != c.opType {
					t.Fatalf("op_type = %q, want %q", r.OpType, c.opType)
				}
				if r.RecordedAt != fixed.UnixNano() {
					t.Fatalf("recorded_at = %d, want %d (single pinned timestamp)", r.RecordedAt, fixed.UnixNano())
				}
				switch r.ElementKind {
				case "node":
					sawNode = true
				case "edge":
					sawEdge = true
				}
			}
			if !sawNode {
				t.Error("expected at least one node provenance row")
			}
			if !sawEdge {
				t.Error("expected at least one edge provenance row (the re-derived reference edge)")
			}
		})
	}
}
