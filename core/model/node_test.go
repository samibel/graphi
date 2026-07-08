package model

import (
	"errors"
	"strings"
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
		name              string
		kind, qname, path string
		line, col         int
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

// TestWithMeta_IdentityInvariant proves NodeMeta is a NON-identity rider: adding
// meta must not change the NodeId (byte-parity of identity), while Meta() must
// round-trip the sorted/deduped value. This is the invariant the persistence and
// snapshot layers rely on.
func TestWithMeta_IdentityInvariant(t *testing.T) {
	base, err := NewNode(goldenNodeKind, goldenNodeQName, goldenNodePath, 10, 4)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	withMeta := base.WithMeta(NewNodeMeta([]string{"Bean", "Test", "Bean"}, []string{"static", "main"}))
	if withMeta.ID() != base.ID() {
		t.Fatalf("meta changed NodeId: base=%q withMeta=%q (meta must be non-identity)", base.ID(), withMeta.ID())
	}
	if withMeta.ID() != goldenNodeID {
		t.Fatalf("NodeId drifted from golden with meta attached: %q", withMeta.ID())
	}
	// The base node is unmodified (WithMeta returns a copy).
	if !base.Meta().IsZero() {
		t.Fatalf("WithMeta mutated the receiver: base meta = %+v", base.Meta())
	}
	m := withMeta.Meta()
	if got := m.Annotations; len(got) != 2 || got[0] != "Bean" || got[1] != "Test" {
		t.Fatalf("annotations not sorted+deduped: %v", got)
	}
	if got := m.Flags; len(got) != 2 || got[0] != "main" || got[1] != "static" {
		t.Fatalf("flags not sorted+deduped: %v", got)
	}
}

// TestNodeMeta_SnapshotRoundTrip ensures a node's meta survives Marshal→Unmarshal
// (the snapshot byte path) and that an empty-meta node encodes with NO meta key,
// preserving byte-parity with the pre-meta format.
func TestNodeMeta_SnapshotRoundTrip(t *testing.T) {
	plain, _ := NewNode("function", "pkg.Plain", "pkg/p.go", 1, 1)
	annotated, _ := NewNode("method", "pkg.Bean", "pkg/b.go", 2, 1)
	annotated = annotated.WithMeta(NewNodeMeta([]string{"Bean"}, []string{"static"}))

	g := NewGraph([]Node{plain, annotated}, nil)
	data, err := g.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	// Empty-meta node must not emit a "meta" key for the plain node; only the
	// annotated node carries one.
	if got := strings.Count(string(data), `"meta"`); got != 1 {
		t.Fatalf("expected exactly 1 meta key in snapshot, got %d: %s", got, data)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	byID := map[NodeId]Node{}
	for _, n := range back.Nodes() {
		byID[n.ID()] = n
	}
	if !byID[plain.ID()].Meta().IsZero() {
		t.Fatalf("plain node gained meta on round-trip: %+v", byID[plain.ID()].Meta())
	}
	rm := byID[annotated.ID()].Meta()
	if len(rm.Annotations) != 1 || rm.Annotations[0] != "Bean" || len(rm.Flags) != 1 || rm.Flags[0] != "static" {
		t.Fatalf("annotated meta did not round-trip: %+v", rm)
	}
}
