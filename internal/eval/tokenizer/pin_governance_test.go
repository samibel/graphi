package tokenizer_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/eval/tokenizer"
)

func TestTokenizer_PinRotationGovernance(t *testing.T) {
	const recordPath = "PIN_ROTATION.md"
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("tokenizer pin governance: read internal/eval/tokenizer/%s: %v", recordPath, err)
	}
	marker := "Current governed vocabulary SHA-256: `" + tokenizer.PinnedVocabularySHA256 + "`."
	if !bytes.Contains(record, []byte(marker)) {
		t.Fatalf("tokenizer pin governance: PinnedVocabularySHA256 %q has no matching rotation record in internal/eval/tokenizer/PIN_ROTATION.md; update approval, golden vectors, conformance evidence, and the stale run-directory inventory before rotating the pin", tokenizer.PinnedVocabularySHA256)
	}
	for _, required := range []string{
		"Current governed tokenizer: `" + tokenizer.TokenizerID + "`.",
		"Current governed file: `" + tokenizer.PinnedVocabularyFile + "`.",
		"Current governed source: `" + tokenizer.PinnedVocabularyURL + "`.",
		"## Approval",
		"## Required rotation record and re-measurement",
		"## Records made stale by the next rotation",
		"CGo-free",
		"golden token vectors",
		"No token-savings run directories exist at adoption time (SW-277).",
	} {
		if !bytes.Contains(record, []byte(required)) {
			t.Errorf("tokenizer pin governance: PIN_ROTATION.md is missing required record text %q", required)
		}
	}
	if got := tokenizer.PinnedSHA256[tokenizer.PinnedVocabularyFile]; got != tokenizer.PinnedVocabularySHA256 {
		t.Errorf("tokenizer pin governance: pin table hash=%q, vocabulary identity=%q", got, tokenizer.PinnedVocabularySHA256)
	}

	runs, err := tokenizerDependentRunDirs("../../../docs/eval/retrieval/runs")
	if err != nil {
		t.Fatalf("tokenizer pin governance: enumerate run directories: %v", err)
	}
	for _, run := range runs {
		if !bytes.Contains(record, []byte("`"+run+"/`")) {
			t.Errorf("tokenizer pin governance: tokenizer-dependent run %s/ is absent from PIN_ROTATION.md; enumerate it as stale before rotating the pin", run)
		}
	}
}

func tokenizerDependentRunDirs(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	needle := []byte(tokenizer.TokenizerID)
	var runs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		found := false
		dir := filepath.Join(root, entry.Name())
		if err := filepath.WalkDir(dir, func(path string, item fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if found || item.IsDir() {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			found = bytes.Contains(body, needle)
			return nil
		}); err != nil {
			return nil, err
		}
		if found {
			runs = append(runs, filepath.ToSlash(filepath.Join("docs/eval/retrieval/runs", entry.Name())))
		}
	}
	return runs, nil
}

func TestTokenizer_RuntimePackageHasNoNetworkOrCgoImports(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		body, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{`"net"`, `"net/http"`, `"net/url"`, `"runtime/cgo"`, `"os/exec"`} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Errorf("%s imports %s; tokenizer measurement must remain offline and CGo-free", entry.Name(), forbidden)
			}
		}
	}
}
