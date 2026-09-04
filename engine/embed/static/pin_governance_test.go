package static_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed/static"
)

var pinDependentRetrievalRuns = []string{
	"docs/eval/retrieval/runs/2026-09-01-static-local",
	"docs/eval/retrieval/runs/2026-09-02-capsule-local",
	"docs/eval/retrieval/runs/2026-09-02-sw263-local",
	"docs/eval/retrieval/runs/2026-09-02-sw263-v3-restoration-local",
	"docs/eval/retrieval/runs/2026-09-02-sw264-task-context-v2-static-local",
}

func TestStatic_PinRotationGovernance(t *testing.T) {
	const recordPath = "PIN_ROTATION.md"
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("static pin governance: read engine/embed/static/%s: %v", recordPath, err)
	}

	marker := "Current governed revision: `" + static.PinnedRevision + "`."
	if !bytes.Contains(record, []byte(marker)) {
		t.Fatalf("static pin governance: PinnedRevision %q has no matching rotation record in engine/embed/static/PIN_ROTATION.md; update the approval, re-measurement, and stale-artifact record before rotating the pin", static.PinnedRevision)
	}

	for _, required := range []string{
		"## Approval",
		"## Required rotation record and re-measurement",
		"## Records made stale by the next rotation",
		"CGo-free",
		"byte-exact",
		"docs/eval/static-embedder-cross-arch/2026-09-03-sw271/",
	} {
		if !bytes.Contains(record, []byte(required)) {
			t.Errorf("static pin governance: engine/embed/static/PIN_ROTATION.md is missing required record text %q", required)
		}
	}
	for _, run := range pinDependentRetrievalRuns {
		if _, err := os.Stat(filepath.Join("../../..", filepath.FromSlash(run))); err != nil {
			t.Errorf("static pin governance: recorded pin-dependent run %s is not readable: %v", run, err)
		}
		if !bytes.Contains(record, []byte("`"+run+"/`")) {
			t.Errorf("static pin governance: pin-dependent run %s/ is absent from engine/embed/static/PIN_ROTATION.md", run)
		}
	}

	runs, err := productionStaticRetrievalRuns("../../../docs/eval/retrieval/runs")
	if err != nil {
		t.Fatalf("static pin governance: enumerate production-static retrieval runs: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("static pin governance: no revision-qualified production-static retrieval runs found; refusing a vacuous stale-artifact gate")
	}
	for _, run := range runs {
		if !bytes.Contains(record, []byte("`"+run+"/`")) {
			t.Errorf("static pin governance: production-static run %s/ is absent from engine/embed/static/PIN_ROTATION.md; enumerate it as stale before rotating the pin", run)
		}
	}
}

func productionStaticRetrievalRuns(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	prefix := []byte("static:" + static.PinnedModel + "@")
	var runs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		usesProductionStatic := false
		err := filepath.WalkDir(dir, func(path string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if usesProductionStatic || item.IsDir() {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			usesProductionStatic = bytes.Contains(body, prefix)
			return nil
		})
		if err != nil {
			return nil, err
		}
		if usesProductionStatic {
			runs = append(runs, filepath.ToSlash(filepath.Join("docs/eval/retrieval/runs", entry.Name())))
		}
	}
	sort.Strings(runs)
	return runs, nil
}

func TestStatic_PinRotationGovernance_EnumeratesRevisionQualifiedRuns(t *testing.T) {
	runs, err := productionStaticRetrievalRuns("../../../docs/eval/retrieval/runs")
	if err != nil {
		t.Fatal(err)
	}
	// Each entry was reviewed before being listed: the run genuinely uses the
	// pinned production static embedder, so a rotation invalidates it. The list
	// is explicit on purpose — a newly landed production-static run must fail
	// this test until a human has looked at it and recorded it in
	// PIN_ROTATION.md. That is exactly what happened to the SW-272 entry below,
	// which this gate caught on its first rebase past the SW-272 merge.
	want := []string{
		"docs/eval/retrieval/runs/2026-09-01-static-local",
		"docs/eval/retrieval/runs/2026-09-02-sw264-task-context-v2-static-local",
		"docs/eval/retrieval/runs/2026-09-03-sw272-field-parity",
		// SW-270: before/after of the bare-filename exact-path rule on the
		// dev split; both sides carry the pinned static selector stamp.
		"docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule",
	}
	if strings.Join(runs, "\n") != strings.Join(want, "\n") {
		t.Fatalf("revision-qualified production-static retrieval runs:\n got %q\nwant %q; review every discovered run and update the explicit governance inventory (legacy static runs without selector stamps remain listed separately)", runs, want)
	}
}
