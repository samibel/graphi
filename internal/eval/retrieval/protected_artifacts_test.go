package retrieval

// SW-282 AC-12: this story sets targets. It changes no retrieval, ranking,
// fusion, budget, prompt or product behaviour, produces no rater response or
// grade, re-runs nothing, and adds no tamper-resistance mechanism.
//
// The one thing a permanent test can hold, and the only thing this file does,
// is that the files SW-282 must not touch still hash to what they hashed
// before it: the frozen datasets, the frozen methodology, SW-279's inclusion
// rule, and the budgets file AC-6 requires to stay BYTE-IDENTICAL. A sha256 per
// file is the whole mechanism.
//
// An earlier draft of this file also carried a whole-directory digest of every
// pre-existing evaluation run, a directory walker to compute them, and an
// allowlist naming SW-282's own three new run directories. That was deleted for
// three reasons. It did not do AC-12's job — a ranking edit under engine/ still
// passed it. It broke work it has no business breaking — any later story's new
// run directory failed until someone edited an SW-282 allowlist. And it is the
// tamper-resistance machinery AC-12's own last sentence forbids this story from
// adding. What still holds the sealed SW-280 run is what always held it:
// TestQrelBlindSmoke_CommittedRunFrozenInputsStillHashTheSameAtRest checks its
// recorded frozen-input digests against the files at rest, and the targets gate
// reads its outcome.json directly.
//
// The rest of AC-12 — that nothing under engine/, surfaces/ or the run
// directories changed in THIS branch — is a property of a diff, not of a
// checkout, and is verified in review from `git diff --name-only`. A test that
// needs a merge base is a test that silently skips where there isn't one.

import (
	"os"
	"path/filepath"
	"testing"
)

// protectedFiles are frozen by digest. Each entry names why.
var protectedFiles = map[string]string{
	// The frozen release dataset and its predecessor. SW-282 corrects how the
	// population is COUNTED; it does not touch a query, a judgement or a split.
	"internal/eval/retrieval/testdata/datasets/cobra-v1.json": "be604ff7b17db5c35b0c63ddbb5d758633535e81e6771858ff860c724fb50d82",
	"internal/eval/retrieval/testdata/datasets/cobra-v2.json": "7de5ce6eef0e58d952b64ea7beaa0b09d158724b1eaa52bbd064ceebf53f35fc",
	// SW-279's inclusion rule.
	"docs/eval/retrieval/dataset-v2-inclusion-rule.md": "d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c",
	// SW-274's frozen measurement contract. SW-282 INHERITS it and does not
	// amend it; see projects/graphi/stories/SW-282/decision-censoring.md.
	"docs/eval/retrieval/methodology.md": "f0ee8fc33c135e4bbe277f071d5c089d6aa5d1112dec0919a73245021077ac7d",
	// AC-6: splitting ImmutableUntil in two exists so this file is left alone.
	"docs/eval/retrieval-budgets.json": "2a6996d5232005431a5cb4d3a7d2c216b1ff8990f12c9a788dc14e0f385930bb",
}

func TestSW282_ProtectedArtifactsAreUnchanged(t *testing.T) {
	root := repoRootForTest(t)

	for rel, want := range protectedFiles {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("protected file %s is unreadable: %v", rel, err)
			continue
		}
		if got := SHA256Hex(raw); got != want {
			t.Errorf("protected file %s changed: sha256 %s, pinned %s. SW-282 sets targets; it does not edit datasets, the frozen methodology, the inclusion rule or the budgets file.", rel, got, want)
		}
	}
}
