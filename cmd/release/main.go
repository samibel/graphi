// Command release builds the canonical static graphi binary reproducibly and
// verifies it (story SW-013). It builds ./cmd/graphi with CGO_ENABLED=0,
// -trimpath, VCS stamping, and an ldflags-stamped version; verifies two builds of
// the same revision are byte-for-byte identical; and prints the embedded version
// + VCS metadata. Exits non-zero on a non-reproducible build.
//
// Usage:
//
//	go run ./cmd/release [-version 1.2.3] [-out graphi]
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/release"
)

func main() {
	version := flag.String("version", "dev", "release version stamped into the binary")
	out := flag.String("out", "graphi", "output binary path")
	verifyOnly := flag.Bool("verify-only", false, "only run the reproducibility check, do not write -out")
	listBlobs := flag.Bool("list-grammar-blobs", false, "print the expected default-build grammar blob set (derived from DefaultGrammarSubsetTags) and exit")
	dist := flag.String("dist", "", "when set, cross-compile the full ReleaseTargets matrix into this dir and write SHA256SUMS, then exit")
	webui := flag.Bool("webui", false, "additionally embed the web UI (-tags webui_embed); surfaces/http/webui/dist must already contain a built web app")
	listTargets := flag.Bool("list-targets", false, "print the release asset name for each ReleaseTargets platform and exit")
	listReleaseAssets := flag.Bool("list-release-assets", false, "print the complete expected GitHub Release asset set and exit")
	listV050Assets := flag.Bool("list-v050-assets", false, "print the frozen historical v0.5.0 asset set and exit")
	listV050ProvenanceAssets := flag.Bool("list-v050-provenance-assets", false, "print the frozen v0.5.0 attested asset set and exit")
	verifyAssets := flag.String("verify-assets", "", "verify an assembled GitHub Release asset directory, including SHA256SUMS, then exit")
	verifyV050Assets := flag.String("verify-v050-assets", "", "verify the exact historical v0.5.0 release contract, then exit")
	verifyHistoricalAssets := flag.String("verify-historical-assets", "", "verify a self-describing historical release from its complete SHA256SUMS and print its attested asset set")
	writeReleaseSums := flag.String("write-release-sums", "", "write complete SHA256SUMS for an assembled release directory, verify it, then exit")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall timeout")
	flag.Parse()

	// Source-of-truth readout for CI: the grammar blobs the default build must
	// embed, derived from internal/release.DefaultGrammarSubsetTags. The release
	// workflow compares the binary's embedded blobs against this, so the gate
	// never drifts from a hand-maintained list. No build required.
	if *listBlobs {
		for _, blob := range release.ExpectedGrammarBlobs() {
			fmt.Println(blob)
		}
		return
	}

	// Source-of-truth readout for CI: the asset name per release target, derived
	// from internal/release.ReleaseTargets. No build required.
	if *listTargets {
		for _, p := range release.ReleaseTargets {
			fmt.Println(release.AssetName(p))
		}
		return
	}
	if *listReleaseAssets {
		for _, name := range releaseAssetNames() {
			fmt.Println(name)
		}
		return
	}
	if *listV050Assets {
		for _, name := range legacyV050AssetNames() {
			fmt.Println(name)
		}
		return
	}
	if *listV050ProvenanceAssets {
		for _, name := range legacyV050ProvenanceAssetNames() {
			fmt.Println(name)
		}
		return
	}
	if *verifyAssets != "" {
		if err := verifyReleaseAssets(*verifyAssets); err != nil {
			fmt.Fprintf(os.Stderr, "release: verify assets: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "release: verified complete asset set in %s\n", *verifyAssets)
		return
	}
	if *verifyV050Assets != "" {
		if err := verifyReleaseAssetContract(*verifyV050Assets, legacyV050AssetNames(), legacyV050TargetNames()); err != nil {
			fmt.Fprintf(os.Stderr, "release: verify v0.5.0 assets: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "release: verified historical v0.5.0 asset contract in %s\n", *verifyV050Assets)
		return
	}
	if *verifyHistoricalAssets != "" {
		names, err := verifySelfDescribingReleaseAssets(*verifyHistoricalAssets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "release: verify historical assets: %v\n", err)
			os.Exit(1)
		}
		for _, name := range names {
			fmt.Println(name)
		}
		fmt.Fprintf(os.Stderr, "release: verified self-describing historical asset contract in %s\n", *verifyHistoricalAssets)
		return
	}
	if *writeReleaseSums != "" {
		if err := writeCompleteReleaseSums(*writeReleaseSums); err != nil {
			fmt.Fprintf(os.Stderr, "release: write release sums: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "release: wrote and verified complete checksums in %s\n", *writeReleaseSums)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Use one config for verification and every emitted binary. In particular,
	// -webui verifies the same webui_embed flavor that -dist publishes; checking
	// the smaller UI-free default build would not establish release
	// reproducibility.
	cfg := releaseBuildConfig(*version, *webui)
	sha, ok, err := release.VerifyReproducible(ctx, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "release: reproducible build failed: %v\n", err)
		os.Exit(2)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "release: build NOT reproducible — two builds differ (sha=%s)\n", sha)
		os.Exit(1)
	}
	flavor := "default"
	if *webui {
		flavor = "webui-embedded"
	}
	fmt.Fprintf(os.Stderr, "release: reproducible %s build verified (sha256=%s)\n", flavor, sha)

	// Cross-compile the full matrix into -dist and write SHA256SUMS. This path
	// does NOT write the host -out binary; it still ran the reproducibility check
	// above. -verify-only short-circuits before any matrix build.
	if *dist != "" && !*verifyOnly {
		paths, err := release.BuildAll(ctx, cfg, *dist)
		if err != nil {
			fmt.Fprintf(os.Stderr, "release: build matrix: %v\n", err)
			os.Exit(2)
		}
		names := make([]string, len(paths))
		for i, p := range paths {
			names[i] = filepath.Base(p)
		}
		if err := release.WriteSHA256SUMS(*dist, names); err != nil {
			fmt.Fprintf(os.Stderr, "release: write SHA256SUMS: %v\n", err)
			os.Exit(2)
		}
		for _, name := range names {
			fmt.Fprintln(os.Stderr, "release: asset "+name)
		}
		fmt.Fprintln(os.Stderr, "release: "+filepath.Join(*dist, "SHA256SUMS"))
		return
	}

	if !*verifyOnly {
		if err := release.Build(ctx, cfg, *out); err != nil {
			fmt.Fprintf(os.Stderr, "release: build: %v\n", err)
			os.Exit(2)
		}
		bi, err := release.ReadBuildInfo(ctx, *out)
		if err == nil {
			fmt.Fprintf(os.Stderr, "release: %s version=%s commit=%s date=%s cgo=%s\n",
				*out, bi.Version, bi.VCSRevision, bi.VCSTime, bi.CGOEnabled)
		}
	}
}

// releaseBuildConfig is the single flavor selection used by reproducibility
// verification, host output, and the cross-compiled matrix.
func releaseBuildConfig(version string, webui bool) release.BuildConfig {
	cfg := release.BuildConfig{Version: version}
	if webui {
		// The bundled release flavor: the default grammar subset plus the
		// go:embed'd UI built into surfaces/http/webui/dist by the workflow.
		cfg.Tags = append(append([]string{}, release.DefaultGrammarSubsetTags...), "webui_embed")
	}
	return cfg
}

// releaseAssetNames is the canonical, ordered public GitHub Release asset set:
// every platform binary followed by the checksum index and supply-chain
// metadata. SHA256SUMS covers every other item in this list.
func releaseAssetNames() []string {
	names := releaseTargetNames()
	return append(names, "SHA256SUMS", "sbom.spdx.json", "capability-manifest.json")
}

func releaseTargetNames() []string {
	names := make([]string, 0, len(release.ReleaseTargets))
	for _, p := range release.ReleaseTargets {
		names = append(names, release.AssetName(p))
	}
	return names
}

// The first release-dag release predates the complete checksum/provenance
// contract. Freeze its public contract so future platform additions cannot
// retroactively make the already-published v0.5.0 impossible to verify.
func legacyV050TargetNames() []string {
	return []string{
		"graphi-linux-amd64",
		"graphi-linux-arm64",
		"graphi-darwin-amd64",
		"graphi-darwin-arm64",
		"graphi-windows-amd64.exe",
	}
}

func legacyV050AssetNames() []string {
	return append(legacyV050TargetNames(), "SHA256SUMS", "sbom.spdx.json", "capability-manifest.json")
}

func legacyV050ProvenanceAssetNames() []string {
	return append(legacyV050TargetNames(), "SHA256SUMS")
}

func releasePayloadNames() []string {
	all := releaseAssetNames()
	payload := make([]string, 0, len(all)-1)
	for _, name := range all {
		if name != "SHA256SUMS" {
			payload = append(payload, name)
		}
	}
	return payload
}

func writeCompleteReleaseSums(dir string) error {
	if err := release.WriteSHA256SUMS(dir, releasePayloadNames()); err != nil {
		return err
	}
	return verifyReleaseAssets(dir)
}

// verifyReleaseAssets fails closed unless dir contains exactly the canonical
// public asset set and SHA256SUMS contains exactly one valid digest for every
// other asset. Extra, missing, empty, non-regular, duplicated, or corrupted
// assets are rejected.
func verifyReleaseAssets(dir string) error {
	return verifyReleaseAssetContract(dir, releaseAssetNames(), releasePayloadNames())
}

// verifyReleaseAssetContract verifies an exact versioned public asset set and
// its exact checksum membership. v0.5.0 used only the five binaries as checksum
// payloads; all later releases use every payload.
func verifyReleaseAssetContract(dir string, wantNames, checksumPayloadNames []string) error {
	want := make(map[string]struct{}, len(wantNames))
	for _, name := range wantNames {
		want[name] = struct{}{}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read asset directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stat asset %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("asset %s is not a regular file", entry.Name())
		}
		if info.Size() == 0 {
			return fmt.Errorf("asset %s is empty", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)
	wantSorted := append([]string{}, wantNames...)
	sort.Strings(wantSorted)
	if strings.Join(actual, "\x00") != strings.Join(wantSorted, "\x00") {
		return fmt.Errorf("asset set mismatch: got %v, want %v", actual, wantSorted)
	}

	sumsPath := filepath.Join(dir, "SHA256SUMS")
	f, err := os.Open(sumsPath)
	if err != nil {
		return fmt.Errorf("open SHA256SUMS: %w", err)
	}
	defer f.Close()

	wantPayload := make(map[string]struct{}, len(checksumPayloadNames))
	for _, name := range checksumPayloadNames {
		if _, exists := wantPayload[name]; exists {
			return fmt.Errorf("checksum contract contains duplicate asset %q", name)
		}
		if _, exists := want[name]; !exists || name == "SHA256SUMS" {
			return fmt.Errorf("checksum contract names invalid asset %q", name)
		}
		wantPayload[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(wantPayload))
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Text()
		digest, name, ok := strings.Cut(raw, "  ")
		if !ok || digest == "" || name == "" || strings.Contains(name, "  ") {
			return fmt.Errorf("SHA256SUMS line %d is not canonical '<hex>  <name>': %q", line, raw)
		}
		if len(digest) != sha256.Size*2 {
			return fmt.Errorf("SHA256SUMS line %d has invalid digest length", line)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return fmt.Errorf("SHA256SUMS line %d has invalid digest: %w", line, err)
		}
		if _, ok := wantPayload[name]; !ok {
			return fmt.Errorf("SHA256SUMS line %d names unexpected asset %q", line, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("SHA256SUMS contains duplicate asset %q", name)
		}
		seen[name] = struct{}{}
		got, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != digest {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, digest)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read SHA256SUMS: %w", err)
	}
	if len(seen) != len(wantPayload) {
		missing := make([]string, 0)
		for name := range wantPayload {
			if _, ok := seen[name]; !ok {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("SHA256SUMS is incomplete; missing %v", missing)
	}
	return nil
}

// verifySelfDescribingReleaseAssets verifies a historical release without
// applying today's platform matrix retroactively. A complete SHA256SUMS is the
// release's own asset contract: it must name every other regular, non-empty
// asset exactly once, use safe basenames, and match every digest. The caller
// must separately verify provenance for every returned name, including
// SHA256SUMS itself, against the historical tag SHA and workflow identity.
func verifySelfDescribingReleaseAssets(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read asset directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("stat asset %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("asset %s is not a regular file", entry.Name())
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("asset %s is empty", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	sort.Strings(actual)

	f, err := os.Open(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		return nil, fmt.Errorf("open SHA256SUMS: %w", err)
	}
	defer f.Close()

	seen := make(map[string]struct{}, len(actual))
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Text()
		digest, name, ok := strings.Cut(raw, "  ")
		if !ok || digest == "" || name == "" || strings.Contains(name, "  ") {
			return nil, fmt.Errorf("SHA256SUMS line %d is not canonical '<hex>  <name>': %q", line, raw)
		}
		if len(digest) != sha256.Size*2 || strings.ToLower(digest) != digest {
			return nil, fmt.Errorf("SHA256SUMS line %d has non-canonical digest", line)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, fmt.Errorf("SHA256SUMS line %d has invalid digest: %w", line, err)
		}
		if name == "SHA256SUMS" || filepath.Base(name) != name || strings.ContainsAny(name, `/\\`) {
			return nil, fmt.Errorf("SHA256SUMS line %d names unsafe asset %q", line, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("SHA256SUMS contains duplicate asset %q", name)
		}
		seen[name] = struct{}{}
		got, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if got != digest {
			return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, digest)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read SHA256SUMS: %w", err)
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("SHA256SUMS contains no release payloads")
	}

	want := make([]string, 0, len(seen)+1)
	for name := range seen {
		want = append(want, name)
	}
	want = append(want, "SHA256SUMS")
	sort.Strings(want)
	if strings.Join(actual, "\x00") != strings.Join(want, "\x00") {
		return nil, fmt.Errorf("self-described asset set mismatch: got %v, checksums describe %v", actual, want)
	}
	return want, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open asset %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash asset %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
