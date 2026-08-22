package main

// SW-191 (EVALFRESH-001 closure, AC-3): the JVM family's freshness pin.
//
// THIS FILE SUBSUMES cmd/eval/freshnessjvm_characterization_test.go, which
// SW-177 (W1.d) added to record the defect. That file pinned three stacked
// mechanisms:
//
//  1. the `.go`-only file filter;
//  2. `goPackageClause`, which read Java's `package a.b.c;` as the identifier
//     "com.google.common.collect;" — semicolon and all;
//  3. `changeSequenceMethod`, which published "Go source files" verbatim into
//     every freshness artifact.
//
// All three are closed. The two characterizations that asserted the DEFECT —
// TestGoPackageClause_DoesNotUnderstandJVMPackageDeclarations and
// TestChangeSequenceMethod_StatesItsGoScope — asserted the wrong thing once the
// mechanism moved, and are retired here rather than inverted in place: a test
// whose name says the reader does not understand JVM packages is unreadable
// once it does. Their REPLACEMENTS are the positive assertions below plus
// TestSourceFamilies_ClauseReaderIsLanguageScoped in sourcefamily_test.go.
//
// The controls SURVIVE unchanged in substance: the filter must still admit JVM
// source (a revert to `.go`-only is EVALFRESH-001 reopening) and must still
// admit Go (without which the JVM assertion would pass against a filter that
// rejects everything).
//
// No network, no corpus clone, no wall clock: every assertion is over path
// strings and file bytes, run through the production gate, the production plan
// and the shipped parser.

import (
	"strings"
	"testing"
)

// jvmCorpusPaths are real repo-relative paths from the three pinned JVM clones
// named in corpus/manifest.json. They are literal rather than read from a clone
// so the test stays offline.
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

// TestChangeSequence_FileFilterAdmitsJVMSource is the surviving control on
// mechanism (1). It goes RED the moment someone reverts to the `.go`-only
// check that was EVALFRESH-001's root cause.
func TestChangeSequence_FileFilterAdmitsJVMSource(t *testing.T) {
	for _, p := range jvmCorpusPaths {
		if !modifiableSourceFile(p) {
			t.Fatalf("modifiableSourceFile(%q) = false, want true.\n\n"+
				"The file filter reverted to Go-only, which is EVALFRESH-001's root cause.\n"+
				"A pure-Java clone (guava) or pure-Kotlin clone (okio) has zero `.go` files\n"+
				"and would again abort with 'the index contains no modifiable source\n"+
				"files to change'.", p)
		}
	}
}

// TestChangeSequence_StillAcceptsGoFiles is the control. Without it the test
// above passes just as happily against a filter that rejects everything, which
// would turn a real regression into a green run. UNCHANGED by SW-191 on
// purpose: the cobra Go control is only a control if the Go path did not move.
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

// TestChangeSequenceMethod_StatesItsRealScope replaces
// TestChangeSequenceMethod_StatesItsGoScope. The method string is emitted
// verbatim into EVERY freshness artifact, so it has to describe the sequence
// that actually ran. It said "Go source files" while the sequence was Go-only;
// now it must name the language-scoped shape and enumerate the families, and it
// must NOT still claim a Go-only scope.
func TestChangeSequenceMethod_StatesItsRealScope(t *testing.T) {
	if strings.Contains(changeSequenceMethod, "Go source files") {
		t.Fatalf("changeSequenceMethod still publishes a Go-only scope while the sequence is "+
			"language-scoped; the string is emitted into every freshness artifact and would "+
			"describe a run that did not happen:\n%s", changeSequenceMethod)
	}
	if !strings.Contains(changeSequenceMethod, "LANGUAGE-SCOPED") {
		t.Errorf("changeSequenceMethod does not state its language scope:\n%s", changeSequenceMethod)
	}
	for _, family := range familyNames() {
		if !strings.Contains(changeSequenceMethod, family) {
			t.Errorf("changeSequenceMethod does not name the %s family; a reader of the artifact "+
				"cannot tell which languages the sequence could have touched:\n%s", family, changeSequenceMethod)
		}
	}
}

// TestFreshnessJVM_JavaPackageClauseIsReadInJavasShape replaces
// TestGoPackageClause_DoesNotUnderstandJVMPackageDeclarations. Java's clause is
// terminated; the reader must return the name WITHOUT the terminator, because
// the terminator would be written into the added file's own package line.
func TestFreshnessJVM_JavaPackageClauseIsReadInJavasShape(t *testing.T) {
	java := []byte("package com.google.common.collect;\n\npublic final class ImmutableList {}\n")
	f := familyForPath("guava/src/com/google/common/collect/ImmutableList.java")
	if f == nil || f.name != "java" {
		t.Fatalf("a .java path resolves to %v, want the java family", f)
	}
	if got := f.packageClause(java); got != "com.google.common.collect" {
		t.Fatalf("java packageClause = %q, want %q (Java's terminator stripped)", got, "com.google.common.collect")
	}
	if strings.Contains(string(addedFileContent(changeStep{
		class: "add", path: "guava/src/com/google/common/collect/GraphiEvalStep0002.java",
		pkg: "com.google.common.collect", symbol: "GraphiEvalStep0002", index: 2,
	})), ";;") {
		t.Error("the added Java file carries a doubled terminator: the clause was read with the semicolon attached")
	}
}

// TestFreshnessJVM_KotlinWithoutAPackageClauseIsStillACandidate is the other
// half of the retired characterization. A Kotlin file may legally declare no
// package; the old gate then dropped its whole directory. It must not.
func TestFreshnessJVM_KotlinWithoutAPackageClauseIsStillACandidate(t *testing.T) {
	const src = "import okio.Buffer\n\nclass Standalone {\n    fun size(): Int = 0\n}\n"
	packages := map[string]string{}
	if !admitSourceFile("okio/src/commonMain/kotlin/Standalone.kt", []byte(src), packages) {
		t.Fatal("a Kotlin file with no package declaration was refused as a candidate: the JVM " +
			"default package is legal and the directory gate must not require a clause")
	}
	f := familyForPath("okio/src/commonMain/kotlin/Standalone.kt")
	if got := f.packageClause([]byte(src)); got != "" {
		t.Errorf("kotlin packageClause = %q, want \"\" for a file that declares none", got)
	}
	// And the file it adds must carry no package line at all rather than an
	// empty one, which would not parse.
	added := f.added("", "GraphiEvalStep0002", 2)
	if strings.Contains(added, "package") {
		t.Errorf("the added Kotlin file declares a package where its siblings declare none:\n%s", added)
	}
}

// TestFreshnessJVM_JavaSequenceMutatesAndConverges is the AC-3 pin for java: a
// guava-shaped tree is driven through the production gate, the production plan
// and the shipped parser.
func TestFreshnessJVM_JavaSequenceMutatesAndConverges(t *testing.T) {
	tree := familyTree{
		"guava/src/com/google/common/collect/ImmutableList.java": "package com.google.common.collect;\n\npublic final class ImmutableList {\n    int size() { return 0; }\n}\n",
		"guava/src/com/google/common/collect/Iterables.java":     "package com.google.common.collect;\n\npublic final class Iterables {\n    int count() { return 0; }\n}\n",
		"guava/src/com/google/common/base/Preconditions.java":    "package com.google.common.base;\n\npublic final class Preconditions {\n    void check() {}\n}\n",
		// A non-code file beside them: it must not become a target.
		"guava/pom.xml":   "<project/>\n",
		"guava/deps.json": "{\"a\": 1}\n",
	}
	steps := runFamilyFreshness(t, "java", tree, 12)
	for _, s := range steps {
		if s.class == "add" && !strings.HasSuffix(s.path, ".java") {
			t.Errorf("step %d adds %s, which is not a Java file", s.index, s.path)
		}
	}
}

// TestFreshnessJVM_KotlinSequenceMutatesAndConverges is the AC-3 pin for
// kotlin, over an okio/kotlinx.serialization-shaped tree.
func TestFreshnessJVM_KotlinSequenceMutatesAndConverges(t *testing.T) {
	tree := familyTree{
		"okio/src/commonMain/kotlin/okio/Buffer.kt":               "package okio\n\nclass Buffer {\n    fun size(): Int = 0\n}\n",
		"okio/src/commonMain/kotlin/okio/ByteString.kt":           "package okio\n\nclass ByteString {\n    fun utf8(): String = \"\"\n}\n",
		"core/commonMain/src/kotlinx/serialization/Serializer.kt": "package kotlinx.serialization\n\ninterface Serializer {\n    fun name(): String\n}\n",
	}
	steps := runFamilyFreshness(t, "kotlin", tree, 12)
	for _, s := range steps {
		if s.class == "add" && !strings.HasSuffix(s.path, ".kt") {
			t.Errorf("step %d adds %s, which is not a Kotlin file", s.index, s.path)
		}
	}
}
