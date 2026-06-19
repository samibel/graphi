package model

import (
	"bytes"
	"math/rand"
	"testing"
)

// goldenGraphJSON is the frozen canonical serialization of the golden 1-node /
// 1-edge graph. Any drift in field ordering, hashing, sorting, or encoding fails
// CI.
const goldenGraphJSON = `{"format_version":1,"identity_schema_version":1,"nodes":[{"id":"72cec54718324ff9","kind":"function","qualified_name":"pkg/foo.Bar","source_path":"Users/x/repo/pkg/foo.go","line":10,"column":4}],"edges":[{"id":"014f8779f702ae6d","from":"72cec54718324ff9","to":"47e9cbdd6d11b69e","kind":"calls","confidence_tier":"derived","confidence":0.9,"reason":"resolved symbol","evidence":["a.go:1","b.go:2"]}]}`

func buildGoldenGraph(t *testing.T) Graph {
	t.Helper()
	n, err := NewNode(goldenNodeKind, goldenNodeQName, goldenNodePath, 10, 4)
	if err != nil {
		t.Fatal(err)
	}
	to, _ := NewNode("function", "pkg/foo.Baz", "pkg/foo.go", 1, 1)
	if to.ID() != "47e9cbdd6d11b69e" {
		t.Fatalf("golden to-node id drifted: %q", to.ID())
	}
	e, err := NewEdge(n.ID(), to.ID(), "calls", TierDerived, 0.9, "resolved symbol", []string{"b.go:2", "a.go:1"})
	if err != nil {
		t.Fatal(err)
	}
	// Only the source node is included to match the frozen golden JSON.
	return NewGraph([]Node{n}, []Edge{e})
}

func TestMarshal_GoldenVector(t *testing.T) {
	g := buildGoldenGraph(t)
	got, err := g.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != goldenGraphJSON {
		t.Fatalf("marshal drift:\n got: %s\nwant: %s", got, goldenGraphJSON)
	}
}

// TestMarshal_InsertionOrderInvariant: shuffling node/edge insertion order
// produces byte-for-byte identical serialization (property-style).
func TestMarshal_InsertionOrderInvariant(t *testing.T) {
	var nodes []Node
	for i := 0; i < 25; i++ {
		n, _ := NewNode("function", randName(i), "pkg/f.go", i, 0)
		nodes = append(nodes, n)
	}
	var edges []Edge
	for i := 0; i < 24; i++ {
		e, _ := NewEdge(nodes[i].ID(), nodes[i+1].ID(), "calls", TierConfirmed, 1.0,
			"r", []string{randName(i + 100), randName(i)})
		edges = append(edges, e)
	}

	ref := NewGraph(nodes, edges)
	want, err := ref.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		ns := append([]Node(nil), nodes...)
		es := append([]Edge(nil), edges...)
		rng.Shuffle(len(ns), func(i, j int) { ns[i], ns[j] = ns[j], ns[i] })
		rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
		got, err := NewGraph(ns, es).Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("trial %d: serialization not insertion-order-invariant", trial)
		}
	}
}

// TestMarshal_MultiRunDeterministic: repeated marshaling of the same graph
// yields identical bytes.
func TestMarshal_MultiRunDeterministic(t *testing.T) {
	g := buildGoldenGraph(t)
	first, _ := g.Marshal()
	for i := 0; i < 100; i++ {
		got, _ := g.Marshal()
		if !bytes.Equal(got, first) {
			t.Fatalf("run %d differs from first run", i)
		}
	}
}

// TestRoundTrip ensures Marshal -> Unmarshal -> Marshal is lossless and stable,
// including ConfidenceTier and numeric Confidence.
func TestRoundTrip(t *testing.T) {
	g := buildGoldenGraph(t)
	b1, _ := g.Marshal()
	g2, err := Unmarshal(b1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := g2.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("round-trip not byte-stable:\n%s\n%s", b1, b2)
	}
	// Spot-check provenance survived.
	e := g2.Edges()[0]
	if e.Tier() != TierDerived || e.Confidence() != 0.9 {
		t.Fatalf("provenance lost on round-trip: tier=%q conf=%v", e.Tier(), e.Confidence())
	}
}

// TestUnmarshal_RejectsUnknownTier: a tampered/unknown confidence_tier is
// rejected on deserialization.
func TestUnmarshal_RejectsUnknownTier(t *testing.T) {
	tampered := `{"format_version":1,"identity_schema_version":1,"nodes":[],"edges":[{"id":"x","from":"a","to":"b","kind":"calls","confidence_tier":"HIGH","confidence":0.5,"reason":"r","evidence":["e"]}]}`
	if _, err := Unmarshal([]byte(tampered)); err == nil {
		t.Fatal("expected unmarshal to reject unknown confidence_tier")
	}
}

func TestUnmarshal_RejectsBadFormatVersion(t *testing.T) {
	bad := `{"format_version":99,"identity_schema_version":1,"nodes":[],"edges":[]}`
	if _, err := Unmarshal([]byte(bad)); err == nil {
		t.Fatal("expected rejection of unsupported format_version")
	}
}

func TestValidate_DanglingEdge(t *testing.T) {
	n, _ := NewNode("function", "a", "f.go", 1, 1)
	e, _ := NewEdge(n.ID(), NodeId("deadbeefdeadbeef"), "calls", TierDerived, 0.5, "r", []string{"e"})
	g := NewGraph([]Node{n}, []Edge{e})
	if err := g.Validate(); err == nil {
		t.Fatal("expected Validate to flag dangling edge endpoint")
	}
}

func randName(i int) string {
	const letters = "abcdefghijklmnop"
	return "sym_" + string(letters[i%len(letters)]) + "_" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
