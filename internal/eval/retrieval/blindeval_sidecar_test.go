package retrieval

// The artifacts the decision depends on must be bound.
//
// Round 2 of the review found the same defect wearing three costumes: the
// decision procedure read optional, mutable sidecar files and trusted what they
// said. A fabricated candidate binding was accepted, an over-broad candidate
// exclusion swallowed the implementation, and deleting the disclosed-concern
// record silently RAISED the corrected count. These tests break each one and
// require a refusal.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// N1: the binding's booleans were read without checking that its fields are
// what they claim to be. A hand-written binding naming candidate_sha
// "not-a-sha" and an unrelated excluded path was accepted as bound.
func TestQrelBlindSmoke_AFabricatedCandidateBindingIsNotBound(t *testing.T) {
	const frozen = "0123456789abcdef0123456789abcdef01234567"
	precondition := PreconditionRecord{
		DatasetSHA256:        strings.Repeat("d", 64),
		CandidateSHA:         frozen,
		CandidateTokenBudget: SavingsCandidateBudget,
		Inputs: []FrozenInput{
			{Role: PreconditionInputGradingRubric, Path: "docs/eval/retrieval/runs/x/grading-rubric.md", SHA256: strings.Repeat("a", 64)},
		},
	}
	sound := CandidateCaptureProvenance{
		CaptureVersion:   CandidateCaptureVersion,
		Transport:        "t",
		Boundary:         string(PayloadBoundaryCandidate),
		RepoSHA:          strings.Repeat("b", 40),
		DatasetSHA256:    precondition.DatasetSHA256,
		EmbedderSelector: "static:x@y",
		IndexFingerprint: "fp",
		SemanticState:    "ready",
		TokenBudget:      SavingsCandidateBudget,
		Binding: &CandidateBinding{
			CandidateSHA:           frozen,
			FrozenCandidateSHA:     frozen,
			CandidateWorktreeClean: true,
			CandidateMatchesFrozen: true,
			CandidateExcludedPath:  "docs/eval/retrieval/runs/x",
			CheckoutSHA:            strings.Repeat("b", 40),
			CheckoutWorktreeClean:  true,
		},
	}
	// Positive control: the sound binding really does bind, so the refusals
	// below are refusals of the defect and not of the shape.
	if assessment := AssessCaptureBinding(precondition, sound); !assessment.Bound {
		t.Fatalf("the sound control did not bind: %v", assessment.Reasons)
	}

	for _, tc := range []struct {
		name    string
		break_  func(*CandidateBinding)
		expects string
	}{
		{
			name:    "candidate_sha is not a commit id",
			break_:  func(b *CandidateBinding) { b.CandidateSHA = "not-a-sha" },
			expects: "not a 40-character commit id",
		},
		{
			name:    "frozen_candidate_sha is not a commit id",
			break_:  func(b *CandidateBinding) { b.FrozenCandidateSHA = "not-a-sha" },
			expects: "not a 40-character commit id",
		},
		{
			name:    "checkout_sha is not a commit id",
			break_:  func(b *CandidateBinding) { b.CheckoutSHA = "HEAD" },
			expects: "not a 40-character commit id",
		},
		{
			name:    "the excluded path is unrelated to this run",
			break_:  func(b *CandidateBinding) { b.CandidateExcludedPath = "docs/eval/retrieval/runs/somewhere-else" },
			expects: "the excluded path is the only place the candidate tree is allowed to differ",
		},
		{
			name:    "the excluded path is the repository root",
			break_:  func(b *CandidateBinding) { b.CandidateExcludedPath = "." },
			expects: "the excluded path is the only place the candidate tree is allowed to differ",
		},
		{
			name:    "the excluded path is empty",
			break_:  func(b *CandidateBinding) { b.CandidateExcludedPath = "" },
			expects: "the excluded path is the only place the candidate tree is allowed to differ",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broken := *sound.Binding
			tc.break_(&broken)
			provenance := sound
			provenance.Binding = &broken
			assessment := AssessCaptureBinding(precondition, provenance)
			if assessment.Bound {
				t.Fatal("a fabricated candidate binding was accepted as bound")
			}
			if !strings.Contains(strings.Join(assessment.Reasons, "\n"), tc.expects) {
				t.Errorf("reasons %v do not say %q", assessment.Reasons, tc.expects)
			}
		})
	}

	t.Run("a provenance whose repo_sha is not a commit id", func(t *testing.T) {
		provenance := sound
		provenance.RepoSHA = "main"
		if assessment := AssessCaptureBinding(precondition, provenance); assessment.Bound {
			t.Fatal("a provenance naming a branch instead of a commit was accepted as bound")
		}
	})

	t.Run("a precondition record that names no run directory", func(t *testing.T) {
		unrooted := precondition
		unrooted.Inputs = nil
		if assessment := AssessCaptureBinding(unrooted, sound); assessment.Bound {
			t.Fatal("a binding was accepted although nothing names the directory this run lives in")
		}
	})
}

// N2: the run directory is the ONE path the candidate binding excludes from its
// comparison against the frozen candidate, so a root or otherwise over-broad
// directory turns that exclusion into a comparison that sees nothing.
func TestQrelBlindSmoke_AnOverBroadRunDirectoryIsRefused(t *testing.T) {
	if err := CheckRunDirectoryRelativePath("docs/eval/retrieval/runs/x"); err != nil {
		t.Fatalf("a proper run directory was refused: %v", err)
	}
	for _, tc := range []struct{ rel, expects string }{
		{".", "excludes the entire implementation"},
		{"", "no repository-relative path"},
		{"..", "outside the repository"},
		{"../elsewhere", "outside the repository"},
		{"/absolute/run", "is absolute"},
		{"docs/eval/../eval/runs/x", "not a clean repository-relative path"},
		{"docs/eval/retrieval/runs/x/", "not a clean repository-relative path"},
		{" docs/eval ", "padded with whitespace"},
	} {
		t.Run("refuses "+tc.rel, func(t *testing.T) {
			err := CheckRunDirectoryRelativePath(tc.rel)
			if err == nil {
				t.Fatalf("run directory %q was accepted", tc.rel)
			}
			if !strings.Contains(err.Error(), tc.expects) {
				t.Errorf("refusal %q does not say %q", err, tc.expects)
			}
		})
	}

	// And the capture instrument applies it before it observes anything, so an
	// over-broad exclusion cannot be recorded in the first place.
	probe := RepoProbe{
		HeadSHA:       func(context.Context, string) (string, error) { return strings.Repeat("a", 40), nil },
		WorktreeClean: func(context.Context, string) (bool, error) { return true, nil },
		PathsDifferingOutside: func(context.Context, string, string, string, string) ([]string, error) {
			return nil, nil
		},
	}
	_, err := ObserveCandidateBinding(context.Background(), probe, CandidateBindingOptions{
		CandidateRoot: "/candidate", FrozenCandidateSHA: strings.Repeat("0", 40),
		ExcludePath: ".", CheckoutRoot: "/cobra", CheckoutSHA: strings.Repeat("b", 40),
	})
	if err == nil {
		t.Fatal("the capture observed a binding that excludes the whole repository")
	}
	if !strings.Contains(err.Error(), "excludes the entire implementation") {
		t.Errorf("refusal %q does not name the swallowed implementation", err)
	}
}

// N3: the disclosed-concern record subtracts from the corrected count, so
// DELETING it raises the count. At the threshold that is 56/55 (NO) becoming
// 56/56 (YES). Nothing recorded that it had ever existed.
func TestQrelBlindSmoke_ADeletedOrReplacedSidecarIsRefused(t *testing.T) {
	pre := PreRegistration{SHA256: strings.Repeat("e", 64)}
	newRun := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range BlindEvalSidecarFiles() {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(`{"file":"`+name+`"}`+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := SealSidecarManifest(dir, pre); err != nil {
			t.Fatal(err)
		}
		if err := CheckSidecarBinding(dir, pre); err != nil {
			t.Fatalf("a freshly sealed run does not check out: %v", err)
		}
		return dir
	}

	t.Run("a recorded sidecar that has been deleted", func(t *testing.T) {
		dir := newRun(t)
		if err := os.Remove(filepath.Join(dir, BlindEvalConcernsFile)); err != nil {
			t.Fatal(err)
		}
		err := CheckSidecarBinding(dir, pre)
		if err == nil {
			t.Fatal("deleting the disclosed-concern record was accepted, which raises the corrected count")
		}
		if !strings.Contains(err.Error(), "never a better number") {
			t.Errorf("refusal %q does not state the direction", err)
		}
		// And re-sealing does not launder it either.
		if _, err := SealSidecarManifest(dir, pre); err == nil {
			t.Fatal("re-sealing accepted the deletion")
		}
	})

	t.Run("a recorded sidecar that has been replaced", func(t *testing.T) {
		dir := newRun(t)
		if err := os.WriteFile(filepath.Join(dir, BlindEvalConcernsFile), []byte("[]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := CheckSidecarBinding(dir, pre); err == nil {
			t.Fatal("emptying the disclosed-concern record was accepted")
		}
		if _, err := SealSidecarManifest(dir, pre); err == nil {
			t.Fatal("re-sealing accepted the replacement")
		}
	})

	t.Run("the manifest itself deleted", func(t *testing.T) {
		dir := newRun(t)
		if err := os.Remove(filepath.Join(dir, BlindEvalSidecarManifestFile)); err != nil {
			t.Fatal(err)
		}
		err := CheckSidecarBinding(dir, pre)
		if err == nil {
			t.Fatal("a run with no sidecar manifest was decided")
		}
		if !strings.Contains(err.Error(), BlindEvalSidecarManifestFile) {
			t.Errorf("refusal %q does not name the manifest", err)
		}
	})

	t.Run("a manifest from another run", func(t *testing.T) {
		dir := newRun(t)
		if err := CheckSidecarBinding(dir, PreRegistration{SHA256: strings.Repeat("f", 64)}); err == nil {
			t.Fatal("a manifest binding a different pre-registration was accepted")
		}
	})

	t.Run("a sidecar appearing after the manifest recorded its absence", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := SealSidecarManifest(dir, pre); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, BlindEvalConcernsFile), []byte("[]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Reading it before it is recorded is a refusal: a sidecar the
		// manifest does not name is a sidecar nothing binds.
		if err := CheckSidecarBinding(dir, pre); err == nil {
			t.Fatal("an unrecorded sidecar was read by the decision")
		}
		// Recording it is the one permitted transition, because a disclosed
		// concern can only ever subtract.
		if _, err := SealSidecarManifest(dir, pre); err != nil {
			t.Fatalf("recording a newly disclosed concern was refused: %v", err)
		}
		if err := CheckSidecarBinding(dir, pre); err != nil {
			t.Fatalf("the recorded sidecar does not check out: %v", err)
		}
	})
}

// N4: the write-once seal read the destination and then wrote it. Two
// concurrent seals of DIFFERING material could both see an absent destination
// and both proceed, so the last writer installed its grade over the first
// one's — the overwrite the seal exists to refuse, reached by running the
// command twice at once instead of twice in a row.
func TestQrelBlindSmoke_WriteOnceIsAnExclusiveCreate(t *testing.T) {
	const writers = 16
	for attempt := 0; attempt < 8; attempt++ {
		path := filepath.Join(t.TempDir(), "grade.json")
		var (
			mu        sync.Mutex
			succeeded []int
			start     sync.WaitGroup
			done      sync.WaitGroup
		)
		start.Add(1)
		for i := 0; i < writers; i++ {
			done.Add(1)
			go func(i int) {
				defer done.Done()
				start.Wait()
				err := WriteBlindEvalJSONWriteOnce("grade", path, map[string]int{"outcome": i})
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					succeeded = append(succeeded, i)
				}
			}(i)
		}
		start.Done()
		done.Wait()
		if len(succeeded) != 1 {
			t.Fatalf("%d of %d concurrent seals of differing material succeeded; exactly one may", len(succeeded), writers)
		}
		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var reencoded string
		func() {
			tmp := filepath.Join(t.TempDir(), "expected.json")
			if err := WriteBlindEvalJSONWriteOnce("grade", tmp, map[string]int{"outcome": succeeded[0]}); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(tmp)
			if err != nil {
				t.Fatal(err)
			}
			reencoded = string(raw)
		}()
		if string(written) != reencoded {
			t.Fatalf("the file holds %q, but the only writer that succeeded sealed %q", written, reencoded)
		}
	}
	// Re-sealing IDENTICAL material stays idempotent.
	path := filepath.Join(t.TempDir(), "grade.json")
	for i := 0; i < 2; i++ {
		if err := WriteBlindEvalJSONWriteOnce("grade", path, map[string]int{"outcome": 1}); err != nil {
			t.Fatalf("re-sealing identical material was refused: %v", err)
		}
	}
}
