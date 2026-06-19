package model

import (
	"errors"
	"testing"
)

// Frozen golden vectors. Any drift in identity layout / hash / normalization
// will fail these and any future determinism regression fails CI.
const (
	goldenNodeKind  = "function"
	goldenNodeQName = "pkg/foo.Bar"
	goldenNodePath  = "/Users/x/repo/pkg/foo.go"
	goldenNodeID    = NodeId("72cec54718324ff9")
)

func TestNewNode_DeterministicID(t *testing.T) {
	n, err := NewNode(goldenNodeKind, goldenNodeQName, goldenNodePath, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID() != goldenNodeID {
		t.Fatalf("NodeId = %q, want golden %q", n.ID(), goldenNodeID)
	}
	if !hexID16.MatchString(string(n.ID())) {
		t.Errorf("NodeId %q is not fixed-width 16-char hex", n.ID())
	}
}

// TestNewNode_IdentityFieldsDriveID: same identity fields -> same ID even when
// non-identity attributes (line/column) and absolute-vs-relative source path
// differ; differing identity fields -> different ID.
func TestNewNode_IdentityFieldsDriveID(t *testing.T) {
	base, _ := NewNode("function", "pkg/foo.Bar", "pkg/foo.go", 1, 1)

	// Same identity, different line/column => same ID (line/col are non-identity).
	moved, _ := NewNode("function", "pkg/foo.Bar", "pkg/foo.go", 999, 7)
	if base.ID() != moved.ID() {
		t.Errorf("line/column changed the NodeId: %q != %q", base.ID(), moved.ID())
	}

	// Each identity field difference => different ID.
	diffs := []Node{}
	dKind, _ := NewNode("method", "pkg/foo.Bar", "pkg/foo.go", 1, 1)
	dName, _ := NewNode("function", "pkg/foo.Baz", "pkg/foo.go", 1, 1)
	dPath, _ := NewNode("function", "pkg/foo.Bar", "pkg/other.go", 1, 1)
	diffs = append(diffs, dKind, dName, dPath)
	for _, d := range diffs {
		if d.ID() == base.ID() {
			t.Errorf("differing identity field produced same NodeId %q", d.ID())
		}
	}
}

func TestNewNode_Validation(t *testing.T) {
	cases := []struct {
		name                              string
		kind, qname, path                 string
		line, col                         int
	}{
		{"empty kind", "", "q", "p", 1, 1},
		{"whitespace kind", "  ", "q", "p", 1, 1},
		{"empty qname", "k", "", "p", 1, 1},
		{"negative line", "k", "q", "p", -1, 1},
		{"negative col", "k", "q", "p", 1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewNode(c.kind, c.qname, c.path, c.line, c.col)
			if !errors.Is(err, ErrInvalidNode) {
				t.Fatalf("expected ErrInvalidNode, got %v", err)
			}
		})
	}
}

// TestNewNode_StableAcrossInsertionLikeReorder ensures ID derivation never
// depends on any global/order state: deriving the same node repeatedly and
// interleaved with others is stable.
func TestNewNode_StableAcrossRuns(t *testing.T) {
	want := goldenNodeID
	for i := 0; i < 1000; i++ {
		_, _ = NewNode("other", "x", "y", i, i) // interleave unrelated derivations
		n, _ := NewNode(goldenNodeKind, goldenNodeQName, goldenNodePath, i, i)
		if n.ID() != want {
			t.Fatalf("iteration %d: NodeId = %q, want %q", i, n.ID(), want)
		}
	}
}
