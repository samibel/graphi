package contract_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

func sampleResult() *contract.Result {
	return &contract.Result{
		Outcome: contract.OutcomeOK,
		Summary: "test summary",
		Items: []contract.Item{
			{RefID: "b", Rank: 1, Reason: "second", EvidenceRefIDs: []string{"e1"}},
			{RefID: "a", Rank: 2, Reason: "first", EvidenceRefIDs: []string{"e1"}},
		},
		Evidence: []contract.Evidence{
			{RefID: "e1", Path: "p.go", Line: 10, Role: "definition"},
		},
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"ok": 1.0},
			Top:          "ok",
			Method:       "test",
		},
	}
}

func TestSerializeDeterministic(t *testing.T) {
	r := sampleResult()
	b1, err := contract.Serialize(r)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	b2, err := contract.Serialize(r)
	if err != nil {
		t.Fatalf("serialize second: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("output not deterministic:\n%s\nvs\n%s", b1, b2)
	}
	if len(b1) == 0 {
		t.Fatal("empty output")
	}
	// No trailing newline.
	if b1[len(b1)-1] == '\n' {
		t.Fatal("trailing newline in canonical output")
	}
}

func TestSerializeFieldOrder(t *testing.T) {
	r := sampleResult()
	b, err := contract.Serialize(r)
	if err != nil {
		t.Fatal(err)
	}
	// The fields are ordered as in the struct: outcome, summary, items, evidence, confidence, limits.
	expected := `{"outcome":"ok","summary":`
	if !bytes.HasPrefix(b, []byte(expected)) {
		t.Fatalf("expected prefix %s, got %s", expected, b)
	}
}

func TestSerializeRejectsInvalidOutcome(t *testing.T) {
	r := &contract.Result{Outcome: "bad"}
	if _, err := contract.Serialize(r); err == nil {
		t.Fatal("expected error for invalid outcome")
	}
}

func TestConfidenceNormalizeAndValidate(t *testing.T) {
	c := &contract.Confidence{Distribution: map[string]float64{"ok": 0.5, "empty": 0.5}}
	if err := contract.NormalizeConfidence(c); err != nil {
		t.Fatal(err)
	}
	if err := contract.ValidateConfidence(c); err != nil {
		t.Fatal(err)
	}
	if c.Top != "ok" && c.Top != "empty" {
		t.Fatalf("unexpected top label %q", c.Top)
	}
}

func TestApplyItemCap(t *testing.T) {
	r := sampleResult()
	out, err := contract.ApplyItemCap(r, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(out.Items))
	}
	if out.Items[0].RefID != "a" {
		t.Fatalf("expected top-ranked item 'a', got %s", out.Items[0].RefID)
	}
	if !out.Limits.Truncated || out.Limits.Dropped != 1 {
		t.Fatalf("unexpected limits: %+v", out.Limits)
	}
}

func TestApplyByteCap(t *testing.T) {
	r := sampleResult()
	full, err := contract.Serialize(r)
	if err != nil {
		t.Fatal(err)
	}
	// Compute a cap between the full result and the result with no items.
	noItems := *r
	noItems.Items = nil
	noItemsBytes, err := contract.Serialize(&noItems)
	if err != nil {
		t.Fatal(err)
	}
	cap := (len(full) + len(noItemsBytes)) / 2
	out, err := contract.ApplyByteCap(r, cap)
	if err != nil {
		t.Fatal(err)
	}
	b, err := contract.Serialize(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > cap {
		t.Fatalf("byte cap exceeded: %d > %d", len(b), cap)
	}
}

func TestNoHTMLEscape(t *testing.T) {
	r := &contract.Result{
		Outcome: contract.OutcomeOK,
		Summary: "a < b & c",
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"ok": 1.0},
			Top:          "ok",
			Method:       "test",
		},
	}
	b, err := contract.Serialize(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("<")) || !bytes.Contains(b, []byte("&")) {
		t.Fatalf("expected literal < and &, got %s", b)
	}
	if bytes.Contains(b, []byte(`\u003c`)) || bytes.Contains(b, []byte(`\u0026`)) {
		t.Fatalf("HTML escaping leaked into canonical output: %s", b)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	r := sampleResult()
	b, err := contract.Serialize(r)
	if err != nil {
		t.Fatal(err)
	}
	var got contract.Result
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Outcome != r.Outcome || got.Summary != r.Summary {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}
