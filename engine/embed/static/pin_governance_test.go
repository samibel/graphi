package static_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/engine/embed/static"
)

func TestStatic_PinRotationGovernance(t *testing.T) {
	const (
		wantModel              = "potion-code-16M-v2"
		wantRevision           = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"
		wantCrossArchVectorSHA = "cc40f422aff6cf1cce6963e391149be0d6f21fcdd592d297469e06fd9f3c0434"
	)
	wantArtifactSHA := map[string]string{
		static.FileConfig:      "148e5691a6fcc553437156859701fba017a1ba5d340b170f17e0f3668fb861a7",
		static.FileTokenizer:   "107bbdcbad4bff1d299b7a4c3a2fb17c52890688b7dd0e4c9deab79d3c4f3d45",
		static.FileSafetensors: "75cf7a6c2171b230ad19b1e7d8e0b1aee86da5a02af8e7cacedd9921d227623c",
		static.FileModules:     "a68dcbed0429dcdd5bfdca92b0b03cc30d09122c0a3fcf4758787d4b244e45b2",
	}
	if static.PinnedModel != wantModel || static.PinnedRevision != wantRevision {
		t.Fatalf("static source pin = %s@%s, want %s@%s", static.PinnedModel, static.PinnedRevision, wantModel, wantRevision)
	}
	if !maps.Equal(static.PinnedSHA256, wantArtifactSHA) {
		t.Fatalf("static artifact pins = %v, want %v", static.PinnedSHA256, wantArtifactSHA)
	}

	policy, err := os.ReadFile("PIN_ROTATION.md")
	if err != nil {
		t.Fatalf("read pin-rotation policy: %v", err)
	}
	for _, required := range []string{
		"Current governed revision: `" + wantRevision + "`.",
		"## Approval",
		"## Required rotation record and re-measurement",
		"all four SHA-256 values",
		"CGO_ENABLED=0",
		"byte-exact across architectures",
	} {
		if !bytes.Contains(policy, []byte(required)) {
			t.Errorf("pin-rotation policy is missing %q", required)
		}
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
	if !maps.Equal(record.Model.ArtifactSHA256, wantArtifactSHA) {
		t.Fatalf("cross-architecture artifact pins = %v, want %v", record.Model.ArtifactSHA256, wantArtifactSHA)
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
