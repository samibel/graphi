//go:build graphi_broad

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

// These tests run ONLY under `-tags graphi_broad` (the broad CGO lane, SW-056
// SEC-C). They assert the broad-lane supply chain: the forest/bare modules are
// pinned in go.mod exactly, their licenses (verified at the PINNED PATH) are
// permissive, and the broad flavor builds OFFLINE (GOPROXY=off) against a warm
// cache after the one-time pin. They never run in the default lane.

// TestBroadProvenance_PinMatchesGoMod asserts every broad-lane module's recorded
// pin matches its require line in go.mod exactly (provenance can never drift from
// the actual pin).
func TestBroadProvenance_PinMatchesGoMod(t *testing.T) {
	root := moduleRootDir(t)
	gomod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}
	norm := go_mod_normalize(string(gomod))
	for _, p := range BroadTierGrammarProvenance {
		want := p.ModulePath + " " + p.Version
		if !strings.Contains(norm, want) {
			t.Errorf("go.mod does not pin %q (provenance/go.mod drift); want require line %q", p.ModulePath, want)
		}
	}
}

// TestBroadProvenance_LicenseAtPinnedPath reads the LICENSE from each broad-lane
// module's RESOLVED/pinned path in the module cache (forest re-vendors upstream
// grammars, so the license is verified at the SMOKE GRAMMAR's pinned path, not just
// the forest root — DN-3) and asserts it is permissive. A future license-changing
// bump fails here.
func TestBroadProvenance_LicenseAtPinnedPath(t *testing.T) {
	cache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatalf("go env GOMODCACHE: %v", err)
	}
	gomodcache := strings.TrimSpace(string(cache))
	for _, p := range BroadTierGrammarProvenance {
		if !p.IsPermissive() {
			t.Errorf("recorded license %q for %q is not in the permissive set", p.LicenseSPDX, p.ModulePath)
			continue
		}
		modDir := filepath.Join(gomodcache, p.ModulePath+"@"+p.Version)
		licPath := filepath.Join(modDir, p.LicenseFile)
		data, err := os.ReadFile(licPath)
		if err != nil {
			t.Errorf("read pinned module LICENSE %s (run `go mod download`): %v", licPath, err)
			continue
		}
		body := string(data)
		if !strings.Contains(body, "MIT License") && !strings.Contains(body, "Permission is hereby granted, free of charge") {
			t.Errorf("pinned module %q LICENSE does not read as MIT (relicense?):\n%s", p.ModulePath, firstLines(body, 5))
		}
	}
}

// TestBroadOfflineBuild_NoNetwork asserts the graphi-broad flavor builds with the
// NETWORK DENIED (SEC-C / DN-3): GOPROXY=off + -mod=readonly + CGO_ENABLED=1
// -tags graphi_broad against a WARM cache. The real offline risk for the broad lane
// is a lazily-fetched grammar subpackage at build time; pinning the zig subpackage
// module (covered by go.sum) removes it. A clean build under GOPROXY=off proves the
// broad lane needs no network after the one-time pin.
func TestBroadOfflineBuild_NoNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping offline broad build in -short mode")
	}
	root := moduleRootDir(t)
	out := filepath.Join(t.TempDir(), "graphi-broad-offline")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	args := []string{"build", "-tags", "graphi_broad", "-o", out, "./cmd/graphi/"}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=1",
		"GOPROXY=off",
		"GOFLAGS=-mod=readonly",
	)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("offline broad build under GOPROXY=off failed (network-denied build must succeed against a warm cache after the one-time pin): %v\n%s", err, combined)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("offline broad build produced no binary: %v", err)
	}
}

// TestBroadSupplyChain_GoModVerify runs `go mod verify` — the downloaded broad-lane
// modules match the hashes in go.sum (no tampered grammar source).
func TestBroadSupplyChain_GoModVerify(t *testing.T) {
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
