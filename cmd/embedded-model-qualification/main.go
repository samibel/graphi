// Command embedded-model-qualification is an evaluation-only evidence tool.
// It never starts, installs, downloads, or selects an embedding runtime.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

var (
	measureQualification  = retrieval.MeasureQualificationOperating
	finalizeQualification = retrieval.FinalizeQualification
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: embedded-model-qualification measure|finalize [flags]")
		return 2
	}
	switch args[0] {
	case "measure":
		return runMeasure(ctx, args[1:], stdout, stderr)
	case "finalize":
		return runFinalize(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n", args[0])
		return 2
	}
}

func runMeasure(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("measure", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options retrieval.MeasureQualificationOptions
	flags.StringVar(&options.PreregistrationPath, "preregistration", "", "sealed qualification preregistration JSON")
	flags.StringVar(&options.DatasetPath, "dataset", "", "frozen development dataset JSON")
	flags.StringVar(&options.ManifestPath, "manifest", "", "pinned CodeRank sidecar manifest JSON")
	flags.StringVar(&options.RepoRoot, "source-repo", "", "local repository containing the pinned source commit")
	flags.StringVar(&options.CandidateRoot, "candidate-root", "", "GrapHi candidate repository root")
	flags.StringVar(&options.WorkDir, "work-dir", "", "new or strictly empty reindex directory")
	flags.StringVar(&options.OutputPath, "out", "", "new operating evidence JSON path")
	flags.StringVar(&options.BackgroundProtocol, "background-protocol", "", "operator observation protocol identifier")
	flags.StringVar(&options.BackgroundMetadata, "background-metadata", "", "operator-observed background-load metadata")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || anyBlank(options.PreregistrationPath, options.DatasetPath, options.ManifestPath, options.RepoRoot, options.CandidateRoot, options.WorkDir, options.OutputPath, options.BackgroundProtocol, options.BackgroundMetadata) {
		if err == nil {
			fmt.Fprintln(stderr, "measure requires every documented flag and no positional arguments")
		}
		return 2
	}
	evidence, err := measureQualification(ctx, options)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "OPERATING EVIDENCE: %s\n", evidence.SHA256)
	return 0
}

func runFinalize(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("finalize", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options retrieval.FinalizeQualificationOptions
	flags.StringVar(&options.PreregistrationPath, "preregistration", "", "sealed qualification preregistration JSON")
	flags.StringVar(&options.DatasetPath, "dataset", "", "frozen development dataset JSON")
	flags.StringVar(&options.CaptureRootPath, "captures", "", "typed eight-capture root JSON")
	flags.StringVar(&options.BlindEvidencePath, "blind-evidence", "", "seven sealed blind evidence sets JSON")
	flags.StringVar(&options.BlindDecisionsPath, "blind-decisions", "", "448 sealed blind decisions JSON")
	flags.StringVar(&options.OperatingPath, "operating", "", "sealed operating evidence JSON")
	flags.StringVar(&options.OutputDir, "out", "", "new report directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || anyBlank(options.PreregistrationPath, options.DatasetPath, options.CaptureRootPath, options.BlindEvidencePath, options.BlindDecisionsPath, options.OperatingPath, options.OutputDir) {
		if err == nil {
			fmt.Fprintln(stderr, "finalize requires every documented flag and no positional arguments")
		}
		return 2
	}
	decision, err := finalizeQualification(options)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if decision.Promote {
		fmt.Fprintln(stdout, "DEVELOPMENT PROMOTION: YES")
	} else {
		fmt.Fprintln(stdout, "DEVELOPMENT PROMOTION: NO")
	}
	return 0
}

func anyBlank(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}
