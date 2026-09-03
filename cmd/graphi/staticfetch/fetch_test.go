// Package staticfetch's tests: AC-4 (HTTPS only, SHA-256-pinned,
// max-per-file ceiling, atomic temp+rename, no partial file on
// failure, expected-vs-actual hash on mismatch, NOTICE + LICENSE
// written beside the artifact) and AC-6 (air-gapped path).
package staticfetch_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		parts := strings.Split(r.URL.Path, "/")
		name := parts[len(parts)-1]
		body, ok := contents[name]
		if !ok {
			return staticResponse(http.StatusNotFound, []byte("not found"), int64(len("not found"))), nil
		}
		return staticResponse(http.StatusOK, body, -1), nil
	})}
	restore := swapPins(pins)
	defer restore()
	// First install: neither the cache root nor the revision directory exists.
	// Production defaults to $XDG_CACHE_HOME/graphi/models/<model@revision>, so
	// arranging the parent here would hide the most common installation path.
	dest := filepath.Join(dir, "graphi", "models", "model")
	if _, err := os.Stat(filepath.Dir(dest)); !os.IsNotExist(err) {
		t.Fatalf("test precondition: cache parent exists or stat failed unexpectedly: %v", err)
	}
	if err := staticfetch.DownloadForTest(client, "https://example.invalid/model", dest); err != nil {
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

// AC-4/AC-9 first install: the cache hierarchy does not exist on a fresh
// machine. InstallLocal shares the exact staging/promotion path with Download
// and lets this filesystem contract run without a localhost test server.
func TestStaticfetch_FirstInstallCreatesCacheRoot(t *testing.T) {
	src := t.TempDir()
	contents := map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
		"model.safetensors": []byte("model"),
		"modules.json":      []byte("[]"),
	}
	pins := map[string]string{}
	for name, body := range contents {
		if err := os.WriteFile(filepath.Join(src, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		pins[name] = hex.EncodeToString(sum[:])
	}
	restore := swapPins(pins)
	defer restore()

	root := t.TempDir()
	dest := filepath.Join(root, "graphi", "models", "model")
	if _, err := os.Stat(filepath.Dir(dest)); !os.IsNotExist(err) {
		t.Fatalf("test precondition: cache parent exists or stat failed unexpectedly: %v", err)
	}
	if err := staticfetch.InstallLocalForTest(src, dest); err != nil {
		t.Fatalf("first install: %v", err)
	}
	for name, want := range contents {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read installed %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("installed %s = %q, want %q", name, got, want)
		}
	}
	license, err := os.ReadFile(filepath.Join(dest, "LICENSE"))
	if err != nil {
		t.Fatalf("read installed LICENSE: %v", err)
	}
	if !strings.Contains(string(license), "MIT License") ||
		!strings.Contains(string(license), "Copyright (c) 2024 Thomas van Dongen") ||
		!strings.Contains(string(license), "Permission is hereby granted, free of charge") {
		t.Fatalf("LICENSE is not the full model licence text: %q", license)
	}
}

// AC-4 interrupted promotion: revision-addressed cache directories are
// immutable. Replacing an existing non-empty directory requires two renames
// and creates a crash window with no canonical destination, so an invalid
// existing cache must be left byte-for-byte untouched and return non-zero.
func TestStaticfetch_ExistingInvalidCacheIsNotReplaced(t *testing.T) {
	src := t.TempDir()
	contents := map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
		"model.safetensors": []byte("model"),
		"modules.json":      []byte("[]"),
	}
	pins := map[string]string{}
	for name, body := range contents {
		if err := os.WriteFile(filepath.Join(src, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		pins[name] = hex.EncodeToString(sum[:])
	}
	restore := swapPins(pins)
	defer restore()

	dest := filepath.Join(t.TempDir(), "model")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	const sentinel = "existing cache must survive"
	if err := os.WriteFile(filepath.Join(dest, "sentinel"), []byte(sentinel), 0o644); err != nil {
		t.Fatal(err)
	}
	err := staticfetch.InstallLocalForTest(src, dest)
	if err == nil {
		t.Fatal("InstallLocal replaced an invalid existing cache; promotion must fail closed")
	}
	got, readErr := os.ReadFile(filepath.Join(dest, "sentinel"))
	if readErr != nil || string(got) != sentinel {
		t.Fatalf("existing cache changed after refused promotion: body=%q err=%v", got, readErr)
	}
	for name := range contents {
		if _, statErr := os.Stat(filepath.Join(dest, name)); !os.IsNotExist(statErr) {
			t.Fatalf("new artifact %s leaked into existing cache: %v", name, statErr)
		}
	}
}

func TestStaticfetch_ExistingPinnedCacheWithoutLicenseFailsClosed(t *testing.T) {
	src := t.TempDir()
	pins := map[string]string{}
	for name, body := range map[string][]byte{
		"config.json":       []byte(`{"normalize":true,"embedding_dtype":"float16"}`),
		"tokenizer.json":    []byte(`{"version":"1.0"}`),
		"model.safetensors": []byte("model"),
		"modules.json":      []byte("[]"),
	} {
		if err := os.WriteFile(filepath.Join(src, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		pins[name] = hex.EncodeToString(sum[:])
	}
	restore := swapPins(pins)
	defer restore()

	dest := filepath.Join(t.TempDir(), "model")
	if err := staticfetch.InstallLocalForTest(src, dest); err != nil {
		t.Fatalf("initial install: %v", err)
	}
	if err := os.Remove(filepath.Join(dest, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	err := staticfetch.InstallLocalForTest(src, dest)
	if err == nil || !strings.Contains(err.Error(), "LICENSE") {
		t.Fatalf("warm setup with missing licence error = %v, want non-zero naming LICENSE", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "LICENSE")); !os.IsNotExist(statErr) {
		t.Fatalf("failed immutable repair modified the destination: %v", statErr)
	}
}

// AC-4: a transport interruption is a truncation even when Content-Length is
// unknown. The error must carry the expected pin and the hash of the bytes
// received so far, and the failed first install must leave no destination.
func TestStaticfetch_InterruptedBodyReportsExpectedAndActualHash(t *testing.T) {
	body := []byte("partial response")
	want := strings.Repeat("a", 64)
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: -1,
			Body:          &interruptedBody{body: body},
			Header:        make(http.Header),
		}, nil
	})}
	restore := swapPins(map[string]string{"config.json": want})
	defer restore()
	dest := filepath.Join(t.TempDir(), "missing", "model")
	err := staticfetch.DownloadForTest(client, "https://example.invalid/model", dest)
	if err == nil {
		t.Fatal("Download accepted an interrupted response body")
	}
	gotSum := sha256.Sum256(body)
	got := hex.EncodeToString(gotSum[:])
	if !strings.Contains(err.Error(), "expected "+want) || !strings.Contains(err.Error(), "actual "+got) {
		t.Fatalf("interrupted-body error lacks expected/actual hashes: %v", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("interrupted first install left destination behind: %v", statErr)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func staticResponse(status int, body []byte, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    status,
		ContentLength: contentLength,
		Body:          io.NopCloser(bytes.NewReader(body)),
		Header:        make(http.Header),
	}
}

type interruptedBody struct {
	body []byte
	done bool
}

func (b *interruptedBody) Read(p []byte) (int, error) {
	if !b.done {
		b.done = true
		return copy(p, b.body), nil
	}
	return 0, errors.New("transport interrupted")
}

func (*interruptedBody) Close() error { return nil }

type repeatedByteReader struct{}

func (repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func TestStaticfetch_UnknownLengthBodyIsCappedAtReader(t *testing.T) {
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: -1,
			Body:          io.NopCloser(io.LimitReader(repeatedByteReader{}, staticfetch.MaxFileBytes+2)),
			Header:        make(http.Header),
		}, nil
	})}
	restore := swapPins(map[string]string{"config.json": strings.Repeat("a", 64)})
	defer restore()
	dest := filepath.Join(t.TempDir(), "missing", "model")
	err := staticfetch.DownloadForTest(client, "https://example.invalid/model", dest)
	if err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Fatalf("unknown-length oversize error = %v, want non-zero naming ceiling", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("unknown-length oversize response left an artifact: %v", statErr)
	}
}

func TestStaticfetch_EveryRedirectHopMustRemainHTTPS(t *testing.T) {
	if err := staticfetch.CheckRedirectForTest("https://cdn.example.invalid/model"); err != nil {
		t.Fatalf("HTTPS redirect refused: %v", err)
	}
	if err := staticfetch.CheckRedirectForTest("http://cdn.example.invalid/model"); err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("HTTP redirect error = %v, want non-HTTPS refusal", err)
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
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		parts := strings.Split(r.URL.Path, "/")
		name := parts[len(parts)-1]
		body, ok := contents[name]
		if !ok {
			return staticResponse(http.StatusNotFound, []byte("not found"), int64(len("not found"))), nil
		}
		return staticResponse(http.StatusOK, body, -1), nil
	})}
	restore := swapPins(wrongPins)
	defer restore()
	dest := filepath.Join(dir, "missing", "model")
	err := staticfetch.DownloadForTest(client, "https://example.invalid/model", dest)
	if err == nil {
		t.Fatal("Download accepted a mismatched hash")
	}
	if !strings.Contains(err.Error(), "model.safetensors") {
		t.Fatalf("error %v must name the offending file", err)
	}
	if !strings.Contains(err.Error(), "expected") || !strings.Contains(err.Error(), "actual") {
		t.Fatalf("error %v must name expected vs actual hash", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("hash mismatch left an artifact destination behind: %v", statErr)
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
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return staticResponse(http.StatusOK, body, 1024), nil
	})}
	restore := swapPins(pins)
	defer restore()
	dest := filepath.Join(dir, "missing", "model")
	err := staticfetch.DownloadForTest(client, "https://example.invalid/model", dest)
	if err == nil {
		t.Fatal("Download accepted a truncated body")
	}
	if !strings.Contains(err.Error(), "truncat") && !strings.Contains(err.Error(), "length") && !strings.Contains(err.Error(), "short") {
		t.Fatalf("error %v must name the truncation", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("truncation left an artifact destination behind: %v", statErr)
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
	client := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		parts := strings.Split(r.URL.Path, "/")
		name := parts[len(parts)-1]
		body, ok := contents[name]
		if !ok {
			return staticResponse(http.StatusNotFound, []byte("not found"), int64(len("not found"))), nil
		}
		if name == "config.json" {
			return staticResponse(http.StatusOK, []byte("x"), 68157440), nil // 65 MiB
		}
		return staticResponse(http.StatusOK, body, -1), nil
	})}
	pins := map[string]string{"config.json": hashOfConfig}
	restore := swapPins(pins)
	defer restore()
	dest := filepath.Join(dir, "missing", "model")
	err := staticfetch.DownloadForTest(client, "https://example.invalid/model", dest)
	if err == nil {
		t.Fatal("Download accepted an oversize Content-Length")
	}
	if !strings.Contains(err.Error(), "size") && !strings.Contains(err.Error(), "limit") && !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error %v must name the size limit", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("oversize response left an artifact destination behind: %v", statErr)
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
	src := t.TempDir()
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
		if err := os.WriteFile(filepath.Join(src, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	restore := swapPins(pins)
	defer restore()
	dest := filepath.Join(t.TempDir(), "missing", "model")
	if err := staticfetch.InstallLocalForTest(src, dest); err != nil {
		t.Fatalf("InstallLocal: %v", err)
	}
	if err := staticfetch.InstallLocalForTest(src, dest); err != nil {
		t.Fatalf("InstallLocal warm cache: %v", err)
	}
	wrong := map[string]string{}
	for k, v := range pins {
		wrong[k] = v
	}
	wrong["config.json"] = strings.Repeat("0", 64)
	restoreWrong := swapPins(wrong)
	defer restoreWrong()
	if err := staticfetch.InstallLocalForTest(src, dest); err == nil {
		t.Fatal("InstallLocal accepted a mismatched hash")
	} else if !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("InstallLocal error %q must name the offending file", err)
	}
}

// AC-4/AC-5: the staticfetch package is the ONLY place in cmd/graphi that
// imports network primitives. Each supply-chain entry point has one explicit
// production caller: setup.go for the model, and cmd/retrieval-eval for the
// eval tokenizer. Both halves matter: the
// canary catches a direct dial in another command, while this call-site guard
// catches a command that tries to reach the allowlisted downloader indirectly.
//
// The network-import check covers cmd/graphi. The supply-chain call-site check
// covers the entire repository, so engine/search, MCP, HTTP, and future packages
// cannot hide a dial by importing this allowlisted package. It follows import
// aliases and rejects references (including function values) to any guarded
// download/install function outside its one named setup path. Dot imports are
// refused.
func TestStaticfetch_IsTheOnlyNetworkCallerInCmdGraphi(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", "..", ".."))
	cmdRoot := filepath.Join(repoRoot, "cmd", "graphi")
	staticfetchRoot := filepath.Join(cmdRoot, "staticfetch")
	setupPath := filepath.Join(cmdRoot, "setup.go")
	tokenizerSetupPath := filepath.Join(repoRoot, "cmd", "retrieval-eval", "main.go")
	banned := []string{`"net/http"`, `"net/http/httptest"`, `"net/url"`, `"crypto/tls"`, `"syscall/js"`}
	wantCalls := map[string]struct {
		path  string
		count int
	}{
		"Download":              {path: setupPath, count: 1},
		"InstallLocal":          {path: setupPath, count: 1},
		"DownloadTokenizer":     {path: tokenizerSetupPath, count: 1},
		"InstallLocalTokenizer": {path: tokenizerSetupPath, count: 1},
	}
	gotCalls := map[string]int{}
	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == staticfetchRoot || entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, cmdRoot+string(filepath.Separator)) {
			for _, importPath := range banned {
				if strings.Contains(string(body), importPath) {
					t.Errorf("%s imports %s; the only network caller in cmd/graphi is staticfetch", path, importPath)
				}
			}
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, body, parser.ImportsOnly)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || importPath != staticPackage {
				continue
			}
			alias := "staticfetch"
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			if alias == "." {
				t.Errorf("%s dot-imports staticfetch; the sole-call-site guard requires a named import", path)
				continue
			}
			aliases[alias] = true
		}
		if len(aliases) == 0 {
			return nil
		}
		// Parse the full file only when it imports staticfetch.
		file, err = parser.ParseFile(fset, path, body, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || !aliases[pkg.Name] {
				return true
			}
			want, guarded := wantCalls[sel.Sel.Name]
			if !guarded {
				return true
			}
			if path != want.path {
				t.Errorf("%s references staticfetch.%s; only %s may invoke that supply-chain entry point", path, sel.Sel.Name, want.path)
				return true
			}
			gotCalls[sel.Sel.Name]++
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range wantCalls {
		if got := gotCalls[name]; got != want.count {
			t.Errorf("%s staticfetch.%s references = %d, want %d", want.path, name, got, want.count)
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
