package retrieval

// SW-282 AC-12: this story sets targets. It changes no retrieval, ranking,
// fusion, budget, prompt or product behaviour, produces no rater response or
// grade, re-runs nothing, and adds no tamper-resistance mechanism.
//
// The part of that claim a test can hold permanently is the artifact side: the
// frozen datasets, the frozen methodology, the budgets file this story must
// leave BYTE-IDENTICAL (AC-6), the inclusion rule, and every evaluation run
// directory that existed before this story. Those are pinned here by digest, so
// an edit fails in this package rather than inside a later claim.
//
// What this test deliberately does NOT do is diff the working tree against a
// base branch. "This story's changed-file set" is a property of a review, not
// of a checkout: a merge base is not reliably present in every CI checkout, and
// a gate that skips when it cannot find one is the silent-skip defect this
// track has already rejected twice. The prefixes AC-12 names that cannot be
// pinned (engine/, surfaces/) legitimately change in later stories, so pinning
// them would make this test permanently and meaninglessly red. They are
// verified in review from `git diff --name-only`, and recorded in the story's
// verification record.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

// protectedRunDirs are the evaluation run directories that existed before
// SW-282. Each digest covers every file in the directory, so a re-graded
// response, an edited report or a re-stamped index fails here.
//
// The sealed SW-280 run is one of them. Rewriting docs/eval/retrieval-targets.json
// SUPERSEDES the targets digest that run's end-of-run-hash-comparison.json
// froze; that drift is by design and is recorded in
// projects/graphi/stories/SW-282/decision-censoring.md. The run itself is not
// edited to hide it — which is exactly what this digest proves.
var protectedRunDirs = map[string]string{
	"2026-08-30-local":                              "76ca320503953411039d84863011bf00a8a2c26eac1d7d2a76bae5306314e652",
	"2026-08-31-ac3ac6-local":                       "e716f1beade9212ce68b03175bb8b8181c146d3194258c9a1806533c47d7ddd5",
	"2026-08-31-conformance-local":                  "4718943d7a73c005884bf2e3246b2a03d7b6c68f50f0893fa09532d749da7862",
	"2026-08-31-local":                              "bc34d9bd4e4138f3e5f05c0f0076e25695f6f73ad28bd4236509d185ff4220d8",
	"2026-08-31-rerun-local":                        "2fda98f662d35cb2c1c62e51105b9647ac375cef831dd7b403b0ff922a29cf1c",
	"2026-09-01-static-local":                       "2585ad6e56fa9d15198447d6b72fda2c9eed77db00ff34d027a87dae51b70a2a",
	"2026-09-02-capsule-local":                      "dff271717e0406319dd9ea3bfa00053968f471206bc10ca68577effb30c0c7fe",
	"2026-09-02-sw263-local":                        "416faca071bc43dc308c9447a38eef038450f19932e4bec2e27c06e673aea360",
	"2026-09-02-sw263-v3-restoration-local":         "25f9a95fdc4b978eb1f593a7a899cd6c40b416473eacf5a681a63406ea9be9ec",
	"2026-09-02-sw264-task-context-v2-static-local": "92599450d9070b8e38e546bf7561a6ab121ce2aad09bf0c69b4766801d24b269",
	"2026-09-03-sw272-field-parity":                 "490e83132a98cf57ecdbd9bdd88492e239e8f45e862c573f288f07f2177705fc",
	"2026-09-04-sw270-bare-filename-path-rule":      "2da78022cfeea302ad0b84aa959276c0c075ae9b095d84c92d1836ee04c5d21a",
	"2026-09-05-sw280-qrel-blind-smoke":             "06a864c86990bb22d387c569fbad9e0813c940cabbc51823d93814187e42c05e",
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

	// Print mode: used once, when a later story legitimately adds a run
	// directory to this list.
	if os.Getenv("SW282_PRINT_RUN_DIGESTS") == "1" {
		names := make([]string, 0, len(protectedRunDirs))
		for name := range protectedRunDirs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d, err := digestDir(filepath.Join(root, "docs", "eval", "retrieval", "runs", name))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("\t%q: %q,", name, d)
		}
	}

	for name, want := range protectedRunDirs {
		got, err := digestDir(filepath.Join(root, "docs", "eval", "retrieval", "runs", name))
		if err != nil {
			t.Errorf("protected run directory %s is unreadable: %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("protected run directory %s changed: digest %s, pinned %s. An existing evaluation run is evidence; it is superseded by a new run, never edited.", name, got, want)
		}
	}

	t.Run("every pre-existing run directory is pinned", func(t *testing.T) {
		// SW-282's own three new directories are the only ones allowed to be
		// absent from the pin list; anything else means a directory was added
		// or renamed without the protection being extended.
		added := map[string]bool{
			"2026-09-06-sw282-recalibration-local": true,
			"2026-09-06-sw282-gate-local":          true,
			"2026-09-06-sw282-coverage-local":      true,
		}
		entries, err := os.ReadDir(filepath.Join(root, "docs", "eval", "retrieval", "runs"))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, pinned := protectedRunDirs[e.Name()]; pinned || added[e.Name()] {
				continue
			}
			t.Errorf("run directory %s is neither pinned nor one of SW-282's own new runs", e.Name())
		}
	})
}

// digestDir hashes every file under dir: sorted relative path, then the file's
// own sha256. Both, so renaming two files past each other is caught.
func digestDir(dir string) (string, error) {
	type entry struct{ rel, sum string }
	var entries []entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		entries = append(entries, entry{rel: filepath.ToSlash(rel), sum: SHA256Hex(raw)})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\n%s\n", e.rel, e.sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
