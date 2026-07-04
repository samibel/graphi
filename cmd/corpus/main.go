// Command corpus runs the real-repository smoke harness (internal/corpus): it
// builds (or accepts) a graphi binary and drives index → search → query →
// analyze → diagnose against every manifest entry, failing on crashes,
// non-zero exits, panic markers, or missing promised results.
//
// Usage:
//
//	go run ./cmd/corpus -manifest corpus/manifest.json -report corpus-report.json
//	go run ./cmd/corpus -manifest m.json -binary ./graphi -workdir /tmp/corpus
//
// Exit codes mirror cmd/bench: 0 = all entries pass, 1 = at least one entry
// failed, 2 = harness/internal error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/samibel/graphi/internal/corpus"
)

func main() {
	os.Exit(run())
}

func run() int {
	manifest := flag.String("manifest", "corpus/manifest.json", "corpus manifest path")
	binary := flag.String("binary", "", "graphi binary to test (default: build ./cmd/graphi into the workdir)")
	workdir := flag.String("workdir", "", "work dir for clones and stores (default: a temp dir)")
	report := flag.String("report", "", "when set, write the machine-readable JSON report here")
	timeout := flag.Duration("entry-timeout", 10*time.Minute, "per-repository timeout")
	tier := flag.Int("tier", 0, "run only entries with this exact tier (1-3)")
	maxTier := flag.Int("max-tier", 0, "run only entries with tier <= this value")
	flag.Parse()

	m, err := corpus.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
		return 2
	}

	wd := *workdir
	if wd == "" {
		td, err := os.MkdirTemp("", "graphi-corpus-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "corpus: temp workdir: %v\n", err)
			return 2
		}
		defer os.RemoveAll(td)
		wd = td
	}

	bin := *binary
	if bin == "" {
		bin = filepath.Join(wd, exeName("graphi"))
		build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "corpus: build graphi: %v\n%s\n", err, out)
			return 2
		}
	}

	r := &corpus.Runner{
		Binary:          bin,
		WorkDir:         wd,
		PerEntryTimeout: *timeout,
		Tier:            *tier,
		MaxTier:         *maxTier,
		Log: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	}
	rep, err := r.Run(context.Background(), m)
	if err != nil {
		fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
		return 2
	}

	if *report != "" {
		if err := corpus.WriteReport(rep, *report); err != nil {
			fmt.Fprintf(os.Stderr, "corpus: %v\n", err)
			return 2
		}
	}

	for _, e := range rep.Entries {
		status := "PASS"
		if !e.Pass {
			status = "FAIL"
		}
		fmt.Printf("corpus: %-16s %s (%.1fs, HEAD %s)\n", e.Name, status, float64(e.DurationMS)/1000, short(e.HeadSHA))
		if !e.Pass {
			for _, s := range e.Steps {
				if !s.OK {
					fmt.Printf("  step %s: %s\n", s.Name, s.Detail)
				}
			}
		}
	}
	if !rep.Pass {
		fmt.Println("corpus: FAIL — at least one repository run failed")
		return 1
	}
	fmt.Println("corpus: PASS — every repository ran the full flow cleanly")
	return 0
}

// exeName appends the platform executable suffix (Windows needs .exe for
// CreateProcess to resolve the built binary).
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	if sha == "" {
		return "?"
	}
	return sha
}
