package main

// SW-126 (P0-C3): the change sequence — a DEFINED, reproducible artifact, not
// random mutation (AC-1).
//
// WHY A DEFINITION AND NOT A FUZZER. PRD §16 wants two consecutive green runs,
// and a randomised mutation sequence makes two runs two different questions:
// the freshness p95 of run A over 100 arbitrary edits says nothing about run
// B's. So the sequence is a pure function of (sorted file list, cross-package
// targets, count). Same inputs, same steps, same order — pinned by a test, and
// published with a digest so a drift between two runs is one string comparison.
//
// WHY A FOUR-STEP CYCLE. AC-2 names four classes and the cycle exercises each
// one once per revolution, so ANY count >= 4 covers all four rather than only
// long runs reaching the rarer class. The add and the delete of a cycle are a
// PAIR — the delete removes exactly the file the add created — which is what
// keeps the tree from drifting arbitrarily far from the pinned checkout over a
// hundred changes: at the end of every cycle the tree is the pinned tree plus
// the appended functions, and nothing else.
//
// WHAT THE SEQUENCE DELIBERATELY DOES NOT DO. It never deletes a file the
// repository itself shipped. A hundred changes that progressively dismantled
// the pinned checkout would measure a tree that no longer resembles the
// repository the SHA pin names, and the corpus pin is the evidence. The cost is
// recorded honestly rather than hidden: a deleted file is one this sequence
// created, so its inbound edges are the ones this sequence created too.

import (
	"fmt"
	"path"
	"strings"

	"github.com/samibel/graphi/internal/evalreport"
)

// changeSequenceCycle is the number of classes in one revolution. It is derived
// from the required-class list rather than written as 4, so adding a class to
// AC-2's set cannot leave the cycle length behind.
var changeSequenceCycle = len(evalreport.RequiredChangeClasses)

// changeSequenceMethod is the sequence's determinism claim, stated in the
// artifact beside the digest so it can be checked rather than believed.
const changeSequenceMethod = "A fixed four-step cycle over the indexed Go source files in canonical (sorted, repo-relative POSIX) order: " +
	"(1) MODIFY file[c] by appending one exported function; (2) ADD a new file beside it in the same package containing " +
	"one exported function; (3) CROSS_PACKAGE — modify a file whose symbols have inbound edges from other directories, " +
	"rotating over the qualifying targets; (4) DELETE the file step 2 added in this same cycle. c advances by one per " +
	"cycle, so a long run walks the whole file list. The sequence is a pure function of the sorted file list, the " +
	"cross-package targets and the requested count: no randomness, no wall-clock, no map iteration."

// changeStep is one planned change. It is data: nothing here reads or writes
// the filesystem, so the whole sequence can be built and asserted without a
// repository.
type changeStep struct {
	// index is 1-based, matching the report's step numbers.
	index int
	class string
	// path is the repo-relative POSIX path the change targets.
	path string
	// pkg is the Go package clause a newly added file must carry. Empty for the
	// classes that modify an existing file.
	pkg string
	// symbol is the exported identifier the change introduces (add, modify,
	// cross_package) or, for a delete, the one the removed file defined.
	symbol string
	// expect states, in words, what "the new state" means for this step. It is
	// published so the convergence criterion is readable off the artifact.
	expect string
	// deleteTargets names the step whose file this delete removes, so an
	// unbalanced sequence is visible in the data rather than only at runtime.
	deleteTargets int
}

// descriptor is the step's identity for the sequence digest. Order-sensitive by
// construction (SampleDigest hashes the ordered list), because the same steps
// in a different order are a different sequence: each step's tree is the
// previous step's output.
func (s changeStep) descriptor() string {
	return s.class + "|" + s.path + "|" + s.symbol
}

// changeSequenceInput is everything the sequence is a function of. Building it
// touches the graph and the filesystem; turning it into steps does not.
type changeSequenceInput struct {
	// files are the indexed, modifiable Go source files in canonical order.
	files []string
	// packages maps a directory to the package clause its files declare, so an
	// added file compiles into the same package as its siblings.
	packages map[string]string
	// crossPackage are the files whose symbols carry inbound edges from other
	// directories, in the order the graph ranked them, with their evidence.
	crossPackage evalreport.CrossPackageEvidence
	// count is the number of changes requested.
	count int
}

// buildChangeSequence turns the input into the ordered steps. It is total: an
// input with no usable files yields no steps and the caller reports that as a
// failure to measure, rather than this function inventing a target.
//
// When no cross-package target qualified, the cross-package slot is NOT
// relabelled as an ordinary modify — the slot is skipped and the class stays
// visibly uncovered. Quietly substituting an in-package change would make a
// single-package repository report full AC-2 coverage it never had.
func buildChangeSequence(in changeSequenceInput) []changeStep {
	if in.count <= 0 || len(in.files) == 0 {
		return nil
	}
	var steps []changeStep
	for cycle := 0; len(steps) < in.count; cycle++ {
		base := in.files[cycle%len(in.files)]
		dir := path.Dir(base)

		// (1) MODIFY: append one exported function to an existing file.
		if len(steps) < in.count {
			symbol := changeSymbol(len(steps) + 1)
			steps = append(steps, changeStep{
				index:  len(steps) + 1,
				class:  evalreport.ChangeClassModify,
				path:   base,
				symbol: symbol,
				expect: "the appended function " + symbol + " is answerable by a search",
			})
		}
		// (2) ADD: a new file in the same package.
		addIndex := 0
		if len(steps) < in.count {
			index := len(steps) + 1
			symbol := changeSymbol(index)
			steps = append(steps, changeStep{
				index:  index,
				class:  evalreport.ChangeClassAdd,
				path:   addedFilePath(dir, index),
				pkg:    in.packages[dir],
				symbol: symbol,
				expect: "the added file's function " + symbol + " is answerable by a search",
			})
			addIndex = index
		}
		// (3) CROSS_PACKAGE: modify a file with inbound edges from other
		// directories. Skipped — never substituted — when none qualified.
		if len(steps) < in.count && len(in.crossPackage.Targets) > 0 {
			target := in.crossPackage.Targets[cycle%len(in.crossPackage.Targets)]
			index := len(steps) + 1
			symbol := changeSymbol(index)
			steps = append(steps, changeStep{
				index:  index,
				class:  evalreport.ChangeClassCrossPackage,
				path:   target.Path,
				symbol: symbol,
				expect: "the appended function " + symbol + " is answerable after re-linking a file with " +
					fmt.Sprintf("%d inbound edge(s) from other directories", target.InboundFromOtherDirs),
			})
		}
		// (4) DELETE: remove the file this cycle's add created.
		if len(steps) < in.count && addIndex > 0 {
			index := len(steps) + 1
			steps = append(steps, changeStep{
				index:         index,
				class:         evalreport.ChangeClassDelete,
				path:          addedFilePath(dir, addIndex),
				symbol:        changeSymbol(addIndex),
				expect:        "the deleted file's function " + changeSymbol(addIndex) + " is no longer answerable",
				deleteTargets: addIndex,
			})
		}
		// A cycle that produced nothing would spin forever; it can only happen
		// if the input is degenerate, and returning what exists is honest.
		if cycle > in.count {
			break
		}
	}
	if len(steps) > in.count {
		steps = steps[:in.count]
	}
	return steps
}

// changeSymbol is the exported identifier step n introduces. Unique per step so
// no two changes in a sequence can be confused with one another, and prefixed
// so it can never collide with a symbol the repository itself defines.
func changeSymbol(n int) string {
	return fmt.Sprintf("GraphiEvalStep%04d", n)
}

// addedFilePath is the file an `add` step creates. The name carries the step
// index so the paired delete addresses exactly the file its cycle added.
func addedFilePath(dir string, n int) string {
	name := fmt.Sprintf("graphi_eval_step%04d.go", n)
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// changeSequenceDigest is the reproducibility check for an ordered sequence:
// SampleDigest over the per-step descriptors. It reuses the query-latency
// sample's digest so there is one digest definition in the harness rather than
// two that could disagree about encoding.
func changeSequenceDigest(steps []changeStep) string {
	ids := make([]string, 0, len(steps))
	for _, s := range steps {
		ids = append(ids, s.descriptor())
	}
	return evalreport.SampleDigest(ids)
}

// changeSequenceInfo is the sequence as the artifact publishes it.
func changeSequenceInfo(in changeSequenceInput, steps []changeStep) evalreport.ChangeSequenceInfo {
	return evalreport.ChangeSequenceInfo{
		Steps:        len(steps),
		Cycle:        changeSequenceCycle,
		Method:       changeSequenceMethod,
		Digest:       changeSequenceDigest(steps),
		SourceFiles:  len(in.files),
		CrossPackage: in.crossPackage,
	}
}

// modifiedFileContent is the appended text for a modify or cross-package step.
// One exported function, no imports, no dependency on the file's existing
// content: it is valid Go in any package, so the change cannot break a file for
// a reason that has nothing to do with the measurement.
func modifiedFileContent(existing []byte, s changeStep) []byte {
	body := "\n// " + s.symbol + " is appended by the SW-126 freshness harness (step " +
		fmt.Sprint(s.index) + "). It is removed with the working tree.\nfunc " + s.symbol +
		"() int { return " + fmt.Sprint(s.index) + " }\n"
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		body = "\n" + body
	}
	return append(append([]byte(nil), existing...), []byte(body)...)
}

// addedFileContent is a whole new file in the sibling package.
func addedFileContent(s changeStep) []byte {
	pkg := s.pkg
	if pkg == "" {
		// Unreachable while the package clause is read from a sibling; a
		// deliberate placeholder rather than an empty package clause, which
		// would not parse and would fail the step for the wrong reason.
		pkg = "main"
	}
	return []byte("package " + pkg + "\n\n// " + s.symbol +
		" is added by the SW-126 freshness harness (step " + fmt.Sprint(s.index) +
		") and deleted later in the same cycle.\nfunc " + s.symbol +
		"() int { return " + fmt.Sprint(s.index) + " }\n")
}
