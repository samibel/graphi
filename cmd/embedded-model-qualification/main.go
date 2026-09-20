// Command embedded-model-qualification is an evaluation-only evidence tool.
// It never starts, installs, downloads, or selects an embedding runtime.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/eval/retrieval"
)

var (
	captureQualification  = retrieval.CaptureQualification
	measureQualification  = retrieval.MeasureQualificationOperating
	finalizeQualification = retrieval.FinalizeQualification
	checkQualification    = retrieval.CheckQualificationDataset
	computeQualification  = retrieval.ComputeQualificationPreregistrationDigests
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: embedded-model-qualification validate-dataset|digests|capture|measure|finalize [flags]")
		return 2
	}
	switch args[0] {
	case "validate-dataset":
		return runValidateDataset(args[1:], stdout, stderr)
	case "digests":
		return runDigests(ctx, args[1:], stdout, stderr)
	case "capture":
		return runCapture(ctx, args[1:], stdout, stderr)
	case "measure":
		return runMeasure(ctx, args[1:], stdout, stderr)
	case "finalize":
		return runFinalize(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n", args[0])
		return 2
	}
}

// runValidateDataset is the pre-flight lamp in front of the expensive gate. It
// lives on this command rather than in a sibling binary on purpose: it must read
// the candidate population through the harness's own strict loader and judge it
// with the harness's own ValidateQualificationDataset, and any second binary
// would eventually be given a second, drifting copy of one of those. It adds no
// state and touches nothing the frozen measure/finalize path depends on - it
// only reads a file and prints - so the pollution risk that would justify a
// separate command does not arise.
//
// The exit code is the operator's loop condition: 0 only when the gate itself
// accepts the population, 1 when the gate would refuse it, 2 for usage and
// unreadable-file failures. The full report goes to stdout in every case,
// because a refusal the operator cannot read is the problem being fixed here.
func runValidateDataset(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("validate-dataset", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options retrieval.QualificationDatasetCheckOptions
	flags.StringVar(&options.DatasetPath, "dataset", "", "candidate development dataset JSON")
	flags.BoolVar(&options.DevSplitOnly, "dev-only", false, "treat the file as a curation source pool: drop holdout rows instead of reporting them")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || anyBlank(options.DatasetPath) {
		if err == nil {
			fmt.Fprintln(stderr, "validate-dataset requires --dataset and no positional arguments")
		}
		return 2
	}
	diagnosis, err := checkQualification(options)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprint(stdout, diagnosis.Render())
	if !diagnosis.Conformant {
		return 1
	}
	return 0
}

// runDigests derives the computable half of preregistration.json.
//
// It is a subcommand rather than a sibling binary for the same reason
// validate-dataset is: the values it prints have to be produced by the SAME
// helpers the gate checks them with - SHA256Hex, GitRepoProbe's canonical diff
// arguments, qualificationPotionIdentity, coderank.Manifest's own identity
// digest - and a second binary is a second place for one of those to drift.
//
// The two output streams are not interchangeable. stdout carries only the JSON
// fragment, so `... digests > fragment.json` is a usable command; stderr
// carries the "cannot compute yet" notes. Exit 1 means the candidate worktree
// is not in a freezable state, which invalidates the two candidate fields the
// fragment DID emit; exit 2 is usage or an unreadable input the operator named.
// Outstanding notes alone are exit 0: they are the normal state of an
// authoring session that is not finished yet.
func runDigests(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("digests", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options retrieval.QualificationPreregistrationDigestInputs
	flags.StringVar(&options.DatasetPath, "dataset", "", "frozen development dataset JSON")
	flags.StringVar(&options.CandidateRoot, "candidate-root", "", "GrapHi candidate repository root")
	flags.StringVar(&options.FrozenCandidateSHA, "candidate-sha", "", "commit to preregister as candidate_sha (default: the candidate worktree's HEAD)")
	flags.StringVar(&options.CodeRankManifestPath, "manifest", "", "finalized CodeRank sidecar manifest JSON")
	flags.StringVar(&options.GraderPromptPath, "grading-rubric", "", "grading rubric the precondition record will freeze")
	// There is no --graph-generation flag any more. The canonical
	// fingerprint's eighth field is minted from crypto/rand by every index
	// build, so it cannot be preregistered; the helper emits a documented
	// placeholder and the gate binds the field at runtime. See
	// internal/eval/retrieval/model_qualification_fingerprint.go.
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || anyBlank(options.DatasetPath, options.CandidateRoot) {
		if err == nil {
			fmt.Fprintln(stderr, "digests requires --dataset and --candidate-root and no positional arguments")
		}
		return 2
	}
	digests, err := computeQualification(ctx, options)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := retrieval.RenderQualificationPreregistrationDigests(digests, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if !digests.CandidateIsFreezable() {
		return 1
	}
	return 0
}

// runCapture is the operator's door to the eight-capture run. Before it
// existed, the only caller of that driver was the live test, which read six
// GRAPHI_* environment variables: an operator could only produce the evidence
// finalize demands by exporting the right variables and running `go test`.
// Environment variables are also the wrong shape for these inputs - they leak
// between runs, an unexported one is an empty string rather than a refusal,
// and nothing on the command line records what a run was given.
//
// Every flag is mandatory and there are no positional arguments, exactly as on
// measure: capture consumes a preregistration that pins each of these inputs,
// so a defaulted one is a run bound to something the operator did not name.
// The exit codes follow the neighbours - 2 for usage, 1 for a refused or
// failed run, 0 only when all eight captures were published.
func runCapture(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options retrieval.CaptureQualificationOptions
	flags.StringVar(&options.PreregistrationPath, "preregistration", "", "sealed qualification preregistration JSON")
	flags.StringVar(&options.DatasetPath, "dataset", "", "frozen development dataset JSON")
	flags.StringVar(&options.ManifestPath, "manifest", "", "pinned CodeRank sidecar manifest JSON")
	flags.StringVar(&options.RepoRoot, "source-repo", "", "local repository containing the pinned source commit")
	flags.StringVar(&options.CandidateRoot, "candidate-root", "", "GrapHi candidate repository root")
	// The Potion arms resolve their artifact through static.ResolveArtifactDir,
	// which reads $GRAPHI_STATIC_MODEL_DIR and falls back to the XDG cache.
	// This flag is the pre-flight assertion that the pinned artifact is
	// installed, so it must name the directory that resolution finds.
	flags.StringVar(&options.StaticModelDir, "static-model-dir", "", "pinned Potion artifact directory (must equal the resolved $GRAPHI_STATIC_MODEL_DIR)")
	flags.StringVar(&options.OutputPath, "out", "", "existing, strictly empty capture output directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || anyBlank(options.PreregistrationPath, options.DatasetPath, options.ManifestPath, options.RepoRoot, options.CandidateRoot, options.StaticModelDir, options.OutputPath) {
		if err == nil {
			fmt.Fprintln(stderr, "capture requires every documented flag and no positional arguments")
		}
		return 2
	}
	if err := captureQualification(ctx, options); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	// The published capture root is the single file finalize --captures wants;
	// printing it saves the operator from reconstructing the path by hand.
	fmt.Fprintf(stdout, "CAPTURES: %s\n", filepath.Join(options.OutputPath, "captures.json"))
	return 0
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
