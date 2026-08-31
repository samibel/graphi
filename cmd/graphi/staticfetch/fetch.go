// Package staticfetch is the static-embedder supply-chain surface. It
// holds the ONLY network code in the production graphi tree — the
// HTTPS download that installs the pinned model artifact — and is the
// reason the canary allowlist can stay narrow: the gate exempts this
// subpackage, not the whole cmd/graphi tree. Every other file in
// cmd/graphi is offline.
//
// The download is HTTPS-only, SHA-256-pinned against
// engine/embed/static.PinnedSHA256, max-per-file-ceiling, atomic via
// a temp-file-plus-rename into a staging dir, and prints expected vs
// actual SHA on any hash mismatch / truncation.
//
// What this file does NOT do:
//   - register a scheme or embed anything; engine/embed/static owns
//     the embedder type;
//   - share a Go symbol with engine/embed/static beyond the Pinned*
//     constants pins.go exports.
package staticfetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samibel/graphi/engine/embed/static"
)

// MaxFileBytes is the per-file ceiling. The download enforces this in
// two places so a chunked / unknown-length response cannot bypass it:
//  1. http.Response.ContentLength (when present) is checked BEFORE
//     writing to disk;
//  2. the response body is wrapped in io.LimitReader(max+1) so a
//     Content-Length-mismatch or chunked transfer that exceeds the
//     ceiling aborts at the read.
//
// The ceiling is above the pinned artifact's largest file
// (model.safetensors at ~32 MiB) so the real download fits, but small
// enough that an over-large Content-Length is refused.
const MaxFileBytes = 64 << 20 // 64 MiB; ceiling is also a hard read cap

// Download fetches every pinned file from the canonical HuggingFace
// URL and installs them atomically into dest, verifying each file's
// SHA-256 against engine/embed/static.PinnedSHA256. On any failure
// (network, hash mismatch, truncation, oversize) the destination is
// untouched — a partial or mixed cache is impossible.
func Download(ctx context.Context, dest string) error {
	client := newHTTPSOnlyClient()
	return downloadImpl(ctx, client, static.PinnedHuggingFaceURL, dest, static.PinnedSHA256)
}

// InstallLocal validates an existing artifact directory against the
// pin table and copies verified files into the destination atomically.
// The air-gapped path (AC-6): when the user passes --local <dir>, this
// is the only install code path that touches the cache.
func InstallLocal(ctx context.Context, src, dest string) error {
	if src == "" {
		return fmt.Errorf("staticfetch: setup-embedder: --local: source directory is empty")
	}
	if err := static.VerifyPins(src); err != nil {
		return err
	}
	return atomicCopyDir(src, dest, static.PinnedFileNames)
}

// newHTTPSOnlyClient returns an http.Client whose redirect checker
// refuses any non-HTTPS hop, preventing the default
// http.DefaultClient.CheckRedirect from silently downgrading a
// https://huggingface.co/... response to http://... on a 30x. The
// client also pins a sane timeout so a slow-loris server does not
// stall the install indefinitely.
func newHTTPSOnlyClient() *http.Client {
	return &http.Client{
		Timeout: 5 * 60 * 1e9, // 5 minutes; large artifacts over slow links
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("staticfetch: setup-embedder: redirect to non-HTTPS URL %q refused (AC-4: HTTPS-only on every hop)", req.URL)
			}
			if len(via) >= 10 {
				return fmt.Errorf("staticfetch: setup-embedder: too many redirects (>= 10)")
			}
			return nil
		},
	}
}

// downloadImpl is the inner loop. Atomicity: every file is downloaded
// into a fresh staging directory and the install is an atomic directory
// swap into dest. A single failure (network, hash mismatch, oversize,
// truncation) removes the staging directory and leaves dest
// untouched — a hash mismatch or truncation leaves no artifact behind.
func downloadImpl(ctx context.Context, client *http.Client, baseURL, dest string, pins map[string]string) error {
	if err := validateScheme(baseURL); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".staging-*")
	if err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: mkdir staging: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(staging) }

	for _, name := range static.PinnedFileNames {
		select {
		case <-ctx.Done():
			cleanupStaging()
			return ctx.Err()
		default:
		}
		want, ok := pins[name]
		if !ok {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: no pin recorded for %s; refusing to download an unverified file", name)
		}
		full := strings.TrimSuffix(baseURL, "/") + "/" + name
		if err := fetchAndVerifyFile(ctx, client, full, staging, name, want); err != nil {
			cleanupStaging()
			return err
		}
	}
	if err := writeNotice(staging); err != nil {
		cleanupStaging()
		return fmt.Errorf("staticfetch: setup-embedder: write NOTICE: %w", err)
	}
	if err := writeLicensePlaceholder(staging); err != nil {
		cleanupStaging()
		return fmt.Errorf("staticfetch: setup-embedder: write LICENSE: %w", err)
	}
	prior := ""
	if _, err := os.Stat(dest); err == nil {
		prior, err = os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".prior-*")
		if err != nil {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: mkdir prior aside: %w", err)
		}
		_ = os.RemoveAll(prior)
		if err := os.Rename(dest, prior); err != nil {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: move prior %s -> %s: %w", dest, prior, err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		if prior != "" {
			_ = os.Rename(prior, dest)
		}
		return fmt.Errorf("staticfetch: setup-embedder: atomic install %s -> %s: %w", staging, dest, err)
	}
	if prior != "" {
		_ = os.RemoveAll(prior)
	}
	return nil
}

// atomicCopyDir copies the named files from src to dest via a staging
// directory, then atomically swaps the staging directory into dest.
// See downloadImpl for the rename-old-aside pattern (the "no partial
// artifact behind" AC-4 contract).
func atomicCopyDir(src, dest string, names []string) error {
	staging, err := os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".staging-*")
	if err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: mkdir staging: %w", err)
	}
	cleanupStaging := func() { _ = os.RemoveAll(staging) }

	for _, name := range names {
		srcPath := filepath.Join(src, name)
		dstPath := filepath.Join(staging, name)
		body, err := os.ReadFile(srcPath)
		if err != nil {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: read %s: %w", srcPath, err)
		}
		sum := sha256.Sum256(body)
		gotHash := hex.EncodeToString(sum[:])
		if want, ok := static.PinnedSHA256[name]; ok && gotHash != want {
			cleanupStaging()
			return &static.PinMismatchError{File: name, Path: srcPath, Expected: want, Actual: gotHash}
		}
		if err := os.WriteFile(dstPath, body, 0o644); err != nil {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: write %s: %w", dstPath, err)
		}
	}
	if err := writeNotice(staging); err != nil {
		cleanupStaging()
		return fmt.Errorf("staticfetch: setup-embedder: write NOTICE: %w", err)
	}
	if err := writeLicensePlaceholder(staging); err != nil {
		cleanupStaging()
		return fmt.Errorf("staticfetch: setup-embedder: write LICENSE: %w", err)
	}
	prior := ""
	if _, err := os.Stat(dest); err == nil {
		prior, err = os.MkdirTemp(filepath.Dir(dest), filepath.Base(dest)+".prior-*")
		if err != nil {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: mkdir prior aside: %w", err)
		}
		_ = os.RemoveAll(prior)
		if err := os.Rename(dest, prior); err != nil {
			cleanupStaging()
			return fmt.Errorf("staticfetch: setup-embedder: move prior %s -> %s: %w", dest, prior, err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		_ = os.RemoveAll(staging)
		if prior != "" {
			_ = os.Rename(prior, dest)
		}
		return fmt.Errorf("staticfetch: setup-embedder: atomic install %s -> %s: %w", staging, dest, err)
	}
	if prior != "" {
		_ = os.RemoveAll(prior)
	}
	return nil
}

// validateScheme rejects non-HTTPS URLs. AC-4: setup-embedder
// downloads over HTTPS only.
func validateScheme(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: parse URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("staticfetch: setup-embedder: refusing non-HTTPS URL %q (HTTPS only, AC-4)", rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("staticfetch: setup-embedder: URL %q has no host", rawURL)
	}
	return nil
}

// fetchAndVerifyFile downloads one pinned file into dest/<name>.tmp,
// verifies its SHA-256, and atomically renames to dest/<name>. The
// response body is wrapped in io.LimitReader(max+1) so a chunked or
// unknown-length response that exceeds the per-file ceiling is
// truncated at the boundary, surfaced as a typed error, and the temp
// file is removed.
func fetchAndVerifyFile(ctx context.Context, client *http.Client, fullURL, dest, name, wantHash string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: build request for %s: %w", name, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("staticfetch: setup-embedder: %s: HTTP %d", name, resp.StatusCode)
	}
	if resp.ContentLength > MaxFileBytes {
		return fmt.Errorf("staticfetch: setup-embedder: %s: declared Content-Length %d exceeds per-file ceiling %d (AC-4)", name, resp.ContentLength, MaxFileBytes)
	}
	limited := io.LimitReader(resp.Body, MaxFileBytes+1)
	tmpPath := filepath.Join(dest, name+".tmp")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: open temp %s: %w", tmpPath, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(tmp, io.TeeReader(limited, h))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("staticfetch: setup-embedder: download %s: %w (truncated at %d bytes)", name, copyErr, n)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("staticfetch: setup-embedder: write %s: %w", name, closeErr)
	}
	if n > MaxFileBytes {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("staticfetch: setup-embedder: %s: body exceeds per-file ceiling %d (received %d bytes; truncated download or wrong content)", name, MaxFileBytes, n)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		_ = os.Remove(tmpPath)
		gotHash := hex.EncodeToString(h.Sum(nil))
		return fmt.Errorf("staticfetch: setup-embedder: %s: truncated download: server claimed %s bytes, received %d (the body is corrupt; expected SHA-256 %s, computed %s)", name, strconv.FormatInt(resp.ContentLength, 10), n, wantHash, gotHash)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHash {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("staticfetch: setup-embedder: %s: SHA-256 mismatch: expected %s, actual %s", name, wantHash, got)
	}
	if err := os.Rename(tmpPath, filepath.Join(dest, name)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("staticfetch: setup-embedder: rename %s: %w", name, err)
	}
	return nil
}

// writeNotice writes the NOTICE file beside the artifact.
func writeNotice(dest string) error {
	notice := "graphi static embedder artifact\n" +
		"\n" +
		"source:    minishlab/" + static.ModelID + "\n" +
		"revision:  " + static.PinnedRevision + "\n" +
		"sha256:\n"
	for _, name := range static.PinnedFileNames {
		notice += "  " + name + ": " + static.PinnedSHA256[name] + "\n"
	}
	return os.WriteFile(filepath.Join(dest, "NOTICE"), []byte(notice), 0o644)
}

// writeLicensePlaceholder writes a LICENSE file pointing at the
// upstream model's MIT licence.
func writeLicensePlaceholder(dest string) error {
	licence := "This artifact is the minishlab/" + static.ModelID + " model, distributed\n" +
		"under the MIT licence. The full licence text lives in the upstream\n" +
		"source repository: https://huggingface.co/minishlab/" + static.ModelID + "\n"
	return os.WriteFile(filepath.Join(dest, "LICENSE"), []byte(licence), 0o644)
}
