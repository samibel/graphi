package jvmbindrate

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/engine/jvmresolve"
)

// namedSymbols is the grammar's own list of named node types, read from the
// embedded language rather than from upstream documentation — the same source
// countInvocations' node types were derived from.
func namedSymbols(g *gts.Language) map[string]bool {
	m := map[string]bool{}
	for i, n := range g.SymbolNames {
		if i < len(g.SymbolMetadata) && g.SymbolMetadata[i].Named {
			m[n] = true
		}
	}
	return m
}

// occurringTypes parses every source in src and returns the set of NAMED node
// types that actually appear.
func occurringTypes(g *gts.Language, src [][]byte) map[string]bool {
	named := namedSymbols(g)
	out := map[string]bool{}
	for _, b := range src {
		tree, err := gts.NewParser(g).Parse(b)
		if err != nil || tree == nil {
			continue
		}
		var walk func(n *gts.Node)
		walk = func(n *gts.Node) {
			if n == nil {
				return
			}
			if t := n.Type(g); named[t] {
				out[t] = true
			}
			for i := 0; i < n.ChildCount(); i++ {
				walk(n.Child(i))
			}
		}
		walk(tree.RootNode())
	}
	return out
}

// TestGrammarEnumeration_ClassifiesEveryOccurringNodeType is the METHOD guard,
// and it exists because patching named defects is not a method.
//
// The denominator has been wrong eight times across two adversarial rounds, and
// the last two were the SAME defect class as the first: a call-bearing node type
// nobody had thought to look for. Fixing the two that a review named would leave
// the ninth exactly as reachable as the eighth was.
//
// So this test does not check any particular node type. It enumerates the
// grammar's own named symbols, intersects them with what a corpus actually
// produces, and fails if ANY of them is in no bucket of grammar_enumeration.go.
// A grammar upgrade that introduces a call-bearing node type — or a corpus that
// exercises one the pins never did — fails here rather than silently landing in
// a "not a call" default, because there IS no default: bucketUnclassified is the
// zero value and it is an error.
func TestGrammarEnumeration_ClassifiesEveryOccurringNodeType(t *testing.T) {
	for _, tc := range []struct {
		lang    string
		g       *gts.Language
		buckets map[string]nodeBucket
		ext     string
		fixture []byte
	}{
		{"java", grammars.JavaLanguage(), javaNodeBuckets, ".java", []byte(javaEnumerationFixture)},
		{"kotlin", grammars.KotlinLanguage(), kotlinNodeBuckets, ".kt", []byte(kotlinEnumerationFixture)},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			srcs := [][]byte{tc.fixture}
			corpus := "the hermetic fixture"
			if pins := os.Getenv("GRAPHI_JVM_CORPUS_PINS"); pins != "" {
				if real := pinSources(t, pins, tc.ext); len(real) > 0 {
					srcs = append(srcs, real...)
					corpus = "the hermetic fixture + the real pins"
				}
			}

			occurring := occurringTypes(tc.g, srcs)
			var unclassified []string
			for nt := range occurring {
				if tc.buckets[nt] == bucketUnclassified {
					unclassified = append(unclassified, nt)
				}
			}
			sort.Strings(unclassified)
			if len(unclassified) > 0 {
				t.Fatalf("%d %s node type(s) occur on %s and are in NO bucket:\n  %s\n\n"+
					"Classify each one in grammar_enumeration.go. If any of them NAMES a callable it belongs in\n"+
					"bucketWidestOnly (or bucketDenominator) and the published denominator is currently too small —\n"+
					"which is exactly the defect this test exists to stop recurring.",
					len(unclassified), tc.lang, corpus, strings.Join(unclassified, "\n  "))
			}

			// Report the coverage the package doc publishes, so the two cannot
			// drift apart unnoticed.
			var d, w, p, n int
			for nt := range occurring {
				switch tc.buckets[nt] {
				case bucketDenominator:
					d++
				case bucketWidestOnly:
					w++
				case bucketProtocol:
					p++
				case bucketNotACall:
					n++
				}
			}
			t.Logf("%s over %s: grammar named symbols %d, occurring %d — D %d, W %d, P %d, N %d",
				tc.lang, corpus, len(namedSymbols(tc.g)), len(occurring), d, w, p, n)

			// Every classified node type must be a REAL symbol of this grammar.
			// A typo in the enumeration would otherwise sit there looking like a
			// decision while classifying nothing.
			named := namedSymbols(tc.g)
			for nt := range tc.buckets {
				if !named[nt] {
					t.Errorf("%q is classified but is not a named symbol of the %s grammar — a typo classifies nothing", nt, tc.lang)
				}
			}
		})
	}
}

// TestGrammarEnumeration_CallBearingBucketsAreReachable closes the other half of
// the loop: the enumeration says which node types name a callable, and this
// asserts countInvocations actually REACTS to each of them. A node type
// classified as a call that no counter increments would be an enumeration that
// documents a fix without performing one.
func TestGrammarEnumeration_CallBearingBucketsAreReachable(t *testing.T) {
	// One fixture per call-bearing node type, exercising the construct that
	// produces it. Each must move the total of "counts that feed the widest
	// denominator" by at least one.
	for _, tc := range []struct{ lang, node, src string }{
		{jvmresolve.LangJava, "method_invocation", "class A { void m() { f(); } }"},
		{jvmresolve.LangJava, "object_creation_expression", "class A { void m() { new B(); } }"},
		{jvmresolve.LangJava, "explicit_constructor_invocation", "class A { A() { super(); } }"},
		{jvmresolve.LangJava, "enum_constant", "enum E { A(1); E(int x) {} }"},
		{jvmresolve.LangKotlin, "call_suffix", "fun m() {\n    f()\n}\n"},
		{jvmresolve.LangKotlin, "constructor_delegation_call", "class A {\n    constructor(x: Int) : this(x, 0)\n}\n"},
		{jvmresolve.LangKotlin, "constructor_invocation", "class A : B(1)\n"},
		{jvmresolve.LangKotlin, "enum_entry", "enum class E(val x: Int) {\n    A(1)\n}\n"},
		{jvmresolve.LangKotlin, "infix_expression", "fun m() {\n    val z = a shl b\n}\n"},
		{jvmresolve.LangKotlin, "indexing_suffix", "fun m() {\n    val z = a[i]\n}\n"},
		{jvmresolve.LangKotlin, "additive_expression", "fun m() {\n    val z = a + b\n}\n"},
		{jvmresolve.LangKotlin, "multiplicative_expression", "fun m() {\n    val z = a * b\n}\n"},
		{jvmresolve.LangKotlin, "prefix_expression", "fun m() {\n    val z = -a\n}\n"},
		{jvmresolve.LangKotlin, "postfix_expression", "fun m() {\n    a++\n}\n"},
		{jvmresolve.LangKotlin, "equality_expression", "fun m() {\n    val z = a == b\n}\n"},
		{jvmresolve.LangKotlin, "comparison_expression", "fun m() {\n    val z = a < b\n}\n"},
		{jvmresolve.LangKotlin, "range_expression", "fun m() {\n    val z = a..b\n}\n"},
		{jvmresolve.LangKotlin, "range_test", "fun m() {\n    when (x) {\n        in 1..5 -> f()\n    }\n}\n"},
		{jvmresolve.LangKotlin, "check_expression", "fun m() {\n    val z = a in b\n}\n"},
		{jvmresolve.LangKotlin, "assignment", "fun m() {\n    a += b\n}\n"},
	} {
		t.Run(tc.lang+"/"+tc.node, func(t *testing.T) {
			c := countInvocations(tc.lang, []byte(tc.src))
			if c.ParseErrorNodes != 0 {
				t.Fatalf("fixture parses with %d ERROR nodes — it would be measuring a recovered tree", c.ParseErrorNodes)
			}
			if c.WidestDenominator() == 0 {
				t.Fatalf("%s is classified as naming a callable, but no counter moved on %q —\n"+
					"the enumeration claims a fix countInvocations does not perform", tc.node, tc.src)
			}
		})
	}
}

// TestGrammarEnumeration_ProtocolBucketsAreReachable is the same closure for the
// synthesized-protocol bucket, which must move ITS counter and move NEITHER
// denominator.
func TestGrammarEnumeration_ProtocolBucketsAreReachable(t *testing.T) {
	for _, tc := range []struct{ lang, node, src string }{
		{jvmresolve.LangJava, "enhanced_for_statement", "class A { void m(java.util.List<String> xs) { for (String x : xs) {} } }"},
		{jvmresolve.LangJava, "resource", "class A { void m() throws Exception { try (X x = new X()) {} } }"},
		{jvmresolve.LangKotlin, "for_statement", "fun m(xs: List<Int>) {\n    for (x in xs) {\n    }\n}\n"},
		{jvmresolve.LangKotlin, "multi_variable_declaration", "fun m(p: Pair<Int, Int>) {\n    val (a, b) = p\n}\n"},
		{jvmresolve.LangKotlin, "property_delegate", "val p: Int by lazy { 1 }\n"},
	} {
		t.Run(tc.lang+"/"+tc.node, func(t *testing.T) {
			c := countInvocations(tc.lang, []byte(tc.src))
			if c.ParseErrorNodes != 0 {
				t.Fatalf("fixture parses with %d ERROR nodes", c.ParseErrorNodes)
			}
			if c.SynthesizedProtocolCalls() == 0 {
				t.Fatalf("%s is classified as a synthesized protocol but no counter moved on %q", tc.node, tc.src)
			}
		})
	}
}

// TestCSTCounts_AddIsExhaustive pins the property that makes the reflection fold
// in Measure safe, and that a hand-written fold silently loses: EVERY field
// accumulates.
//
// The failure mode it stops is specific and quiet — add a counter, publish it,
// forget its `+=`, and it reads 0 on every real corpus while every single-file
// test still passes, because a single file never exercises the fold.
func TestCSTCounts_AddIsExhaustive(t *testing.T) {
	rt := reflect.TypeOf(CSTCounts{})

	// The fold is only valid if every field is an int.
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).Type.Kind() != reflect.Int {
			t.Fatalf("CSTCounts.%s is %s, not int — CSTCounts.add folds by reflection and assumes every field accumulates by addition",
				rt.Field(i).Name, rt.Field(i).Type)
		}
	}

	// Give every field a distinct non-zero value, fold it twice, and require
	// every field to have doubled. A field the fold skips stays at its first
	// value and is named in the failure.
	var one CSTCounts
	v := reflect.ValueOf(&one).Elem()
	for i := 0; i < rt.NumField(); i++ {
		v.Field(i).SetInt(int64(i + 1))
	}
	var sum CSTCounts
	sum.add(one)
	sum.add(one)
	got := reflect.ValueOf(sum)
	for i := 0; i < rt.NumField(); i++ {
		if want := int64(2 * (i + 1)); got.Field(i).Int() != want {
			t.Errorf("CSTCounts.%s = %d after two folds, want %d — the fold does not cover this field",
				rt.Field(i).Name, got.Field(i).Int(), want)
		}
	}

	if rt.NumField() < 30 {
		t.Errorf("CSTCounts has %d fields; the enumeration should have grown it past 30 — is this the old struct?", rt.NumField())
	}
}

// pinSources reads the real pins when they are available, so the enumeration is
// checked against the corpus the figures are published from and not only against
// a fixture chosen by the same person who wrote the enumeration.
func pinSources(t *testing.T, pinsDir, ext string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, pin := range []string{"guava", "okio", "kotlinx.serialization"} {
		root := filepath.Join(pinsDir, pin)
		if _, err := os.Stat(root); err != nil {
			continue
		}
		cmd := exec.Command("git", "-C", root, "ls-tree", "-r", "HEAD", "--name-only", "-z")
		b, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, rel := range strings.Split(string(b), "\x00") {
			if rel == "" || !strings.EqualFold(path.Ext(rel), ext) {
				continue
			}
			src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
			if err == nil {
				out = append(out, src)
			}
		}
	}
	return out
}

// The hermetic fixtures below exist so the enumeration is checked on every PR
// run, not only on the dispatch-only pin run. They are deliberately dense: the
// point is to reach as many named node types as one readable file can.
//
// Both parse with ZERO ERROR nodes, asserted by
// TestEnumerationFixtures_ParseCleanly — a fixture on a recovered tree is how an
// earlier grammar-pinning test in this very package passed by luck.

const javaEnumerationFixture = `package a.b;

import java.util.List;
import java.util.function.Supplier;

@Deprecated
public class A<T extends Number> extends B implements C, D {
    static final int K = 1 << 3;
    private final List<String> xs;
    static { init(); }

    A(int x) { super(x); this.xs = null; }
    A() { this(0); }

    public <R> R m(T t, String... rest) throws Exception {
        int i = 0, j = K;
        i += 2; i++; --j;
        boolean b = (i > j) ? i != j : !(i instanceof Object);
        int[] arr = new int[]{1, 2, 3};
        int v = arr[0];
        char c = 'x';
        String s = "a\tb" + c;
        Object o = new Object() { public String toString() { return s; } };
        Supplier<String> f = A::name;
        Runnable r = () -> System.out.println(s);
        for (String q : xs) { if (q == null) continue; else break; }
        for (int k = 0; k < 3; k++) { synchronized (this) { } }
        while (b) { do { } while (!b); }
        try (AutoCloseable ac = open()) { assert b : "msg"; }
        catch (RuntimeException | Error e) { throw new IllegalStateException(e); }
        finally { }
        switch (i) { case 1: break; default: break; }
        label: { }
        return (R) f.get();
    }

    enum E { ONE(1), TWO(2); E(int x) {} }
    interface I { int go(); }
    @interface Ann { String value() default "v"; }
    record Rec(int x, int y) {}
}
`

const kotlinEnumerationFixture = `@file:JvmName("X")

package a.b

import kotlin.math.abs as absolute

typealias S = String

annotation class Ann(val value: String = "v")

open class B(x: Int)

interface I {
    fun go(): Int
}

@Ann("k")
class A<T : Number>(private val xs: List<String>) : B(1), I {
    companion object {
        const val K = 8
    }

    init {
        f()
    }

    constructor(n: Int) : this(listOf())

    val lazyOne: Int by lazy { 1 }
    var counter: Int = 0
        get() = field
        set(v) {
            field = v
        }

    override fun go(): Int = 0

    infix fun shl(other: Int): Int = 0

    operator fun get(i: Int): String = xs[i]

    operator fun set(i: Int, v: String) {
    }

    fun m(t: T?, vararg rest: String): String {
        var i = 0
        i += 2
        i++
        --i
        val neg = -i
        val not = !(i > 0)
        val sum = i + 1 - 2
        val prod = i * 2 / 3 % 4
        val cmp = i < 2 && i >= 0 || i <= 9
        val eq = (i == 1) != (i === null)
        val rng = 1..5
        val has = i in rng
        val isS = t is Number
        val idx = this[0]
        this[0] = "v"
        val elvis = t ?: 0
        val safe = t?.toString()
        val bang = t!!
        val cast = t as Number
        val (p, q) = Pair(1, 2)
        val lam = { x: Int -> x + 1 }
        val templ = "v=${'$'}{m(t)} plain ${'$'}i"
        val obj = object : I {
            override fun go(): Int = 1
        }
        val ref = ::f
        val arr = arrayOf(1, 2)
        for (x in xs) {
            if (x.isEmpty()) continue else break
        }
        while (i < 3) {
            i++
        }
        do {
            i--
        } while (i > 0)
        when (i) {
            in 1..5 -> f()
            is Int -> f()
            else -> f()
        }
        try {
            f()
        } catch (e: Exception) {
            throw IllegalStateException(e)
        } finally {
            f()
        }
        return templ
    }
}

enum class E(val x: Int) {
    ONE(1),
    TWO(2)
}

object Single : I {
    override fun go(): Int = 0
}

fun f(): Int = 0

fun List<Int>.ext(): Int = size
`

// TestEnumerationFixtures_ParseCleanly guards the guard. An enumeration fixture
// that parses to a recovered tree would silently stop producing the node types
// it was written to produce, and the coverage test above would go green by
// measuring less.
func TestEnumerationFixtures_ParseCleanly(t *testing.T) {
	for _, tc := range []struct{ lang, src string }{
		{jvmresolve.LangJava, javaEnumerationFixture},
		{jvmresolve.LangKotlin, kotlinEnumerationFixture},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			c := countInvocations(tc.lang, []byte(tc.src))
			if c.ParseErrorNodes != 0 {
				t.Fatalf("the %s enumeration fixture yields %d ERROR nodes — it is measuring a recovered tree, exactly the way this package's original grammar fixture passed by luck",
					tc.lang, c.ParseErrorNodes)
			}
			if c.UnclassifiedOperator != 0 {
				t.Fatalf("the %s fixture produced %d unclassified operator token(s) — the operator discrimination is stale",
					tc.lang, c.UnclassifiedOperator)
			}
		})
	}
}
