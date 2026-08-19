package main

// SW-177 (W1.d) characterization: the freshness suite is Go-only, so ONE of the
// four perf suites cannot include a Java or Kotlin corpus pin at all.
//
// This is not a shortfall in a number. It is a structural block, and it was
// found by running the harness rather than by reading it: on the pinned Kotlin
// clone the invocation
//
//	eval -manifest corpus/manifest.json -full-run okio -incremental-changes 100
//
// terminates with
//
//	FAIL - full run over okio: incremental: the index contains no modifiable Go
//	source files to change
//
// and the same holds for guava and kotlinx.serialization, which contain no `.go`
// file at all. The mechanism is `modifiableGoFile` (cmd/eval/incremental.go),
// the single filter every change class is drawn through, plus
// `goPackageClause`, which supplies the package a newly added sibling must
// declare and only understands Go's `package <ident>` line.
//
// WHY A TEST AND NOT ONLY A DOCUMENT. The block is the reason
// GA-LANG-{java,kotlin}-G7 cannot read "the four perf suites include L's
// corpus", and a reason recorded only in prose is a reason that silently stops
// being true. When someone extends the change sequence to the JVM languages,
// TestChangeSequence_IsGoOnly_SoTheFreshnessSuiteCannotIncludeAJVMPin goes RED
// and says what else has to move with it.
//
// Deliberately NOT done here: extending the sequence. That is new measurement
// machinery — the story's own test notes scope this work to "corpus wiring and
// budget derivation rather than new measurement machinery" — and a change
// sequence invented by the builder who then publishes a p95 from it is exactly
// the shape of evidence this programme has already been burned by.
//
// No network, no corpus clone, no wall clock: every assertion below is over
// path strings and file bytes.

import (
	"strings"
	"testing"
)

// jvmCorpusPaths are real repo-relative paths from the three pinned JVM
// clones named in corpus/manifest.json. They are literal rather than read from
// a clone so the test stays offline.
var jvmCorpusPaths = []string{
	// guava (java) at 2214c63670fc161da170ac6e1a2d6d07e1531a55
	"guava/src/com/google/common/collect/ImmutableList.java",
	"guava/src/com/google/common/base/Preconditions.java",
	"futures/failureaccess/src/com/google/common/util/concurrent/internal/InternalFutures.java",
	// okio (kotlin) at 8b870e8eaacecb1c1ceffbbb47246112604a1f92
	"okio/src/commonMain/kotlin/okio/Buffer.kt",
	"okio/src/jvmMain/kotlin/okio/Okio.kt",
	// kotlinx.serialization (kotlin) at 3efe324be422ead21ca44f2f6318e1791c166556
	"core/commonMain/src/kotlinx/serialization/Serializer.kt",
	"core/jvmMain/src/kotlinx/serialization/internal/Platform.kt",
}

// TestChangeSequence_IsGoOnly_SoTheFreshnessSuiteCannotIncludeAJVMPin pins the
// block itself. It fails the moment the filter admits a JVM source file, which
// is the moment the rest of the sequence has to be re-examined.
func TestChangeSequence_IsGoOnly_SoTheFreshnessSuiteCannotIncludeAJVMPin(t *testing.T) {
	for _, p := range jvmCorpusPaths {
		if modifiableGoFile(p) {
			t.Fatalf("modifiableGoFile(%q) = true, want false.\n\n"+
				"The freshness/incremental suite now admits a JVM source file. That is a\n"+
				"change in what the suite covers, so it is NOT enough to relax this test:\n"+
				"  1. changeSequenceMethod (cmd/eval/changeseq.go) still says the sequence\n"+
				"     runs over \"the indexed Go source files\" — it is published verbatim\n"+
				"     beside the sequence digest and would now be false;\n"+
				"  2. the ADD class writes graphi_eval_step<N>.go with a Go package clause\n"+
				"     (changeseq.go, newFileName/goPackageClause) — a .java sibling needs a\n"+
				"     matching public class name and a .kt one needs neither, so the class\n"+
				"     is language-dependent and is currently not;\n"+
				"  3. GA-LANG-java-G7 and GA-LANG-kotlin-G7 in docs/rc/evidence-index.yaml\n"+
				"     record 3-of-4 suite coverage BECAUSE of this block, and\n"+
				"     docs/eval/runs/2026-08-19-local-sandbox/g7-jvm-baseline.md §5 is the\n"+
				"     published statement of it. Both must move with the code.\n"+
				"Update all three, then update this test.", p)
		}
	}
}

// TestChangeSequence_StillAcceptsGoFiles is the control. Without it the test
// above passes just as happily against a filter that rejects everything, which
// would turn a real regression into a green run.
func TestChangeSequence_StillAcceptsGoFiles(t *testing.T) {
	for _, p := range []string{
		"server/server.go",
		"internal/transport/http2_client.go",
	} {
		if !modifiableGoFile(p) {
			t.Fatalf("modifiableGoFile(%q) = false, want true: the Go path the freshness "+
				"suite actually measures must still qualify, or the test above is vacuous", p)
		}
	}
}

// TestChangeSequenceMethod_StatesItsGoScope keeps the published determinism
// claim and the code in one place. The method string is emitted into every
// freshness artifact, so a sequence that stopped being Go-only while the string
// still said "Go source files" would publish a false description of itself.
func TestChangeSequenceMethod_StatesItsGoScope(t *testing.T) {
	if !strings.Contains(changeSequenceMethod, "Go source files") {
		t.Fatalf("changeSequenceMethod no longer states its Go scope; it reads:\n%s\n\n"+
			"If the sequence became language-aware, this string is published verbatim into "+
			"every freshness report and must say so.", changeSequenceMethod)
	}
}

// TestGoPackageClause_DoesNotUnderstandJVMPackageDeclarations records the
// second half of the mechanism. Even if the file filter were widened, the ADD
// class needs the directory's package clause, and this reader only speaks Go:
// Java's `package a.b.c;` keeps its semicolon and Kotlin files frequently
// declare no package at all. Both are pinned so the next reader does not have
// to rediscover that widening the filter is not the whole change.
func TestGoPackageClause_DoesNotUnderstandJVMPackageDeclarations(t *testing.T) {
	java := []byte("package com.google.common.collect;\n\npublic final class ImmutableList {}\n")
	if got := goPackageClause(java); got != "com.google.common.collect;" {
		t.Fatalf("goPackageClause(java) = %q; the reader takes Go's `package <ident>` line "+
			"literally, so Java's trailing semicolon is carried into the identifier. "+
			"Pinned so that a future JVM change class does not inherit it silently.", got)
	}

	kotlinNoPackage := []byte("import okio.Buffer\n\nfun main() {}\n")
	if got := goPackageClause(kotlinNoPackage); got != "" {
		t.Fatalf("goPackageClause(kotlin without a package declaration) = %q, want \"\": "+
			"a Kotlin file may legally declare no package, and the ADD class has no "+
			"clause to give a new sibling in that directory", got)
	}
}
