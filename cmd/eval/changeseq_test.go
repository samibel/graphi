package main

// SW-126 (P0-C3): the change sequence is a DATA ARTIFACT, so it gets the test a
// data artifact gets — the same definition yields the same steps in the same
// order (AC-1), and the four AC-2 classes are all present.

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func testSequenceInput(count int) changeSequenceInput {
	return changeSequenceInput{
		files: []string{"a/one.go", "a/two.go", "b/three.go"},
		// SW-191: the clause map is keyed per (family, directory), because two
		// families can share a directory and their clauses are not
		// interchangeable. The Go family and the Go paths above are unchanged.
		packages: map[string]string{
			packageKey("a", familyForPath("a/one.go")):   "alpha",
			packageKey("b", familyForPath("b/three.go")): "beta",
		},
		crossPackage: evalreport.CrossPackageEvidence{
			Satisfied: true,
			Targets: []evalreport.CrossPackageTarget{
				{Path: "a/one.go", Symbol: "a.One", InboundFromOtherDirs: 3},
				{Path: "b/three.go", Symbol: "b.Three", InboundFromOtherDirs: 1},
			},
		},
		count: count,
	}
}

// AC-1: the same sequence definition yields the same steps in the same order.
// Built twice from equal inputs, the descriptors and the digest must match
// exactly — otherwise PRD §16's two consecutive runs measure two questions.
func TestChangeSequence_IsReproducible(t *testing.T) {
	first := buildChangeSequence(testSequenceInput(100))
	second := buildChangeSequence(testSequenceInput(100))

	if len(first) != 100 || len(second) != 100 {
		t.Fatalf("sequence lengths %d and %d, want the requested 100", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("step %d drifted between two builds:\n%+v\n%+v", i+1, first[i], second[i])
		}
	}
	if changeSequenceDigest(first) != changeSequenceDigest(second) {
		t.Fatal("the sequence digest drifted between two builds of the same definition")
	}
	// The digest is order-sensitive: a reordered sequence is a different
	// sequence, because each step's tree is the previous step's output.
	reordered := append([]changeStep(nil), first...)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	if changeSequenceDigest(reordered) == changeSequenceDigest(first) {
		t.Error("reordering the steps did not change the digest")
	}
}

// AC-2: the four classes are all exercised, and they are exercised early —
// every full cycle covers all four, so coverage is not a property only long
// runs have.
func TestChangeSequence_CoversTheFourClassesInEveryCycle(t *testing.T) {
	steps := buildChangeSequence(testSequenceInput(changeSequenceCycle))
	if len(steps) != changeSequenceCycle {
		t.Fatalf("one cycle produced %d steps, want %d", len(steps), changeSequenceCycle)
	}
	seen := map[string]bool{}
	for _, s := range steps {
		seen[s.class] = true
	}
	for _, class := range evalreport.RequiredChangeClasses {
		if !seen[class] {
			t.Errorf("the first cycle does not exercise %s: %+v", class, steps)
		}
	}

	full := buildChangeSequence(testSequenceInput(100))
	counts := map[string]int{}
	for _, s := range full {
		counts[s.class]++
	}
	for _, class := range evalreport.RequiredChangeClasses {
		if counts[class] < 25 {
			t.Errorf("class %s appears %d times over 100 changes, want a quarter of them", class, counts[class])
		}
	}
}

// The add/delete pair is balanced: every delete removes exactly the file its
// own cycle's add created, and no delete targets a file the repository shipped.
func TestChangeSequence_DeletesOnlyWhatItAdded(t *testing.T) {
	steps := buildChangeSequence(testSequenceInput(100))
	added := map[string]int{}
	for _, s := range steps {
		switch s.class {
		case evalreport.ChangeClassAdd:
			added[s.path] = s.index
		case evalreport.ChangeClassDelete:
			at, ok := added[s.path]
			if !ok {
				t.Fatalf("step %d deletes %s, which this sequence never added — the pinned checkout must stay intact", s.index, s.path)
			}
			if at != s.deleteTargets {
				t.Errorf("step %d deletes the file added at step %d but records %d", s.index, at, s.deleteTargets)
			}
			delete(added, s.path)
		}
	}
	if len(added) > 1 {
		t.Errorf("%d added files were never deleted; at most the final unfinished cycle may leave one: %v", len(added), added)
	}
}

// Every step introduces a unique symbol, so no two changes in a sequence can be
// confused for one another by the convergence probe.
func TestChangeSequence_SymbolsAreUniquePerStep(t *testing.T) {
	steps := buildChangeSequence(testSequenceInput(100))
	introduced := map[string]int{}
	for _, s := range steps {
		if s.class == evalreport.ChangeClassDelete {
			continue // deliberately re-uses its add's symbol
		}
		if prev, dup := introduced[s.symbol]; dup {
			t.Errorf("step %d re-introduces %s, already introduced at step %d", s.index, s.symbol, prev)
		}
		introduced[s.symbol] = s.index
		if s.expect == "" {
			t.Errorf("step %d states no convergence expectation", s.index)
		}
	}
}

// Without a qualifying cross-package target the slot is SKIPPED, not
// relabelled: a single-package repository must report the class as uncovered
// rather than pass off an in-package change as cross-package evidence.
func TestChangeSequence_NoCrossPackageTargetLeavesTheClassEmpty(t *testing.T) {
	in := testSequenceInput(20)
	in.crossPackage = evalreport.CrossPackageEvidence{Satisfied: false, Reason: "single package"}
	steps := buildChangeSequence(in)
	if len(steps) != 20 {
		t.Fatalf("got %d steps, want the requested 20", len(steps))
	}
	for _, s := range steps {
		if s.class == evalreport.ChangeClassCrossPackage {
			t.Fatalf("step %d claims cross_package with no qualifying target: %+v", s.index, s)
		}
	}
}

// A degenerate input measures nothing rather than inventing a target.
func TestChangeSequence_EmptyInputProducesNoSteps(t *testing.T) {
	if steps := buildChangeSequence(changeSequenceInput{count: 10}); len(steps) != 0 {
		t.Fatalf("a sequence over no files produced %d steps", len(steps))
	}
	if steps := buildChangeSequence(testSequenceInput(0)); len(steps) != 0 {
		t.Fatalf("a zero-count sequence produced %d steps", len(steps))
	}
}

// The generated Go must be valid in any package and must not depend on the
// file's existing content.
func TestChangeSequence_GeneratedContentIsSelfContained(t *testing.T) {
	steps := buildChangeSequence(testSequenceInput(4))
	var modify, add changeStep
	for _, s := range steps {
		switch s.class {
		case evalreport.ChangeClassModify:
			modify = s
		case evalreport.ChangeClassAdd:
			add = s
		}
	}

	appended := string(modifiedFileContent([]byte("package alpha\n\nfunc One() {}"), modify))
	if !strings.HasPrefix(appended, "package alpha\n\nfunc One() {}") {
		t.Error("the modify step rewrote the file instead of appending to it")
	}
	if !strings.Contains(appended, "func "+modify.symbol+"() int") {
		t.Errorf("the appended content does not define %s: %s", modify.symbol, appended)
	}
	// A file that did not end in a newline must not have its last line joined
	// to the appended declaration.
	if strings.Contains(appended, "func One() {}\nfunc ") {
		t.Errorf("the append ran into the previous declaration: %s", appended)
	}

	added := string(addedFileContent(add))
	if !strings.HasPrefix(added, "package alpha\n") {
		t.Errorf("the added file does not join its siblings' package: %s", added)
	}
	if !strings.Contains(added, "func "+add.symbol+"() int") {
		t.Errorf("the added file does not define %s: %s", add.symbol, added)
	}
}
