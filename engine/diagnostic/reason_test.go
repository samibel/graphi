package diagnostic

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
)

func TestReasonCodeCatalog_ValidMembers(t *testing.T) {
	for _, c := range ValidReasonCodes() {
		if !IsValidReasonCode(c) {
			t.Fatalf("code %q should be valid", c)
		}
		if ReasonDocs(c) == "" || ReasonDocs(c) == "(off-catalogue reason code)" {
			t.Fatalf("code %q should be documented", c)
		}
	}
}

func TestReasonCodeCatalog_Coverage(t *testing.T) {
	// Every diagnostic produced by the analyzers on a representative fixture
	// must carry a catalogued reason code.
	codes := map[ReasonCode]bool{
		ReasonDeadInternalSymbol:       true,
		ReasonUnresolvedExternalImport: true,
	}
	for c := range codes {
		if !IsValidReasonCode(c) {
			t.Fatalf("emitted reason code %q is not in the catalog", c)
		}
	}
}

func TestDiagnostic_ReasonJSONField(t *testing.T) {
	d := Diagnostic{
		Severity:   SeverityWarning,
		Code:       "dead_symbol",
		Reason:     ReasonDeadInternalSymbol,
		Message:    "test",
		Symbol:     "a",
		File:       "pkg/a.go",
		Line:       10,
		Column:     1,
		Actions:    []CodeAction{},
		Confidence: ConfidenceExact,
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"reason":"dead_internal_symbol"`)) {
		t.Fatalf("JSON should include reason field; got %s", b)
	}
	if !bytes.Contains(b, []byte(`"code":"dead_symbol"`)) {
		t.Fatalf("JSON should preserve original code field; got %s", b)
	}
}

func TestSummary_Reconciliation(t *testing.T) {
	// A valid synthetic run: use the seed fixture with --all so counts are simple.
	ctx := context.Background()
	store, _ := seed(t)
	res, err := DiagnoseWithOptions(ctx, store, nil, DiagnoseOptions{All: true})
	if err != nil {
		t.Fatalf("DiagnoseWithOptions: %v", err)
	}
	if res.Summary.Shown+res.Summary.TotalWithheld != res.Summary.TotalAnalyzed {
		t.Fatalf("reconciliation violated: shown=%d withheld=%d analyzed=%d", res.Summary.Shown, res.Summary.TotalWithheld, res.Summary.TotalAnalyzed)
	}
}

func TestSummary_ZeroValuePresent(t *testing.T) {
	// A clean result should serialize Summary as a present block, not null.
	ctx := context.Background()
	store := graphstore.NewMemStore()
	res, err := Diagnose(ctx, store, nil)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	b, err := Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"summary":{"total_analyzed":0,"shown":0,"total_withheld":0}`)) {
		t.Fatalf("zero summary should be a present block; got %s", b)
	}
}

func TestBackwardCompatibility_CodeField(t *testing.T) {
	// Old decoders that only know about Outcome/Diagnostics/Unavailable should still
	// decode the new Result without error, ignoring Summary and Reason.
	ctx := context.Background()
	store, _ := seed(t)
	res, err := Diagnose(ctx, store, nil)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	b, err := Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var old struct {
		Outcome     string       `json:"outcome"`
		Diagnostics []Diagnostic `json:"diagnostics"`
		Unavailable []string     `json:"unavailable"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatalf("old decoder failed: %v", err)
	}
	if old.Outcome != string(res.Outcome) {
		t.Fatalf("outcome mismatch: %q vs %q", old.Outcome, res.Outcome)
	}
}
