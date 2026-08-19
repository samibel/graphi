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
// act on. `int xs[]` puts the brackets on the DECLARATOR, so TypeRef.Raw reads
// "int" and the harness renders `(I)` where javac compiled `([I)`. The binder's
// choice here is CORRECT — there is only one method — so a naive comparison
// would emit a FABRICATED JVMSOUND stop-ship.
//
// DeclaredMethods.Verify catches it because `apply(I)` is not among the
// descriptors javac compiled for a/Rate.java, and demotes the fact to an
// abstention instead. The test asserts both halves: unverified renderings do
// not become violations, AND the demotion is named.
func TestVerify_CStyleArrayDeclarator(t *testing.T) {
	files := map[string]string{
		"a/Rate.java": `package a;
public class Rate {
    public int apply(int xs[]) { return xs.length; }
}
`,
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
	// Without the guard the rendering stands and forges a counterexample —
	// asserted, so the guard cannot be deleted without this test going red.
	unguarded := jvmgroundtruth.CompareAt(raw, truth, jvmgroundtruth.BySignature)
	t.Logf("UNGUARDED:\n%s", unguarded.Format())
	if unguarded.Sound() {
		t.Fatal("precondition lost: this fixture is the one whose rendering is wrong; if it no longer is, the guard is being tested against nothing")
	}

	guarded := jvmgroundtruth.CompareAt(declared.Verify(raw), truth, jvmgroundtruth.BySignature)
	t.Logf("GUARDED:\n%s", guarded.Format())
	if !guarded.Sound() {
		t.Fatalf("a rendering javac never compiled must abstain, never accuse: %+v", guarded.Violations)
	}
	if guarded.AbstainReasons[jvmgroundtruth.AbstainBinderSignatureUnverified] != 1 {
		t.Fatalf("the demotion must be NAMED, got %v", guarded.AbstainReasons)
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
}

// --- helpers -------------------------------------------------------------

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
