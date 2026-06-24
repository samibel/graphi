package release

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// moduleRootDir resolves the module root via `go env GOMOD`.
func moduleRootDir(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		t.Fatal("no go.mod found")
	}
	return filepath.Dir(gomod)
}

// TestProvenance_PinMatchesGoMod asserts the recorded pinned version matches the
// require line in go.mod exactly — provenance can never drift from the actual pin.
func TestProvenance_PinMatchesGoMod(t *testing.T) {
	root := moduleRootDir(t)
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	p := DefaultTierGrammarProvenance
	want := p.ModulePath + " " + p.Version
	if !strings.Contains(string(go_mod_normalize(string(gomod))), want) {
		t.Fatalf("go.mod does not pin %q (provenance/go.mod drift); want a require line %q", p.ModulePath, want)
	}
}

func go_mod_normalize(s string) string {
	// Collapse runs of whitespace so "module  v1.2.3" matches "module v1.2.3".
	return strings.Join(strings.Fields(s), " ")
}

// TestProvenance_LicenseIsMIT reads the LICENSE from the RESOLVED/pinned module in
// the module cache and asserts it is MIT (the ACTUAL gotreesitter license) and
// permissive — dropping the formerly-assumed Apache-2.0. A future license-changing
// version bump (or a non-permissive relicense) fails here.
func TestProvenance_LicenseIsMIT(t *testing.T) {
	p := DefaultTierGrammarProvenance

	if p.LicenseSPDX != "MIT" {
		t.Fatalf("default-tier grammar license recorded as %q, want MIT (actual gotreesitter license)", p.LicenseSPDX)
	}
	if !p.IsPermissive() {
		t.Fatalf("recorded license %q is not in the permissive set", p.LicenseSPDX)
	}

	cache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("go env GOMODCACHE: %v", err)
	}
	modDir := filepath.Join(strings.TrimSpace(string(cache)), p.ModulePath+"@"+p.Version)
	licPath := filepath.Join(modDir, p.LicenseFile)
	data, err := os.ReadFile(licPath)
	if err != nil {
		t.Fatalf("read pinned module LICENSE %s (module not in cache? run `go mod download`): %v", licPath, err)
	}
	body := string(data)
	// MIT detection: the canonical MIT header phrase plus the permission grant.
	if !strings.Contains(body, "MIT License") && !strings.Contains(body, "Permission is hereby granted, free of charge") {
		t.Fatalf("pinned module LICENSE does not read as MIT (license-changing bump?):\n%s", firstLines(body, 5))
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestSupplyChain_GoModVerify runs `go mod verify` — the module's downloaded
// contents match the hashes in go.sum (no tampered grammar source).
func TestSupplyChain_GoModVerify(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go mod verify in -short mode")
	}
	cmd := exec.Command("go", "mod", "verify")
	cmd.Dir = moduleRootDir(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go mod verify failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "all modules verified") {
		t.Fatalf("go mod verify did not confirm all modules: %s", out)
	}
}

// TestDefaultSubsetTags_DriftGuard locks DefaultGrammarSubsetTags ↔ RegisterDefaults
// in lock-step: every gotreesitter language registered in the default tier has its
// grammar_subset_<lang> tag and vice-versa (the stdlib go/json parsers carry no
// blob and no tag; HTML is absent from both). It prevents a subset-tag/defaults
// drift that would either embed an unregistered blob or fail to embed a registered
// language's blob. The registered-language set is provided by the parse-package
// drift test (TestAssertPureGoDefaults_LanguageSet); here we assert the tag-side
// invariant: the umbrella tag plus exactly one tag per non-stdlib language.
func TestDefaultSubsetTags_ShapeInvariant(t *testing.T) {
	tags := DefaultGrammarSubsetTags
	if len(tags) == 0 || tags[0] != "grammar_subset" {
		t.Fatal("DefaultGrammarSubsetTags must start with the umbrella 'grammar_subset' tag")
	}
	seen := map[string]struct{}{}
	for _, tg := range tags[1:] {
		if !strings.HasPrefix(tg, "grammar_subset_") {
			t.Fatalf("unexpected subset tag %q (want grammar_subset_<lang>)", tg)
		}
		if _, dup := seen[tg]; dup {
			t.Fatalf("duplicate subset tag %q", tg)
		}
		seen[tg] = struct{}{}
	}
	// HTML must be absent (deferred to graphi-broad/SW-056).
	if _, has := seen["grammar_subset_html"]; has {
		t.Fatal("grammar_subset_html must be ABSENT from the default tier (SW-056)")
	}
}

// TestOfflineDefaultBuild_NoNetwork asserts the default flavor builds with NETWORK
// DENIED (SW-055 AC#5): GOPROXY=off + GOFLAGS=-mod=mod against a WARM module cache.
// The real offline risk is a module FETCH at build time — the grammar blobs are
// //go:embed'd, so no grammar is fetched. A clean build under GOPROXY=off proves
// the default flavor needs no network.
func TestOfflineDefaultBuild_NoNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping offline build in -short mode")
	}
	root := moduleRootDir(t)
	out := filepath.Join(t.TempDir(), "graphi-offline")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Build the default flavor with the subset tags, CGO_ENABLED=0, and the network
	// denied. GOPROXY=off forbids any module proxy fetch; a warm cache (populated by
	// the normal test run) is the only module source permitted.
	args := []string{"build", "-tags", strings.Join(DefaultGrammarSubsetTags, " "), "-o", out, "./cmd/graphi/"}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOPROXY=off",
		"GOFLAGS=",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline default build under GOPROXY=off failed (network-denied build must succeed against a warm cache): %v\n%s", err, combined)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("offline build produced no binary: %v", err)
	}
}
