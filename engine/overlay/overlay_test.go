package overlay

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// defParser is a tiny deterministic stub: each `def:NAME` line defines a node.
type defParser struct{}

func (defParser) Parse(_ context.Context, path string, src []byte) (*parse.ParseResult, error) {
	var nodes []model.Node
	for i, raw := range bytes.Split(src, []byte("\n")) {
		s := strings.TrimSpace(string(raw))
		if !strings.HasPrefix(s, "def:") {
			continue
		}
		n, err := model.NewNode("function", strings.TrimPrefix(s, "def:"), path, i+1, 1)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, n)
	}
	return &parse.ParseResult{Meta: parse.SourceMeta{Path: path, Language: "stub", Size: len(src)}, Nodes: nodes}, nil
}

// fullReindex is the byte-identical oracle: parse buf from scratch and marshal.
func fullReindex(t *testing.T, p Parser, path string, buf []byte) []byte {
	t.Helper()
	res, err := p.Parse(context.Background(), path, buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b, err := model.NewGraph(res.Nodes, res.Edges).Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestRegister_FragmentMatchesFullReindex(t *testing.T) {
	ctx := context.Background()
	s := NewSet(defParser{})
	content := []byte("def:Foo\ndef:Bar\n")
	id, err := s.Register("a.go", content)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	frag, err := s.Fragment(ctx, id)
	if err != nil {
		t.Fatalf("Fragment: %v", err)
	}
	if want := fullReindex(t, defParser{}, "a.go", content); !bytes.Equal(frag, want) {
		t.Fatalf("overlay fragment != full re-index:\n got=%s\n want=%s", frag, want)
	}
}

func TestPush_ReDerivesAndMatchesFullReindex(t *testing.T) {
	ctx := context.Background()
	s := NewSet(defParser{})
	id, _ := s.Register("a.go", []byte("def:Foo\n"))
	// Replace "Foo" (bytes 4..7) with "Bar".
	frag, err := s.Push(ctx, id, []Edit{{Start: 4, End: 7, Replacement: "Bar"}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	buf, _ := s.Buffer(id)
	if string(buf) != "def:Bar\n" {
		t.Fatalf("buffer after push = %q, want def:Bar\\n", buf)
	}
	if want := fullReindex(t, defParser{}, "a.go", buf); !bytes.Equal(frag, want) {
		t.Fatalf("pushed fragment != full re-index of buffer:\n got=%s\n want=%s", frag, want)
	}
	// And it actually reflects Bar, not Foo.
	if !bytes.Contains(frag, []byte("Bar")) || bytes.Contains(frag, []byte("Foo")) {
		t.Fatalf("fragment did not re-derive to Bar: %s", frag)
	}
}

func TestPush_Deterministic(t *testing.T) {
	ctx := context.Background()
	mk := func() []byte {
		s := NewSet(defParser{})
		id, _ := s.Register("a.go", []byte("def:Foo\n"))
		b, err := s.Push(ctx, id, []Edit{{Start: 4, End: 7, Replacement: "Baz"}})
		if err != nil {
			t.Fatalf("Push: %v", err)
		}
		return b
	}
	if a, b := mk(), mk(); !bytes.Equal(a, b) {
		t.Fatalf("push not deterministic:\n a=%s\n b=%s", a, b)
	}
}

func TestFork_Isolation(t *testing.T) {
	ctx := context.Background()
	s := NewSet(defParser{})
	parent, _ := s.Register("a.go", []byte("def:Foo\n"))
	parentBefore, _ := s.Fragment(ctx, parent)

	child, err := s.Fork(parent)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if child == parent {
		t.Fatal("fork must yield a distinct ID")
	}
	// Push to the child only.
	if _, err := s.Push(ctx, child, []Edit{{Start: 4, End: 7, Replacement: "Bar"}}); err != nil {
		t.Fatalf("Push child: %v", err)
	}
	// Parent is unaffected.
	parentAfter, _ := s.Fragment(ctx, parent)
	if !bytes.Equal(parentBefore, parentAfter) {
		t.Fatalf("parent fragment changed after pushing to fork:\n before=%s\n after=%s", parentBefore, parentAfter)
	}
	childFrag, _ := s.Fragment(ctx, child)
	if bytes.Equal(childFrag, parentAfter) {
		t.Fatal("child fragment should differ from parent after divergent push")
	}
}

func TestSwitch_RestoresPriorView(t *testing.T) {
	ctx := context.Background()
	s := NewSet(defParser{})
	a, _ := s.Register("a.go", []byte("def:Aaa\n"))
	b, _ := s.Register("b.go", []byte("def:Bbb\n"))

	if err := s.Switch(a); err != nil {
		t.Fatalf("Switch a: %v", err)
	}
	fragA1, _ := s.ActiveFragment(ctx)
	if err := s.Switch(b); err != nil {
		t.Fatalf("Switch b: %v", err)
	}
	if err := s.Switch(a); err != nil {
		t.Fatalf("Switch back to a: %v", err)
	}
	fragA2, _ := s.ActiveFragment(ctx)
	if !bytes.Equal(fragA1, fragA2) {
		t.Fatalf("switching back to a did not restore its view:\n %s\n %s", fragA1, fragA2)
	}
}

func TestMerge_FoldsChildIntoParent(t *testing.T) {
	ctx := context.Background()
	s := NewSet(defParser{})
	parent, _ := s.Register("a.go", []byte("def:Foo\n"))
	child, _ := s.Fork(parent)
	childFrag, _ := s.Push(ctx, child, []Edit{{Start: 4, End: 7, Replacement: "Bar"}})

	got, err := s.Merge(child)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got != parent {
		t.Fatalf("merge returned %q, want parent %q", got, parent)
	}
	// Parent now derives the child's fragment exactly; child is gone.
	parentFrag, _ := s.Fragment(ctx, parent)
	if !bytes.Equal(parentFrag, childFrag) {
		t.Fatalf("merged parent fragment != child fragment:\n parent=%s\n child=%s", parentFrag, childFrag)
	}
	if _, ok := s.Buffer(child); ok {
		t.Fatal("child overlay should be removed after merge")
	}
}

func TestRegister_StableID(t *testing.T) {
	s := NewSet(defParser{})
	id1, _ := s.Register("a.go", []byte("def:Foo\n"))
	id2, _ := s.Register("a.go", []byte("def:Foo\n"))
	if id1 != id2 {
		t.Fatalf("same path+content must yield same ID: %q vs %q", id1, id2)
	}
	id3, _ := s.Register("a.go", []byte("def:Other\n"))
	if id3 == id1 {
		t.Fatal("different content must yield a different ID")
	}
}

func TestPush_RejectsOverlapAndInvalidSpan(t *testing.T) {
	ctx := context.Background()
	s := NewSet(defParser{})
	id, _ := s.Register("a.go", []byte("def:Foo\n"))
	if _, err := s.Push(ctx, id, []Edit{{Start: 0, End: 100, Replacement: "x"}}); err == nil {
		t.Fatal("expected error for out-of-range span")
	}
	if _, err := s.Push(ctx, id, []Edit{{Start: 0, End: 5, Replacement: "x"}, {Start: 3, End: 7, Replacement: "y"}}); err == nil {
		t.Fatal("expected error for overlapping edits")
	}
}
