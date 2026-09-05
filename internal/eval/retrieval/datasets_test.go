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

	// One env var, one upstream string, one sentinel for the cobra clone, used
	// by every cobra span test. SW-279 briefly gave the v2 test its own
	// GRAPHI_COBRA_ROOT and its own upstream spelling, which meant pointing one
	// variable at a clone left the other test silently skipping.
	cobraEnvVar   = "GRAPHI_CORPUS_COBRA"
	cobraUpstream = "spf13/cobra"
	cobraSentinel = "command.go"

	// grpcGoDataset is the PERFORMANCE-ONLY dataset over the large size
	// class (AC-8); grpcGoPinnedSHA is corpus/manifest.json's pin for grpc-go
	// v1.60.1.
	// cobraV2Dataset is the SW-279 release dataset: cobra-v1's 40 queries minus
	// the withdrawn cb-05, plus questions harvested from real spf13/cobra issues
	// under a rule frozen before any issue was fetched.
	cobraV2Dataset = "testdata/datasets/cobra-v2.json"

	grpcGoDataset   = "testdata/datasets/grpc-go-perf-v1.json"
	grpcGoPinnedSHA = "dbbcf59957fec0bd58063224cbf105b3b3698d4e"
)

// pinnedCheckout locates a read-only clone of a pinned corpus repository —
// $<envVar>, else $HOME/.cache/graphi/corpus/<name> — and SKIPS, visibly,
// when it is absent, not a git checkout, or not at the pinned sha. The PR
// path never clones (AC-10).
func pinnedCheckout(t *testing.T, envVar, name, upstream, sentinel, pinned, dataset string) string {
	t.Helper()
	root := os.Getenv(envVar)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("SKIP: no home directory to locate the %s clone under", name)
		}
		root = filepath.Join(home, ".cache", "graphi", "corpus", name)
	}
	if _, err := os.Stat(filepath.Join(root, sentinel)); err != nil {
		t.Skipf("SKIP: %s clone absent at %s (set %s or place a read-only clone of %s at %s there); the span-coverage check for %s did not run", name, root, envVar, upstream, pinned, dataset)
	}
	head, err := CheckoutHEAD(context.Background(), root)
	if err != nil {
		t.Skipf("SKIP: %s is not a git checkout (%v); cannot verify the pin", root, err)
	}
	if !strings.EqualFold(head, pinned) {
		t.Skipf("SKIP: %s clone at %s is at %s, not the pinned %s; judgements are only valid at the pin", name, root, head, pinned)
	}
	return root
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

// The v2 dataset's shape, checkable without the clone. The properties asserted
// here are the ones SW-279 exists to guarantee, so a future edit that quietly
// breaks one fails here rather than in a release claim: every query carries a
// family and a provenance, no family straddles the split, cb-05 is absent while
// cb-11 keeps the dev split it has had since SW-258, and the answerable counts
// clear 30 on both sides.
func TestDatasets_CobraV2Shape(t *testing.T) {
	ds, err := LoadDataset(cobraV2Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Dataset.RepoSHA != cobraPinnedSHA {
		t.Errorf("repo_sha = %q, want the pin %q", ds.Dataset.RepoSHA, cobraPinnedSHA)
	}

	// v2's issue-derived rows were reviewed by an independent agent, not by a
	// human. Filing the set under the human-reviewed label would overstate the
	// evidence, so the label must not be that constant and must say what the
	// review actually was.
	if ds.Dataset.EvidenceClass == EvidenceClassAgentHumanReviewed {
		t.Errorf("evidence_class = %q; v2's issue-derived rows were agent-reviewed and must not be filed under the human-reviewed label", ds.Dataset.EvidenceClass)
	}
	if !strings.Contains(ds.Dataset.EvidenceClass, "agent-reviewed") {
		t.Errorf("evidence_class = %q, want it to state that the issue-derived rows were agent-reviewed", ds.Dataset.EvidenceClass)
	}

	answerable := map[string]int{}
	byID := map[string]Query{}
	for _, q := range ds.Dataset.Queries {
		byID[q.ID] = q
		if q.FamilyID == "" || q.Provenance == "" {
			t.Errorf("query %q: family_id=%q provenance=%q, both are required in v2", q.ID, q.FamilyID, q.Provenance)
		}
		if q.Stratum != StratumNoHit {
			answerable[q.Split]++
		}
	}

	// Validate already refuses a family that crosses splits; assert it here too,
	// because this is the property the whole holdout claim rests on and it should
	// fail loudly in the dataset test, not only inside a validator.
	splitOf := map[string]string{}
	for _, q := range ds.Dataset.Queries {
		if prior, seen := splitOf[q.FamilyID]; seen && prior != q.Split {
			t.Errorf("family %q crosses splits %s and %s at query %q", q.FamilyID, prior, q.Split, q.ID)
		}
		splitOf[q.FamilyID] = q.Split
	}

	if _, present := byID["cb-05"]; present {
		t.Error("cb-05 is withdrawn from v2; see projects/graphi/stories/SW-279/decision-holdout-dev-overlap.md")
	}
	cb11, present := byID["cb-11"]
	if !present {
		t.Fatal("cb-11 must be carried into v2")
	}
	if cb11.Split != SplitDev {
		t.Errorf("cb-11 split = %q, want %q; no SW-258 assignment may move", cb11.Split, SplitDev)
	}

	const want = 30
	if answerable[SplitDev] < want || answerable[SplitHoldout] < want {
		t.Errorf("answerable dev=%d holdout=%d, want >= %d on both (SW-266 AC-2)", answerable[SplitDev], answerable[SplitHoldout], want)
	}

	// Every issue-derived query is a natural-language question in one of the three
	// strata Section 5 allows it to enter, and carries a github provenance.
	for _, q := range ds.Dataset.Queries {
		if !strings.HasPrefix(q.Provenance, "github:spf13/cobra#") {
			continue
		}
		switch q.Stratum {
		case StratumConfigDocs, StratumArchitectureFlow, StratumNLBehaviour:
		default:
			t.Errorf("issue-derived query %q has stratum %q; Section 5 permits only config_docs, architecture_flow, nl_behaviour", q.ID, q.Stratum)
		}
	}
}

// AC-9 for v2: every judged span still resolves at the pin. Skips visibly when
// the clone is absent, exactly as the v1 check does.
func TestDatasets_CobraV2SpansResolveAtPinnedSHA(t *testing.T) {
	root := pinnedCheckout(t, cobraEnvVar, "cobra", cobraUpstream, cobraSentinel, cobraPinnedSHA, cobraV2Dataset)
	ds, err := LoadDataset(cobraV2Dataset)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSpanCoverage(root, ds.Dataset); err != nil {
		t.Fatal(err)
	}
}

// AC-9 over the pinned public repository: runs only against a local clone at
// the pinned sha, and SKIPS — visibly — otherwise. A stale judgement fails.
func TestDatasets_CobraSpansResolveAtPinnedSHA(t *testing.T) {
	root := pinnedCheckout(t, cobraEnvVar, "cobra", cobraUpstream, cobraSentinel, cobraPinnedSHA, "cobra-v1.json")
	ds, err := LoadDataset(cobraDataset)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSpanCoverage(root, ds.Dataset); err != nil {
		t.Fatal(err)
	}
}

// AC-8's large-class dataset is performance-only: a property of the JSON
// alone, checked without the clone. It must say so, stay small, and stay
// inside the cheap-to-judge strata so nobody mistakes it for a quality set
// or derives a target from it.
func TestDatasets_GrpcGoDatasetShape(t *testing.T) {
	ds, err := LoadDataset(grpcGoDataset)
	if err != nil {
		t.Fatal(err)
	}
	d := ds.Dataset
	if d.Repo != "grpc-go" || d.RepoSHA != grpcGoPinnedSHA || d.Language != "go" {
		t.Errorf("grpc-go dataset header = repo %q sha %q language %q", d.Repo, d.RepoSHA, d.Language)
	}
	if d.EvidenceClass != EvidenceClassAgentHumanReviewed {
		t.Errorf("evidence_class = %q", d.EvidenceClass)
	}
	if !strings.Contains(d.Notes, "PERFORMANCE-ONLY") || !strings.Contains(d.Notes, "NOT a quality dataset") {
		t.Errorf("notes must state that the dataset measures the large size class and is not a quality dataset; got %q", d.Notes)
	}
	if n := len(d.Queries); n < 6 || n > 8 {
		t.Errorf("%d queries; a performance-only dataset carries 6..8", n)
	}
	cheap := map[string]bool{StratumExactIdentifier: true, StratumExactPath: true, StratumNoHit: true}
	seen := map[string]bool{}
	for _, q := range d.Queries {
		if !cheap[q.Stratum] {
			t.Errorf("%s: stratum %s is not one of the cheap-to-judge strata a performance-only dataset is confined to", q.ID, q.Stratum)
		}
		seen[q.Stratum] = true
		if q.Language != "en" {
			t.Errorf("%s: language %q, the validated query contract is English", q.ID, q.Language)
		}
		for i, j := range q.Judgements {
			if j.Annotator != "claude-delegate (SW-258 rework)" || j.Reviewer != "orchestrator" {
				t.Errorf("%s judgement %d: annotator/reviewer = %q/%q", q.ID, i, j.Annotator, j.Reviewer)
			}
		}
	}
	for _, s := range []string{StratumExactIdentifier, StratumExactPath, StratumNoHit} {
		if !seen[s] {
			t.Errorf("stratum %s has no query", s)
		}
	}
}

// AC-9 over the large-class repository: the same span-coverage check as
// cobra, against a local clone at the pinned sha, SKIPPING otherwise.
func TestDatasets_GrpcGoSpansResolveAtPinnedSHA(t *testing.T) {
	root := pinnedCheckout(t, "GRAPHI_CORPUS_GRPC_GO", "grpc-go", "grpc/grpc-go", "clientconn.go", grpcGoPinnedSHA, "grpc-go-perf-v1.json")
	ds, err := LoadDataset(grpcGoDataset)
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
