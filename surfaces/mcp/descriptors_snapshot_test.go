package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ARCH-P0 (architecture Phase 0, contract freeze). TestCharacterization_ToolNames_Snapshot
// already pins the advertised tool NAMES. It does not pin their input schemas,
// descriptions, or annotations — so a schema field could be renamed, a required
// argument added, or a read-only annotation dropped without any gate noticing.
// Those are wire-contract facts for every MCP consumer, and the P2 architecture
// refactor must preserve them byte-for-byte while use-case logic moves into the
// application layer.
//
// This snapshot closes that gap: it freezes the full descriptor documents for
// both profiles (Stable and the Labs/maximal catalog) as canonical JSON.
//
// The descriptors are captured BEFORE filterSupportedToolDescriptors runs, so the
// baseline is a pure function of the code — profile membership and schema shape
// only, never a particular client's wiring. That keeps it deterministic and makes
// a diff here unambiguously a contract change rather than a composition change.
//
// Regeneration is deliberately NOT automatic: on a mismatch the test writes the
// observed document to <baseline>.actual and fails. Review that diff, and only if
// the change is an intended, approved contract change replace the baseline with
// it. Never regenerate a baseline merely to turn a failing test green.
const descriptorBaselineRelPath = "docs/rc/mcp-tool-descriptors.baseline.json"

// descriptorSnapshotSchemaVersion versions the artifact envelope itself, so a
// future change to what we capture is distinguishable from a contract drift.
const descriptorSnapshotSchemaVersion = 1

// snapshotModuleRoot walks up from the test working directory to the module root
// (the directory holding go.mod). Hermetic: no go toolchain needed at runtime.
func snapshotModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}

// encodeDescriptorSnapshot renders the snapshot envelope as canonical JSON:
// HTML escaping off and stable two-space indentation, so the artifact stays
// reviewable as a diff. encoding/json sorts map keys, and every slice order is
// code-defined, so the bytes are a deterministic function of the descriptors.
func encodeDescriptorSnapshot(t *testing.T) []byte {
	t.Helper()
	envelope := map[string]any{
		"schema_version": descriptorSnapshotSchemaVersion,
		"stable_names":   StableMCPToolNames(),
		"stable":         stableToolDescriptors(),
		"maximal":        maximalToolDescriptors(),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(envelope); err != nil {
		t.Fatalf("encode descriptor snapshot: %v", err)
	}
	return buf.Bytes()
}

// TestCharacterization_ToolDescriptors_Deterministic is the determinism half of
// the acceptance criterion: serializing the catalog repeatedly must produce
// identical bytes. If map iteration order, a clock, or a pointer address ever
// leaked into a descriptor, the snapshot below would be worthless as a gate.
func TestCharacterization_ToolDescriptors_Deterministic(t *testing.T) {
	first := encodeDescriptorSnapshot(t)
	for i := 0; i < 8; i++ {
		if got := encodeDescriptorSnapshot(t); !bytes.Equal(first, got) {
			t.Fatalf("tool descriptor serialization is not deterministic (iteration %d differs)", i+1)
		}
	}
}

// TestCharacterization_ToolDescriptors_Snapshot pins the descriptor documents
// against the checked-in baseline.
func TestCharacterization_ToolDescriptors_Snapshot(t *testing.T) {
	baselinePath := filepath.Join(snapshotModuleRoot(t), filepath.FromSlash(descriptorBaselineRelPath))
	got := encodeDescriptorSnapshot(t)

	want, err := os.ReadFile(baselinePath)
	if err != nil {
		writeActual(t, baselinePath, got)
		t.Fatalf("read descriptor baseline %s: %v (observed document written to %s.actual)", descriptorBaselineRelPath, err, descriptorBaselineRelPath)
	}
	if !bytes.Equal(got, want) {
		writeActual(t, baselinePath, got)
		t.Fatalf("MCP tool descriptor contract drifted from %s.\n"+
			"The observed document was written to %s.actual — diff it against the baseline.\n"+
			"A diff here is an MCP wire-contract change (schema, description, annotation, or profile membership).\n"+
			"Only replace the baseline when that change is intended and approved; never to make this test pass.",
			descriptorBaselineRelPath, descriptorBaselineRelPath)
	}
}

// writeActual records the observed document beside the baseline. It never
// touches the baseline itself: a human moves the file after reviewing the diff.
func writeActual(t *testing.T, baselinePath string, got []byte) {
	t.Helper()
	if err := os.WriteFile(baselinePath+".actual", got, 0o644); err != nil {
		t.Logf("could not write observed descriptor document: %v", err)
	}
}

// TestCharacterization_ToolDescriptors_CoverAdvertisedNames binds this snapshot
// to the existing name snapshot: the maximal catalog must describe exactly the
// tools ToolNames() advertises. Without this, a tool could be dropped from the
// descriptor builder while its name constant lived on (or vice versa) and each
// snapshot alone would still look self-consistent.
func TestCharacterization_ToolDescriptors_CoverAdvertisedNames(t *testing.T) {
	described := map[string]bool{}
	for _, d := range maximalToolDescriptors() {
		name, ok := d["name"].(string)
		if !ok || name == "" {
			t.Fatalf("descriptor without a usable name: %#v", d)
		}
		if described[name] {
			t.Errorf("tool %q described twice in the maximal catalog", name)
		}
		described[name] = true
	}
	for _, name := range ToolNames() {
		if !described[name] {
			t.Errorf("advertised tool %q has no descriptor in the maximal catalog", name)
		}
		delete(described, name)
	}
	for name := range described {
		t.Errorf("descriptor for %q is not in the advertised ToolNames() set", name)
	}
}

// TestCharacterization_StableDescriptors_MatchStableNames pins the profile
// boundary itself: the default (non-Labs) catalog must describe exactly
// StableMCPToolNames() — the frozen stable set minus lifecycle-only index.
// This is the fact the P2 refactor must not disturb when capability reporting
// moves behind application services.
func TestCharacterization_StableDescriptors_MatchStableNames(t *testing.T) {
	got := map[string]bool{}
	for _, d := range stableToolDescriptors() {
		name, _ := d["name"].(string)
		got[name] = true
	}
	want := map[string]bool{}
	for _, name := range StableMCPToolNames() {
		want[name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("stable tool %q missing from the default profile catalog", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("default profile catalog advertises non-stable tool %q", name)
		}
	}
}
