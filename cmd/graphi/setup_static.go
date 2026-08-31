// Package main's static-embedder setup path (SW-262). This file lives at
// cmd/graphi — NOT at engine/embed/static — because the only entry point
// that initiates a download is `graphi setup-embedder static:<model>@<rev>`,
// and the default graphi binary must NOT link an outbound HTTP client
// (engine/embed/static is reachable from index/search/MCP/HTTP via the
// registry's registered scheme; putting net/http there violates AC-5 and
// AC-8 structurally, not by discipline).
//
// The download is HTTPS-only, SHA-256-pinned against engine/embed/static's
// PinnedSHA256 table, atomic via a temp-file-plus-rename into a staging
// subdir, and refuses any Content-Length above the per-file ceiling
// before writing to disk. On any failure (network, hash mismatch, truncate,
// oversize) no artifact is left at dest. The NOTICE and LICENSE files are
// written beside the artifact on success so a reviewer can audit the
// supply chain without re-running the download.
//
// What this file does NOT do:
//   - It does not register a scheme or embed anything into the engine. The
//     engine/embed/static package owns the embedder type; this file only
//     installs the artifact the embedder reads from.
//   - It does not parse a flag set; the flag parsing stays in runStaticSetup
//     in setup.go so the user-facing message shape is unchanged.
//   - It does not share a Go symbol with engine/embed/static beyond the
//     PinnedSHA256 / PinnedRevision / PinnedFileNames / PinnedHuggingFaceURL
//     constants pins.go exports — those are data, not behaviour, so the
//     default-graph isolation holds.
package main

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

	"github.com/samibel/graphi/engine/embed/static"
)

// staticMaxFileBytes is the per-file ceiling the download enforces BEFORE
// any bytes are written. It is above the pinned artifact's largest file
// (model.safetensors at ~32 MiB) so the real download fits, but small
// enough that an over-large Content-Length is refused (AC-4).
const staticMaxFileBytes = 40 << 20 // 40 MiB

// StaticDownload fetches every pinned file from the canonical HuggingFace
// URL, verifies its SHA-256 against engine/embed/static.PinnedSHA256, writes
// to a temp file under dest/.staging/<name>.tmp and atomically renames each
// file into place. On ANY failure (network, hash mismatch, truncation,
// oversize) no artifact is left at dest; the function returns a typed
// error naming the offending file and expected vs actual hash where
// applicable.
//
// This is the ONLY function in the production codebase that reaches the
// network on behalf of the static embedder. It is called exclusively from
// `graphi setup-embedder static:<model>@<revision>` (runStaticSetup in
// setup.go). The MCP server, the HTTP server, `index --semantic` and
// `search -semantic` read the cached artifact and never call into this
// function.
func StaticDownload(ctx context.Context, dest string) error {
	return staticDownloadImpl(ctx, http.DefaultClient, static.PinnedHuggingFaceURL, dest, static.PinnedSHA256)
}

// StaticInstallLocal validates an existing artifact directory against the
// pin table and copies verified files into the destination. The air-gapped
// path (AC-6): when the user passes `--local <dir>`, the loader reads it
// via this function which validates every file's SHA-256 against the pin
// table. A mismatch surfaces as a typed error naming the offending file and
// expected vs actual hash — never a warning.
//
// src must be a directory containing the four pinned files. dest is the
// install location (typically the model cache under $XDG_CACHE_HOME).
func StaticInstallLocal(ctx context.Context, src, dest string) error {
	if src == "" {
		return errors.New("static: setup-embedder: --local: source directory is empty")
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("static: setup-embedder: mkdir %s: %w", dest, err)
	}
	for _, name := range static.PinnedFileNames {
		want := static.PinnedSHA256[name]
		srcPath := filepath.Join(src, name)
		body, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("static: setup-embedder: read %s: %w", srcPath, err)
		}
		got := sha256Hex(body)
		if got != want {
			return fmt.Errorf("static: setup-embedder: %s: SHA-256 mismatch: expected %s, actual %s (refusing to install an unverified file; --local points at an artifact that does not match the pin table)", name, want, got)
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
	if err := staticWriteNotice(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write NOTICE: %w", err)
	}
	if err := staticWriteLicensePlaceholder(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write LICENSE: %w", err)
	}
	return nil
}

// staticDownloadImpl is the inner loop. Atomicity: every file is
// downloaded into dest/.staging/<name>.tmp and renamed into place ONLY
// after every pinned file has been verified. A single failure (network,
// hash mismatch, oversize) removes dest/.staging and leaves dest
// untouched — a hash mismatch or truncation leaves no artifact behind
// (AC-4).
func staticDownloadImpl(ctx context.Context, client *http.Client, baseURL, dest string, pins map[string]string) error {
	if err := staticValidateScheme(baseURL); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("static: setup-embedder: mkdir %s: %w", dest, err)
	}
	staging := filepath.Join(dest, ".staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("static: setup-embedder: mkdir staging %s: %w", staging, err)
	}
	for _, name := range static.PinnedFileNames {
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
		if err := staticFetchAndVerifyFile(ctx, client, full, staging, name, want); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
	}
	for _, name := range static.PinnedFileNames {
		src := filepath.Join(staging, name)
		dst := filepath.Join(dest, name)
		if err := os.Rename(src, dst); err != nil {
			_ = os.RemoveAll(staging)
			return fmt.Errorf("static: setup-embedder: install %s: %w", name, err)
		}
	}
	if err := staticWriteNotice(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write NOTICE: %w", err)
	}
	if err := staticWriteLicensePlaceholder(dest); err != nil {
		return fmt.Errorf("static: setup-embedder: write LICENSE: %w", err)
	}
	_ = os.RemoveAll(staging)
	return nil
}

// staticValidateScheme rejects non-HTTPS URLs. AC-4: setup-embedder
// downloads over HTTPS only.
func staticValidateScheme(rawURL string) error {
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

// staticFetchAndVerifyFile downloads one pinned file into dest/<name>.tmp,
// verifies its SHA-256, and atomically renames to dest/<name>. On ANY
// failure (Content-Length too large, body shorter, hash mismatch, IO
// failure) the temp file is removed and the typed error is returned.
func staticFetchAndVerifyFile(ctx context.Context, client *http.Client, fullURL, dest, name, wantHash string) error {
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
	if resp.ContentLength > staticMaxFileBytes {
		return fmt.Errorf("static: setup-embedder: %s: declared Content-Length %d exceeds per-file ceiling %d (AC-4)", name, resp.ContentLength, staticMaxFileBytes)
	}
	tmpPath := filepath.Join(dest, name+".tmp")
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("static: setup-embedder: open temp %s: %w", tmpPath, err)
	}
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
	if err := os.Rename(tmpPath, filepath.Join(dest, name)); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("static: setup-embedder: rename %s: %w", name, err)
	}
	return nil
}

// staticWriteNotice writes the NOTICE file beside the artifact. The
// notice names the source (minishlab/potion-code-16M-v2), the pinned
// revision and the SHA-256 of every file, so a future reviewer can
// verify the supply-chain provenance without re-running the download.
func staticWriteNotice(dest string) error {
	notice := "graphi static embedder artifact\n" +
		"\n" +
		"source:    minishlab/potion-code-16M-v2\n" +
		"revision:  " + static.PinnedRevision + "\n" +
		"sha256:\n"
	for _, name := range static.PinnedFileNames {
		notice += "  " + name + ": " + static.PinnedSHA256[name] + "\n"
	}
	return os.WriteFile(filepath.Join(dest, "NOTICE"), []byte(notice), 0o644)
}

// staticWriteLicensePlaceholder writes a LICENSE file pointing at the
// upstream model's MIT licence.
func staticWriteLicensePlaceholder(dest string) error {
	licence := "This artifact is the minishlab/potion-code-16M-v2 model, distributed\n" +
		"under the MIT licence. See the README.md file in this directory for\n" +
		"the full licence text; the upstream source is:\n" +
		"\n" +
		"  https://huggingface.co/minishlab/potion-code-16M-v2\n"
	return os.WriteFile(filepath.Join(dest, "LICENSE"), []byte(licence), 0o644)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// StaticDownloadForTest is the test seam: like StaticDownload but with a
// controllable client and baseURL. Production code never calls it. The
// pin table is the package var engine/embed/static.PinnedSHA256 which the
// test mutates before calling and restores after.
func StaticDownloadForTest(client *http.Client, baseURL, dest string) error {
	return staticDownloadImpl(context.Background(), client, baseURL, dest, static.PinnedSHA256)
}

// StaticInstallLocalForTest is the test seam for the air-gapped path.
// It validates src against the (currently-set) PinnedSHA256 table and
// copies the files to dest.
func StaticInstallLocalForTest(src string) error {
	return StaticInstallLocal(context.Background(), src, src)
}

// StaticInstallLocalForTestAt lets the caller choose both src and dest.
func StaticInstallLocalForTestAt(src, dest string) error {
	return StaticInstallLocal(context.Background(), src, dest)
}
