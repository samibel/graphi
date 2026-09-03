package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed/static"
)

func TestRecordVectors_MissingArtifactFailsRatherThanSkipping(t *testing.T) {
	inputPath := filepath.Join(t.TempDir(), "inputs.json")
	if err := os.WriteFile(inputPath, []byte("{\n  \"format_version\": 1,\n  \"inputs\": [{\"id\": \"one\", \"text\": \"same input\"}]\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := recordVectors(context.Background(), filepath.Join(t.TempDir(), "absent"), inputPath)
	if err == nil {
		t.Fatal("recordVectors succeeded without the pinned artifact; the architecture check must fail closed")
	}
	if !strings.Contains(err.Error(), "pinned artifact is required; no skip or download is permitted") {
		t.Fatalf("missing-artifact error = %q", err)
	}
}

func TestCompareFiles_ByteExact(t *testing.T) {
	record := sampleRecord()
	body, err := marshalRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	left := writeTestRecord(t, "left.json", body)
	right := writeTestRecord(t, "right.json", body)
	var stdout bytes.Buffer
	if err := compareFiles(left, right, &stdout); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.Contains(got, "byte-exact (1 inputs x 2 dimensions") {
		t.Fatalf("stdout = %q", got)
	}
}

func TestCompareFiles_ReportsInputComponentAndBitsOnDivergence(t *testing.T) {
	leftRecord := sampleRecord()
	rightRecord := sampleRecord()
	rightRecord.Vectors[0].Float32Bits[1] = "3f800001"
	leftBody, _ := marshalRecord(leftRecord)
	rightBody, _ := marshalRecord(rightRecord)
	left := writeTestRecord(t, "left.json", leftBody)
	right := writeTestRecord(t, "right.json", rightBody)
	err := compareFiles(left, right, &bytes.Buffer{})
	if err == nil {
		t.Fatal("compareFiles succeeded for divergent vector bits")
	}
	want := `static embedder cross-architecture divergence: input "same-input" ("func Embed()"), component 1: left=0x3f800000 right=0x3f800001`
	if err.Error() != want {
		t.Fatalf("divergence error:\n got %q\nwant %q", err, want)
	}
}

func TestRunSelectorUsesProductionPin(t *testing.T) {
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"selector"}, &stdout); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(stdout.String()), static.PinnedSelector; got != want {
		t.Fatalf("selector = %q, want %q", got, want)
	}
}

func TestWorkflow_UsesOneArtifactProducerAndTwoNativeCGoFreeArchitectures(t *testing.T) {
	body, err := os.ReadFile("../../.github/workflows/static-embedder-cross-arch.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(body)
	for _, required := range []string{
		`CGO_ENABLED: "0"`,
		"runner: ubuntu-24.04\n            goarch: amd64",
		"runner: ubuntu-24.04-arm\n            goarch: arm64",
		"needs: prepare-model",
		"No condition and no skip",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("workflow is missing %q", required)
		}
	}
	const fetchCommand = "go run ./cmd/graphi setup-embedder"
	if got := strings.Count(workflow, fetchCommand); got != 1 {
		t.Fatalf("workflow contains %d model fetch commands, want exactly one producer", got)
	}
}

func sampleRecord() vectorRecord {
	return vectorRecord{
		FormatVersion:  recordFormatVersion,
		Selector:       static.PinnedSelector,
		ArtifactSHA256: clonePins(static.PinnedSHA256),
		InputsSHA256:   strings.Repeat("a", 64),
		Vectors: []vector{{
			ID:          "same-input",
			Text:        "func Embed()",
			Float32Bits: []string{"00000000", "3f800000"},
		}},
	}
}

func writeTestRecord(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
