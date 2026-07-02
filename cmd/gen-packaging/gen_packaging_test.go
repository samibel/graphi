package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/release"
)

// renderFiles returns the two manifests rendered with the canonical placeholder.
func renderFiles(t *testing.T) map[string]string {
	t.Helper()
	files, err := render("0.0.0", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.name] = f.content
	}
	return out
}

// TestFormulaReferencesMacAndLinuxAssets proves the Homebrew formula references
// EXACTLY the release.AssetName the source of truth produces for every macOS and
// Linux target — the formula's download set is provably the release matrix.
func TestFormulaReferencesMacAndLinuxAssets(t *testing.T) {
	rb := renderFiles(t)[formulaPath]
	for _, p := range release.ReleaseTargets {
		if p.OS != "darwin" && p.OS != "linux" {
			continue
		}
		name := release.AssetName(p)
		if !strings.Contains(rb, name) {
			t.Errorf("formula missing macOS/Linux release asset %q", name)
		}
	}
	// The windows asset must NOT leak into the Homebrew formula.
	for _, p := range release.ReleaseTargets {
		if p.OS == "windows" && strings.Contains(rb, release.AssetName(p)) {
			t.Errorf("formula unexpectedly references windows asset %q", release.AssetName(p))
		}
	}
}

// TestScoopReferencesWindowsAsset proves the Scoop manifest references the
// windows asset and unmarshals as valid JSON.
func TestScoopReferencesWindowsAsset(t *testing.T) {
	js := renderFiles(t)[scoopPath]

	var win release.Platform
	found := false
	for _, p := range release.ReleaseTargets {
		if p.OS == "windows" && p.Arch == "amd64" {
			win = p
			found = true
		}
	}
	if !found {
		t.Fatal("ReleaseTargets has no windows/amd64 target")
	}
	if !strings.Contains(js, release.AssetName(win)) {
		t.Errorf("scoop manifest missing windows asset %q", release.AssetName(win))
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("scoop manifest is not valid JSON: %v", err)
	}
	if m["license"] != "Apache-2.0" {
		t.Errorf("scoop license = %v, want Apache-2.0", m["license"])
	}
}

// TestVersionTagSplit pins the version-prefix quirk fix: Homebrew's `version`
// field and Scoop's `version` want the BARE semver, download URLs want the
// TAG — and both input forms ("v0.2.0" / "0.2.0") must render byte-identically
// so the workflow can pass the git tag verbatim.
func TestVersionTagSplit(t *testing.T) {
	fromTag, err := render("v0.2.0", nil)
	if err != nil {
		t.Fatalf("render(v0.2.0): %v", err)
	}
	fromBare, err := render("0.2.0", nil)
	if err != nil {
		t.Fatalf("render(0.2.0): %v", err)
	}
	for i := range fromTag {
		if fromTag[i].content != fromBare[i].content {
			t.Errorf("%s: tag-form and bare-form inputs render differently", fromTag[i].name)
		}
	}
	byName := map[string]string{}
	for _, f := range fromTag {
		byName[f.name] = f.content
	}
	rb, js := byName[formulaPath], byName[scoopPath]
	if !strings.Contains(rb, `version "0.2.0"`) {
		t.Errorf("formula version field must be the bare semver")
	}
	if strings.Contains(rb, `version "v0.2.0"`) {
		t.Errorf("formula version field carries the tag prefix")
	}
	if !strings.Contains(rb, "/releases/download/v0.2.0/") {
		t.Errorf("formula URLs must use the TAG path (/download/v0.2.0/)")
	}
	if !strings.Contains(js, `"version": "0.2.0"`) {
		t.Errorf("scoop version field must be the bare semver")
	}
	if !strings.Contains(js, "/releases/download/v0.2.0/") {
		t.Errorf("scoop URL must use the TAG path (/download/v0.2.0/)")
	}
}

// TestRenderDeterministic proves two renders are byte-identical.
func TestRenderDeterministic(t *testing.T) {
	a, err := render("v1.2.3", map[string]string{"graphi-linux-amd64": "abc"})
	if err != nil {
		t.Fatalf("render A: %v", err)
	}
	b, err := render("v1.2.3", map[string]string{"graphi-linux-amd64": "abc"})
	if err != nil {
		t.Fatalf("render B: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("render produced different file counts: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].name != b[i].name || a[i].content != b[i].content {
			t.Fatalf("render %q is not deterministic across two runs", a[i].name)
		}
	}
}

// TestSumsInjection proves a SHA256SUMS hash is injected into the rendered
// manifests for the matching asset (and the placeholder is used otherwise).
func TestSumsInjection(t *testing.T) {
	hashes := map[string]string{
		"graphi-darwin-arm64":      "1111111111111111111111111111111111111111111111111111111111111111",
		"graphi-windows-amd64.exe": "2222222222222222222222222222222222222222222222222222222222222222",
	}
	files, err := render("v9.9.9", hashes)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var rb, js string
	for _, f := range files {
		switch f.name {
		case formulaPath:
			rb = f.content
		case scoopPath:
			js = f.content
		}
	}
	if !strings.Contains(rb, hashes["graphi-darwin-arm64"]) {
		t.Error("formula did not inject the darwin/arm64 sha256 from sums")
	}
	if !strings.Contains(js, hashes["graphi-windows-amd64.exe"]) {
		t.Error("scoop manifest did not inject the windows sha256 from sums")
	}
	// An asset without a sums entry keeps the zero placeholder.
	if !strings.Contains(rb, zeroSHA) {
		t.Error("formula lost the zero placeholder for assets without a sums entry")
	}
}

// TestNoDriftAfterGenerate proves the committed manifests at the module root
// match the canonical placeholder render — the local mirror of `-check`.
func TestNoDriftAfterGenerate(t *testing.T) {
	root := moduleRoot()
	files, err := render("0.0.0", nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, f := range files {
		got, rerr := os.ReadFile(filepath.Join(root, f.name))
		if rerr != nil {
			t.Fatalf("read committed %s: %v", f.name, rerr)
		}
		if string(got) != f.content {
			t.Errorf("committed %s is stale — run `go run ./cmd/gen-packaging`", f.name)
		}
	}
}

// TestParseSums covers the SHA256SUMS line parsing (two-space canonical form and
// a leading binary '*' marker).
func TestParseSums(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SHA256SUMS")
	content := "aaaa  graphi-linux-amd64\nbbbb *graphi-windows-amd64.exe\n\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write sums: %v", err)
	}
	got, err := parseSums(path)
	if err != nil {
		t.Fatalf("parseSums: %v", err)
	}
	if got["graphi-linux-amd64"] != "aaaa" || got["graphi-windows-amd64.exe"] != "bbbb" {
		t.Errorf("parseSums = %v", got)
	}
}

// TestRubyLintsFormula runs `ruby -c` on the rendered formula when ruby is
// available, asserting the generated Ruby is syntactically valid.
func TestRubyLintsFormula(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not installed — skipping ruby -c lint (CI lints it)")
	}
	rb := renderFiles(t)[formulaPath]
	dir := t.TempDir()
	path := filepath.Join(dir, "graphi.rb")
	if werr := os.WriteFile(path, []byte(rb), 0o644); werr != nil {
		t.Fatalf("write formula: %v", werr)
	}
	out, cerr := exec.Command(ruby, "-c", path).CombinedOutput()
	if cerr != nil {
		t.Fatalf("ruby -c failed: %v\n%s", cerr, out)
	}
}
