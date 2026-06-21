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
	"time"

	"github.com/samibel/graphi/internal/release"
)

func main() {
	version := flag.String("version", "dev", "release version stamped into the binary")
	out := flag.String("out", "graphi", "output binary path")
	verifyOnly := flag.Bool("verify-only", false, "only run the reproducibility check, do not write -out")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall timeout")
	flag.Parse()

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
