package retrieval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	cobraDataset = "testdata/datasets/cobra-v1.json"
	// cobraPinnedSHA is corpus/manifest.json's pin for cobra v1.8.0; the
	// dataset states the same value and TestDatasets_CobraDatasetShape
	// checks they agree.
	cobraPinnedSHA = "a0a6ae020bb3899ff0276067863e50523f897370"
)

// cobraCheckout is where a read-only pinned clone is expected locally. The
// PR path never clones (AC-10); when the clone is absent the span-coverage
// test SKIPS with a message that says how to get one.
func cobraCheckout() string {
	if p := os.Getenv("GRAPHI_CORPUS_COBRA"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "graphi", "corpus", "cobra")
}

// AC-9 over the hermetic fixture: always runs.
func TestDatasets_FixtureSpansResolve(t *testing.T) {
	ds, err := LoadDataset(fixtureDataset)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSpanCoverage(fixtureRepo, ds.Dataset); err != nil {
		t.Fatal(err)
	}
	if ds.Dataset.Repo != "fixture" || ds.Dataset.EvidenceClass != EvidenceClassAgentHumanReviewed {
		t.Errorf("fixture dataset header = %+v", ds.Dataset)
	}
	if err := ds.Dataset.CheckDevelopmentRequirements(5, 1, 1); err != nil {
		t.Errorf("the mini dataset must cover every stratum at least once with >=5 dev queries: %v", err)
	}
	for _, q := range ds.Dataset.Queries {
		for i, j := range q.Judgements {
			if j.Annotator != "claude-delegate (SW-258 build)" || j.Reviewer != "orchestrator" {
				t.Errorf("%s judgement %d: annotator/reviewer = %q/%q", q.ID, i, j.Annotator, j.Reviewer)
			}
		}
	}
}

// AC-2 shape of the 30-query development dataset: checkable without the
// clone, because it is a property of the JSON alone.
func TestDatasets_CobraDatasetShape(t *testing.T) {
	ds, err := LoadDataset(cobraDataset)
	if err != nil {
		t.Fatal(err)
	}
	d := ds.Dataset
	if d.Repo != "cobra" || d.RepoSHA != cobraPinnedSHA || d.Language != "go" {
		t.Errorf("cobra dataset header = repo %q sha %q language %q", d.Repo, d.RepoSHA, d.Language)
	}
	if d.EvidenceClass != EvidenceClassAgentHumanReviewed {
		t.Errorf("evidence_class = %q", d.EvidenceClass)
	}
	if err := d.CheckDevelopmentRequirements(30, 3, 10); err != nil {
		t.Error(err)
	}
	for _, q := range d.Queries {
		if q.Language != "en" {
			t.Errorf("%s: language %q, the validated query contract is English", q.ID, q.Language)
		}
		for i, j := range q.Judgements {
			if j.Annotator != "claude-delegate (SW-258 build)" || j.Reviewer != "orchestrator" {
				t.Errorf("%s judgement %d: annotator/reviewer = %q/%q", q.ID, i, j.Annotator, j.Reviewer)
			}
		}
	}
}

// AC-9 over the pinned public repository: runs only against a local clone at
// the pinned sha, and SKIPS — visibly — otherwise. A stale judgement fails.
func TestDatasets_CobraSpansResolveAtPinnedSHA(t *testing.T) {
	root := cobraCheckout()
	if root == "" {
		t.Skip("SKIP: no home directory to locate the cobra clone under")
	}
	if _, err := os.Stat(filepath.Join(root, "command.go")); err != nil {
		t.Skipf("SKIP: cobra clone absent at %s (set GRAPHI_CORPUS_COBRA or place a read-only clone of spf13/cobra at %s there); the span-coverage check for cobra-v1.json did not run", root, cobraPinnedSHA)
	}
	head, err := CheckoutHEAD(context.Background(), root)
	if err != nil {
		t.Skipf("SKIP: %s is not a git checkout (%v); cannot verify the pin", root, err)
	}
	if !strings.EqualFold(head, cobraPinnedSHA) {
		t.Skipf("SKIP: cobra clone at %s is at %s, not the pinned %s; judgements are only valid at the pin", root, head, cobraPinnedSHA)
	}
	ds, err := LoadDataset(cobraDataset)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSpanCoverage(root, ds.Dataset); err != nil {
		t.Fatal(err)
	}
}

// AC-10: the fixture is a real, buildable Go module. GOWORK=off keeps the
// nested module out of the workspace; the build touches nothing but stdlib.
func TestDatasets_FixtureRepoBuilds(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("SKIP: no go toolchain on PATH")
	}
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = fixtureRepo
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the fixture module does not vet: %v\n%s", err, out)
	}
}
