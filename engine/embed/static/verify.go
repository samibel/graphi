// Package static's load-time SHA-256 verification. The production embedder
// refuses to load an artifact whose bytes do not match the in-tree pin
// table (AC-2/AC-6). This file is the single point that does the
// verification — `New` (and the lazy `load` path), the air-gapped
// `StaticInstallLocal` and the downloader all route through it so the
// three callers cannot drift.
//
// A mismatch is a typed `PinMismatchError` (carrying expected vs actual,
// so the operator-facing message is the one the brief requires: "expected
// vs actual hash"). A single bad file fails the whole verification; no
// partial load is permitted.
package static

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// PinMismatchError is the typed error VerifyPins (and the load path) return
// when a file's bytes do not match the in-tree pin table. The expected and
// actual hex digests are exposed as fields so callers can render them
// without re-parsing the message — the brief requires the error to name
// expected vs actual.
type PinMismatchError struct {
	// File is the pinned file name (e.g. "model.safetensors").
	File string
	// Path is the absolute path that was hashed.
	Path string
	// Expected is the hex digest recorded in PinnedSHA256.
	Expected string
	// Actual is the hex digest computed from the file's bytes.
	Actual string
}

func (e *PinMismatchError) Error() string {
	return fmt.Sprintf("static: %s: SHA-256 mismatch: expected %s, actual %s (refusing to load a model whose bytes do not match the pinned identity; run `graphi setup-embedder static:%s@%s` to install a fresh artifact, or restore the file at %s)",
		e.File, e.Expected, e.Actual, PinnedModel, PinnedRevision, e.Path)
}

// sha256File reads path and returns its lower-case hex SHA-256.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// VerifyPins reads every pinned file under dir, computes its SHA-256, and
// compares to the in-tree PinnedSHA256 table. A mismatch is a typed
// PinMismatchError naming the offending file and the expected vs actual
// hash. The function is the single source of the AC-2/AC-6 contract; every
// code path that reads the artifact runs it.
//
// The function does NOT parse the safetensors header or the tokenizer
// JSON; that is `LoadModel`'s job. VerifyPins is the byte-level identity
// check; LoadModel is the structural check.
func VerifyPins(dir string) error {
	if dir == "" {
		return fmt.Errorf("static: VerifyPins: directory is empty")
	}
	for _, name := range PinnedFileNames {
		want, ok := PinnedSHA256[name]
		if !ok {
			return fmt.Errorf("static: VerifyPins: no pin recorded for %s; refusing to load a file with no recorded identity", name)
		}
		path := filepath.Join(dir, name)
		got, err := sha256File(path)
		if err != nil {
			return fmt.Errorf("static: VerifyPins: hash %s: %w", name, err)
		}
		if got != want {
			return &PinMismatchError{
				File:     name,
				Path:     path,
				Expected: want,
				Actual:   got,
			}
		}
	}
	return nil
}
