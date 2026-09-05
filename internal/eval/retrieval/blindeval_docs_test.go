package retrieval

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

// blindEvalRunDir is the committed run directory for this evaluation, relative
// to the repository root.
const blindEvalRunDir = "docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke"

func repoRootForBlindEvalDocs(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found walking up from the working directory")
		}
		dir = parent
	}
}

// AC-14: the run directory is discoverable by the tokenizer rotation gate, so a
// later rotation cannot leave this evaluation silently un-invalidated.
//
// The assertion is positive on both halves: the directory really does carry the
// pinned tokenizer id somewhere inside it (which is what the gate's scan looks
// for), and PIN_ROTATION.md really does enumerate it.
func TestQrelBlindSmoke_RunDirectoryIsDiscoverableByTheTokenizerRotationGate(t *testing.T) {
	root := repoRootForBlindEvalDocs(t)
	dir := filepath.Join(root, filepath.FromSlash(blindEvalRunDir))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the committed run directory %s is absent: %v", blindEvalRunDir, err)
	}

	// Reproduce the gate's scan: walk the directory looking for the pinned
	// tokenizer id in any file.
	needle := []byte(evaltokenizer.TokenizerID)
	var carriers []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(body, needle) {
			rel, _ := filepath.Rel(dir, path)
			carriers = append(carriers, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(carriers) == 0 {
		t.Fatalf("no file under %s carries the pinned tokenizer id %q, so the rotation gate's scan would not discover this run and a later rotation could leave it silently un-invalidated",
			blindEvalRunDir, evaltokenizer.TokenizerID)
	}
	t.Logf("the rotation gate's scan discovers this run through %d file(s): %s", len(carriers), strings.Join(carriers, ", "))

	record, err := os.ReadFile(filepath.Join(root, "internal/eval/tokenizer/PIN_ROTATION.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(record, []byte("`"+blindEvalRunDir+"/`")) {
		t.Errorf("PIN_ROTATION.md does not enumerate %s/ as a tokenizer-dependent run", blindEvalRunDir)
	}
}

// AC-12: the naming discipline is applied to what is actually committed, not
// only to test strings. Every markdown document in the run directory must pass
// the checker, and the two that must exist are named so their absence fails
// here rather than reducing the population this test evaluates.
func TestQrelBlindSmoke_CommittedRunDirectoryDocumentsObeyTheNamingDiscipline(t *testing.T) {
	root := repoRootForBlindEvalDocs(t)
	dir := filepath.Join(root, filepath.FromSlash(blindEvalRunDir))
	for _, required := range []string{"METHOD.md", "grading-rubric.md"} {
		if _, err := os.Stat(filepath.Join(dir, required)); err != nil {
			t.Fatalf("%s/%s is absent: %v", blindEvalRunDir, required, err)
		}
	}
	checked := 0
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		checked++
		if err := CheckQrelBlindSmokeWording(string(body)); err != nil {
			t.Errorf("%s: %v", filepath.ToSlash(rel), err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if checked < 2 {
		t.Fatalf("only %d markdown documents were checked under %s", checked, blindEvalRunDir)
	}
	t.Logf("checked %d markdown documents under %s", checked, blindEvalRunDir)
}

// The story ticket and this evaluation's own method notes must not drift apart
// on the one thing the slice is named for. If the committed method notes stop
// describing the order of operations, the executable checks are the only record
// left, and a reader cannot see what they enforce.
func TestQrelBlindSmoke_MethodNotesStateTheOrderOfOperations(t *testing.T) {
	root := repoRootForBlindEvalDocs(t)
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(blindEvalRunDir), "METHOD.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"before the first rater response exists",
		"content-addressed",
		"not adjudicated, re-requested or replaced",
		"never clamped to `N`",
		"RELEASE: NO",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("METHOD.md no longer states %q", required)
		}
	}
}
