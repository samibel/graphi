// Command gobindrate is the CLI wrapper around internal/gobindrate.Run. It
// walks a Go repository on disk, builds the same files map the
// engine/typeresolve.Resolve call takes, and prints the rendered report to
// stdout. The trailing report_sha256=<hash> line is the SW-187 AC-4
// reproducibility token; two consecutive invocations against the same
// pinned tree produce the same SHA byte-for-byte.
//
// Usage:
//
//	go run ./internal/gobindrate/cmd/gobindrate <repo-dir>
//
// The repo-dir must contain a go.mod at its root (or be a single-package
// tree without one, in which case intra-package binding still resolves but
// every cross-package import is external).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/gobindrate"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gobindrate <repo-dir>")
		os.Exit(2)
	}
	repoDir, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	files, err := collectFiles(repoDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	r, err := gobindrate.Run(files)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Print(r.Rendered)
	fmt.Printf("\nreport_sha256=%s\n", r.ReportSHA256)
}

func collectFiles(dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == "vendor" || base == ".git" || strings.HasPrefix(base, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}
