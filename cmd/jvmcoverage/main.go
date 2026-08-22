// Command jvmcoverage computes per-pin compile-coverage figures (the
// PARITY-COV-001 closure) for the JVM corpus pins and emits the result as a
// JSON Patch the operator can apply to corpus/manifest.json.
//
// It is read-only: it never rewrites the manifest on its own. The numbers it
// returns come from parity.ComputeCompileCoverage, which walks the pin's
// filesystem and never invokes a compiler, so a missing javac/kotlinc never
// affects its results — the dispatch-only jvmcorpus run remains the source of
// ground truth for the compile outcome.
//
// Usage:
//
//	go run ./cmd/jvmcoverage -manifest corpus/manifest.json \
//	    -pins-root /private/tmp/graphi-pins \
//	    -runner-class "linux-x64/ccr-container" \
//	    -candidate-sha 9f687849cec2b26311401191e90b60e40b5f6cee
//
// Each JVM pin emits a single object whose shape mirrors
// corpus.CompileCoverage: source_files, compiled_files, coverage,
// measured_at, candidate_sha, runner_class, oracle and (when present)
// excluded_reason. The emitted JSON is the patch the manifest editor applies
// per pin; it is NOT the manifest itself.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parity"
)

// jvmCompileShape is the manifest-side jvm_compile block, read narrowly so
// jvmcoverage does not have to take a transitive dependency on the dispatch
// tools. JSON keys match the manifest exactly.
type jvmCompileShape struct {
	Strategy                string   `json:"strategy"`
	SourceRoots             []string `json:"source_roots"`
	CommonSourceRoots       []string `json:"common_source_roots"`
	ExcludedFromCorpusScale bool     `json:"excluded_from_corpus_scale"`
	ExclusionReason         string   `json:"exclusion_reason"`
}

type manifestShape struct {
	Entries []struct {
		Name       string           `json:"name"`
		Language   string           `json:"language"`
		JVMCompile *jvmCompileShape `json:"jvm_compile"`
	} `json:"entries"`
}

type result struct {
	Name   string                 `json:"name"`
	Path   string                 `json:"pin_root"`
	Cover  corpus.CompileCoverage `json:"compile_coverage"`
	Errors []string               `json:"errors,omitempty"`
}

func main() {
	manifestPath := flag.String("manifest", "corpus/manifest.json", "corpus manifest path")
	pinsRoot := flag.String("pins-root", "/private/tmp/graphi-pins", "directory holding one clone per pin, named after the manifest entry")
	runnerClass := flag.String("runner-class", "", "machine class this run happened on (required)")
	candidateSha := flag.String("candidate-sha", "9f687849cec2b26311401191e90b60e40b5f6cee", "parityreport.CandidateSHA the run was measured against")
	date := flag.String("measured-at", "", "ISO date the oracle ran (default: today, UTC)")
	flag.Parse()

	if *runnerClass == "" {
		fmt.Fprintln(os.Stderr, "jvmcoverage: -runner-class is required (e.g. \"Darwin-ARM64/apple-m2-max\")")
		os.Exit(2)
	}
	if *date == "" {
		*date = time.Now().UTC().Format("2006-01-02")
	}

	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jvmcoverage: read %s: %v\n", *manifestPath, err)
		os.Exit(2)
	}
	var ms manifestShape
	if err := json.Unmarshal(raw, &ms); err != nil {
		fmt.Fprintf(os.Stderr, "jvmcoverage: parse %s: %v\n", *manifestPath, err)
		os.Exit(2)
	}

	var results []result
	for _, e := range ms.Entries {
		if e.JVMCompile == nil {
			continue
		}
		if e.Language != "java" && e.Language != "kotlin" {
			continue
		}
		pinRoot := *pinsRoot + "/" + e.Name
		if _, statErr := os.Stat(pinRoot); statErr != nil {
			results = append(results, result{Name: e.Name, Path: pinRoot, Errors: []string{statErr.Error()}})
			continue
		}
		cov, cerr := parity.ComputeCompileCoverage(parity.CompileCoverageInput{
			PinRoot:                  pinRoot,
			SourceRoots:              e.JVMCompile.SourceRoots,
			CommonSourceRoots:        e.JVMCompile.CommonSourceRoots,
			Strategy:                 e.JVMCompile.Strategy,
			ExcludedFromCorpusScale:  e.JVMCompile.ExcludedFromCorpusScale,
			ExcludedReason:           e.JVMCompile.ExclusionReason,
			RunnerClass:              *runnerClass,
			CandidateSHA:             *candidateSha,
			Now:                      func() string { return *date },
		})
		if cerr != nil {
			results = append(results, result{Name: e.Name, Path: pinRoot, Errors: []string{cerr.Error()}})
			continue
		}
		results = append(results, result{Name: e.Name, Path: pinRoot, Cover: cov})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// Stamp the candidate+date in a stderr banner so an operator cannot
	// paste a fresh run into a manifest dated for an older one.
	banner := strings.Repeat("=", 72)
	fmt.Fprintln(os.Stderr, banner)
	fmt.Fprintf(os.Stderr, "jvmcoverage: runner_class=%s candidate_sha=%s measured_at=%s pins=%d\n",
		*runnerClass, *candidateSha, *date, len(results))
	for _, r := range results {
		if len(r.Errors) > 0 {
			fmt.Fprintf(os.Stderr, "  %-24s ERROR: %s\n", r.Name, strings.Join(r.Errors, "; "))
			continue
		}
		fmt.Fprintf(os.Stderr, "  %-24s source=%4d  compiled=%4d  coverage=%.4f  excluded=%q\n",
			r.Name, r.Cover.SourceFiles, r.Cover.CompiledFiles, r.Cover.Coverage, r.Cover.ExcludedReason)
	}
	fmt.Fprintln(os.Stderr, banner)
}
