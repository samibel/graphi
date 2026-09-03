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
	"bytes"
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
	return newHTTPSOnlyClientFor("staticfetch: setup-embedder", "AC-4: HTTPS-only on every hop")
}

func newHTTPSOnlyClientFor(operation, redirectPolicy string) *http.Client {
	return &http.Client{
		Timeout: 5 * 60 * 1e9, // 5 minutes; large artifacts over slow links
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" {
				return fmt.Errorf("%s: redirect to non-HTTPS URL %q refused (%s)", operation, req.URL, redirectPolicy)
			}
			if len(via) >= 10 {
				return fmt.Errorf("%s: too many redirects (>= 10)", operation)
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
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: create cache root for %s: %w", dest, err)
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
	if err := writeModelLicense(staging); err != nil {
		cleanupStaging()
		return fmt.Errorf("staticfetch: setup-embedder: write LICENSE: %w", err)
	}
	if err := promoteImmutable(staging, dest); err != nil {
		cleanupStaging()
		return err
	}
	return nil
}

// atomicCopyDir copies the named files from src to dest via a staging
// directory, then promotes the immutable revision directory with one rename.
func atomicCopyDir(src, dest string, names []string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: create cache root for %s: %w", dest, err)
	}
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
	if err := writeModelLicense(staging); err != nil {
		cleanupStaging()
		return fmt.Errorf("staticfetch: setup-embedder: write LICENSE: %w", err)
	}
	if err := promoteImmutable(staging, dest); err != nil {
		cleanupStaging()
		return err
	}
	return nil
}

// promoteImmutable consumes staging with one atomic rename when dest does not
// exist. Cache directories are revision-addressed and immutable: replacing a
// populated directory portably requires moving the old directory aside before
// moving the new one in, which creates an interruption window with no dest.
// Therefore an existing verified cache is a warm no-op, while an existing
// invalid/partial cache is left untouched and refused with an actionable error.
func promoteImmutable(staging, dest string) error {
	_, err := os.Lstat(dest)
	switch {
	case os.IsNotExist(err):
		if err := os.Rename(staging, dest); err != nil {
			return fmt.Errorf("staticfetch: setup-embedder: atomic first install %s -> %s: %w", staging, dest, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("staticfetch: setup-embedder: inspect destination %s: %w", dest, err)
	}

	if err := static.VerifyPins(dest); err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: destination %s already exists but is not the pinned artifact; left untouched to avoid a non-atomic replacement: %w (move the directory aside and retry)", dest, err)
	}
	for _, name := range []string{"NOTICE", "LICENSE"} {
		want, err := os.ReadFile(filepath.Join(staging, name))
		if err != nil {
			return fmt.Errorf("staticfetch: setup-embedder: read staged %s: %w", name, err)
		}
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || !bytes.Equal(got, want) {
			return fmt.Errorf("staticfetch: setup-embedder: destination %s has the pinned model files but missing or stale %s; left untouched to avoid a non-atomic repair (move the directory aside and retry)", dest, name)
		}
	}
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("staticfetch: setup-embedder: verified cache already installed at %s, but remove redundant staging %s: %w", dest, staging, err)
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

func validateSchemeFor(rawURL, operation string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%s: parse URL %q: %w", operation, rawURL, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s: refusing non-HTTPS URL %q (HTTPS only)", operation, rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("%s: URL %q has no host", operation, rawURL)
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
	return fetchAndVerifyPinnedFile(ctx, client, fullURL, dest, name, wantHash, MaxFileBytes, "staticfetch: setup-embedder")
}

func fetchAndVerifyPinnedFile(ctx context.Context, client *http.Client, fullURL, dest, name, wantHash string, maxBytes int64, operation string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return fmt.Errorf("%s: build request for %s: %w", operation, name, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: download %s: %w", operation, name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s: HTTP %d", operation, name, resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return fmt.Errorf("%s: %s: declared Content-Length %d exceeds per-file ceiling %d", operation, name, resp.ContentLength, maxBytes)
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	tmpPath := filepath.Join(dest, name+".tmp")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("%s: open temp %s: %w", operation, tmpPath, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(tmp, io.TeeReader(limited, h))
	closeErr := tmp.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		gotHash := hex.EncodeToString(h.Sum(nil))
		return fmt.Errorf("%s: download %s: %w (truncated at %d bytes; expected %s, actual %s)", operation, name, copyErr, n, wantHash, gotHash)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("%s: write %s: %w", operation, name, closeErr)
	}
	if n > maxBytes {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("%s: %s: body exceeds per-file ceiling %d (received %d bytes; truncated download or wrong content)", operation, name, maxBytes, n)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		_ = os.Remove(tmpPath)
		gotHash := hex.EncodeToString(h.Sum(nil))
		return fmt.Errorf("%s: %s: truncated download: server claimed %s bytes, received %d (the body is corrupt; expected SHA-256 %s, computed %s)", operation, name, strconv.FormatInt(resp.ContentLength, 10), n, wantHash, gotHash)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != wantHash {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("%s: %s: SHA-256 mismatch: expected %s, actual %s", operation, name, wantHash, got)
	}
	if err := os.Rename(tmpPath, filepath.Join(dest, name)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("%s: rename %s: %w", operation, name, err)
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

// writeModelLicense writes the complete MIT licence beside the pinned model,
// not a link that becomes unavailable in an air-gapped installation.
func writeModelLicense(dest string) error {
	const license = `MIT License

Copyright (c) 2024 Thomas van Dongen

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`
	return os.WriteFile(filepath.Join(dest, "LICENSE"), []byte(license), 0o644)
}
