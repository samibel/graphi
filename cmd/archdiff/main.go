// Command archdiff records and verifies the ARCH-P0 differential baseline: the
// outcome of every use case, driven through the surfaces/client.Client seam.
//
//	go run ./cmd/archdiff -record -commit <sha> \
//	    -out docs/rc/archdiff-baseline.json   # record the legacy outcomes
//	go run ./cmd/archdiff -check              # re-record and compare
//
// A -check diff is not automatically a failure of the tool: it means a use case
// now behaves differently than when the baseline was taken. During the migration
// phases that is exactly the signal to stop and explain before continuing.
//
// Exit codes mirror cmd/layerguard and cmd/coverage: 0 = clean, 1 = drift,
// 2 = internal error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/samibel/graphi/internal/archdiff"
)

func main() {
	var (
		record = flag.Bool("record", false, "record the baseline")
		check  = flag.Bool("check", false, "re-record and compare against the checked-in baseline")
		out    = flag.String("out", "docs/rc/archdiff-baseline.json", "baseline artifact path")
		commit = flag.String("commit", "", "with -record: the revision being recorded (required)")
	)
	flag.Parse()

	if *record == *check {
		fmt.Fprintln(os.Stderr, "archdiff: exactly one of -record or -check is required")
		os.Exit(2)
	}
	if *record && *commit == "" {
		fmt.Fprintln(os.Stderr, "archdiff: -commit is required when recording")
		os.Exit(2)
	}

	ctx := context.Background()
	root, err := archdiff.ModuleRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "archdiff: %v\n", err)
		os.Exit(2)
	}
	path := *out
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(*out))
	}

	recorded, err := archdiff.RecordAll(ctx, root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archdiff: %v\n", err)
		os.Exit(2)
	}
	fmt.Print(archdiff.Summary(recorded))

	if *record {
		baseline := archdiff.NewBaseline(*commit, archdiff.FixtureRelPath, recorded)
		rendered, err := archdiff.Render(baseline)
		if err != nil {
			fmt.Fprintf(os.Stderr, "archdiff: %v\n", err)
			os.Exit(2)
		}
		if err := os.WriteFile(path, rendered, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "archdiff: write %s: %v\n", path, err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s\n", *out)
		return
	}

	// -check
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archdiff: read %s: %v\n", *out, err)
		os.Exit(2)
	}
	baseline, err := archdiff.Parse(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archdiff: %v\n", err)
		os.Exit(2)
	}
	diffs := archdiff.DiffRecorded(baseline.Cases, recorded)
	if len(diffs) == 0 {
		fmt.Printf("archdiff check PASS — every use case matches the baseline recorded at %s.\n", baseline.Commit)
		return
	}
	fmt.Fprintf(os.Stderr, "archdiff check FAIL — %d use case(s) differ from the baseline recorded at %s:\n",
		len(diffs), baseline.Commit)
	for _, d := range diffs {
		fmt.Fprintf(os.Stderr, "  %s\n", d)
	}
	fmt.Fprintln(os.Stderr, "  → A diff is a behaviour change. Explain it before continuing the phase; "+
		"do not re-record the baseline to make this pass.")
	os.Exit(1)
}
