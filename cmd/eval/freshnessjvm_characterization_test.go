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
// file at all.
//
// THREE INDEPENDENT MECHANISMS STACK TO BLOCK JVM PINS.
//
//   1. The file filter (was `modifiableGoFile`, now `modifiableSourceFile` in
//      cmd/eval/incremental.go).
//   2. The package-clause reader (`goPackageClause`) which supplies the package
//      a newly added sibling must declare and only understands Go's
//      `package <ident>` line.
//   3. The published determinism string (`changeSequenceMethod`) which is
//      emitted verbatim into every freshness artifact and currently says
//      "Go source files".
//
// SW-191 fix-half closed MECHANISM (1): the file filter is now registry-driven
// (it admits any extension registered in parse.NewDefaultRegistry(), so
// `.java`, `.kt`, `.py`, `.ts`, … all qualify). Mechanisms (2) and (3) are
// UNCHANGED and remain the active block on a JVM pin. The four tests below
// are split so that fixing any one of them is a named event, not a silent
// collapse of the pin:
//
//   - TestChangeSequence_FileFilterAdmitsJVMSource  pins mechanism (1)
//     REVERSED: the filter must now admit JVM source. This test goes RED the
//     moment someone reverts to the `.go`-only check.
//   - TestChangeSequence_StillAcceptsGoFiles        unchanged control.
//   - TestChangeSequenceMethod_StatesItsGoScope     pins mechanism (3).
//   - TestGoPackageClause_DoesNotUnderstandJVMPackageDeclarations  pins (2).
//
// WHAT A FULL CLOSE WOULD HAVE TO MOVE, so it is recorded in one place rather
// than rederived each time the next story revisits it:
//
//   - goPackageClause becomes language-aware (Java's `package a.b.c;`, Kotlin
//     defaults, Python's __init__.py/absence, …).
//   - changeSequenceMethod describes the new scope truthfully.
//   - The ADD class (changeseq.go: newFileName) stops hard-coding
//     graphi_eval_step<N>.go and instead writes a file whose name AND content
//     match the directory's language (a Java class name, a Kotlin top-level,
//     …).
//   - GA-LANG-{java,kotlin}-G7 in docs/rc/evidence-index.yaml move from
//     3-of-4 to 4-of-4, with the published g7-jvm-baseline.md §5 updated.
//
// WHY A TEST AND NOT ONLY A DOCUMENT. The block is the reason
// GA-LANG-{java,kotlin}-G7 cannot read "the four perf suites include L's
// corpus", and a reason recorded only in prose is a reason that silently stops
// being true. When someone closes mechanism (2) and (3), the two remaining
// tests go RED and say what else has to move with them.
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

// TestChangeSequence_FileFilterAdmitsJVMSource pins MECHANISM (1) REVERSED:
//
// the file filter is now registry-driven (SW-191 fix-half), so a JVM source
// file MUST be admitted by modifiableSourceFile. This test catches a revert
// to the `.go`-only check that was EVALFRESH-001's root cause.
//
// Mechanisms (2) and (3) — goPackageClause speaks only Go, and
// changeSequenceMethod still publishes "Go source files" verbatim — are the
// ACTIVE block on a JVM pin and remain pinned by the two tests below. They
// have to move together the moment the file filter alone is no longer the
// reason a JVM pin aborts.
func TestChangeSequence_FileFilterAdmitsJVMSource(t *testing.T) {
	for _, p := range jvmCorpusPaths {
		if !modifiableSourceFile(p) {
			t.Fatalf("modifiableSourceFile(%q) = false, want true.\n\n"+
				"The file filter reverted to Go-only, which is EVALFRESH-001's root cause.\n"+
				"A pure-Java clone (guava) or pure-Kotlin clone (okio) has zero `.go` files\n"+
				"and would again abort with 'the index contains no modifiable Go source\n"+
				"files to change'. The filter is registry-driven and must accept every\n"+
				"extension parse.NewDefaultRegistry() registers.", p)
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
		if !modifiableSourceFile(p) {
			t.Fatalf("modifiableSourceFile(%q) = false, want true: the Go path the freshness "+
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
