package main

import (
	"os"
	"path/filepath"
	"testing"
)

// repoRoot returns the repository root (two levels up from cmd/eval).
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRunScenarios_AllCorpusScenariosPass executes every checked-in scenario
// against its fixture graph — the Tier-1 PR gate in test form. Each of the
// four EP-020 agent tools must be covered and every scenario must pass.
func TestRunScenarios_AllCorpusScenariosPass(t *testing.T) {
	root := repoRoot(t)
	manifest := filepath.Join(root, "corpus", "manifest.json")
	_, fixtures, err := loadCorpusManifest(manifest)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	results, err := runScenarios(filepath.Join(root, "corpus", "scenarios"), root, fixtures)
	if err != nil {
		t.Fatalf("run scenarios: %v", err)
	}
	covered := map[string]bool{}
	for _, r := range results {
		covered[r.Operation] = true
		if r.Outcome != "pass" {
			t.Errorf("scenario %s (%s): outcome %s, evidence %v", r.ID, r.Operation, r.Outcome, r.Evidence)
		}
	}
	for _, op := range []string{"explain_symbol", "related_files", "change_risk", "agent_brief"} {
		if !covered[op] {
			t.Errorf("no scenario covers agent tool %q", op)
		}
	}
}

func TestLoadCorpusManifest(t *testing.T) {
	root := repoRoot(t)
	version, fixtures, err := loadCorpusManifest(filepath.Join(root, "corpus", "manifest.json"))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if version < 1 {
		t.Fatalf("expected corpus version >= 1, got %d", version)
	}
	if fixtures["tier1-fixture-go"] != "corpus/fixtures/go" {
		t.Fatalf("unexpected fixture index: %v", fixtures)
	}
}

func TestLoadCorpusManifest_Missing(t *testing.T) {
	if _, _, err := loadCorpusManifest(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

func TestRunScenarios_MissingDir(t *testing.T) {
	if _, err := runScenarios(filepath.Join(t.TempDir(), "none"), t.TempDir(), map[string]string{}); err == nil {
		t.Fatal("expected error for empty scenario dir")
	}
}

func TestRunScenarios_UnknownFixtureRef(t *testing.T) {
	dir := t.TempDir()
	scenarioYAML := "id: x\nfixture_ref: nope\noperation:\n  name: search\n  args:\n    query: y\nexpect:\n  outcome: empty\n"
	if err := os.WriteFile(filepath.Join(dir, "x.yaml"), []byte(scenarioYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runScenarios(dir, t.TempDir(), map[string]string{}); err == nil {
		t.Fatal("expected error for unknown fixture_ref")
	}
}
