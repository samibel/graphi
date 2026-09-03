package staticfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

const (
	maxTokenizerFileBytes = 2 << 20
	tokenizerOperation    = "staticfetch: setup-tokenizer"
)

// DownloadTokenizer is the explicit, user-invoked acquisition step for the
// SW-266 real-tokenizer artifact. All HTTP remains in this already-allowlisted
// egress package; the tokenizer loader and measurement path are network-free.
func DownloadTokenizer(ctx context.Context, dest string) error {
	client := newHTTPSOnlyClientFor(tokenizerOperation, "HTTPS-only on every hop")
	return downloadTokenizerImpl(ctx, client, evaltokenizer.PinnedVocabularyURL, dest, evaltokenizer.PinnedVocabularySHA256)
}

// InstallLocalTokenizer verifies and installs an artifact already transferred
// into an air-gapped environment. src is a directory containing the canonical
// pinned vocabulary file.
func InstallLocalTokenizer(ctx context.Context, src, dest string) error {
	if src == "" {
		return fmt.Errorf("%s: --tokenizer-local source directory is empty", tokenizerOperation)
	}
	if _, err := evaltokenizer.Load(src); err != nil {
		return err
	}
	return installTokenizerFile(ctx, filepath.Join(src, evaltokenizer.PinnedVocabularyFile), dest, evaltokenizer.PinnedVocabularySHA256)
}

func downloadTokenizerImpl(ctx context.Context, client *http.Client, sourceURL, dest, wantHash string) error {
	if err := validateSchemeFor(sourceURL, tokenizerOperation); err != nil {
		return err
	}
	staging, cleanup, err := tokenizerStagingDir(dest)
	if err != nil {
		return err
	}
	defer cleanup()
	if err := fetchAndVerifyPinnedFile(ctx, client, sourceURL, staging, evaltokenizer.PinnedVocabularyFile, wantHash, maxTokenizerFileBytes, tokenizerOperation); err != nil {
		return err
	}
	if err := writeTokenizerNotice(staging); err != nil {
		return fmt.Errorf("%s: write NOTICE: %w", tokenizerOperation, err)
	}
	return promoteTokenizer(staging, dest, wantHash)
}

func installTokenizerFile(ctx context.Context, src, dest, wantHash string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	staging, cleanup, err := tokenizerStagingDir(dest)
	if err != nil {
		return err
	}
	defer cleanup()
	body, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("%s: read %s: %w", tokenizerOperation, src, err)
	}
	actual := sha256.Sum256(body)
	actualHex := hex.EncodeToString(actual[:])
	if actualHex != wantHash {
		return fmt.Errorf("%s: %s: SHA-256 mismatch: expected %s, actual %s", tokenizerOperation, evaltokenizer.PinnedVocabularyFile, wantHash, actualHex)
	}
	if err := os.WriteFile(filepath.Join(staging, evaltokenizer.PinnedVocabularyFile), body, 0o644); err != nil {
		return fmt.Errorf("%s: write staged vocabulary: %w", tokenizerOperation, err)
	}
	if err := writeTokenizerNotice(staging); err != nil {
		return fmt.Errorf("%s: write NOTICE: %w", tokenizerOperation, err)
	}
	return promoteTokenizer(staging, dest, wantHash)
}

func tokenizerStagingDir(dest string) (string, func(), error) {
	if dest == "" {
		return "", func() {}, fmt.Errorf("%s: destination directory is empty", tokenizerOperation)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", func() {}, fmt.Errorf("%s: create cache root for %s: %w", tokenizerOperation, dest, err)
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".staging-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("%s: mkdir staging: %w", tokenizerOperation, err)
	}
	return staging, func() { _ = os.RemoveAll(staging) }, nil
}

func promoteTokenizer(staging, dest, wantHash string) error {
	_, err := os.Lstat(dest)
	switch {
	case os.IsNotExist(err):
		if err := os.Rename(staging, dest); err != nil {
			return fmt.Errorf("%s: atomic first install %s -> %s: %w", tokenizerOperation, staging, dest, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("%s: inspect destination %s: %w", tokenizerOperation, dest, err)
	}
	actual, hashErr := fileSHA256(filepath.Join(dest, evaltokenizer.PinnedVocabularyFile))
	if hashErr != nil || actual != wantHash {
		return fmt.Errorf("%s: destination %s already exists but is not the pinned artifact; left untouched (expected %s, actual %s, error %v)", tokenizerOperation, dest, wantHash, actual, hashErr)
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("%s: remove redundant staging %s: %w", tokenizerOperation, staging, err)
	}
	return nil
}

func writeTokenizerNotice(dir string) error {
	body := "graphi SW-266 real-tokenizer artifact\n\n" +
		"tokenizer_id: " + evaltokenizer.TokenizerID + "\n" +
		"source: " + evaltokenizer.PinnedVocabularyURL + "\n" +
		"sha256: " + evaltokenizer.PinnedVocabularySHA256 + "\n"
	return os.WriteFile(filepath.Join(dir, "NOTICE"), []byte(body), 0o644)
}

func fileSHA256(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
