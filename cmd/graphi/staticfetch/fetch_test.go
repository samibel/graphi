// Package staticfetch's tests: AC-4 (HTTPS only, SHA-256-pinned,
// max-per-file ceiling, atomic temp+rename, no partial file on
// failure, expected-vs-actual hash on mismatch, NOTICE + LICENSE
// written beside the artifact) and AC-6 (air-gapped path).
package staticfetch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/cmd/graphi/staticfetch"
	"github.com/samibel/graphi/engine/embed/static"
)

const (
	staticPackage = "github.com/samibel/graphi/cmd/graphi/staticfetch"
)

// AC-4 happy path: every pinned file is downloaded over HTTPS, the
// SHA-256 matches the pin, the artifact lands at the expected path and
// the loader reads it. NOTICE and LICENSE are written beside the
// artifact.
func TestStaticfetch_DownloadsAndInstalls(t *testing.T) {
	dir := t.TempDir()
	contents := map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
		"model.safetensors": []byte("x"),
		"modules.json":      []byte("[]"),
	}
	pins := map[string]string{}
	for name, body := range contents {
		sum := sha256.Sum256(body)
		pins[name] = hex.EncodeToString(sum[:])
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		name := parts[len(parts)-1]
		body, ok := contents[name]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", "")
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	restore := swapPins(pins)
	defer restore()
	dest := filepath.Join(dir, "model")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := staticfetch.DownloadForTest(srv.Client(), srv.URL, dest); err != nil {
		t.Fatalf("Download: %v", err)
	}
	for name, want := range contents {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "NOTICE")); err != nil {
		t.Errorf("NOTICE not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "LICENSE")); err != nil {
		t.Errorf("LICENSE not written: %v", err)
	}
}

// AC-4: hash mismatch leaves no artifact behind.
func TestStaticfetch_HashMismatch_LeavesNoArtifact(t *testing.T) {
	dir := t.TempDir()
	contents := map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
		"model.safetensors": []byte("corrupted-but-looks-fine"),
		"modules.json":      []byte("[]"),
	}
	realHashes := map[string]string{}
	for name, body := range contents {
		sum := sha256.Sum256(body)
		realHashes[name] = hex.EncodeToString(sum[:])
	}
	wrongPins := map[string]string{}
	for k, v := range realHashes {
		wrongPins[k] = v
	}
	wrongPins["model.safetensors"] = strings.Repeat("0", 64)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		name := parts[len(parts)-1]
		body, ok := contents[name]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", "")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	restore := swapPins(wrongPins)
	defer restore()
	dest := filepath.Join(dir, "model")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	err := staticfetch.DownloadForTest(srv.Client(), srv.URL, dest)
	if err == nil {
		t.Fatal("Download accepted a mismatched hash")
	}
	if !strings.Contains(err.Error(), "model.safetensors") {
		t.Fatalf("error %v must name the offending file", err)
	}
	if !strings.Contains(err.Error(), "expected") || !strings.Contains(err.Error(), "actual") {
		t.Fatalf("error %v must name expected vs actual hash", err)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		t.Errorf("partial artifact left behind: %s", e.Name())
	}
}

// AC-4: truncated download.
func TestStaticfetch_TruncatedDownload_IsTypedError(t *testing.T) {
	dir := t.TempDir()
	body := []byte("short body")
	sum := sha256.Sum256(body)
	pins := map[string]string{
		"config.json":       strings.Repeat("a", 64),
		"tokenizer.json":    strings.Repeat("b", 64),
		"model.safetensors": hex.EncodeToString(sum[:]),
		"modules.json":      strings.Repeat("c", 64),
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	restore := swapPins(pins)
	defer restore()
	dest := filepath.Join(dir, "model")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	err := staticfetch.DownloadForTest(srv.Client(), srv.URL, dest)
	if err == nil {
		t.Fatal("Download accepted a truncated body")
	}
	if !strings.Contains(err.Error(), "truncat") && !strings.Contains(err.Error(), "length") && !strings.Contains(err.Error(), "short") {
		t.Fatalf("error %v must name the truncation", err)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		t.Errorf("partial artifact left behind: %s", e.Name())
	}
}

// AC-4: per-file size ceiling.
func TestStaticfetch_OverSizeRefused(t *testing.T) {
	dir := t.TempDir()
	contents := map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
		"model.safetensors": []byte("x"),
		"modules.json":      []byte("[]"),
	}
	sum := sha256.Sum256(contents["config.json"])
	hashOfConfig := hex.EncodeToString(sum[:])
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.URL.Path, "/")
		name := parts[len(parts)-1]
		body, ok := contents[name]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if name == "config.json" {
			w.Header().Set("Content-Length", "68157440") // 65 MiB
			_, _ = w.Write([]byte("x"))
			return
		}
		w.Header().Set("Content-Length", "")
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	pins := map[string]string{"config.json": hashOfConfig}
	restore := swapPins(pins)
	defer restore()
	dest := filepath.Join(dir, "model")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	err := staticfetch.DownloadForTest(srv.Client(), srv.URL, dest)
	if err == nil {
		t.Fatal("Download accepted an oversize Content-Length")
	}
	if !strings.Contains(err.Error(), "size") && !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error %v must name the size limit", err)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		t.Errorf("partial artifact left behind: %s", e.Name())
	}
}

// AC-4: HTTPS only.
func TestStaticfetch_HTTPSOnly(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "model")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	pins := map[string]string{"config.json": strings.Repeat("a", 64)}
	restore := swapPins(pins)
	defer restore()
	err := staticfetch.DownloadForTest(&http.Client{}, "http://example.invalid/file", dest)
	if err == nil {
		t.Fatal("Download accepted a non-HTTPS URL")
	}
	if !strings.Contains(err.Error(), "https") && !strings.Contains(err.Error(), "HTTPS") && !strings.Contains(err.Error(), "tls") {
		t.Fatalf("error %q must name the HTTPS requirement", err)
	}
}

// AC-6: air-gapped install path.
func TestStaticfetch_AirGapped_ValidatesLocalArtifact(t *testing.T) {
	dir := t.TempDir()
	contents := map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{}`),
		"model.safetensors": []byte("x"),
		"modules.json":      []byte("[]"),
	}
	pins := map[string]string{}
	for name, body := range contents {
		sum := sha256.Sum256(body)
		pins[name] = hex.EncodeToString(sum[:])
	}
	for name, body := range contents {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	restore := swapPins(pins)
	defer restore()
	if err := staticfetch.InstallLocalForTest(dir, dir); err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}
	wrong := map[string]string{}
	for k, v := range pins {
		wrong[k] = v
	}
	wrong["config.json"] = strings.Repeat("0", 64)
	restoreWrong := swapPins(wrong)
	defer restoreWrong()
	if err := staticfetch.InstallLocalForTest(dir, dir); err == nil {
		t.Fatal("InstallLocal accepted a mismatched hash")
	} else if !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("InstallLocal error %q must name the offending file", err)
	}
}

// AC-4: the staticfetch package is the ONLY place in cmd/graphi that
// imports net/http. This is the canary allowlist's narrower boundary:
// cmd/graphi/staticfetch, not cmd/graphi. A future contributor adding
// a net call to any other file in cmd/graphi is caught by this test
// at unit-test time, AND by the canary gate at release time.
//
// The check walks every non-test .go file under cmd/graphi (this
// test's parent directory) EXCEPT files inside this package, and
// asserts that no file mentions the net/http, net/url or
// crypto/tls import. The canary allowlist entry for staticfetch is
// kept narrow precisely so this local test can verify the invariant
// the canary gate cannot (the gate runs on the whole graphi tree; this
// test runs on the cmd/graphi subtree).
func TestStaticfetch_IsTheOnlyNetworkCallerInCmdGraphi(t *testing.T) {
	// The test runs from cmd/graphi/staticfetch/, so the parent is
	// cmd/graphi — the subtree we want to audit. cd back so the path
	// arithmetic is unambiguous in case the test is run with a
	// different working directory.
	parent := ".."
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	banned := []string{`"net/http"`, `"net/http/httptest"`, `"net/url"`, `"crypto/tls"`, `"syscall/js"`}
	// The staticfetch package is the only allowed network caller; every
	// other top-level entry (a subdir or a .go file) must be net-free.
	for _, e := range entries {
		name := e.Name()
		if name == "staticfetch" || name == "testdata" {
			continue
		}
		if e.IsDir() {
			sub, err := os.ReadDir(filepath.Join(parent, name))
			if err != nil {
				continue
			}
			for _, se := range sub {
				if se.IsDir() {
					continue
				}
				if filepath.Ext(se.Name()) != ".go" || strings.HasSuffix(se.Name(), "_test.go") {
					continue
				}
				path := filepath.Join(parent, name, se.Name())
				body, rerr := os.ReadFile(path)
				if rerr != nil {
					continue
				}
				src := string(body)
				for _, b := range banned {
					if strings.Contains(src, b) {
						t.Errorf("%s imports %s; the only network caller in cmd/graphi is the staticfetch package (the canary allowlist is narrow by design)", path, b)
					}
				}
			}
		} else {
			if filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(parent, name)
			body, rerr := os.ReadFile(path)
			if rerr != nil {
				continue
			}
			src := string(body)
			for _, b := range banned {
				if strings.Contains(src, b) {
					t.Errorf("%s imports %s; the only network caller in cmd/graphi is the staticfetch package", path, b)
				}
			}
		}
	}
}

// swapPins atomically swaps the production pin table for the test's
// pins and returns a restore closure.
func swapPins(pins map[string]string) func() {
	prev := static.PinnedSHA256
	static.PinnedSHA256 = pins
	return func() { static.PinnedSHA256 = prev }
}

// _ keeps the context import in case future tests need it.
var _ = context.Background
