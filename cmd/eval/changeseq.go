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
var changeSequenceMethod = "A fixed four-step cycle over the indexed, LANGUAGE-SCOPED modifiable source files in canonical " +
	"(sorted, repo-relative POSIX) order: (1) MODIFY file[c] by appending one top-level declaration written in THAT FILE'S " +
	"language; (2) ADD a new file beside it, in the same package where the language has one and in the same directory " +
	"where it does not, containing one top-level declaration; (3) CROSS_PACKAGE — modify a file whose symbols have " +
	"inbound edges from other directories, rotating over the qualifying targets; (4) DELETE the file step 2 added in " +
	"this same cycle. c advances by one per cycle, so a long run walks the whole file list. A file is a candidate only " +
	"when its extension belongs to one of the families cmd/eval/sourcefamily.go states a mutation shape for (" +
	strings.Join(familyNames(), ", ") + "); the data/markup languages the index can parse are deliberately NOT " +
	"candidates, because there is no top-level declaration to append to them. Each family supplies its own package-clause " +
	"reader (Go's bare identifier, the JVM's dotted-and-optionally-terminated name, or none at all for the languages " +
	"that locate a module by path), its own declaration text and its own added-file name. The sequence is a pure " +
	"function of the sorted file list, the per-directory package clauses, the cross-package targets and the requested " +
	"count: no randomness, no wall-clock, no map iteration."

// changeStep is one planned change. It is data: nothing here reads or writes
// the filesystem, so the whole sequence can be built and asserted without a
// repository.
type changeStep struct {
	// index is 1-based, matching the report's step numbers.
	index int
	class string
	// path is the repo-relative POSIX path the change targets.
	path string
	// pkg is the package clause a newly added file must carry, in its own
	// language's shape. Empty for the classes that modify an existing file, and
	// also for the families that HAVE no package clause.
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
	// files are the indexed, modifiable source files — of every language family
	// with a mutation shape — in canonical order.
	files []string
	// packages maps packageKey(directory, family) to the package clause that
	// family's files declare there, so an added file joins the same package as
	// its siblings. A family with no package clause records no entry, and that
	// absence is admissible rather than disqualifying.
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
		// The ADD/DELETE pair is rendered in the language of the file the cycle
		// is anchored on, so the sibling it creates is parseable in that
		// directory. A base with no family cannot happen (the file list is
		// already family-filtered) but is skipped rather than assumed.
		family := familyForPath(base)
		if family == nil {
			continue
		}

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
				path:   addedFilePath(dir, family, symbol, index),
				pkg:    in.packages[packageKey(dir, family)],
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
				path:          addedFilePath(dir, family, changeSymbol(addIndex), addIndex),
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

// addedFilePath is the file an `add` step creates, named by the family whose
// directory it lands in. The name is a pure function of (family, symbol, step
// index) so the paired delete addresses exactly the file its cycle added, and
// it carries the family's own extension so the added file is parsed by the
// parser its siblings are.
func addedFilePath(dir string, family *sourceFamily, symbol string, n int) string {
	name := family.addedFileBase(symbol, n)
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
// One top-level declaration, no imports, no dependency on the file's existing
// content, written in the language of the file being modified — so the change
// cannot break a file for a reason that has nothing to do with the measurement,
// and the appended symbol is really extractable by the parser that file's
// siblings go through.
//
// The language comes from the PATH BEING MODIFIED, not from the cycle's anchor:
// a cross-package step targets whatever file the graph ranked, which may belong
// to a different family than the file the cycle started on.
func modifiedFileContent(existing []byte, s changeStep) []byte {
	family := familyForPath(s.path)
	if family == nil {
		// The step would not have been planned; returning the file unchanged
		// makes the step fail its convergence probe honestly rather than
		// corrupting a file the harness cannot write.
		return append([]byte(nil), existing...)
	}
	body := family.appended(s.symbol, s.index)
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		body = "\n" + body
	}
	return append(append([]byte(nil), existing...), []byte(body)...)
}

// addedFileContent is a whole new file beside its siblings, in their language
// and — where the language has one — in their package.
func addedFileContent(s changeStep) []byte {
	family := familyForPath(s.path)
	if family == nil {
		return nil
	}
	return []byte(family.added(s.pkg, s.symbol, s.index))
}
