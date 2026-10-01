package static_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/embed/static"
)

func TestStatic_PinRotationGovernance(t *testing.T) {
	const (
		wantModel              = "potion-code-16M-v2"
		wantRevision           = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"
		wantModelSHA           = "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c"
		wantTokenizerSHA       = "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45"
		wantCrossArchVectorSHA = "cc40f422aff6cf1cce6963e391149be0d6f21fcdd592d297469e06fd9f3c0434"
	)
	if static.PinnedModel != wantModel || static.PinnedRevision != wantRevision {
		t.Fatalf("static source pin = %s@%s, want %s@%s", static.PinnedModel, static.PinnedRevision, wantModel, wantRevision)
	}
	if got := static.PinnedSHA256[static.FileSafetensors]; got != wantModelSHA {
		t.Fatalf("model sha256 = %s, want %s", got, wantModelSHA)
	}
	if got := static.PinnedSHA256[static.FileTokenizer]; got != wantTokenizerSHA {
		t.Fatalf("tokenizer sha256 = %s, want %s", got, wantTokenizerSHA)
	}

	recordPath := filepath.Join("..", "..", "..", "docs", "eval", "static-embedder-cross-arch", "2026-09-03-sw271", "run.json")
	raw, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("read cross-architecture record: %v", err)
	}
	var record struct {
		Model struct {
			Selector       string            `json:"selector"`
			ArtifactSHA256 map[string]string `json:"artifact_sha256"`
		} `json:"model"`
		Executions []struct {
			CGOEnabled string `json:"cgo_enabled"`
		} `json:"executions"`
		Result struct {
			Status                 string `json:"status"`
			CanonicalVectorsSHA256 string `json:"canonical_vectors_sha256"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("decode cross-architecture record: %v", err)
	}
	if record.Model.Selector != static.PinnedSelector {
		t.Fatalf("cross-architecture selector = %q, want %q", record.Model.Selector, static.PinnedSelector)
	}
	if record.Model.ArtifactSHA256[static.FileSafetensors] != wantModelSHA || record.Model.ArtifactSHA256[static.FileTokenizer] != wantTokenizerSHA {
		t.Fatalf("cross-architecture artifact pins = %v", record.Model.ArtifactSHA256)
	}
	if len(record.Executions) < 2 {
		t.Fatalf("cross-architecture record has %d executions, want at least 2", len(record.Executions))
	}
	for i, execution := range record.Executions {
		if execution.CGOEnabled != "0" {
			t.Errorf("execution %d has cgo_enabled=%q, want 0", i, execution.CGOEnabled)
		}
	}
	if record.Result.Status != "byte_exact" || record.Result.CanonicalVectorsSHA256 != wantCrossArchVectorSHA {
		t.Fatalf("cross-architecture result = %+v", record.Result)
	}
}
