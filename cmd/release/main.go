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
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Reproducibility check first: two builds of the same revision must be identical.
	sha, ok, err := release.VerifyReproducible(ctx, release.BuildConfig{Version: *version})
	if err != nil {
		fmt.Fprintf(os.Stderr, "release: reproducible build failed: %v\n", err)
		os.Exit(2)
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "release: build NOT reproducible — two builds differ (sha=%s)\n", sha)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "release: reproducible build verified (sha256=%s)\n", sha)

	// Cross-compile the full matrix into -dist and write SHA256SUMS. This path
	// does NOT write the host -out binary; it still ran the reproducibility check
	// above. -verify-only short-circuits before any matrix build.
	if *dist != "" && !*verifyOnly {
		cfg := release.BuildConfig{Version: *version}
		if *webui {
			// The bundled release flavor: same grammar subset as the default
			// build, plus the go:embed'd web UI served at "/" over the
			// loopback-only HTTP surface. The web app must have been built and
			// copied into surfaces/http/webui/dist beforehand (the release
			// workflow does this; locally use scripts/build-release-webui.sh).
			cfg.Tags = append(append([]string{}, release.DefaultGrammarSubsetTags...), "webui_embed")
		}
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
		if err := release.Build(ctx, release.BuildConfig{Version: *version}, *out); err != nil {
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
