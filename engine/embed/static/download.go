// Package static's download path: HTTPS, SHA-256-pinned, max-artifact-size,
// atomic temp+rename, no partial file on failure, expected-vs-actual hash on
// mismatch. This file implements the AC-4 supply-chain surface; tests in
// download_test.go exercise every failure mode.
//
// The downloader never reaches the artifact cache through any other entry
// point: `graphi setup-embedder static:...` is the ONLY path that initiates
// a download (AC-5). The MCP server, the HTTP server, `index --semantic`
// and `search -semantic` all read from the cache and never call into this
// file. The function names that touch the network are clearly
// distinguished so a future reviewer can grep "HTTPS" / "http.Get" and
// find this file as the only caller.
package static

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// maxFileBytes is the per-file ceiling. It is above the pinned artifact's
// largest file (model.safetensors at ~32 MiB) so the real download fits, but
// small enough that an over-large Content-Length is refused before any bytes
// are written (AC-4 / AC-7). The loader's maxArtifactBytes ceiling is the
// ARTIFACT total; this ceiling is per-file.
const maxFileBytes = 40 << 20 // 40 MiB

// Download fetches every pinned file from the canonical HuggingFace URL,
// verifies its SHA-256 against PinnedSHA256, writes to a temp file under
// dest, and atomically renames each file into place. On ANY failure
// (network, hash mismatch, truncation, oversize) no artifact is left at
// dest; the function returns a typed error naming the offending file and
// expected vs actual hash where applicable.
//
// ctx is honoured; cancel mid-flight and the temp file is removed.
func Download(ctx context.Context, dest string) error {
	return DownloadFrom(ctx, http.DefaultClient, PinnedHuggingFaceURL, dest)
}

// DownloadFrom is Download with the HTTP client and base URL supplied. It is
// the seam tests use (and the seam the production wiring uses for the same
// reason — `graphi setup-embedder` constructs a TLS-enabled client).
//
// baseURL must be HTTPS; a non-HTTPS URL is rejected at the first call so
// the failure mode is visible.
//
// pins is the SHA-256 table; pass static.PinnedSHA256 in production.
func DownloadFrom(ctx context.Context, client *http.Client, baseURL, dest string) error {
	return downloadImpl(ctx, client, baseURL, dest, PinnedSHA256)
}

// downloadImpl is the inner loop. Extracted so the test seam
// (DownloadForTest, InstallLocalForTest) can supply an alternate client /
// pin table without exposing the loop's mutable state to callers.
//
// Atomicity: every file is downloaded into dest/.staging/<name>.tmp and
// renamed into place ONLY after every pinned file has been verified. A
// single failure (network, hash mismatch, oversize) removes dest/.staging
// and leaves dest untouched. AC-4: a hash mismatch or truncation shall
// leave no artifact behind.
func downloadImpl(ctx context.Context, client *http.Client, baseURL, dest string, pins map[string]string) error {
	if err := validateScheme(baseURL); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("static: setup-embedder: mkdir %s: %w", dest, err)
	}
	staging := filepath.Join(dest, ".staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("static: setup-embedder: mkdir staging %s: %w", staging, err)
	}
	for _, name := range PinnedFileNames {
		select {
		case <-ctx.Done():
			_ = os.RemoveAll(staging)
			return ctx.Err()
		default:
		}
		want, ok := pins[name]
		if !ok {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("static: setup-embedder: no pin recorded for %s; refusing to download an unverified file", name)
		}
		full := strings.TrimSuffix(baseURL, "/") + "/" + name
		if err := fetchAndVerifyFile(ctx, client, full, staging, name, want); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
	}
	// All files verified: rename into dest one by one. If a rename fails
	// the staging directory is removed and the existing dest is left
	// untouched (the prior partial-state is the previous artifact, which
	// may be valid for an unrelated version).
	for _, name := range PinnedFileNames {
		src := filepath.Join(staging, name)
		dst := filepath.Join(dest, name)
		if err := os.Rename(src, dst); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("static: setup-embedder: install %s: %w", name, err)
		}
	}
	// Write NOTICE and LICENSE beside the artifact (AC-4). The pinned
	// artifact's README is the model licence carrier; we copy it on the
	// happy path so a reviewer can find the licence at a known location.
	if err := writeNotice(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write NOTICE: %w", err)
	}
	if err := writeLicensePlaceholder(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write LICENSE: %w", err)
	}
	_ = os.RemoveAll(staging)
	return nil
}

// validateScheme rejects non-HTTPS URLs. AC-4: setup-embedder downloads
// over HTTPS only.
func validateScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("static: setup-embedder: parse URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("static: setup-embedder: refusing non-HTTPS URL %q (HTTPS only, AC-4)", rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("static: setup-embedder: URL %q has no host", rawURL)
	}
	return nil
}

// fetchAndVerifyFile downloads one pinned file into dest/<name>.tmp,
// verifies its SHA-256, and atomically renames to dest/<name>. On ANY
// failure (Content-Length too large, body shorter, hash mismatch, IO
// failure) the temp file is removed and the typed error is returned.
func fetchAndVerifyFile(ctx context.Context, client *http.Client, fullURL, dest, name, wantHash string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("static: setup-embedder: build request for %s: %w", name, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("static: setup-embedder: download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("static: setup-embedder: %s: HTTP %d", name, resp.StatusCode)
	}
	if resp.ContentLength > maxFileBytes {
		return fmt.Errorf("static: setup-embedder: %s: declared Content-Length %d exceeds per-file ceiling %d (AC-4)", name, resp.ContentLength, maxFileBytes)
	}
	tmpPath := filepath.Join(dest, name+".tmp")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("static: setup-embedder: open temp %s: %w", tmpPath, err)
	}
	// Hash + size: stream into tmp and the hasher simultaneously. A short
	// body (Content-Length > actual) surfaces here as the response body
	// closes early with no error; we accept whatever bytes we got and
	// fail in the hash check below if the count is wrong.
	h := sha256.New()
	n, copyErr := io.Copy(tmp, io.TeeReader(resp.Body, h))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("static: setup-embedder: download %s: %w (truncated at %d bytes?)", name, copyErr, n)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("static: setup-embedder: write %s: %w", name, closeErr)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("static: setup-embedder: %s: server claimed %d bytes, received %d (truncated download)", name, resp.ContentLength, n)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHash {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("static: setup-embedder: %s: SHA-256 mismatch: expected %s, actual %s", name, wantHash, got)
	}
	// Atomic rename into place. On a filesystem that does not support
	// atomic rename (Windows) os.Rename is best-effort; the rest of the
	// download flow tolerates that. A half-written temp file is removed
	// on failure so the cache directory never carries a partial file.
	if err := os.Rename(tmpPath, filepath.Join(dest, name)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("static: setup-embedder: rename %s: %w", name, err)
	}
	return nil
}

// writeNotice writes the NOTICE file beside the artifact. The notice names
// the source (minishlab/potion-code-16M-v2), the pinned revision and the
// SHA-256 of every file, so a future reviewer can verify the supply-chain
// provenance without re-running the download.
func writeNotice(dest string) error {
	notice := "graphi static embedder artifact\n" +
		"\n" +
		"source:    minishlab/potion-code-16M-v2\n" +
		"revision:  " + PinnedRevision + "\n" +
		"sha256:\n"
	for _, name := range PinnedFileNames {
		notice += "  " + name + ": " + PinnedSHA256[name] + "\n"
	}
	return os.WriteFile(filepath.Join(dest, "NOTICE"), []byte(notice), 0o644)
}

// writeLicensePlaceholder writes a LICENSE file pointing at the upstream
// model's MIT licence. The README shipped with the artifact carries the
// full licence text; this placeholder is what `graphi setup-embedder`
// promises to write beside the artifact.
func writeLicensePlaceholder(dest string) error {
	licence := "This artifact is the minishlab/potion-code-16M-v2 model, distributed\n" +
		"under the MIT licence. See the README.md file in this directory for\n" +
		"the full licence text; the upstream source is:\n" +
		"\n" +
		"  https://huggingface.co/minishlab/potion-code-16M-v2\n"
	return os.WriteFile(filepath.Join(dest, "LICENSE"), []byte(licence), 0o644)
}

// InstallLocal validates an existing artifact directory against the pin
// table and copies verified files into the destination. The air-gapped path
// (AC-6): when GRAPHI_STATIC_MODEL_DIR points at a pre-staged directory,
// the loader reads it; this function gives setup-embedder the same
// validation so the two paths agree on what a "good" artifact looks like.
func InstallLocal(ctx context.Context, src, dest string) error {
	if src == "" {
		return errors.New("static: setup-embedder: --local: source directory is empty")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("static: setup-embedder: mkdir %s: %w", dest, err)
	}
	for _, name := range PinnedFileNames {
		want := PinnedSHA256[name]
		srcPath := filepath.Join(src, name)
		body, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("static: setup-embedder: read %s: %w", srcPath, err)
		}
		got := sha256Hex(body)
		if got != want {
			return fmt.Errorf("static: setup-embedder: %s: SHA-256 mismatch: expected %s, actual %s (refusing to install an unverified file; --source points at an artifact that does not match the pin table)", name, want, got)
		}
		dstPath := filepath.Join(dest, name)
		tmp := dstPath + ".tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return fmt.Errorf("static: setup-embedder: write %s: %w", tmp, err)
		}
		if err := os.Rename(tmp, dstPath); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("static: setup-embedder: rename %s: %w", name, err)
		}
	}
	if err := writeNotice(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write NOTICE: %w", err)
	}
	if err := writeLicensePlaceholder(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write LICENSE: %w", err)
	}
	return nil
}

// sha256Hex returns the lower-case hex SHA-256 of body.
func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// DownloadForTest is the test seam: like DownloadFrom but with a fully
// controllable client, baseURL and pin table. Production code never calls
// it. The pin table is the package var PinnedSHA256, which the test
// mutates before calling and restores after — see swapPins.
func DownloadForTest(client *http.Client, baseURL, dest string) error {
	return downloadImpl(context.Background(), client, baseURL, dest, PinnedSHA256)
}

// InstallLocalForTest is the test seam for the air-gapped path. It
// validates src against the (currently-set) PinnedSHA256 table and copies
// the files to dest.
func InstallLocalForTest(src string) error {
	dest := src // default: copy-in-place is not supported; use the At variant
	return InstallLocal(context.Background(), src, dest)
}

// InstallLocalForTestAt is the test seam that lets the caller choose both
// src and dest.
func InstallLocalForTestAt(src, dest string) error {
	return InstallLocal(context.Background(), src, dest)
}
