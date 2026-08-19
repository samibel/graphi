package jvmgroundtruth_test

// SW-173 round 1, MAJOR-1 — the `$`-suffix demangle must not rewrite a name the
// SOURCE declared.
//
// The round-1 reviewer found that SW-173's multifile demangling stripped `$` +
// the owner's simple name from ANY method name, so the legal Java method
// `foo$Bar()` declared in class `Bar` was rewritten to `foo` and the correct
// confirmed call was accused at by-name. The parent commit scored the same
// fixture `matched=1`, so it was a regression, and the published document
// asserted it could not happen.
//
// This file is the standing version of that attack. The first three cases are
// the reviewer's own fixtures, transcribed from review.md §2. The rest are the
// attacks written AFTER the narrowing, against the narrowing itself: the whole
// point of a rule that says "only these owners" is that someone will go looking
// for an owner that satisfies it by accident.
//
// The assertion has two halves, and both matter:
//
//   - ZERO VIOLATIONS at every precision. Declining is allowed; accusing is not.
//   - THE FACT IS NOT REWRITTEN. A green on soundness alone would also be
//     produced by declining everything, so each case additionally pins that the
//     bytecode fact still carries the name the source declared. That is what
//     tells a future reader whether the rule fired.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// kotlinToolchain skips when kotlinc is absent. It is NOT the same skip as the
// Java one: kotlinc is installed by the jvm-groundtruth workflow and by the
// operator running the pin corpus, so a local skip here is a coverage gap to
// state, never a pass.
func kotlinToolchain(t *testing.T) (kotlinc, javap string) {
	t.Helper()
	kotlinc, err := exec.LookPath("kotlinc")
	if err != nil {
		t.Skip("kotlinc unavailable; the jvm-groundtruth workflow installs the pinned 1.9.24")
	}
	javap, err = exec.LookPath("javap")
	if err != nil {
		t.Skip("javap unavailable")
	}
	return kotlinc, javap
}

func TestDemangle_LegalDollarNameIsNotRewritten(t *testing.T) {
	cases := []struct {
		name string
		why  string
		// wantName must appear as a Callee (or CallerMethod, when caller is
		// true) in the bytecode truth; notName must not.
		caller   bool
		wantName string
		notName  string
		files    map[string]string
	}{
		{
			name:     "reviewer-callee-foo-dollar-Bar-in-class-Bar",
			why:      "review.md §2, the callee-side forge: the suffix `$Bar` IS the owner's simple name, and the method is real",
			wantName: "foo$Bar",
			notName:  "foo",
			files: map[string]string{
				"a/Bar.java": "package a;\npublic class Bar { public int foo$Bar() { return 1; } }\n",
				"a/App.java": "package a;\npublic class App { public int run(Bar b) { return b.foo$Bar(); } }\n",
			},
		},
		{
			name:     "reviewer-caller-run-dollar-App-in-class-App",
			why:      "review.md §2, the caller-side forge: the CALLER method's name ends in `$` + its own class's simple name",
			caller:   true,
			wantName: "run$App",
			notName:  "run",
			files: map[string]string{
				"a/Rate.java": "package a;\npublic class Rate { public int apply(int x) { return x; } }\n",
				"a/App.java":  "package a;\npublic class App { public int run$App(Rate r) { return r.apply(1); } }\n",
			},
		},
		{
			name:     "reviewer-control-foo-dollar-zzz-in-class-Bar",
			why:      "review.md §2's CONTROL: a `$` name that does NOT match the owner was already safe, and must stay safe",
			wantName: "foo$zzz",
			notName:  "foo",
			files: map[string]string{
				"a/Bar.java": "package a;\npublic class Bar { public int foo$zzz() { return 1; } }\n",
				"a/App.java": "package a;\npublic class App { public int run(Bar b) { return b.foo$zzz(); } }\n",
			},
		},
		// --- attacks on the NARROWING itself ---------------------------------
		//
		// The reviewer's suggested discriminator was `__` in the owner's simple
		// name. On its own that is not enough: `__` is a perfectly legal Java
		// identifier fragment, so a Java class can be named `Foo__Bar` and
		// declare `m$Foo__Bar`. This case forges under the `__`-only rule and is
		// the reason the shipped rule ALSO requires a `.kt` source.
		{
			name:     "java-class-named-Foo__Bar-with-a-matching-dollar-method",
			why:      "the `__`-only narrowing would still rewrite this; the owner is javac-compiled, so the kotlinc lowering cannot have produced it",
			wantName: "m$Foo__Bar",
			notName:  "m",
			files: map[string]string{
				"a/Foo__Bar.java": "package a;\npublic class Foo__Bar { public int m$Foo__Bar() { return 1; } }\n",
				"a/App.java":      "package a;\npublic class App { public int run(Foo__Bar f) { return f.m$Foo__Bar(); } }\n",
			},
		},
		{
			name:     "java-class-named-exactly-two-underscores",
			why:      "`__` is a legal class name in Java 8..20, so simpleName(owner) can BE the separator the rule looks for",
			wantName: "f$__",
			notName:  "f",
			files: map[string]string{
				"a/__.java":  "package a;\npublic class __ { public int f$__() { return 1; } }\n",
				"a/App.java": "package a;\npublic class App { public int run(__ u) { return u.f$__(); } }\n",
			},
		},
		{
			name:     "dollar-suffix-with-an-empty-base",
			why:      "a method named exactly `$Bar` in class `Bar` would strip to the empty string; the rule must refuse rather than mint a nameless callee",
			wantName: "$Bar",
			notName:  "",
			files: map[string]string{
				"a/Bar.java": "package a;\npublic class Bar { public int $Bar() { return 1; } }\n",
				"a/App.java": "package a;\npublic class App { public int run(Bar b) { return b.$Bar(); } }\n",
			},
		},
		{
			name:     "two-dollar-segments-ending-in-the-owner-name",
			why:      "`a$b$Bar` in class Bar: the LAST segment matches, which is exactly the shape kotlinc's lowering also has",
			wantName: "a$b$Bar",
			notName:  "a$b",
			files: map[string]string{
				"a/Bar.java": "package a;\npublic class Bar { public int a$b$Bar() { return 1; } }\n",
				"a/App.java": "package a;\npublic class App { public int run(Bar b) { return b.a$b$Bar(); } }\n",
			},
		},
		{
			name:     "nested-class-whose-simple-name-is-the-suffix",
			why:      "the owner's internal name is a/Outer$In, so simpleName() is `In` — a nested owner must not widen what matches",
			wantName: "v$In",
			notName:  "v",
			files: map[string]string{
				"a/Outer.java": `package a;
public class Outer {
    public static class In { public int v$In() { return 1; } }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Outer.In i) { return i.v$In(); } }\n",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			confirmed, truth := binderVsBytecode(t, tc.files)
			if len(confirmed) == 0 {
				t.Fatalf("VACUOUS CASE: the binder confirmed no call, so `never accuses` cannot fail.\nwhy: %s", tc.why)
			}
			for _, p := range precisions() {
				res := jvmgroundtruth.CompareAt(confirmed, truth, p)
				if !res.Sound() {
					t.Errorf("FORGED STOP-SHIP on correct Java at %s.\nwhy this case exists: %s\n%s",
						p, tc.why, res.Format())
				}
			}
			// The fact itself: the demangling must not have fired.
			if !hasName(truth, tc.caller, tc.wantName) {
				t.Errorf("no bytecode fact carries %s %q — the truth set does not contain the name the SOURCE declared.\nwhy: %s\n%s",
					side(tc.caller), tc.wantName, tc.why, dumpNames(truth))
			}
			if tc.notName != "" && hasName(truth, tc.caller, tc.notName) {
				t.Errorf("a bytecode fact carries the DEMANGLED %s %q — the rule rewrote a name the source declared.\nwhy: %s\n%s",
					side(tc.caller), tc.notName, tc.why, dumpNames(truth))
			}
			fine := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByName)
			t.Logf("by-name: matched=%d abstained=%d reasons=%v", fine.Matched, len(fine.Abstained), fine.AbstainReasons)
		})
	}
}

// TestDemangle_KotlinCanSpellTheResidual is the disclosure test for blind spot
// #16, and it is deliberately a MEASUREMENT rather than an assertion of safety.
//
// The shipped rule demangles only when the owner's simple name contains `__`
// AND the owner was compiled from a `.kt` file. Writing the comment, the
// tempting claim was that Kotlin cannot spell a member whose name ends in `$` +
// its own class's simple name. That claim is FALSE, and this test is how it was
// found: kotlinc 1.9.24 accepts a backquoted identifier containing `$`, so
//
//	package k
//	class Foo__Bar { fun `x$Foo__Bar`(): Int = 1 }
//
// compiles to exactly the shape the rule matches. The residual is therefore
// REACHABLE from Kotlin source, and it is recorded as blind spot #17 rather than
// asserted closed. What this test pins is what the harness actually does with
// it, so the day someone closes it, the test says so instead of staying green
// either way.
func TestDemangle_KotlinCanSpellTheResidual(t *testing.T) {
	kotlinc, javap := kotlinToolchain(t)
	files := map[string]string{
		"k/Foo__Bar.kt": "package k\nclass Foo__Bar {\n    fun `x$Foo__Bar`(): Int { return 1 }\n}\n",
		"k/App.kt":      "package k\nclass App {\n    fun run(b: Foo__Bar): Int { return b.`x$Foo__Bar`() }\n}\n",
	}
	root := writeFixture(t, files)
	truth := bytecodeTruthKotlin(t, kotlinc, javap, root, files)
	rewritten := hasName(truth, false, "x")
	kept := hasName(truth, false, "x$Foo__Bar")
	t.Logf("blind spot #17 — kotlinc 1.9.24 compiles the residual; truth carries %q=%v, demangled %q=%v",
		"x$Foo__Bar", kept, "x", rewritten)
	if !rewritten && !kept {
		t.Fatalf("VACUOUS: neither name reached the truth set, so this measures nothing.\n%s", dumpNames(truth))
	}
	// No assertion that the rule declines here: it does not, and pretending
	// otherwise is the failure this whole round is about. See blind spot #17.
}

// TestDemangle_RealMultifilePartStillDemangles is the NON-VACUITY half of the
// narrowing, and the reason it is not simply "stop demangling". A rule narrowed
// until it never fires is sound and useless; this pins that the lowering the
// rule exists for still reaches it.
//
// It also measures the EXACT closure for blind spot #17, so the next story does
// not have to rediscover it: a genuine multifile part class carries
// `kotlin.Metadata(k=5)` — kind 5 is "multi-file class part" — while an
// ordinary Kotlin class carries k=1. That annotation is printed by `javap -v`
// and NOT by the `javap -c -p -s` this capture takes, which is why the shipped
// rule has to infer the same thing from the class NAME. It is the same capture
// upgrade the ACC_SYNTHETIC bridge discriminator needs (JVMCAP-001).
func TestDemangle_RealMultifilePartStillDemangles(t *testing.T) {
	kotlinc, javap := kotlinToolchain(t)
	files := map[string]string{
		"k/M.kt": "@file:JvmName(\"Facade\")\n@file:JvmMultifileClass\npackage k\nprivate fun helper(): Int = 1\nfun entry(): Int = helper()\n",
	}
	root := writeFixture(t, files)
	truth := bytecodeTruthKotlin(t, kotlinc, javap, root, files)
	// kotlinc lowers the file into k/Facade__MKt and renames the private
	// top-level function to helper$Facade__MKt at its declaration and at the
	// call site. The rule must strip it back to what the source wrote.
	if !hasName(truth, false, "helper") {
		t.Fatalf("the multifile demangling STOPPED FIRING: no truth fact names the callee `helper`. "+
			"The narrowing has cut off the lowering it exists for, and kotlinx.serialization's "+
			"forged stop-ships come straight back.\n%s", dumpNames(truth))
	}
	if hasName(truth, false, "helper$Facade__MKt") {
		t.Errorf("a truth fact still carries the MANGLED callee `helper$Facade__MKt`\n%s", dumpNames(truth))
	}
	t.Logf("multifile part demangled: callee `helper` present, mangled form absent")

	// The exact discriminator, measured rather than assumed.
	out := compiledDirOf(t, kotlinc, root, files)
	if k := metadataKind(t, javap, filepath.Join(out, "k", "Facade__MKt.class")); k != "5" {
		t.Errorf("expected kotlin.Metadata k=5 (multi-file class part) on the part class, got %q; "+
			"the JVMCAP-001 closure route for blind spot #17 does not hold as recorded", k)
	}
}

// TestAnonymousCtorRedirect_OnlyForATrulyAnonymousClass — SW-173 round 1,
// minor-1. The constructor redirect rewrites a fact to name the anonymous
// class's SUPERCLASS and the superclass's file. That is right for `new X(){…}`,
// where the source named X and javac minted `Outer$1`. It is a fabrication for
// a LOCAL class (`Outer$1L`) and for the legal user type `Foo$1Bar`, where the
// source named something else entirely — the digit-PREFIX test could not tell
// them apart.
func TestAnonymousCtorRedirect_OnlyForATrulyAnonymousClass(t *testing.T) {
	t.Run("legal-user-type-named-Foo$1Bar-is-not-redirected", func(t *testing.T) {
		files := map[string]string{
			"a/Base.java":     "package a;\npublic class Base { public int v() { return 1; } }\n",
			"a/Foo$1Bar.java": "package a;\npublic class Foo$1Bar extends Base {}\n",
			"a/App.java":      "package a;\npublic class App { public int make() { return new Foo$1Bar().v(); } }\n",
		}
		_, truth := binderVsBytecode(t, files)
		if fabricated(truth, "a/App.java", "make", "a/Base.java", "Base") {
			t.Fatalf("FABRICATED FACT: the truth set claims App.make constructs `Base` in a/Base.java; "+
				"the source wrote `new Foo$1Bar()`.\n%s", dumpNames(truth))
		}
	})

	t.Run("local-class-is-not-redirected", func(t *testing.T) {
		files := map[string]string{
			"a/Base.java": "package a;\npublic class Base { public int v() { return 1; } }\n",
			"a/App.java": `package a;
public class App {
    public int run() {
        class L extends Base {}
        return new L().v();
    }
}
`,
		}
		_, truth := binderVsBytecode(t, files)
		if fabricated(truth, "a/App.java", "run", "a/Base.java", "Base") {
			t.Fatalf("FABRICATED FACT: the truth set claims App.run constructs `Base`; the source wrote `new L()`.\n%s",
				dumpNames(truth))
		}
	})

	// NON-VACUITY, and the reason the predicate was split rather than deleted:
	// a REAL anonymous subclass must still be redirected, or forges 2 and 3 —
	// four by-name and 29 by-arity stop-ships on guava — come straight back.
	t.Run("control-a-real-anonymous-subclass-IS-redirected", func(t *testing.T) {
		files := map[string]string{
			"a/Base.java": "package a;\npublic class Base { public int v() { return 1; } }\n",
			"a/App.java":  "package a;\npublic class App { public int run() { return new Base(){}.v(); } }\n",
		}
		_, truth := binderVsBytecode(t, files)
		if !fabricated(truth, "a/App.java", "run", "a/Base.java", "Base") {
			t.Fatalf("the anonymous-subclass redirect STOPPED FIRING: `new Base(){}` must yield the constructed "+
				"type the source named. Without it the callee is the javac counter `1`.\n%s", dumpNames(truth))
		}
	})
}

// --- helpers ---------------------------------------------------------------

func side(caller bool) string {
	if caller {
		return "caller"
	}
	return "callee"
}

func hasName(truth []jvmgroundtruth.Call, caller bool, name string) bool {
	for _, c := range truth {
		if caller && c.CallerMethod == name {
			return true
		}
		if !caller && c.Callee == name {
			return true
		}
	}
	return false
}

// fabricated reports whether the truth set holds the exact (caller, callee)
// fact named — used to assert a constructor redirect did or did not happen.
func fabricated(truth []jvmgroundtruth.Call, callerFile, callerMethod, calleeFile, callee string) bool {
	for _, c := range truth {
		if c.CallerFile == callerFile && c.CallerMethod == callerMethod &&
			c.CalleeFile == calleeFile && c.Callee == callee {
			return true
		}
	}
	return false
}

// compiledDirOf compiles the Kotlin fixture again into a directory the test can
// point javap at directly. bytecodeTruthKotlin discards its output directory,
// and re-deriving it is cheaper than widening that helper's signature for one
// caller.
func compiledDirOf(t *testing.T, kotlinc, root string, files map[string]string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(kotlinc, append(sourcePaths(root, files), "-d", out)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kotlinc: %v\n%s", err, b)
	}
	return out
}

// metadataKind returns the `k=` field of the class's kotlin.Metadata
// annotation, as `javap -v` prints it, or "" when there is none.
func metadataKind(t *testing.T, javap, class string) string {
	t.Helper()
	out, err := exec.Command(javap, "-v", "-p", class).CombinedOutput()
	if err != nil {
		t.Fatalf("javap -v %s: %v\n%s", class, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.TrimSpace(line); strings.HasPrefix(f, "k=") {
			return strings.TrimPrefix(f, "k=")
		}
	}
	return ""
}

func dumpNames(truth []jvmgroundtruth.Call) string {
	var b strings.Builder
	b.WriteString("bytecode truth facts:\n")
	for _, c := range truth {
		b.WriteString("  " + c.CallerFile + "." + c.CallerMethod + " -> " + c.CalleeFile + "." + c.Callee + "\n")
	}
	return b.String()
}
