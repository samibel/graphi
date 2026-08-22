package jvmgroundtruth_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/semantic"
	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

// TestGroundTruth_Java_LiveJDK is the WP-J9 soundness gate, run END TO END with
// a real JDK: it builds a graphi graph with the JVM binder live, compiles the
// SAME sources with javac, extracts the bytecode call facts via javap, and
// asserts every confirmed graphi call is backed by bytecode (zero tolerance).
// It skips when javac/javap are absent — CI installs them; the sandbox already
// has them, so this runs here. Kotlin ground truth needs kotlinc and is the CI
// workflow's job; this hermetic test is Java-only.
//
// This is the first proof that the binder is SOUND on real bytecode, not just
// self-consistent: graphi and javac are independent implementations of the
// same static-binding contract (ADR 0008 D1), and this asserts graphi never
// claims a call javac's bytecode does not make.
func TestGroundTruth_Java_LiveJDK(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable; the jvm-groundtruth CI workflow installs it")
	}
	javap, err := exec.LookPath("javap")
	if err != nil {
		t.Skip("javap unavailable")
	}

	// The fixture deliberately includes an AMBIGUOUS overload (apply(int) /
	// apply(String)): graphi DROPS the r.apply(1) call (D6), so confirmed is a
	// strict subset of the bytecode — the most instructive soundness case,
	// where the conservative drop still leaves graphi ⊆ truth.
	files := map[string]string{
		"tax/Rate.java": `package tax;
public class Rate {
    public Rate(int seed) {}
    public int rate() { return 7; }
    public int scaled(Rate other) { return other.rate(); }
    public int apply(int x) { return x; }
    public int apply(String s) { return 0; }
    public static int base() { return 1; }
}
`,
		"shop/Cart.java": `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.rate() + r.apply(1); }
    public Rate build() { return new Rate(9); }
    public int total() { return Rate.base(); }
}
`,
		// A super.m() call: the receiver types to the direct class supertype
		// (intra-repo, chain closed), which javac binds NON-VIRTUALLY as
		// `invokespecial tax/Base.seed` — a distinct dispatch to a class OTHER
		// than the <init> case, so it exercises the invokespecial path on a
		// regular method.
		"tax/Base.java": `package tax;
public class Base {
    public int seed() { return 3; }
}
`,
		"tax/Derived.java": `package tax;
public class Derived extends Base {
    public int reseed() { return super.seed(); }
}
`,
	}

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

	// (1) graphi confirmed calls, binder live.
	confirmed := confirmedCalls(t, root)

	// (2) bytecode truth via javac + javap.
	truth := bytecodeTruth(t, javac, javap, root, files)

	// (3) the zero-tolerance soundness verdict + measured recall.
	res := jvmgroundtruth.Compare(confirmed, truth)
	t.Log(strings.TrimSpace(res.Format()))
	if !res.Sound() {
		t.Fatalf("SOUNDNESS FAILURE:\n%s\nconfirmed: %+v\ntruth: %+v", res.Format(), confirmed, truth)
	}

	// Five provable calls must be confirmed, spanning the invoke opcodes real
	// code emits so the oracle covers the dispatch surface, not one form:
	//   checkout→rate  invokevirtual  (instance call through a declared param)
	//   scaled→rate    invokevirtual  (instance call through a declared param)
	//   build→Rate     invokespecial  (constructor; the bytecode's
	//                                  `invokespecial tax/Rate.<init>` normalizes
	//                                  to the type node graphi targets)
	//   total→base     invokestatic   (static call through the type name)
	//   reseed→seed    invokespecial  (super.seed() — non-virtual dispatch to a
	//                                  class OTHER than <init>)
	// The ambiguous checkout→apply must be DROPPED (present in bytecode, absent
	// from confirmed), so recall stays strictly below 100% — the honest, sound
	// outcome. That static and both invokespecial forms key identically to a
	// virtual call in the comparator (Compare is method-to-method, opcode-blind)
	// is exactly the property being asserted: graphi ⊆ bytecode holds across
	// dispatch forms.
	if got := len(confirmed); got != 5 {
		t.Fatalf("expected exactly 5 confirmed calls (checkout→rate, scaled→rate, build→Rate ctor, total→base static, reseed→seed super), got %d: %+v", got, confirmed)
	}
	if res.TruthIntra <= res.Matched {
		t.Fatalf("the ambiguous overload must leave a recall gap (truth %d > matched %d)", res.TruthIntra, res.Matched)
	}
}

// TestGroundTruth_Java_SignatureAware_LiveJDK is the SW-172 gate: the same
// differential as above, run at all three precisions against the binder's own
// binding decisions rather than the graph store's projection of them.
//
// It exists because the by-name gate above is structurally incapable of failing
// on an overload mis-binding — `apply(int)` and `apply(String)` are one node and
// one fact — so a green run of it says nothing about overload resolution. This
// one keys on arity and on erased parameter types, and it reports its own
// coverage: how many facts it DECLINED to judge and why, so a green verdict can
// never be read as covering more than it did.
//
// The graph-store gate is not replaced. The two answer different questions:
// that one asks whether the EMITTED EDGES are backed by bytecode, this one
// whether the BINDING DECISIONS behind them are.
func TestGroundTruth_Java_SignatureAware_LiveJDK(t *testing.T) {
	javac, err := exec.LookPath("javac")
	if err != nil {
		t.Skip("javac unavailable; the jvm-groundtruth CI workflow installs it")
	}
	javap, err := exec.LookPath("javap")
	if err != nil {
		t.Skip("javap unavailable")
	}

	files := map[string]string{
		"tax/Rate.java": `package tax;
public class Rate {
    public Rate(int seed) {}
    public int rate() { return 7; }
    public int scaled(Rate other) { return other.rate(); }
    public int apply(int x) { return x; }
    public int apply(String s) { return 0; }
    public static int base() { return 1; }
}
`,
		"shop/Cart.java": `package shop;
import tax.Rate;
public class Cart {
    public int checkout(Rate r) { return r.rate() + r.apply(1); }
    public Rate build() { return new Rate(9); }
    public int total() { return Rate.base(); }
}
`,
		"tax/Base.java": `package tax;
public class Base {
    public int seed() { return 3; }
}
`,
		"tax/Derived.java": `package tax;
public class Derived extends Base {
    public int reseed() { return super.seed(); }
}
`,
	}

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

	out := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	compile := exec.Command(javac, append([]string{"-g", "-d", out}, sourcePaths(root, files)...)...)
	if b, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, b)
	}
	truth, declared := disassembleWithDeclared(t, javap, out)

	src := map[string][]byte{}
	for rel, content := range files {
		src[rel] = []byte(content)
	}
	// Verify FIRST: no verdict may rest on a rendering javac never compiled.
	confirmed := declared.Verify(jvmgroundtruth.BinderCalls(src))

	for _, p := range []jvmgroundtruth.Precision{
		jvmgroundtruth.ByName, jvmgroundtruth.ByArity, jvmgroundtruth.BySignature,
	} {
		res := jvmgroundtruth.CompareAt(confirmed, truth, p)
		t.Log(strings.TrimSpace(res.Format()))
		if !res.Sound() {
			t.Fatalf("SOUNDNESS FAILURE at %s:\n%s\nconfirmed: %+v\ntruth: %+v", p, res.Format(), confirmed, truth)
		}
	}

	// NON-VACUITY. A differential that judges nothing is green for free, so the
	// finest precision must be shown to have actually DECIDED something on this
	// fixture — not merely abstained its way to silence.
	fine := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.BySignature)
	if fine.Matched == 0 {
		t.Fatalf("by-signature judged nothing on this fixture — a vacuous green:\n%s", fine.Format())
	}
	// And the arity key must genuinely separate facts the name key merges: the
	// two `apply` overloads are one by-name key and two by-arity ones.
	byNameKeys, byArityKeys := map[string]struct{}{}, map[string]struct{}{}
	for _, c := range truth {
		if c.Callee != "apply" {
			continue
		}
		byNameKeys[c.CallerFile+"|"+c.CallerMethod+"|"+c.CalleeFile] = struct{}{}
		byArityKeys[c.CallerFile+"|"+c.CallerMethod+"|"+c.CalleeFile+"|"+c.CalleeParams] = struct{}{}
	}
	if len(byNameKeys) == 0 {
		t.Fatal("the fixture's overloaded call vanished from the truth set")
	}
}

// TestGroundTruth_Kotlin_LiveKotlinc is the Kotlin half of the WP-J9 soundness
// gate, run END TO END with a real kotlinc: it builds a graphi graph with the
// binder live, compiles the SAME .kt sources with kotlinc, extracts the
// bytecode call facts via javap, and asserts every confirmed graphi call is
// backed by bytecode (zero tolerance).
//
// UNPROVEN IN A JDK-ONLY SANDBOX BY DESIGN (plan M1.3): kotlinc was absent where
// this was written, so the test SKIPS locally and is proven for the FIRST time
// by the jvm-groundtruth CI workflow, which installs kotlinc. The split is
// deliberate and honest — the graphi side (confirmedCalls) needs no toolchain
// and IS validated locally, so only the bytecode side waits for CI. The
// comparator is language-independent below the compiler: ConfirmedCalls and
// ParseJavap both key on (source-path, simple-name), and kotlinc's `.class`
// files carry the SourceFile attribute and plain method refs exactly as javac's
// do, so a class-method fixture disassembles to the same shape.
func TestGroundTruth_Kotlin_LiveKotlinc(t *testing.T) {
	kotlinc, err := exec.LookPath("kotlinc")
	if err != nil {
		t.Skip("kotlinc unavailable; the jvm-groundtruth CI workflow installs it")
	}
	javap, err := exec.LookPath("javap")
	if err != nil {
		t.Skip("javap unavailable")
	}

	// A minimal CLASS-METHOD fixture — no top-level functions, data classes,
	// default args or inline: the forms whose synthetic bytecode could surprise
	// the oracle are deliberately excluded, so the classes reduce to plain
	// invokevirtual method refs. Two confirmed calls through declared-typed
	// receivers: App.run (a declared local) and Rate.scaled (a declared param),
	// both binding Rate.rate.
	files := map[string]string{
		"k/Rate.kt": `package k
class Rate {
    fun rate(): Int { return 7 }
    fun scaled(other: Rate): Int { return other.rate() }
}
`,
		"k/App.kt": `package k
class App {
    fun run(r: Rate): Int {
        val typed: Rate = r
        return typed.rate()
    }
}
`,
	}

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

	// The graphi side is deterministic and toolchain-free: exactly the two
	// declared-typed calls must be confirmed, regardless of kotlinc. A drift
	// here is a product change, caught even in a JDK-only run.
	confirmed := confirmedCalls(t, root)
	if got := len(confirmed); got != 2 {
		t.Fatalf("expected exactly 2 confirmed Kotlin calls (run→rate, scaled→rate), got %d: %+v", got, confirmed)
	}

	truth := bytecodeTruthKotlin(t, kotlinc, javap, root, files)
	res := jvmgroundtruth.Compare(confirmed, truth)
	t.Log(strings.TrimSpace(res.Format()))
	if !res.Sound() {
		t.Fatalf("SOUNDNESS FAILURE — before reading this as a product defect, rule out a Kotlin bytecode-keying difference in the harness (source-path reconstruction or method-name mangling), since this path is proven for the first time in CI:\n%s\nconfirmed: %+v\ntruth: %+v", res.Format(), confirmed, truth)
	}
	// Soundness ⟺ every confirmed call is in truth, so recall is 2/2 here (the
	// external Intrinsics null-checks kotlinc inserts are kotlin-stdlib calls
	// graphi never confirms and the comparator scores as external, not intra).
}

// confirmedCalls builds the graph with the binder live and projects its
// confirmed calls edges. Language-agnostic: ingest runs every registered
// binder over root, so the same helper serves the Java and Kotlin gates.
func confirmedCalls(t *testing.T, root string) []jvmgroundtruth.Call {
	t.Helper()
	t.Setenv(semantic.EnvJVM, "1")
	ctx := context.Background()
	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("nodes: %v", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		t.Fatalf("edges: %v", err)
	}
	return jvmgroundtruth.ConfirmedCalls(nodes, edges)
}

// bytecodeTruth compiles the fixture with javac -g and disassembles every
// class into the bytecode call facts.
func bytecodeTruth(t *testing.T, javac, javap, root string, files map[string]string) []jvmgroundtruth.Call {
	t.Helper()
	out := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	compile := exec.Command(javac, append([]string{"-g", "-d", out}, sourcePaths(root, files)...)...)
	if b, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("javac: %v\n%s", err, b)
	}
	return disassemble(t, javap, out)
}

// bytecodeTruthKotlin compiles the fixture with kotlinc and disassembles every
// class into the bytecode call facts. kotlinc emits standard JVM `.class` files
// with the SourceFile attribute set to the `.kt` name, so javap and the shared
// ParseJavap read them exactly as they read javac's output — the differential
// oracle is language-independent below the compiler.
func bytecodeTruthKotlin(t *testing.T, kotlinc, javap, root string, files map[string]string) []jvmgroundtruth.Call {
	t.Helper()
	out := filepath.Join(t.TempDir(), "classes")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	compile := exec.Command(kotlinc, append(sourcePaths(root, files), "-d", out)...)
	if b, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("kotlinc: %v\n%s", err, b)
	}
	return disassemble(t, javap, out)
}

// sourcePaths turns the fixture map into absolute source paths.
func sourcePaths(root string, files map[string]string) []string {
	var srcs []string
	for rel := range files {
		srcs = append(srcs, filepath.Join(root, filepath.FromSlash(rel)))
	}
	return srcs
}

// disassemble enumerates every compiled class under out, runs `javap -c -p -s`
// over them, and parses the call facts. Shared by the Java and Kotlin gates —
// once the class files exist, the disassembly path is identical.
func disassemble(t *testing.T, javap, out string) []jvmgroundtruth.Call {
	t.Helper()
	truth, _ := disassembleWithDeclared(t, javap, out)
	return truth
}

// compiledClasses enumerates every `.class` under out as a dotted class name.
func compiledClasses(t *testing.T, out string) []string {
	t.Helper()
	var classes []string
	err := filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".class") {
			return err
		}
		rel, rerr := filepath.Rel(out, p)
		if rerr != nil {
			return rerr
		}
		cls := strings.TrimSuffix(filepath.ToSlash(rel), ".class")
		classes = append(classes, strings.ReplaceAll(cls, "/", "."))
		return nil
	})
	if err != nil {
		t.Fatalf("walk classes: %v", err)
	}
	if len(classes) == 0 {
		t.Fatal("compiler produced no classes")
	}
	return classes
}

// disassembleWithoutDescriptors is the `-s`-LESS capture: a truth set that can
// only be keyed at ByName. It exists so the "the truth set cannot answer at
// this precision" branch is proven against a real javap run, not only against
// hand-built Call values.
func disassembleWithoutDescriptors(t *testing.T, javap, out string) []jvmgroundtruth.Call {
	t.Helper()
	b, err := exec.Command(javap, append([]string{"-c", "-p", "-classpath", out}, compiledClasses(t, out)...)...).Output()
	if err != nil {
		t.Fatalf("javap: %v", err)
	}
	truth, err := jvmgroundtruth.ParseJavap(b)
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	return truth
}

// disassembleWithDeclared is disassemble plus javac's own declared-method
// index, which the binder-level differential needs to check its rendering
// against (DeclaredMethods.Verify). `-s` is what prints the `descriptor:`
// lines both the JVM owner walk and that index read.
func disassembleWithDeclared(t *testing.T, javap, out string) ([]jvmgroundtruth.Call, jvmgroundtruth.DeclaredMethods) {
	t.Helper()
	classes := compiledClasses(t, out)
	disasm := exec.Command(javap, append([]string{"-c", "-p", "-s", "-classpath", out}, classes...)...)
	b, err := disasm.Output()
	if err != nil {
		t.Fatalf("javap: %v", err)
	}
	truth, err := jvmgroundtruth.ParseJavap(b)
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	declared, err := jvmgroundtruth.ParseDeclaredMethods(b)
	if err != nil {
		t.Fatalf("ParseDeclaredMethods: %v", err)
	}
	return truth, declared
}
