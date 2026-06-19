package model

import (
	"errors"
	"testing"
)

const goldenEdgeID = EdgeId("014f8779f702ae6d")

func goldenEdge(t *testing.T) Edge {
	t.Helper()
	from := goldenNodeID
	to := NodeId("47e9cbdd6d11b69e")
	e, err := NewEdge(from, to, "calls", TierDerived, 0.9, "resolved symbol", []string{"b.go:2", "a.go:1"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNewEdge_DeterministicID(t *testing.T) {
	e := goldenEdge(t)
	if e.ID() != goldenEdgeID {
		t.Fatalf("EdgeId = %q, want golden %q", e.ID(), goldenEdgeID)
	}
	if !hexID16.MatchString(string(e.ID())) {
		t.Errorf("EdgeId %q is not fixed-width 16-char hex", e.ID())
	}
}

// TestNewEdge_ProvenanceMandatory: every incomplete-provenance case fails at
// construction so an under-provenanced Edge is unrepresentable.
func TestNewEdge_ProvenanceMandatory(t *testing.T) {
	from, to := NodeId("aaaa000000000000"), NodeId("bbbb000000000000")
	cases := []struct {
		name       string
		tier       ConfidenceTier
		confidence float64
		reason     string
		evidence   []string
	}{
		{"empty tier", ConfidenceTier(""), 0.5, "r", []string{"e"}},
		{"unknown tier", ConfidenceTier("HIGH"), 0.5, "r", []string{"e"}},
		{"confidence below range", TierDerived, -0.1, "r", []string{"e"}},
		{"confidence above range", TierDerived, 1.1, "r", []string{"e"}},
		{"empty reason", TierDerived, 0.5, "", []string{"e"}},
		{"whitespace reason", TierDerived, 0.5, "   ", []string{"e"}},
		{"nil evidence", TierDerived, 0.5, "r", nil},
		{"empty evidence slice", TierDerived, 0.5, "r", []string{}},
		{"all-empty evidence", TierDerived, 0.5, "r", []string{"", "  "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewEdge(from, to, "calls", c.tier, c.confidence, c.reason, c.evidence)
			if !errors.Is(err, ErrInvalidEdge) {
				t.Fatalf("expected ErrInvalidEdge for %s, got %v", c.name, err)
			}
		})
	}
}

func TestNewEdge_IdentityValidation(t *testing.T) {
	cases := []struct {
		name           string
		from, to, kind string
	}{
		{"empty from", "", "b", "calls"},
		{"empty to", "a", "", "calls"},
		{"empty kind", "a", "b", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewEdge(NodeId(c.from), NodeId(c.to), c.kind, TierConfirmed, 1, "r", []string{"e"})
			if !errors.Is(err, ErrInvalidEdge) {
				t.Fatalf("expected ErrInvalidEdge, got %v", err)
			}
		})
	}
}

func TestConfidenceTier_ClosedEnum(t *testing.T) {
	valid := []ConfidenceTier{TierHeuristic, TierDerived, TierConfirmed}
	for _, tr := range valid {
		if !tr.Valid() {
			t.Errorf("expected %q to be valid", tr)
		}
	}
	invalid := []ConfidenceTier{"", "HIGH", "MEDIUM", "LOW", "Heuristic", "DERIVED"}
	for _, tr := range invalid {
		if tr.Valid() {
			t.Errorf("expected %q to be invalid (closed enum)", tr)
		}
	}
}

// TestNewEdge_EvidenceCanonicallySorted: evidence is sorted regardless of input
// order, so equal provenance produces identical bytes.
func TestNewEdge_EvidenceCanonicallySorted(t *testing.T) {
	from, to := NodeId("aaaa000000000000"), NodeId("bbbb000000000000")
	e1, _ := NewEdge(from, to, "calls", TierDerived, 0.5, "r", []string{"c", "a", "b"})
	e2, _ := NewEdge(from, to, "calls", TierDerived, 0.5, "r", []string{"b", "c", "a"})
	ev1, ev2 := e1.Evidence(), e2.Evidence()
	for i := range ev1 {
		if ev1[i] != ev2[i] {
			t.Fatalf("evidence not order-independent: %v vs %v", ev1, ev2)
		}
	}
	want := []string{"a", "b", "c"}
	for i := range want {
		if ev1[i] != want[i] {
			t.Fatalf("evidence not sorted: got %v want %v", ev1, want)
		}
	}
}

// TestEdge_EvidenceDefensiveCopy ensures the accessor cannot mutate internal state.
func TestEdge_EvidenceDefensiveCopy(t *testing.T) {
	e := goldenEdge(t)
	ev := e.Evidence()
	ev[0] = "TAMPERED"
	if e.Evidence()[0] == "TAMPERED" {
		t.Fatal("Evidence() accessor leaked mutable internal slice")
	}
}
