package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/release"
)

func TestReleaseBuildConfig_WebUIFlavorIsSharedAndExact(t *testing.T) {
	cfg := releaseBuildConfig("v1.2.3", true)
	if cfg.Version != "v1.2.3" {
		t.Fatalf("Version = %q, want v1.2.3", cfg.Version)
	}
	want := append(append([]string{}, release.DefaultGrammarSubsetTags...), "webui_embed")
	if !slices.Equal(cfg.Tags, want) {
		t.Fatalf("web UI tags = %v, want %v", cfg.Tags, want)
	}
	if !slices.Contains(cfg.Tags, "webui_embed") {
		t.Fatal("published flavor must include webui_embed")
	}

	// The helper must not mutate the global default tag source of truth.
	cfg.Tags[0] = "mutated"
	if release.DefaultGrammarSubsetTags[0] == "mutated" {
		t.Fatal("releaseBuildConfig aliases DefaultGrammarSubsetTags")
	}
}

func TestReleaseBuildConfig_DefaultFlavorUsesDefaulting(t *testing.T) {
	cfg := releaseBuildConfig("v1.2.3", false)
	if cfg.Tags != nil {
		t.Fatalf("default flavor Tags = %v, want nil so internal/release defaults apply", cfg.Tags)
	}
}

func TestReleaseAssetNames_CompleteAndOrdered(t *testing.T) {
	want := []string{
		"graphi-linux-amd64",
		"graphi-linux-arm64",
		"graphi-darwin-amd64",
		"graphi-darwin-arm64",
		"graphi-windows-amd64.exe",
		"SHA256SUMS",
		"sbom.spdx.json",
		"capability-manifest.json",
	}
	if got := releaseAssetNames(); !slices.Equal(got, want) {
		t.Fatalf("releaseAssetNames() = %v, want %v", got, want)
	}
}

func TestLegacyV050ContractIsFrozen(t *testing.T) {
	wantAssets := []string{
		"graphi-linux-amd64",
		"graphi-linux-arm64",
		"graphi-darwin-amd64",
		"graphi-darwin-arm64",
		"graphi-windows-amd64.exe",
		"SHA256SUMS",
		"sbom.spdx.json",
		"capability-manifest.json",
	}
	if !slices.Equal(legacyV050AssetNames(), wantAssets) {
		t.Fatalf("legacy assets = %v, want %v", legacyV050AssetNames(), wantAssets)
	}
	wantProvenance := append(append([]string{}, wantAssets[:5]...), "SHA256SUMS")
	if !slices.Equal(legacyV050ProvenanceAssetNames(), wantProvenance) {
		t.Fatalf("legacy provenance assets = %v, want %v", legacyV050ProvenanceAssetNames(), wantProvenance)
	}
}

func TestVerifyReleaseAssets_CompleteChecksummedSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range releasePayloadNames() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeCompleteReleaseSums(dir); err != nil {
		t.Fatalf("writeCompleteReleaseSums: %v", err)
	}
	if err := verifyReleaseAssets(dir); err != nil {
		t.Fatalf("verifyReleaseAssets: %v", err)
	}
}

func TestVerifyReleaseAssets_FailsClosed(t *testing.T) {
	newCompleteDir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range releasePayloadNames() {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeCompleteReleaseSums(dir); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("missing asset", func(t *testing.T) {
		dir := newCompleteDir(t)
		if err := os.Remove(filepath.Join(dir, "sbom.spdx.json")); err != nil {
			t.Fatal(err)
		}
		if err := verifyReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "asset set mismatch") {
			t.Fatalf("error = %v, want asset-set failure", err)
		}
	})

	t.Run("extra asset", func(t *testing.T) {
		dir := newCompleteDir(t)
		if err := os.WriteFile(filepath.Join(dir, "surprise.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := verifyReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "asset set mismatch") {
			t.Fatalf("error = %v, want asset-set failure", err)
		}
	})

	t.Run("tampered payload", func(t *testing.T) {
		dir := newCompleteDir(t)
		if err := os.WriteFile(filepath.Join(dir, "capability-manifest.json"), []byte("tampered"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := verifyReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("error = %v, want checksum failure", err)
		}
	})

	t.Run("checksum omits metadata", func(t *testing.T) {
		dir := newCompleteDir(t)
		path := filepath.Join(dir, "SHA256SUMS")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if err := os.WriteFile(path, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := verifyReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "incomplete") {
			t.Fatalf("error = %v, want incomplete-checksum failure", err)
		}
	})
}

func TestVerifyV050ReleaseAssets_UsesOnlyTheVersionedLegacyChecksumSet(t *testing.T) {
	dir := t.TempDir()
	for _, name := range releasePayloadNames() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := release.WriteSHA256SUMS(dir, releaseTargetNames()); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseAssetContract(dir, legacyV050AssetNames(), legacyV050TargetNames()); err != nil {
		t.Fatalf("historical v0.5.0 contract: %v", err)
	}
	if err := verifyReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("current contract must reject legacy sums, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, releaseTargetNames()[0]), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyReleaseAssetContract(dir, legacyV050AssetNames(), legacyV050TargetNames()); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("legacy binary tamper error = %v, want checksum mismatch", err)
	}
}

func TestVerifySelfDescribingReleaseAssets_FollowsHistoricalSumsNotCurrentMatrix(t *testing.T) {
	dir := t.TempDir()
	payload := []string{"future-platform-riscv64", "future-sbom.cdx.json"}
	for _, name := range payload {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("payload:"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := release.WriteSHA256SUMS(dir, payload); err != nil {
		t.Fatal(err)
	}
	want := []string{"SHA256SUMS", "future-platform-riscv64", "future-sbom.cdx.json"}
	got, err := verifySelfDescribingReleaseAssets(dir)
	if err != nil {
		t.Fatalf("verifySelfDescribingReleaseAssets: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("verified assets = %v, want %v", got, want)
	}

	if err := os.WriteFile(filepath.Join(dir, "unchecksummed-extra"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verifySelfDescribingReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "asset set mismatch") {
		t.Fatalf("extra asset error = %v, want exact-set failure", err)
	}
}

func TestVerifySelfDescribingReleaseAssets_RejectsUnsafeOrTamperedSums(t *testing.T) {
	t.Run("unsafe name", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "payload"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+"  ../payload\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := verifySelfDescribingReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "unsafe asset") {
			t.Fatalf("unsafe name error = %v", err)
		}
	})

	t.Run("tampered payload", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "payload"), []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := release.WriteSHA256SUMS(dir, []string{"payload"}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "payload"), []byte("tampered"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := verifySelfDescribingReleaseAssets(dir); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
			t.Fatalf("tamper error = %v", err)
		}
	})
}
