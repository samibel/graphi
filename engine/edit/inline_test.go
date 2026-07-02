package edit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
)

// --- pure helper unit tests -------------------------------------------------

func TestExtractSingleLineValue(t *testing.T) {
	cases := []struct {
		line, name, want string
		ok               bool
	}{
		{"const Foo = 42\n", "Foo", "42", true},
		{"var X = bar()\n", "X", "bar()", true},
		{"y := compute(a, b) // note\n", "y", "compute(a, b)", true},
		{"type Alias = Underlying\n", "Alias", "Underlying", true},
		{"const Foo = 7;\n", "Foo", "7", true},
		{"func Foo() {\n", "Foo", "", false},    // brace → unsupported
		{"if Foo == Bar {\n", "Foo", "", false}, // brace + equality
		{"a == b\n", "a", "", false},            // equality, no assignment
		{"const Other = 5\n", "Foo", "", false}, // lhs lacks name
		{"return\n", "Foo", "", false},          // no assignment
	}
	for _, c := range cases {
		got, ok := extractSingleLineValue([]byte(c.line), c.name)
		if ok != c.ok || got != c.want {
			t.Errorf("extractSingleLineValue(%q,%q) = (%q,%v), want (%q,%v)", c.line, c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestNeedsParens(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"42", false},
		{"1.5", false},
		{"Foo", false},
		{"pkg.Foo", false},
		{`"hello"`, false},
		{"`raw`", false},
		{"a + b", true},     // binary expression: `x * a + b` ≠ `x * (a + b)`
		{"-1", true},        // unary minus: `x - -1` needs the wrap to stay readable/safe
		{"bar()", true},     // call
		{`"a" + "b"`, true}, // quote closes early — compound, not one literal
		{"a<<2", true},
	}
	for _, c := range cases {
		if got := needsParens(c.value); got != c.want {
			t.Errorf("needsParens(%q) = %v, want %v", c.value, got, c.want)
		}
	}
}

func TestAssignmentIndex(t *testing.T) {
	if i := assignmentIndex("a = b"); i != 2 {
		t.Errorf("a = b: got %d, want 2", i)
	}
	for _, s := range []string{"a == b", "a != b", "a <= b", "a >= b", "a := b", "no equals"} {
		if i := assignmentIndex(s); i != -1 {
			t.Errorf("%q: got %d, want -1", s, i)
		}
	}
}

func TestLineByteSpan(t *testing.T) {
	content := []byte("l1\nl2\nl3\n")
	sp, ok := lineByteSpan(content, 2)
	if !ok || string(content[sp.Start:sp.End]) != "l2\n" {
		t.Fatalf("line 2 = %q ok=%v, want \"l2\\n\"", content[sp.Start:sp.End], ok)
	}
	if _, ok := lineByteSpan(content, 0); ok {
		t.Errorf("line 0 should be out of range")
	}
}

func TestLastSegmentAndContainsCall(t *testing.T) {
	for in, want := range map[string]string{"pkg/Foo": "Foo", "pkg.Foo": "Foo", "Foo": "Foo", "a/b.C": "C"} {
		if got := lastSegment(in); got != want {
			t.Errorf("lastSegment(%q) = %q, want %q", in, got, want)
		}
	}
	if !containsCall("bar()") || containsCall("42") {
		t.Errorf("containsCall wrong")
	}
}

// --- ApplyInline block-path + dry-run tests ---------------------------------

type stubInlineParser struct{}

func (stubInlineParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	return &parse.ParseResult{Meta: parse.SourceMeta{Path: path, Language: "stub", Size: len(src)}}, nil
}

func newInlineApplier(t *testing.T, files map[string]string) (*Applier, *graphstore.MemStore, string) {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, stubInlineParser{}, t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	a, err := NewApplier(store, ing, root, nil)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}
	return a, store, root
}

func putNode(t *testing.T, store *graphstore.MemStore, kind, qn, path string, line int) model.NodeId {
	t.Helper()
	n, err := model.NewNode(kind, qn, path, line, 1)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	if err := store.PutNode(context.Background(), n); err != nil {
		t.Fatalf("PutNode: %v", err)
	}
	return n.ID()
}

func TestApplyInline_BlockUnresolvedReference(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n", "b.go": "use Foo\n"})
	ctx := context.Background()
	foo := putNode(t, store, "const", "Foo", "a.go", 1)
	user := putNode(t, store, "function", "User", "b.go", 1)
	// Heuristic (unresolved) inbound reference to Foo.
	e, err := model.NewEdge(user, foo, "references", model.TierHeuristic, 0.4, "best-effort", []string{"b.go:1"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	if err := store.PutEdge(ctx, e); err != nil {
		t.Fatalf("PutEdge: %v", err)
	}
	res, err := a.ApplyInline(ctx, InlineOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplyInline: %v", err)
	}
	if res.Outcome != InlineBlocked || res.BlockReason != BlockUnresolvedReference {
		t.Fatalf("got outcome=%q reason=%q, want blocked/unresolved_reference", res.Outcome, res.BlockReason)
	}
}

func TestApplyInline_BlockUnsupportedShape(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "func Foo() {\n"})
	foo := putNode(t, store, "function", "Foo", "a.go", 1)
	res, err := a.ApplyInline(context.Background(), InlineOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplyInline: %v", err)
	}
	if res.Outcome != InlineBlocked || res.BlockReason != BlockUnsupportedShape {
		t.Fatalf("got outcome=%q reason=%q, want blocked/unsupported_shape", res.Outcome, res.BlockReason)
	}
}

func TestApplyInline_BlockAddressTaken(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "var Foo = 5\nx := &Foo\n"})
	foo := putNode(t, store, "var", "Foo", "a.go", 1)
	res, err := a.ApplyInline(context.Background(), InlineOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplyInline: %v", err)
	}
	if res.Outcome != InlineBlocked || res.BlockReason != BlockAddressTaken {
		t.Fatalf("got outcome=%q reason=%q, want blocked/address_taken", res.Outcome, res.BlockReason)
	}
}

func TestApplyInline_BlockSideEffectMultiEval(t *testing.T) {
	a, store, _ := newInlineApplier(t, map[string]string{"a.go": "var Foo = compute()\nprintln(Foo)\nprintln(Foo)\n"})
	foo := putNode(t, store, "var", "Foo", "a.go", 1)
	res, err := a.ApplyInline(context.Background(), InlineOp{TargetSymbol: string(foo)})
	if err != nil {
		t.Fatalf("ApplyInline: %v", err)
	}
	if res.Outcome != InlineBlocked || res.BlockReason != BlockSideEffectMultiEval {
		t.Fatalf("got outcome=%q reason=%q, want blocked/side_effecting_multi_eval", res.Outcome, res.BlockReason)
	}
	if res.ReferenceSites != 2 {
		t.Errorf("reference sites = %d, want 2", res.ReferenceSites)
	}
}

func TestApplyInline_DryRunPlansWithoutMutating(t *testing.T) {
	src := "const Foo = 42\nprintln(Foo)\n"
	a, store, root := newInlineApplier(t, map[string]string{"a.go": src})
	foo := putNode(t, store, "const", "Foo", "a.go", 1)
	res, err := a.ApplyInline(context.Background(), InlineOp{TargetSymbol: string(foo), DryRun: true})
	if err != nil {
		t.Fatalf("ApplyInline: %v", err)
	}
	if res.Outcome != InlineApplied || !res.DryRun {
		t.Fatalf("got outcome=%q dryRun=%v, want applied/true", res.Outcome, res.DryRun)
	}
	if res.ReferenceSites != 1 {
		t.Fatalf("reference sites = %d, want 1", res.ReferenceSites)
	}
	// Plan = 1 substitution + 1 declaration removal.
	if len(res.PlannedOps) != 2 {
		t.Fatalf("planned ops = %d, want 2: %+v", len(res.PlannedOps), res.PlannedOps)
	}
	// No mutation on disk.
	got, _ := os.ReadFile(filepath.Join(root, "a.go"))
	if string(got) != src {
		t.Fatalf("dry-run mutated source: %q", got)
	}
}

func TestApplyInline_Unavailable(t *testing.T) {
	a, _, _ := newInlineApplier(t, map[string]string{"a.go": "const Foo = 42\n"})
	res, err := a.ApplyInline(context.Background(), InlineOp{TargetSymbol: "0000000000000000"})
	if err != nil {
		t.Fatalf("ApplyInline: %v", err)
	}
	if res.Outcome != InlineUnavailable {
		t.Fatalf("got outcome=%q, want unavailable", res.Outcome)
	}
}
