package jvmgroundtruth_test

// SW-172 — the proof that signature-awareness is NOT VACUOUS.
//
// An oracle that never changes a verdict has proved nothing, so every test
// here is a PAIR: the same fixture, the same real javac bytecode, judged at two
// precisions, with the coarse one recorded as the WRONG answer it actually
// gives. The mis-bindings are real — they come out of the shipped binder, not
// from a fault injected into the harness — and each is a JVMSOUND candidate
// filed on the backlog.
//
// The other half of the discipline is the refusal cases: where the finer key
// cannot be rendered soundly, the oracle must ABSTAIN rather than guess, and
// TestSignature_RefusesRatherThanGuessing / TestVerify_CStyleArrayDeclarator
// pin that a rendering the harness cannot stand behind produces no verdict
// instead of a fabricated stop-ship.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// TestArity_CatchesOverloadMisbind_JVMSOUND003 is AC-2, on a REAL mis-binding.
//
// `r.apply(1 /* the scale */)` passes ONE argument. javac binds
// `a/Rate.apply:(I)I`. The binder's countArgs walks the argument_list's CST
// children and counts the `comment` node as an argument, so it looks up arity
// 2 and binds `apply(int,int)` — the wrong overload, from a call site whose
// ground truth is not in doubt.
//
// Both overloads live in a/Rate.java under the name `apply`, so the by-name key
// is IDENTICAL for the right and the wrong answer: the pre-SW-172 oracle calls
// this SOUND. Stage 1 puts arity in the key and calls it what it is.
func TestArity_CatchesOverloadMisbind_JVMSOUND003(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate {
    public int apply(int x) { return x; }
    public int apply(int x, int y) { return x + y; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Rate r) { return r.apply(1 /* the scale */); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)

	// RED BEFORE stage 1: the by-name key cannot see it.
	byName := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByName)
	t.Logf("BEFORE (stage 0):\n%s", byName.Format())
	if !byName.Sound() {
		t.Fatalf("the by-name key must be BLIND here — that blindness is the premise of this story; got %+v", byName.Violations)
	}

	// GREEN AFTER stage 1.
	byArity := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByArity)
	t.Logf("AFTER (stage 1):\n%s", byArity.Format())
	if byArity.Sound() {
		t.Fatal("stage 1 must report the arity mis-binding as a counterexample")
	}
	v := byArity.Violations[0]
	if v.CallerMethod != "run" || v.Callee != "apply" || v.CalleeArity != 2 {
		t.Fatalf("the counterexample must name the WRONG arity the binder chose (2), got %+v", v)
	}
	// The truth side must carry the right one, or the report is unreadable.
	if !hasFact(truth, "a/App.java", "run", "a/Rate.java", "apply", 1) {
		t.Fatalf("javac's fact (arity 1) missing from the truth set: %+v", truth)
	}
}

// TestSignature_CatchesEqualArityMisbind_JVMSOUND004 is AC-5, on a REAL
// mis-binding at EQUAL arity.
//
// `r.apply(ts)` passes a `Thing[]`. javac binds `a/Rate.apply:([La/Thing;)I`.
// The binder binds `apply(Thing)` — the scalar overload — because
// hierarchy.go's callableSig keys each parameter on TypeRef.Base, and Base has
// ARRAYS ERASED (table.go: "the binding name with generics/nullability/arrays
// erased"). Both overloads therefore produce the identical signature, so the
// overload set is misread as an override/hiding pair and the first-found
// binding stands where AmbiguousMember was required.
//
// Same file, same name, same ARITY: stage 1 is blind to it by construction.
// Only the parameter types separate them.
func TestSignature_CatchesEqualArityMisbind_JVMSOUND004(t *testing.T) {
	files := map[string]string{
		"a/Thing.java": `package a;
public class Thing {}
`,
		"a/Rate.java": `package a;
public class Rate {
    public int apply(Thing t) { return 1; }
    public int apply(Thing[] ts) { return ts.length; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Rate r, Thing[] ts) { return r.apply(ts); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)

	// RED BEFORE stage 2: arity agrees (1 = 1), so stage 1 cannot see it.
	byArity := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByArity)
	t.Logf("BEFORE (stage 1):\n%s", byArity.Format())
	if !byArity.Sound() {
		t.Fatalf("the by-arity key must be BLIND at equal arity; got %+v", byArity.Violations)
	}

	// GREEN AFTER stage 2.
	bySig := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.BySignature)
	t.Logf("AFTER (stage 2):\n%s", bySig.Format())
	if bySig.Sound() {
		t.Fatal("stage 2 must report the equal-arity parameter-type mis-binding")
	}
	v := bySig.Violations[0]
	if v.CalleeParams != "(La/Thing;)" {
		t.Fatalf("the counterexample must name the WRONG signature the binder chose, got %q in %+v", v.CalleeParams, v)
	}
	if !hasSig(truth, "a/Rate.java", "apply", "([La/Thing;)") {
		t.Fatalf("javac's fact ([La/Thing;) missing from the truth set: %+v", truth)
	}
}

// TestSignature_RefusesRatherThanGuessing pins the other half of the contract:
// where a parameter is an EXTERNAL type, nothing in the declaration proves
// which `String` is meant, so both sides decline and the oracle abstains — it
// does not fall back to a name match and call the fact sound, and it does not
// invent `java.lang` and call it a violation.
func TestSignature_RefusesRatherThanGuessing(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate {
    public int apply(String s) { return 1; }
    public int apply(String s, String t) { return 2; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Rate r) { return r.apply("x"); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)

	// Arity is decidable and correct here — the binder gets this one right.
	byArity := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByArity)
	if !byArity.Sound() || byArity.Matched != 1 {
		t.Fatalf("arity must decide this correctly: %s", byArity.Format())
	}

	bySig := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.BySignature)
	t.Logf("%s", bySig.Format())
	if !bySig.Sound() {
		t.Fatalf("an undecidable parameter type must never become a counterexample: %+v", bySig.Violations)
	}
	if len(bySig.Abstained) != 1 {
		t.Fatalf("the fact must be ABSTAINED, not silently matched: %+v", bySig)
	}
	if bySig.Matched != 0 {
		t.Fatal("an abstention must not be counted as a match — that is the confidence laundering the rules forbid")
	}
	if bySig.AbstainReasons[jvmgroundtruth.AbstainBinderParamUnresolved] != 1 {
		t.Fatalf("the abstention must be NAMED, got %v", bySig.AbstainReasons)
	}
}

// TestVerify_CStyleArrayDeclarator pins the guard that makes stage 2 safe to
// act on, in BOTH of the shapes it has to survive. `int xs[]` puts the brackets
// on the DECLARATOR, so TypeRef.Raw reads "int" and the harness renders `(I)`
// where javac compiled `([I)`. The binder's choice is CORRECT in both cases, so
// a naive comparison emits a FABRICATED JVMSOUND stop-ship.
//
// The two sub-cases are not the same test:
//
//   - "alone" is the easy one, and the only one the first version of this test
//     covered: `apply(I)` is simply not in javac's table for the class, so
//     membership alone catches it.
//
//   - "sibling-collision" is the one that MATTERS, and the one that forged a
//     stop-ship through the first version of the guard. Adding the scalar
//     overload makes `(I)` a descriptor javac really did compile for that name
//     — for the OTHER member — so a file-scoped (or even class-scoped)
//     membership test waves the mis-rendering through and accuses correct code.
//     Only the member-scoped rule catches it, by noticing that the two
//     declarations RENDER THE SAME descriptor.
//
// Without the second sub-case this test is non-vacuous for what it asserts but
// vacuous for the class it claims to close.
func TestVerify_CStyleArrayDeclarator(t *testing.T) {
	cases := []struct {
		name string
		rate string
	}{
		{"alone", `package a;
public class Rate {
    public int apply(int xs[]) { return xs.length; }
}
`},
		{"sibling-collision", `package a;
public class Rate {
    public int apply(int xs[]) { return xs.length; }
    public int apply(int x)    { return x; }
}
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{
				"a/Rate.java": tc.rate,
				"a/App.java": `package a;
public class App {
    public int run(Rate r, int[] xs) { return r.apply(xs); }
}
`,
			}
			javac, javap := toolchain(t)
			root := writeFixture(t, files)
			classes := compile(t, javac, root, files)
			truth, declared := disassembleWithDeclared(t, javap, classes)

			raw := jvmgroundtruth.BinderCalls(sourceBytes(files))
			// The binder's binding is CORRECT here — that is the whole point.
			// Assert it, so a future binder change cannot turn this into a test
			// of something else.
			if !hasSig(truth, "a/Rate.java", "apply", "([I)") {
				t.Fatalf("javac must have compiled apply([I): %+v", truth)
			}
			// Without the guard the rendering stands and forges a counterexample
			// — asserted, so the guard cannot be deleted without going red.
			unguarded := jvmgroundtruth.CompareAt(raw, truth, jvmgroundtruth.BySignature)
			t.Logf("UNGUARDED:\n%s", unguarded.Format())
			if unguarded.Sound() {
				t.Fatal("precondition lost: this fixture is the one whose rendering is wrong; if it no longer is, the guard is being tested against nothing")
			}

			guarded := jvmgroundtruth.CompareAt(declared.Verify(raw), truth, jvmgroundtruth.BySignature)
			t.Logf("GUARDED:\n%s", guarded.Format())
			if !guarded.Sound() {
				t.Fatalf("a rendering that is not uniquely attributable must abstain, never accuse: %+v", guarded.Violations)
			}
			if guarded.AbstainReasons[jvmgroundtruth.AbstainBinderSignatureUnverified] != 1 {
				t.Fatalf("the demotion must be NAMED, got %v", guarded.AbstainReasons)
			}
		})
	}
}

// TestVerify_IsClassScopedNotFileScoped pins the other half of the same fix.
// A source file may declare more than one class, and an index keyed on the
// SOURCE PATH silently unions their method tables — so a mis-rendering in one
// class is "confirmed" by a descriptor javac compiled for a different class
// that merely happens to share the file.
//
// Here `Pair.apply(int xs[])` renders `(I)`, which javac compiled for the
// package-private `Sibling` in the same file and never for `Pair`.
func TestVerify_IsClassScopedNotFileScoped(t *testing.T) {
	files := map[string]string{
		"a/Pair.java": `package a;
public class Pair {
    public int apply(int xs[]) { return xs.length; }
}
class Sibling {
    public int apply(int x) { return x; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Pair p, int[] xs) { return p.apply(xs); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	res := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.BySignature)
	t.Logf("%s", res.Format())
	if !res.Sound() {
		t.Fatalf("a descriptor compiled for a DIFFERENT class in the same file must not confirm a rendering: %+v", res.Violations)
	}
	if res.AbstainReasons[jvmgroundtruth.AbstainBinderSignatureUnverified] != 1 {
		t.Fatalf("the demotion must be NAMED, got %v", res.AbstainReasons)
	}
}

// TestReferenceReturns_AreNotAFalseCounterexample is the live half of the
// pre-existing parseMethodHeader defect (see
// TestParseMethodHeader_RejectsInvokeLines). Plain covariant-return Java, with
// every graphi binding CORRECT, produced three fabricated stop-ships at every
// precision — including by-name, which this story did not even touch — because
// every `self()` invoke line was eaten as a constructor header and the
// following invokes were re-parented onto `<init>`.
func TestReferenceReturns_AreNotAFalseCounterexample(t *testing.T) {
	files := map[string]string{
		"a/Base.java": `package a;
public class Base { public Base self() { return this; } }
`,
		"a/Derived.java": `package a;
public class Derived extends Base {
    @Override public Derived self() { return this; }
    public int tag() { return 1; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Derived d)   { return d.self().tag(); }
    public int viaBase(Base b)  { return b.self().hashCode(); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	// The truth set must actually CARRY the reference-returning invokes, under
	// the method that made them.
	if !hasFact(truth, "a/App.java", "run", "a/Derived.java", "self", 0) {
		t.Fatalf("the reference-returning invoke vanished from the truth set: %+v", truth)
	}
	for _, c := range truth {
		if c.CallerMethod == "<init>" && (c.Callee == "tag" || c.Callee == "self" || c.Callee == "hashCode") {
			t.Fatalf("an invoke was re-parented onto a constructor: %+v", c)
		}
	}
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		if !res.Sound() {
			t.Fatalf("correct covariant-return Java must never be accused at %s:\n%s", p, res.Format())
		}
	}
}

// TestInterfaceDispatch_IsNotAFalseCounterexample pins the third truth-losing
// parser defect, also pre-existing: javac writes `// InterfaceMethod` (not
// `// Method`) for every invoke whose resolved owner is an interface, and the
// ref parser matched only `// Method `. Every invokeinterface, every interface
// `invokestatic`, and every `X.super.m()` was therefore MISSING from the truth
// set — so correct code calling through any interface was accused at every
// precision. That is most real Java.
func TestInterfaceDispatch_IsNotAFalseCounterexample(t *testing.T) {
	files := map[string]string{
		"a/HasSeed.java": `package a;
public interface HasSeed {
    default int seed() { return 3; }
    static int base() { return 5; }
}
`,
		"a/Impl.java": `package a;
public class Impl implements HasSeed {
    public int seed() { return HasSeed.super.seed() + 1; }
}
`,
		"a/App.java": `package a;
public class App {
    public int viaIface(HasSeed h) { return h.seed(); }
    public int viaStatic()         { return HasSeed.base(); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	for _, want := range [][4]string{
		{"a/App.java", "viaIface", "a/HasSeed.java", "seed"},  // invokeinterface
		{"a/App.java", "viaStatic", "a/HasSeed.java", "base"}, // interface invokestatic
		{"a/Impl.java", "seed", "a/HasSeed.java", "seed"},     // X.super.m()
	} {
		if !hasFact(truth, want[0], want[1], want[2], want[3], 0) {
			t.Fatalf("interface invoke %v missing from the truth set: %+v", want, truth)
		}
	}
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		if !res.Sound() {
			t.Fatalf("correct interface dispatch must never be accused at %s:\n%s", p, res.Format())
		}
	}
}

// TestInterfaceDefault_AbstainsRatherThanAccusing pins the fourth verdict
// branch on real bytecode. `i.seed()` on a class that INHERITS an interface
// default compiles to `invokevirtual a/Impl.seed:()I`, and the owner walk —
// which climbs the SUPERCLASS chain only (JVMS 5.4.3.3), not 5.4.3.4's
// maximally-specific interface rule — runs off the end. graphi points at
// a/HasSeed.java, which is genuinely where `seed` is declared: graphi is RIGHT.
//
// The oracle must therefore decline. Before the fix the owner-unresolved fact
// was dropped before it could register its named reason, and the correct
// confirmed call became a violation at all three precisions.
func TestInterfaceDefault_AbstainsRatherThanAccusing(t *testing.T) {
	files := map[string]string{
		"a/HasSeed.java": `package a;
public interface HasSeed { default int seed() { return 3; } }
`,
		"a/Impl.java": `package a;
public class Impl implements HasSeed {}
`,
		"a/App.java": `package a;
public class App { public int run(Impl i) { return i.seed(); } }
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	if !hasFact(confirmed, "a/App.java", "run", "a/HasSeed.java", "seed", 0) {
		t.Fatalf("precondition: graphi must bind the declaring interface: %+v", confirmed)
	}
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		t.Logf("%s", strings.TrimSpace(res.Format()))
		if !res.Sound() {
			t.Fatalf("an unmodelled interface default must abstain at %s, never accuse: %+v", p, res.Violations)
		}
		if res.AbstainReasons[jvmgroundtruth.AbstainBytecodeOwnerUnresolved] != 1 {
			t.Fatalf("the abstention must be NAMED at %s, got %v", p, res.AbstainReasons)
		}
	}
}

// TestLocalClassCaller_AbstainsRatherThanAccusing pins the caller-side twin of
// the lambda normalization. javac compiles a local class body into its own
// class (`a/App$1L`) whose methods are the bytecode CALLER; graphi mints no
// node for the body and attributes the call to the enclosing declaration. The
// enclosing method's name lives in the EnclosingMethod attribute, which
// `javap -c -p -s` does not print, so the two sides cannot be aligned on the
// caller — and correct code was accused at every precision.
func TestLocalClassCaller_AbstainsRatherThanAccusing(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate { public int rate() { return 7; } }
`,
		"a/App.java": `package a;
public class App {
    public int run(final Rate r) {
        class L { int go() { return r.rate(); } }
        return new L().go();
    }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	if !hasFact(confirmed, "a/App.java", "run", "a/Rate.java", "rate", 0) {
		t.Fatalf("precondition: graphi must attribute the call to the enclosing method: %+v", confirmed)
	}
	if !hasFact(truth, "a/App.java", "go", "a/Rate.java", "rate", 0) {
		t.Fatalf("precondition: javac must attribute it to the local class's own method: %+v", truth)
	}
	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		t.Logf("%s", strings.TrimSpace(res.Format()))
		if !res.Sound() {
			t.Fatalf("a caller the bytecode cannot name must abstain at %s, never accuse: %+v", p, res.Violations)
		}
		if res.AbstainReasons[jvmgroundtruth.AbstainBytecodeCallerNotAlignable] != 1 {
			t.Fatalf("the abstention must be NAMED at %s, got %v", p, res.AbstainReasons)
		}
	}
}

// TestCompareAt_NoDescriptorTable_AbstainsAtEveryPrecision is the live proof
// for the multi-level coarse fallback (the unit pin is
// TestCompareAt_MultiLevelCoarseFallback). A real `-s`-less javap capture can
// only be keyed at by-name; a single-step fallback from by-signature accused
// the confirmed call because it looked for a by-arity key the truth fact never
// had. The doc comment on ParseJavap PROMISES this degrades legibly, and a
// promise nothing enforces is how SW-173 gets surprised.
func TestCompareAt_NoDescriptorTable_AbstainsAtEveryPrecision(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate { public int apply(int x) { return x; } }
`,
		"a/App.java": `package a;
public class App { public int run(Rate r) { return r.apply(1); } }
`,
	}
	javac, javap := toolchain(t)
	root := writeFixture(t, files)
	classes := compile(t, javac, root, files)
	truth := disassembleWithoutDescriptors(t, javap, classes)
	confirmed := jvmgroundtruth.BinderCalls(sourceBytes(files))

	for _, p := range precisions() {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		t.Logf("no -s, %s: %s", p, strings.TrimSpace(res.Format()))
		if !res.Sound() {
			t.Fatalf("a truth set captured without -s must decline at %s, never accuse: %+v", p, res.Violations)
		}
	}
}

// TestJVMSOUND004_ArrayDimensionality is a SECOND instance of the array-erasure
// defect, found by adversarial fixture rather than by the gate: `r.apply(xs)`
// with `xs` an `int[][]` binds `apply(int[])`, because callableSig keys each
// parameter on TypeRef.Base and Base has arrays erased — so `apply(int[])` and
// `apply(int[][])` produce the identical signature at every dimensionality, not
// only scalar-versus-array.
//
// It is recorded because it widens JVMSOUND-004's blast radius and because it
// is a TRUE POSITIVE the strengthened rendering guard still reports: both
// overloads render distinctly and both renderings are in javac's table, so the
// guard verifies rather than abstains and the real defect is not hidden by the
// fix for the forged one. Same pin discipline as the two cross-file tests: RED
// WITH INSTRUCTIONS the moment the defect is fixed.
func TestJVMSOUND004_ArrayDimensionality(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate {
    public int apply(int[] xs)   { return xs.length; }
    public int apply(int[][] xs) { return xs.length; }
}
`,
		"a/App.java": `package a;
public class App { public int run(Rate r, int[][] xs) { return r.apply(xs); } }
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	if !hasSig(truth, "a/Rate.java", "apply", "([[I)") {
		t.Fatalf("javac binds apply([[I); truth set says otherwise: %+v", truth)
	}
	if !hasSig(confirmed, "a/Rate.java", "apply", "([I)") {
		t.Fatalf("JVMSOUND-004 APPEARS FIXED (dimensionality instance).\n"+
			"The binder no longer binds an int[][] argument to apply(int[]).\n"+
			"If callableSig started keying array dimensionality, DELETE this pin and\n"+
			"add a positive regression test in engine/jvmresolve instead. Do not weaken it.\n"+
			"confirmed: %+v", confirmed)
	}
	res := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.BySignature)
	t.Logf("JVMSOUND-004 (dimensionality):\n%s", res.Format())
	if res.Sound() {
		t.Fatal("the strengthened rendering guard must still REPORT a real mis-binding, not abstain it away")
	}
}

// TestOwnerWalk_InheritedCallIsNotAFalseCounterexample pins the JVM method
// resolution the truth side now performs (JVMS 5.4.3.3).
//
// For `d.seed()` where `seed` is inherited, javac writes the SYMBOLIC owner
// `a/Derived.seed` into the constant pool; the declaring class is `a.Base`.
// graphi's confirmed edge points at the declaration. Keying the truth on the
// symbolic owner therefore disagrees with graphi on a fact BOTH get right, and
// the pre-SW-172 oracle would have reported a stop-ship JVMSOUND-0xx that does
// not exist. This is a defect in the ORACLE, and a wrong oracle is worse than
// no oracle — it corrupts every row measured against it.
func TestOwnerWalk_InheritedCallIsNotAFalseCounterexample(t *testing.T) {
	files := map[string]string{
		"a/Base.java": `package a;
public class Base {
    public int seed() { return 3; }
}
`,
		"a/Derived.java": `package a;
public class Derived extends Base {
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Derived d) { return d.seed(); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)

	// The symbolic owner is a/Derived; the walk must land on a/Base.java.
	if !hasFact(truth, "a/App.java", "run", "a/Base.java", "seed", 0) {
		t.Fatalf("the truth fact must be attributed to the DECLARING class a/Base.java, got %+v", truth)
	}
	for _, c := range truth {
		if c.CalleeFile == "a/Derived.java" && c.Callee == "seed" {
			t.Fatalf("the SYMBOLIC owner must not survive as a fact: %+v", c)
		}
	}
	for _, p := range []jvmgroundtruth.Precision{jvmgroundtruth.ByName, jvmgroundtruth.ByArity, jvmgroundtruth.BySignature} {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		if !res.Sound() {
			t.Fatalf("an inherited call is not a soundness violation at %s: %s", p, res.Format())
		}
	}
}

// TestBinderCalls_LambdaCallerNormalization pins the second false-counterexample
// source the finer keys would have exposed: javac compiles a lambda body into a
// synthetic `lambda$run$0` method, while graphi attributes the call to the
// enclosing declaration (a lambda body mints no node). Without the
// normalization the two sides disagree on the CALLER of every call written
// inside a lambda.
func TestBinderCalls_LambdaCallerNormalization(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate {
    public int rate() { return 7; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Rate r) {
        java.util.function.IntSupplier s = () -> r.rate();
        return s.getAsInt();
    }
}
`,
	}
	_, truth := binderVsBytecode(t, files)
	if !hasFact(truth, "a/App.java", "run", "a/Rate.java", "rate", 0) {
		t.Fatalf("the lambda body's call must be attributed to its enclosing method `run`, got %+v", truth)
	}
	for _, c := range truth {
		if strings.HasPrefix(c.CallerMethod, "lambda$") {
			t.Fatalf("a synthetic lambda method must not survive as a caller: %+v", c)
		}
	}
}

// TestKotlinAbstainsAtFinerPrecisions pins the honest gap: no Kotlin source has
// ever been compiled in this sandbox (kotlinc is absent, and CI installs it
// from `releases/latest`, unpinned), so the declared-Kotlin → JVM-descriptor
// mapping is UNPROVEN. Rather than assert a mapping nothing has checked, the
// Kotlin side declines at both finer precisions under a named reason. Proving
// it is SW-173's job; this test exists so the gap cannot be forgotten or
// quietly filled in with a guess.
func TestKotlinAbstainsAtFinerPrecisions(t *testing.T) {
	files := map[string][]byte{
		"k/Rate.kt": []byte(`package k
class Rate {
    fun rate(): Int { return 7 }
    fun scaled(other: Rate): Int { return other.rate() }
}
`),
	}
	calls := jvmgroundtruth.BinderCalls(files)
	if len(calls) == 0 {
		t.Fatal("expected the Kotlin binder to prove at least one call site")
	}
	for _, c := range calls {
		if c.CalleeArity != jvmgroundtruth.ArityUnknown || c.CalleeParams != jvmgroundtruth.SigUnknown {
			t.Fatalf("Kotlin must decline at the finer precisions, got %+v", c)
		}
		if c.ArityReason != jvmgroundtruth.AbstainKotlinShapeUnproven {
			t.Fatalf("the Kotlin refusal must be NAMED, got %q", c.ArityReason)
		}
	}
}

// TestJVMSOUND003_CrossFileWrongEdge pins the USER-VISIBLE consequence of the
// arity defect, which the in-file fixture above cannot show: when the two
// overloads live in DIFFERENT files, the wrong binding is a wrong confirmed
// EDGE, not merely a wrong choice inside one node.
//
// `d.m(1 /* scale */)` must bind `a.Base.m(int)` (a/Base.java). The binder
// counts the comment, looks up arity 2, and binds `a.Derived.m(int,int)`
// (a/Derived.java) — a different declaration, in a different file, reachable
// from `callers`/`callees`. D5 makes a wrong edge stop-ship.
//
// This test PINS THE DEFECT rather than asserting the fix: it is green while
// the defect exists and RED WITH INSTRUCTIONS the moment it is fixed, which is
// the discipline the parity harness's known-defect rows already use. Fixing it
// is not this story's job — SW-172 is the oracle, and "the oracle measures the
// binder; it does not improve it" is in its own Out of scope.
func TestJVMSOUND003_CrossFileWrongEdge(t *testing.T) {
	files := map[string]string{
		"a/Base.java": `package a;
public class Base {
    public int m(int x) { return x; }
}
`,
		"a/Derived.java": `package a;
public class Derived extends Base {
    public int m(int x, int y) { return x + y; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Derived d) { return d.m(1 /* scale */); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	if !hasFact(truth, "a/App.java", "run", "a/Base.java", "m", 1) {
		t.Fatalf("javac binds a/Base.java m(int); truth set says otherwise: %+v", truth)
	}
	if !hasFact(confirmed, "a/App.java", "run", "a/Derived.java", "m", 2) {
		t.Fatalf("JVMSOUND-003 APPEARS FIXED.\n"+
			"The binder no longer binds `d.m(1 /* scale */)` to a/Derived.java m(int,int).\n"+
			"If countArgs stopped counting the comment node, DELETE this pin and add a\n"+
			"positive regression test in engine/jvmresolve instead. Do not weaken it.\n"+
			"confirmed: %+v", confirmed)
	}
	res := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByName)
	t.Logf("JVMSOUND-003 (cross-file, user-visible):\n%s", res.Format())
	if res.Sound() {
		t.Fatal("a cross-file wrong edge must be a counterexample at every precision")
	}
	// …and at the GRAPH-STORE level, which is the claim that matters. The
	// binder-level set above reports binding DECISIONS and is a documented
	// SUPERSET of emitted edges (blind spot #7), so on its own it proves a wrong
	// choice, not a wrong edge. D5 makes a wrong EDGE the stop-ship, so the
	// evidence has to be an edge.
	assertWrongEdgeReachesTheStore(t, files, "a/App.java", "run", "a/Derived.java", "m")
}

// assertWrongEdgeReachesTheStore ingests the fixture with the JVM binder live
// and asserts the mis-binding survives all the way to a CONFIRMED edge in the
// graph store, pointing at the wrong file.
func assertWrongEdgeReachesTheStore(t *testing.T, files map[string]string, callerFile, callerMethod, calleeFile, callee string) {
	t.Helper()
	edges := confirmedCalls(t, writeFixture(t, files))
	for _, c := range edges {
		if c.CallerFile == callerFile && c.CallerMethod == callerMethod &&
			c.CalleeFile == calleeFile && c.Callee == callee {
			t.Logf("GRAPH-STORE EDGE (wrong file): %s.%s --calls--> %s.%s",
				c.CallerFile, c.CallerMethod, c.CalleeFile, c.Callee)
			return
		}
	}
	t.Fatalf("THE DEFECT NO LONGER REACHES THE STORE.\n"+
		"Expected a confirmed edge %s.%s --calls--> %s.%s. If the binder or the\n"+
		"emitter was fixed, delete this pin rather than weakening it.\n"+
		"confirmed edges: %+v", callerFile, callerMethod, calleeFile, callee, edges)
}

// TestJVMSOUND004_CrossFileWrongEdge is the same, for the array-erasure defect.
//
// `d.apply(t)` passes a scalar `Thing`, so javac binds `a.Base.apply(Thing)`
// (a/Base.java). callableSig keys both declarations on TypeRef.Base — which has
// arrays erased — so `apply(Thing)` and `apply(Thing[])` produce the IDENTICAL
// signature, the overload set is misread as an override pair, and the
// most-derived `a.Derived.apply(Thing[])` (a/Derived.java) wins. Wrong edge,
// different file, and the binder should have returned AmbiguousMember.
func TestJVMSOUND004_CrossFileWrongEdge(t *testing.T) {
	files := map[string]string{
		"a/Thing.java": `package a;
public class Thing {}
`,
		"a/Base.java": `package a;
public class Base {
    public int apply(Thing t) { return 1; }
}
`,
		"a/Derived.java": `package a;
public class Derived extends Base {
    public int apply(Thing[] ts) { return ts.length; }
}
`,
		"a/App.java": `package a;
public class App {
    public int run(Derived d, Thing t) { return d.apply(t); }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	if !hasFact(truth, "a/App.java", "run", "a/Base.java", "apply", 1) {
		t.Fatalf("javac binds a/Base.java apply(Thing); truth set says otherwise: %+v", truth)
	}
	if !hasSig(confirmed, "a/Derived.java", "apply", "([La/Thing;)") {
		t.Fatalf("JVMSOUND-004 APPEARS FIXED.\n"+
			"The binder no longer binds `d.apply(t)` to a/Derived.java apply(Thing[]).\n"+
			"If callableSig started keying array dimensionality (or the lookup started\n"+
			"returning AmbiguousMember here), DELETE this pin and add a positive\n"+
			"regression test in engine/jvmresolve instead. Do not weaken it.\n"+
			"confirmed: %+v", confirmed)
	}
	res := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.ByName)
	t.Logf("JVMSOUND-004 (cross-file, user-visible):\n%s", res.Format())
	if res.Sound() {
		t.Fatal("a cross-file wrong edge must be a counterexample at every precision")
	}
	assertWrongEdgeReachesTheStore(t, files, "a/App.java", "run", "a/Derived.java", "apply")
}

// TestEmptyExtensionInterface_AbstainsRatherThanAccusing is the CRITICAL-4 pin
// (round 2). `interface B extends A {}` compiles to a class file with ZERO
// declared methods — and an empty decl set was ALSO how resolveOwner recognised
// a capture taken without `-s`. Only an interface can be genuinely empty (javac
// gives every class a default constructor), so the overload was invisible until
// a fixture declared one.
//
// The consequence was the worst an oracle has: AbstainBytecodeNoDescriptors
// neither zeroes CalleeFile nor opens the truthDeclined rescue, so the truth
// fact kept the SYMBOLIC owner's path (a/B.java), stayed fully decidable at
// ByName, and CONTRADICTED graphi's CORRECT answer (a/A.java) — a fabricated
// stop-ship on three tokens of legal Java, at every precision including the one
// the pre-SW-172 gate ran at.
//
// The two controls are the point of the test: they prove the discriminator is
// exactly EMPTINESS and not "is an interface" or "is a subtype".
func TestEmptyExtensionInterface_AbstainsRatherThanAccusing(t *testing.T) {
	for _, tc := range []struct {
		name string
		// wantAbstain: the empty-interface shapes must DECLINE; the controls
		// must RESOLVE and match.
		wantAbstain bool
		files       map[string]string
	}{
		{
			name:        "empty-extension-interface",
			wantAbstain: true,
			files: map[string]string{
				"a/A.java":   "package a;\npublic interface A { default int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic interface B extends A {}\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
		{
			name:        "control-nonempty-subinterface-resolves",
			wantAbstain: false,
			files: map[string]string{
				"a/A.java":   "package a;\npublic interface A { default int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic interface B extends A { int other(); }\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
		{
			name:        "control-empty-subclass-resolves",
			wantAbstain: false,
			files: map[string]string{
				"a/A.java":   "package a;\npublic class A { public int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic class B extends A {}\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			confirmed, truth := binderVsBytecode(t, tc.files)
			// Precondition: graphi's answer is the CORRECT one — the declaring
			// type, a/A.java. If this ever stops holding the test below is
			// asserting something else.
			if !hasFact(confirmed, "a/App.java", "viaSub", "a/A.java", "seed", 0) {
				t.Fatalf("precondition: graphi must bind the DECLARING type a/A.java: %+v", confirmed)
			}
			for _, p := range precisions() {
				res := jvmgroundtruth.CompareAt(confirmed, truth, p)
				t.Logf("%s: %s", p, strings.TrimSpace(res.Format()))
				if !res.Sound() {
					t.Fatalf("correct Java accused at %s — the oracle's one absolute obligation: %+v", p, res.Violations)
				}
				if tc.wantAbstain {
					if res.AbstainReasons[jvmgroundtruth.AbstainBytecodeOwnerUnresolved] != 1 {
						t.Fatalf("an empty intermediate interface must DECLINE under %q at %s, got %v",
							jvmgroundtruth.AbstainBytecodeOwnerUnresolved, p, res.AbstainReasons)
					}
					if res.AbstainReasons[jvmgroundtruth.AbstainBytecodeNoDescriptors] != 0 {
						t.Fatalf("a genuinely empty type is NOT an -s-less capture; %q must not be claimed at %s: %v",
							jvmgroundtruth.AbstainBytecodeNoDescriptors, p, res.AbstainReasons)
					}
					continue
				}
				if res.Matched != 1 {
					t.Fatalf("the control must RESOLVE and match at %s, got matched=%d reasons=%v", p, res.Matched, res.AbstainReasons)
				}
			}
		})
	}
}

// TestCallerUnalignableRescue_IsFinestLevelOnly is the MAJOR-8 pin (round 2).
//
// The caller-unalignable rescue used to register a synthetic-caller truth fact
// at EVERY level in `levels`, and the ByName caller-agnostic key carries
// neither arity nor params nor a caller method. So the mere presence of a local
// class calling `apply` anywhere in a file rescued ANY confirmed call to
// `apply` from that file, at ANY signature — and it silently swallowed the
// filed, reproduced JVMSOUND-003 mis-binding sitting right beside it.
//
// The fix registers at the FINEST keyable level only. Both halves are asserted
// here, because either one alone is the wrong fix: the real defect must be
// reported, AND MAJOR-7's legitimate abstention must survive.
func TestCallerUnalignableRescue_IsFinestLevelOnly(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate {
    public int apply(int x) { return x; }
    public int apply(int x, int y) { return x + y; }
}
`,
		// run() carries the JVMSOUND-003 construct (the comment is counted as an
		// argument, so the binder picks arity 2). go() carries a local class
		// that calls the SAME NAME correctly — the rescue's trigger.
		"a/App.java": `package a;
public class App {
    public int run(Rate r) { return r.apply(1 /* the scale */); }
    public int go(final Rate r) {
        class L { int g() { return r.apply(1); } }
        return new L().g();
    }
}
`,
	}
	confirmed, truth := binderVsBytecode(t, files)
	if !hasFact(confirmed, "a/App.java", "run", "a/Rate.java", "apply", 2) {
		t.Fatalf("precondition: the JVMSOUND-003 mis-binding (arity 2) must be present: %+v", confirmed)
	}
	if !hasFact(truth, "a/App.java", "g", "a/Rate.java", "apply", 1) {
		t.Fatalf("precondition: javac must attribute the local class's call to a/App$1L.g: %+v", truth)
	}

	for _, p := range []jvmgroundtruth.Precision{jvmgroundtruth.ByArity, jvmgroundtruth.BySignature} {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		t.Logf("%s: %s", p, strings.TrimSpace(res.Format()))
		if res.Sound() {
			t.Fatalf("the rescue swallowed a REAL mis-binding at %s: a local class calling the same NAME must not excuse a different SIGNATURE", p)
		}
		if len(res.Violations) != 1 || res.Violations[0].CallerMethod != "run" || res.Violations[0].CalleeArity != 2 {
			t.Fatalf("the counterexample must be the arity-2 mis-binding from run(), got %+v", res.Violations)
		}
		// MAJOR-7's legitimate abstention must survive the narrowing: the
		// confirmed call from go() names a caller the bytecode calls `g`.
		if res.AbstainReasons[jvmgroundtruth.AbstainBytecodeCallerNotAlignable] != 1 {
			t.Fatalf("MAJOR-7's abstention must still fire once at %s, got %v", p, res.AbstainReasons)
		}
	}
}

// --- helpers -------------------------------------------------------------

// precisions is every precision, coarsest first. A guard that only holds at the
// precision the story added is not a guard: two of the defects fixed in round 1
// forged at ByName, which SW-172 did not touch.
func precisions() []jvmgroundtruth.Precision {
	return []jvmgroundtruth.Precision{
		jvmgroundtruth.ByName, jvmgroundtruth.ByArity, jvmgroundtruth.BySignature,
	}
}

func toolchain(t *testing.T) (javac, javap string) {
	t.Helper()
	var err error
	if javac, err = exec.LookPath("javac"); err != nil {
		t.Skip("javac unavailable; the jvm-groundtruth CI workflow installs it")
	}
	if javap, err = exec.LookPath("javap"); err != nil {
		t.Skip("javap unavailable")
	}
	return javac, javap
}

func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func compile(t *testing.T, javac, root string, files map[string]string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(javac, append([]string{"-g", "-d", out}, sourcePaths(root, files)...)...).CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, b)
	}
	return out
}

func sourceBytes(files map[string]string) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for p, c := range files {
		out[p] = []byte(c)
	}
	return out
}

// binderVsBytecode is the stage-2 differential: the SHIPPED binder's binding
// decisions on one side, javac's bytecode on the other, with the harness's own
// signature rendering checked against javac's declared-method table first.
func binderVsBytecode(t *testing.T, files map[string]string) (confirmed, truth []jvmgroundtruth.Call) {
	t.Helper()
	javac, javap := toolchain(t)
	root := writeFixture(t, files)
	classes := compile(t, javac, root, files)
	truth, declared := disassembleWithDeclared(t, javap, classes)
	return declared.Verify(jvmgroundtruth.BinderCalls(sourceBytes(files))), truth
}

func hasFact(cs []jvmgroundtruth.Call, callerFile, callerMethod, calleeFile, callee string, arity int) bool {
	for _, c := range cs {
		if c.CallerFile == callerFile && c.CallerMethod == callerMethod &&
			c.CalleeFile == calleeFile && c.Callee == callee && c.CalleeArity == arity {
			return true
		}
	}
	return false
}

func hasSig(cs []jvmgroundtruth.Call, calleeFile, callee, params string) bool {
	for _, c := range cs {
		if c.CalleeFile == calleeFile && c.Callee == callee && c.CalleeParams == params {
			return true
		}
	}
	return false
}
