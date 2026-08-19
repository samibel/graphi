package jvmbindrate_test

import (
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/internal/jvmbindrate"
)

// countNodes tallies node types over the REAL embedded grammar. It exists so
// the assertions below are about the grammar this binary actually parses with,
// not about upstream documentation — the same discipline the walkers' own
// comments state ("node-type and field names below were taken from the REAL
// embedded grammar, not from upstream docs").
func countNodes(t *testing.T, lang *gts.Language, src string) map[string]int {
	t.Helper()
	out := map[string]int{}
	tree, err := gts.NewParser(lang).Parse([]byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		out[n.Type(lang)]++
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tree.RootNode())
	// A fixture that does not parse cleanly is measuring a partially-recovered
	// tree, and an assertion about a recovered tree is an assertion about
	// tree-sitter's error recovery rather than about the grammar. An earlier
	// draft of this file asserted the Kotlin annotation/delegation split on a
	// one-line `class A : B(1) { … }` fixture that yields TWO ERROR nodes; it
	// passed by luck. Every fixture here now fails loudly instead.
	if out["ERROR"] > 0 {
		t.Fatalf("fixture does not parse cleanly (%d ERROR node(s)) — it cannot pin the grammar:\n%s", out["ERROR"], src)
	}
	return out
}

// TestGrammarShapes_AreWhatWeCount is the review question asked of the grammar
// rather than of a comment: does the denominator count anything that is not a
// call site, and does it miss anything that is?
//
// The two constructs an adversarial reader should reach for first are
// ANNOTATIONS (`@Dep("x")` looks like a call and is not) and CONSTRUCTOR
// DELEGATION (`super(…)` / `: B(1)` is a call and does not look like one). Both
// are pinned here against the real grammar, in both languages.
func TestGrammarShapes_AreWhatWeCount(t *testing.T) {
	t.Run("java annotations are not method_invocation", func(t *testing.T) {
		// Four annotations with arguments, and exactly ZERO calls.
		n := countNodes(t, grammars.JavaLanguage(), `
package a;
@Anno(v = 1) @Other("s")
class A {
  @Dep("x") @Inject(name = "n") void m() {}
}
`)
		if n["method_invocation"] != 0 || n["object_creation_expression"] != 0 {
			t.Fatalf("annotations leaked into the call-site node types: %v", n)
		}
		if n["annotation"] != 4 {
			t.Fatalf("fixture is vacuous — expected 4 annotation nodes, got %d", n["annotation"])
		}
	})

	t.Run("java super and this are their own node type", func(t *testing.T) {
		n := countNodes(t, grammars.JavaLanguage(), `
package a;
class A { A() { super(1); } A(int x) { this(); } }
`)
		if got := n["explicit_constructor_invocation"]; got != 2 {
			t.Fatalf("explicit_constructor_invocation = %d, want 2", got)
		}
		// The load-bearing half: they are NOT method_invocation, so the
		// primary denominator does not contain them and the exclusion is a
		// real decision rather than an accident of the walk.
		if got := n["method_invocation"]; got != 0 {
			t.Fatalf("super()/this() leaked into method_invocation: %d", got)
		}
	})

	t.Run("kotlin annotations and delegation SHARE a node type", func(t *testing.T) {
		// This is the single most dangerous shape in the whole measurement:
		// the Kotlin grammar spells `@Anno(v = 1)` and `class A : B(1)` with
		// the SAME node type. A denominator counting constructor_invocation
		// would put annotations in a call-site count.
		//
		// NOTE the formatting: members are on their own lines. The Kotlin
		// grammar SHREDS a class body whose members share a line with the
		// brace — the one-line form of this very fixture yields 2 ERROR nodes
		// — and countNodes now refuses such a fixture outright.
		const src = `
package a
@Anno(v = 1)
class A : B(1) {
    @Other("s")
    fun m() {}
}
`
		n := countNodes(t, grammars.KotlinLanguage(), src)
		if got := n["constructor_invocation"]; got != 3 {
			t.Fatalf("constructor_invocation = %d, want 3 (2 annotations + 1 delegation)", got)
		}
		if got := n["call_suffix"]; got != 0 {
			t.Fatalf("call_suffix = %d, want 0 — nothing here is an invocation expression", got)
		}
		// And the measurement separates them by PARENT, which is the only
		// thing that can: 1 delegation, 2 annotations, 0 in the denominator.
		r := measureOne(t, "A.kt", src)["kotlin"]
		if r.CST.Denominator() != 0 {
			t.Fatalf("denominator = %d, want 0 — an annotation is not a call site", r.CST.Denominator())
		}
		if r.CST.DelegationCtorCall != 1 || r.CST.AnnotationCtorCall != 2 {
			t.Fatalf("delegation/annotation split = %d/%d, want 1/2", r.CST.DelegationCtorCall, r.CST.AnnotationCtorCall)
		}
	})

	// The findings below were produced by an adversarial review of the
	// denominator and REPRODUCED first-hand before anything was changed. Each
	// is a construct the first draft got wrong; each now has a fixture.

	t.Run("kotlin string-template calls have NO call_expression", func(t *testing.T) {
		// The largest error in the first draft: counting `call_expression`
		// dropped every call written inside a string template. The grammar
		// emits `interpolated_expression → simple_identifier + call_suffix`
		// with no enclosing call_expression at all.
		const src = `
fun m(h: H): String {
    return "a ${f()} b ${g(1)} c ${h.i()}"
}
`
		n := countNodes(t, grammars.KotlinLanguage(), src)
		if n["call_expression"] != 0 {
			t.Fatalf("call_expression = %d — the fixture no longer demonstrates the mechanism", n["call_expression"])
		}
		if got := n["call_suffix"]; got != 3 {
			t.Fatalf("call_suffix = %d, want 3", got)
		}
		if got := measureOne(t, "a.kt", src)["kotlin"].CST.Denominator(); got != 3 {
			t.Fatalf("denominator = %d, want 3 — interpolated calls must be in the denominator", got)
		}
	})

	t.Run("kotlin call_suffix does not pull non-calls in", func(t *testing.T) {
		// The other direction, and what makes call_suffix exact rather than
		// merely wider: annotations, both kinds of constructor delegation and
		// enum entries all carry `value_arguments`, never `call_suffix`.
		for name, src := range map[string]string{
			"annotation + superclass delegation": "@Anno(v = 1)\nclass A : B(1) {\n    fun m() {}\n}\n",
			"secondary constructor delegation":   "class A(x: Int, y: Int) {\n    constructor(x: Int) : this(x, 0)\n}\n",
			"enum entries":                       "enum class E(val x: Int) {\n    A(1),\n    B(2)\n}\n",
		} {
			if got := countNodes(t, grammars.KotlinLanguage(), src)["call_suffix"]; got != 0 {
				t.Errorf("%s: call_suffix = %d, want 0 — a non-call reached the denominator", name, got)
			}
		}
		// Control: on ordinary code the two counts coincide exactly, so
		// call_suffix is not merely a bigger number.
		n := countNodes(t, grammars.KotlinLanguage(), "fun m(a: A, c: C) {\n    f()\n    a.b()\n    C(1)\n}\n")
		if n["call_suffix"] != 3 || n["call_expression"] != 3 {
			t.Fatalf("control: call_suffix %d, call_expression %d — want 3 and 3", n["call_suffix"], n["call_expression"])
		}
	})

	t.Run("java explicit ctor's TRUE kotlin twin is constructor_delegation_call", func(t *testing.T) {
		// The first draft paired java `super(…)` with kotlin `: B(1)`. That
		// was wrong: `: B(1)` is the PRIMARY constructor's superclass call,
		// whose java counterpart is `extends B` — no invocation node at all.
		// The real twin is a different node type entirely.
		j := measureOne(t, "A.java", "class A {\n  A(int x, int y) {}\n  A(int x) { this(x, 0); }\n  A() { super(1); }\n}\n")["java"]
		k := measureOne(t, "A.kt", "class A(x: Int, y: Int) : B(1) {\n    constructor(x: Int) : this(x, 0)\n    constructor() : super(1)\n}\n")["kotlin"]
		if j.CST.ExplicitCtorInvocation != 2 {
			t.Fatalf("java explicit_constructor_invocation = %d, want 2", j.CST.ExplicitCtorInvocation)
		}
		if k.CST.CtorDelegationCall != 2 {
			t.Fatalf("kotlin constructor_delegation_call = %d, want 2 — the true twin of java's 2", k.CST.CtorDelegationCall)
		}
		// And the construct they are NOT the twin of is counted separately.
		if k.CST.DelegationCtorCall != 1 {
			t.Fatalf("kotlin superclass delegation = %d, want 1 (java's `extends B`, which has no node)", k.CST.DelegationCtorCall)
		}
	})

	t.Run("enum constant argument lists are excluded symmetrically and counted", func(t *testing.T) {
		j := measureOne(t, "E.java", "enum E {\n  A(1), B(2);\n  E(int x) {}\n}\n")["java"]
		k := measureOne(t, "E.kt", "enum class E(val x: Int) {\n    A(1),\n    B(2)\n}\n")["kotlin"]
		if j.CST.Denominator() != 0 || k.CST.Denominator() != 0 {
			t.Fatalf("denominators = %d/%d, want 0/0", j.CST.Denominator(), k.CST.Denominator())
		}
		if j.CST.EnumConstantArgs != 2 || k.CST.EnumEntryArgs != 2 {
			t.Fatalf("enum exclusion sizes = %d/%d, want 2/2 — an uncounted exclusion is a place to hide",
				j.CST.EnumConstantArgs, k.CST.EnumEntryArgs)
		}
		// Both are calls to a constructor, so the widest denominator takes them.
		if j.CST.WidestDenominator() != 2 || k.CST.WidestDenominator() != 2 {
			t.Fatalf("widest = %d/%d, want 2/2", j.CST.WidestDenominator(), k.CST.WidestDenominator())
		}
	})

	t.Run("kotlin operator-shaped calls are excluded and counted", func(t *testing.T) {
		r := measureOne(t, "a.kt", "fun m(a: X, b: Int, i: Int) {\n    val c = a shl b\n    val d = a[i]\n}\n")["kotlin"]
		if r.CST.Denominator() != 0 {
			t.Fatalf("denominator = %d, want 0 — excluded for symmetry with java", r.CST.Denominator())
		}
		if r.CST.InfixExpression != 1 || r.CST.IndexingSuffix != 1 {
			t.Fatalf("infix/indexing = %d/%d, want 1/1", r.CST.InfixExpression, r.CST.IndexingSuffix)
		}
		if r.CST.WidestDenominator() != 2 {
			t.Fatalf("widest = %d, want 2 — the widest variant must be able to make the rate look WORSE", r.CST.WidestDenominator())
		}
	})
}

// TestIndexingSuffix_CountsWritesAndInterpolations is the MAJOR-2 regression.
//
// The F1 defect — the Kotlin grammar's invocation marker is the SUFFIX node,
// not the EXPRESSION node, because some contexts emit the suffix with no
// enclosing expression — was fixed for calls (`call_suffix` over
// `call_expression`) and NOT carried across to indexing. `a[i] = v` invokes
// `operator fun set`, and it parses with NO `indexing_expression` node at all.
//
// Measured on the pins with the old marker: okio counted 269 and missed 133
// (+49 %), kotlinx counted 274 and missed 113 (+41 %). The direction was
// FLATTERING, because WidestDenominator includes the counter.
func TestIndexingSuffix_CountsWritesAndInterpolations(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{"read", "fun m(a: X, i: Int) {\n    val v = a[i]\n}\n", 1},
		// The three forms the old marker missed entirely.
		{"indexed set", "fun m(a: X, i: Int, v: Int) {\n    a[i] = v\n}\n", 1},
		{"indexed augmented assign", "fun m(a: X, i: Int, v: Int) {\n    a[i] += v\n}\n", 1},
		{"inside a string template", "fun m(a: X, i: Int) {\n    val s = \"${a[i]}\"\n}\n", 1},
		// The ADJACENT error, checked rather than assumed: a multi-dimensional
		// read emits one suffix PER DIMENSION, so nothing is undercounted here
		// and the fix must not "correct" it.
		{"multi-dimensional read is two suffixes", "fun m(a: X, i: Int, j: Int) {\n    val v = a[i][j]\n}\n", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := measureOne(t, "a.kt", tc.src)["kotlin"]
			if r.CST.ParseErrorNodes != 0 {
				t.Fatalf("fixture parses with %d ERROR nodes — it is measuring a recovered tree", r.CST.ParseErrorNodes)
			}
			if r.CST.IndexingSuffix != tc.want {
				t.Fatalf("indexing_suffix = %d, want %d", r.CST.IndexingSuffix, tc.want)
			}
			// It must stay OUT of the primary denominator (symmetry with java's
			// array_access) and IN the widest one.
			if r.CST.Denominator() != 0 {
				t.Fatalf("primary denominator = %d, want 0 — indexing is excluded for symmetry with java", r.CST.Denominator())
			}
			if r.CST.WidestDenominator() < tc.want {
				t.Fatalf("widest = %d, want >= %d", r.CST.WidestDenominator(), tc.want)
			}
		})
	}

	t.Run("an indexed assignment is counted ONCE, not twice", func(t *testing.T) {
		// `a[i] = v` is one `set` call. It is counted as the indexing suffix;
		// the enclosing `assignment` must NOT also count it, or the exact sites
		// MAJOR-2 was about would be double-counted by the MAJOR-3 fix.
		r := measureOne(t, "a.kt", "fun m(a: X, i: Int, v: Int) {\n    a[i] = v\n}\n")["kotlin"]
		if r.CST.IndexingSuffix != 1 || r.CST.AugmentedAssignment != 0 || r.CST.PlainAssignment != 1 {
			t.Fatalf("indexing_suffix/augmented/plain = %d/%d/%d, want 1/0/1",
				r.CST.IndexingSuffix, r.CST.AugmentedAssignment, r.CST.PlainAssignment)
		}
		if r.CST.WidestDenominator() != 1 {
			t.Fatalf("widest = %d, want 1 — one `set` call, counted once", r.CST.WidestDenominator())
		}
	})
}

// TestOperatorConvention_IsCountedAndDiscriminated is the MAJOR-3 regression.
//
// The whole operator-convention family was counted in NO bucket — neither in
// the denominator nor among the published exclusions — while `infix_expression`
// and indexing WERE published, which implied the published figure was the whole
// Kotlin-only operator residual. It is roughly four times larger: ~4 135 on okio
// and ~2 214 on kotlinx against the ~944 / ~1 071 that were published.
//
// It also pins the DISCRIMINATION, which is why the family cannot be counted by
// node type: three of these node types carry a non-call form under the same
// name, and counting the node would overcount okio's postfix family by more than
// half (157 `!!` against 103 real inc/dec).
func TestOperatorConvention_IsCountedAndDiscriminated(t *testing.T) {
	calls := []struct {
		name, expr string
		get        func(jvmbindrate.CSTCounts) int
	}{
		{"additive → plus", "a + b", func(c jvmbindrate.CSTCounts) int { return c.AdditiveExpression }},
		{"multiplicative → times", "a * b", func(c jvmbindrate.CSTCounts) int { return c.MultiplicativeExpression }},
		{"prefix → unaryMinus", "-a", func(c jvmbindrate.CSTCounts) int { return c.PrefixExpression }},
		{"prefix → not", "!a", func(c jvmbindrate.CSTCounts) int { return c.PrefixExpression }},
		{"postfix → inc", "a++", func(c jvmbindrate.CSTCounts) int { return c.PostfixExpression }},
		{"equality → equals", "a == b", func(c jvmbindrate.CSTCounts) int { return c.EqualityExpression }},
		{"comparison → compareTo", "a < b", func(c jvmbindrate.CSTCounts) int { return c.ComparisonExpression }},
		{"range → rangeTo", "a..b", func(c jvmbindrate.CSTCounts) int { return c.RangeExpression }},
		{"check → contains", "a in b", func(c jvmbindrate.CSTCounts) int { return c.ContainsExpression }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			r := measureOne(t, "a.kt", "fun m() {\n    val z = "+tc.expr+"\n}\n")["kotlin"]
			if r.CST.ParseErrorNodes != 0 {
				t.Fatalf("fixture parses with %d ERROR nodes", r.CST.ParseErrorNodes)
			}
			if got := tc.get(r.CST); got != 1 {
				t.Fatalf("counter = %d, want 1 for %q", got, tc.expr)
			}
			if r.CST.UnclassifiedOperator != 0 {
				t.Fatalf("UnclassifiedOperator = %d — the operator token is not recognised", r.CST.UnclassifiedOperator)
			}
			// Excluded from the primary denominator (java symmetry), present in
			// the widest one — so the family can only make the rate look WORSE.
			if r.CST.Denominator() != 0 {
				t.Fatalf("primary denominator = %d, want 0", r.CST.Denominator())
			}
			if r.CST.WidestDenominator() != 1 {
				t.Fatalf("widest = %d, want 1", r.CST.WidestDenominator())
			}
		})
	}

	// The non-calls that share these node types. Counting the node type alone
	// would have swept every one of them into the denominator.
	notCalls := []struct {
		name, src string
		get       func(jvmbindrate.CSTCounts) int
	}{
		{"`a!!` is a null assertion, not inc/dec", "fun m() {\n    val z = a!!\n}\n",
			func(c jvmbindrate.CSTCounts) int { return c.NotNullAssertion }},
		{"`a === b` is identity, not equals", "fun m() {\n    val z = a === b\n}\n",
			func(c jvmbindrate.CSTCounts) int { return c.ReferentialEquality }},
		{"`a is B` is a type test, not contains", "fun m() {\n    val z = a is B\n}\n",
			func(c jvmbindrate.CSTCounts) int { return c.TypeTest }},
		{"plain `a = b` is not a call", "fun m() {\n    a = b\n}\n",
			func(c jvmbindrate.CSTCounts) int { return c.PlainAssignment }},
	}
	for _, tc := range notCalls {
		t.Run(tc.name, func(t *testing.T) {
			r := measureOne(t, "a.kt", tc.src)["kotlin"]
			if got := tc.get(r.CST); got != 1 {
				t.Fatalf("counter = %d, want 1", got)
			}
			if r.CST.OperatorConventionCalls() != 0 {
				t.Fatalf("operator-convention calls = %d, want 0 — a non-call reached a call counter",
					r.CST.OperatorConventionCalls())
			}
			if r.CST.WidestDenominator() != 0 {
				t.Fatalf("widest = %d, want 0 — this construct calls nothing", r.CST.WidestDenominator())
			}
		})
	}

	t.Run("augmented assignment is a call, plain assignment is not", func(t *testing.T) {
		r := measureOne(t, "a.kt", "fun m() {\n    a += b\n    c = d\n}\n")["kotlin"]
		if r.CST.AugmentedAssignment != 1 || r.CST.PlainAssignment != 1 {
			t.Fatalf("augmented/plain = %d/%d, want 1/1", r.CST.AugmentedAssignment, r.CST.PlainAssignment)
		}
		if r.CST.WidestDenominator() != 1 {
			t.Fatalf("widest = %d, want 1", r.CST.WidestDenominator())
		}
	})
}

// TestSynthesizedProtocol_IsMeasuredInBothLanguagesAndInNoDenominator pins the
// line WidestDenominator stops at, and pins it SYMMETRICALLY.
//
// An earlier draft claimed the widest denominator was "deliberately the most
// damning variant: nothing here can make the published rate look better". That
// claim was false — it was missing a third of the indexing suffixes and the
// entire operator family. The replacement is not another superlative: it is a
// stated line (a construct whose source NAMES a callable is in; one where the
// compiler invents the call is out) plus the measured size of what the line
// excludes, in BOTH languages, so the exclusion cannot flatter one against the
// other.
func TestSynthesizedProtocol_IsMeasuredInBothLanguagesAndInNoDenominator(t *testing.T) {
	t.Run("the for-in protocol is excluded identically in java and kotlin", func(t *testing.T) {
		k := measureOne(t, "a.kt", "fun m(xs: List<Int>) {\n    for (x in xs) {\n        f()\n    }\n}\n")["kotlin"]
		j := measureOne(t, "A.java", "class A { void m(java.util.List<String> xs) { for (String x : xs) { f(); } } }\n")["java"]
		if k.CST.ForInLoops != 1 || j.CST.ForInLoops != 1 {
			t.Fatalf("for-in loops kotlin/java = %d/%d, want 1/1 — the same construct must be counted the same way",
				k.CST.ForInLoops, j.CST.ForInLoops)
		}
		// Each loop implies at least iterator() + hasNext() + next().
		if k.CST.SynthesizedProtocolCalls() != 3 || j.CST.SynthesizedProtocolCalls() != 3 {
			t.Fatalf("synthesized calls kotlin/java = %d/%d, want 3/3",
				k.CST.SynthesizedProtocolCalls(), j.CST.SynthesizedProtocolCalls())
		}
		// The loop contributes NOTHING to either denominator; only the `f()`
		// written in the body does.
		if k.CST.Denominator() != 1 || j.CST.Denominator() != 1 {
			t.Fatalf("denominator kotlin/java = %d/%d, want 1/1 (the body's f() only)",
				k.CST.Denominator(), j.CST.Denominator())
		}
		if k.CST.WidestDenominator() != 1 || j.CST.WidestDenominator() != 1 {
			t.Fatalf("widest kotlin/java = %d/%d, want 1/1 — a synthesized call is in NO denominator",
				k.CST.WidestDenominator(), j.CST.WidestDenominator())
		}
	})

	t.Run("destructuring counts one componentN per declared variable", func(t *testing.T) {
		r := measureOne(t, "a.kt", "fun m(p: Pair<Int, Int>) {\n    val (a, b) = p\n}\n")["kotlin"]
		if r.CST.DestructuringComponents != 2 {
			t.Fatalf("components = %d, want 2 — one per variable, not one per declaration", r.CST.DestructuringComponents)
		}
		if r.CST.WidestDenominator() != 0 {
			t.Fatalf("widest = %d, want 0", r.CST.WidestDenominator())
		}
	})

	t.Run("a delegated property is measured, and its `by` expression is still a real call", func(t *testing.T) {
		r := measureOne(t, "a.kt", "val p: Int by lazy { 1 }\n")["kotlin"]
		if r.CST.DelegatedProperties != 1 {
			t.Fatalf("delegated properties = %d, want 1", r.CST.DelegatedProperties)
		}
		// `lazy { 1 }` IS written in the source and IS a call site.
		if r.CST.Denominator() != 1 {
			t.Fatalf("denominator = %d, want 1 — `lazy {}` is a written call", r.CST.Denominator())
		}
	})

	t.Run("java try-with-resources counts one close() per resource", func(t *testing.T) {
		r := measureOne(t, "A.java", "class A { void m() throws Exception { try (X x = open(); Y y = open2()) { } } }\n")["java"]
		if r.CST.TryWithResources != 2 {
			t.Fatalf("resources = %d, want 2 — one close() per resource, not per statement", r.CST.TryWithResources)
		}
		// The two initialisers are written calls and stay in the denominator.
		if r.CST.Denominator() != 2 {
			t.Fatalf("denominator = %d, want 2", r.CST.Denominator())
		}
	})
}

// TestParseDegradation_IsCountedNotAssumedAway — tree-sitter recovers from a
// syntax error instead of failing, so a file can contribute a silently PARTIAL
// count. An earlier draft of the package doc claimed "a parse error yields zero
// counts for that file". That was false and is measured false here.
func TestParseDegradation_IsCountedNotAssumedAway(t *testing.T) {
	t.Run("java truncated file still yields counts, and says so", func(t *testing.T) {
		r := measureOne(t, "a/Bad.java", "package a;\nclass A { void m() { alpha(); beta(); gamma(")["java"]
		if r.CST.Denominator() == 0 {
			t.Fatal("denominator = 0 — the fixture no longer demonstrates partial recovery")
		}
		if r.CST.ParseErrorNodes == 0 || r.CST.FilesWithParseErrors != 1 {
			t.Fatalf("degradation invisible: ERROR nodes %d, files %d — a partial count MUST be published as partial",
				r.CST.ParseErrorNodes, r.CST.FilesWithParseErrors)
		}
	})

	t.Run("kotlin one-line class body loses calls, and says so", func(t *testing.T) {
		// Whitespace alone changes the count 3×. This is the shape that was
		// contaminating this suite's own grammar fixture.
		shredded := measureOne(t, "a.kt", "class A { fun m() { f() } fun n() { g() } fun o() { h() } }\n")["kotlin"]
		clean := measureOne(t, "a.kt", "class A {\n    fun m() { f() }\n    fun n() { g() }\n    fun o() { h() }\n}\n")["kotlin"]
		if clean.CST.Denominator() != 3 || clean.CST.ParseErrorNodes != 0 {
			t.Fatalf("control: denominator %d, ERROR %d — want 3 and 0", clean.CST.Denominator(), clean.CST.ParseErrorNodes)
		}
		if shredded.CST.Denominator() >= clean.CST.Denominator() {
			t.Fatalf("shredded denominator %d >= clean %d — the fixture no longer demonstrates the loss",
				shredded.CST.Denominator(), clean.CST.Denominator())
		}
		if shredded.CST.ParseErrorNodes == 0 {
			t.Fatal("the shredded parse reports no ERROR nodes — the loss would be invisible in the published figures")
		}
	})
}

// measureOne is a one-file convenience.
func measureOne(t *testing.T, path, src string) map[string]jvmbindrate.LanguageReport {
	t.Helper()
	return jvmbindrate.Measure(map[string][]byte{path: []byte(src)})
}

// javaHandCounted is a fixture whose call sites were counted BY HAND, listed
// exhaustively in the comment so the count can be re-derived by a reader rather
// than trusted. It deliberately contains sites the binder CANNOT see at all —
// those are exactly the sites a binder-derived denominator would silently drop,
// and AC-2 exists because dropping them makes the rate tautological.
//
// method_invocation (9):
//  1. helper()            2. this.helper()       3. A.stat()
//  4. l.get(0)            5. l.get(0).length()   6. helper()  [inside the anonymous class body]
//  7. l.size()            8. l.isEmpty()         9. helper()  [inside the switch rule]
//
// object_creation_expression (2):
//
//  10. new A(1)          11. new A(1){…}
//
// DENOMINATOR = 11.
//
// Excluded, and each counted separately: explicit_constructor_invocation 2
// (`super(1)`, `this()`), method_reference 1 (`A::stat`), array_creation 1
// (`new int[3]`). Annotations: 2, contributing nothing.
const javaHandCounted = `
package a;
@Anno(v = 1)
class A extends B {
  int f;
  A() { super(1); }
  A(int x) { this(); }
  @Dep("x") void m(java.util.List<String> l) {
    helper();
    this.helper();
    A.stat();
    new A(1);
    new A(1){ void q(){ helper(); } };
    l.get(0).length();
    Runnable r = A::stat;
    int[] xs = new int[3];
    var z = l.size();
    assert l.isEmpty();
    switch (f) { case 1 -> helper(); }
  }
}
`

// kotlinHandCounted is the Kotlin twin.
//
// call_expression (9):
//  1. helper()            2. this.helper()      3. A.stat()
//  4. A(1)                5. l.map { … }        6. l.get(0)
//  7. super.m(l)          8. l.isEmpty()        9. helper()  [in the if body]
//
// DENOMINATOR = 9.
//
// Excluded and counted: delegation ctor 1 (`: B(1)`), annotation ctor 1
// (`@Anno(v = 1)`), callable_reference 1 (`::stat`). `l.get(0).length` is a
// navigation_expression wrapping call 6 — a VALUE site, not a call.
const kotlinHandCounted = `
package a
@Anno(v = 1)
class A(val x: Int) : B(1) {
  fun m(l: List<String>) {
    helper()
    this.helper()
    A.stat()
    A(1)
    l.map { it.length }
    l.get(0).length
    val r = ::stat
    super.m(l)
    if (l.isEmpty()) helper()
  }
}
`

// TestDenominator_MatchesTheHandCount is AC-2's core assertion.
func TestDenominator_MatchesTheHandCount(t *testing.T) {
	j := measureOne(t, "a/A.java", javaHandCounted)["java"]
	if got := j.CST.Denominator(); got != 11 {
		t.Fatalf("java denominator = %d, want the hand count 11 (mi=%d, oc=%d)",
			got, j.CST.MethodInvocation, j.CST.ObjectCreation)
	}
	if j.CST.MethodInvocation != 9 || j.CST.ObjectCreation != 2 {
		t.Fatalf("java parts = %d/%d, want 9/2", j.CST.MethodInvocation, j.CST.ObjectCreation)
	}

	k := measureOne(t, "a/A.kt", kotlinHandCounted)["kotlin"]
	if got := k.CST.Denominator(); got != 9 {
		t.Fatalf("kotlin denominator = %d, want the hand count 9", got)
	}

	// Both hand counts are only meaningful over a CLEAN parse. A recovered
	// tree can be missing arbitrary subtrees, so a hand count agreeing with a
	// degraded parse would agree by coincidence.
	if j.CST.ParseErrorNodes != 0 || k.CST.ParseErrorNodes != 0 {
		t.Fatalf("hand-count fixtures do not parse cleanly (java %d, kotlin %d ERROR nodes) — the counts would be coincidental",
			j.CST.ParseErrorNodes, k.CST.ParseErrorNodes)
	}
}

// TestExclusions_AreCountedNotHidden — an exclusion whose size is not published
// is a place to hide a bad rate. Every excluded construct must show up in
// CSTCounts with the hand-counted size.
func TestExclusions_AreCountedNotHidden(t *testing.T) {
	j := measureOne(t, "a/A.java", javaHandCounted)["java"]
	for _, c := range []struct {
		name string
		got  int
		want int
	}{
		{"explicit_constructor_invocation", j.CST.ExplicitCtorInvocation, 2},
		{"method_reference", j.CST.MethodReference, 1},
		{"array_creation_expression", j.CST.ArrayCreation, 1},
	} {
		if c.got != c.want {
			t.Errorf("java %s = %d, want %d", c.name, c.got, c.want)
		}
	}
	// The widest denominator adds back exactly the constructor delegations —
	// the one exclusion that is a real call to a real callable.
	if got, want := j.CST.WidestDenominator(), 13; got != want {
		t.Errorf("java widest denominator = %d, want %d", got, want)
	}

	k := measureOne(t, "a/A.kt", kotlinHandCounted)["kotlin"]
	if k.CST.DelegationCtorCall != 1 || k.CST.AnnotationCtorCall != 1 || k.CST.CallableReference != 1 {
		t.Errorf("kotlin exclusions = delegation %d / annotation %d / callable-ref %d, want 1/1/1",
			k.CST.DelegationCtorCall, k.CST.AnnotationCtorCall, k.CST.CallableReference)
	}
	if got, want := k.CST.WidestDenominator(), 10; got != want {
		t.Errorf("kotlin widest denominator = %d, want %d", got, want)
	}
}

// TestDenominator_IsIndependentOfTheBinder is the anti-tautology test, and it
// is the reason this harness does not derive its denominator from bound plus
// skipped. Two properties, each on a fixture where the binder produces NOTHING:
//
//  1. a file the binder tables but binds nothing in still contributes its full
//     CST count;
//  2. a file BuildTable REFUSES entirely — an unparseable one — is still in the
//     denominator's file count, so its absence is visible rather than silently
//     improving the rate.
//
// If the denominator were binder-derived, both fixtures would report 0/0 and a
// binder that saw nothing would score 100 %.
func TestDenominator_IsIndependentOfTheBinder(t *testing.T) {
	t.Run("bound nothing, denominator unmoved", func(t *testing.T) {
		// Every receiver is an untabled external type, so Phase B binds no
		// call site at all — but there are four calls written in the source.
		// The static initializer is load-bearing, not decoration. The Java
		// walker's memberBodies switches on method_declaration and
		// constructor_declaration ONLY, so a `static { … }` block is never
		// entered and its calls produce NO counter at all — neither a bound
		// site nor a named skip. Without it, bound+skips happens to equal the
		// hand count on this fixture and a tautological denominator would slip
		// through this very test. With it, the two quantities differ by 2 and
		// the assertion bites.
		r := measureOne(t, "a/A.java", `
package a;
class A {
  static { com.external.Thing.setup(); com.external.Thing.warm(); }
  void m(com.external.Thing t, com.external.Other o) {
    t.alpha();
    o.beta();
    t.alpha().gamma();
    new com.external.Thing().delta();
  }
}
`)["java"]
		if r.BoundCallSites != 0 {
			t.Fatalf("fixture is not doing its job — binder bound %d call sites, want 0", r.BoundCallSites)
		}
		// Hand count: setup() · warm() [static initializer] · t.alpha() ·
		// o.beta() · t.alpha() [the inner call of the chain] · .gamma() ·
		// .delta() = 7 method_invocation, plus `new com.external.Thing()` = 1
		// object_creation. Eight.
		if got := r.CST.Denominator(); got != 8 {
			t.Fatalf("denominator = %d, want 8 (7 method_invocation + 1 object_creation) — a binder that binds nothing must not score 100%%", got)
		}
		if r.Rate() != 0 {
			t.Fatalf("rate = %v, want 0", r.Rate())
		}
		// The anti-tautology assertion, stated explicitly: the denominator is
		// NOT bound+skips. The two static-initializer calls are in the source,
		// in the denominator, and in neither of the binder's own tallies.
		if got, tautological := r.CST.Denominator(), r.BoundCallSites+r.SkipTotal(); got == tautological {
			t.Fatalf("denominator (%d) equals bound+skips (%d) — this fixture can no longer distinguish a CST denominator from a binder-derived one", got, tautological)
		}
	})

	t.Run("a file the table refuses still counts", func(t *testing.T) {
		files := map[string][]byte{
			"a/Good.java": []byte("package a;\nclass Good { void m() { helper(); } void helper() {} }\n"),
			// Not a Java compilation unit at all. What matters is the
			// PROPERTY asserted below, which holds whether the table refuses
			// it or merely tables it empty: its call sites stay in the
			// denominator either way.
			"a/Bad.java": []byte("package a;\nclass Bad { void m() { alpha(); beta(); gamma("),
		}
		r := jvmbindrate.Measure(files)["java"]
		if r.SourceFiles != 2 {
			t.Fatalf("source files = %d, want 2", r.SourceFiles)
		}
		if r.CST.Files != 2 {
			t.Fatalf("CST files = %d, want 2 — a file the binder cannot use is still a file with call sites in it", r.CST.Files)
		}
		if r.CST.Denominator() < 3 {
			t.Fatalf("denominator = %d, want at least 3 — the malformed file's calls must not vanish from the denominator", r.CST.Denominator())
		}
	})
}

// TestHistogram_IsNotCallSiteKeyed is AC-4's finding, demonstrated rather than
// asserted.
//
// AC-4 asks the histogram to sum to the unbound remainder and, IF it does not,
// for the discrepancy to be published as a finding. It does not, and the reason
// is structural rather than a lost-sites bug: the named counters are NOT
// call-site-keyed. Three mechanisms are isolated here, each on its own fixture
// with a hand count, and each pushes the residual in a nameable direction:
//
//	(a) a VALUE site (`t.field`) increments the same counters as a call site,
//	    while contributing nothing to a call-site denominator  → residual DOWN
//	(b) java_body_unmatched fires ONCE PER BODY, so one increment can stand for
//	    an unbounded number of unbound call sites              → residual UP
//
// (a) drives the residual NEGATIVE — which a reader expecting "histogram =
// remainder" would read as sites appearing from nowhere. Both signs occur in the
// real-pin data, which is why neither is dismissible as an artefact.
//
// The third subtest is a CONTROL that bounds the explanation rather than
// widening it: a chained call does NOT double-count, so "the counters overcount
// calls" is excluded as a mechanism and the two above are the whole story for
// call sites. A hypothesis that explains everything explains nothing.
func TestHistogram_IsNotCallSiteKeyed(t *testing.T) {
	t.Run("a value site increments the call-site vocabulary", func(t *testing.T) {
		// One field READ through an external receiver. Zero call sites.
		r := measureOne(t, "a/A.java", `
package a;
class A { int m(com.external.Thing t) { return t.field; } }
`)["java"]
		if got := r.CST.Denominator(); got != 0 {
			t.Fatalf("denominator = %d, want 0 — `t.field` is not a call site", got)
		}
		if got := r.SkipTotal(); got == 0 {
			t.Fatal("histogram is empty — the fixture no longer demonstrates the mechanism")
		}
		if got := r.Residual(); got >= 0 {
			t.Fatalf("residual = %d, want negative: a value site adds to the histogram and not to the denominator", got)
		}
	})

	t.Run("CONTROL: a chained call does not double-count", func(t *testing.T) {
		// `t.alpha().gamma()` is two call sites and produces exactly two
		// counter increments — one per site, not one per typing attempt. This
		// EXCLUDES over-counting as a residual mechanism, which matters because
		// the two mechanisms above are then the whole explanation for call
		// sites rather than merely two examples of many.
		r := measureOne(t, "a/A.java", `
package a;
class A { void m(com.external.T t) { t.alpha().gamma(); } }
`)["java"]
		if got := r.CST.Denominator(); got != 2 {
			t.Fatalf("denominator = %d, want 2", got)
		}
		if got := r.Skips["java_receiver_external"]; got != 2 {
			t.Fatalf("java_receiver_external = %d, want exactly 2 — one per site", got)
		}
		if got := r.Residual(); got != 0 {
			t.Fatalf("residual = %d, want 0 on a pure call-site fixture", got)
		}
	})

	t.Run("body_unmatched is per body, not per site", func(t *testing.T) {
		// Two files declaring the SAME fully qualified type. tabledType binds
		// a FQN only when it has exactly one candidate, so BOTH bodies are
		// abandoned — and each abandonment costs exactly ONE counter while
		// hiding every call site in the body.
		//
		// This is the guava mechanism, hermetically: a monorepo shipping the
		// same class twice (a JRE and an Android flavour) collides every FQN.
		body := "{ alpha(); beta(); gamma(); delta(); epsilon(); }"
		files := map[string][]byte{
			"jre/a/Dup.java":     []byte("package a;\nclass Dup { void m() " + body + " }\n"),
			"android/a/Dup.java": []byte("package a;\nclass Dup { void m() " + body + " }\n"),
		}
		r := jvmbindrate.Measure(files)["java"]
		if got := r.CST.Denominator(); got != 10 {
			t.Fatalf("denominator = %d, want 10 (5 calls × 2 files)", got)
		}
		if got := r.BoundCallSites; got != 0 {
			t.Fatalf("bound = %d, want 0 — the collision abandons both walks", got)
		}
		if got := r.Skips["java_body_unmatched"]; got != 2 {
			t.Fatalf("java_body_unmatched = %d, want 2 — ONE per abandoned type, standing for 5 unbound call sites each", got)
		}
		if got := r.CollidedFQNs; got != 1 {
			t.Fatalf("collided FQNs = %d, want 1", got)
		}
		if got := r.TypesInCollidedFQNs; got != 2 {
			t.Fatalf("types in collided FQNs = %d, want 2", got)
		}
		// The headline: 10 unbound call sites, and the named vocabulary
		// accounts for 2. That ratio is unbounded — lengthen the body and the
		// counter does not move.
		if got := r.Residual(); got != 8 {
			t.Fatalf("residual = %d, want 8 — the histogram accounts for 2 of 10 unbound sites", got)
		}
	})

	t.Run("the residual grows without the counter moving", func(t *testing.T) {
		// The same collision with a LONGER body. If the histogram were
		// site-keyed the counter would grow with the body; it does not.
		long := "{ " + strings.Repeat("alpha(); ", 40) + "}"
		files := map[string][]byte{
			"jre/a/Dup.java":     []byte("package a;\nclass Dup { void m() " + long + " }\n"),
			"android/a/Dup.java": []byte("package a;\nclass Dup { void m() " + long + " }\n"),
		}
		r := jvmbindrate.Measure(files)["java"]
		if got := r.Skips["java_body_unmatched"]; got != 2 {
			t.Fatalf("java_body_unmatched = %d, want 2 — unchanged by an 8× longer body", got)
		}
		if got := r.CST.Denominator(); got != 80 {
			t.Fatalf("denominator = %d, want 80", got)
		}
		if got := r.Residual(); got != 78 {
			t.Fatalf("residual = %d, want 78 — the histogram still accounts for 2", got)
		}
	})
}

// TestMeasure_IsDeterministic is AC-5's hermetic half: two runs over the same
// input must agree on every published figure, not merely on the headline rate.
// The real-pin half is TestPins_BindingRateIsReproducible.
func TestMeasure_IsDeterministic(t *testing.T) {
	files := map[string][]byte{
		"a/A.java": []byte(javaHandCounted),
		"a/A.kt":   []byte(kotlinHandCounted),
		"a/B.java": []byte("package a;\nclass B { void helper() {} static void stat() {} }\n"),
	}
	for i := 0; i < 5; i++ {
		a, b := jvmbindrate.Measure(files), jvmbindrate.Measure(files)
		for _, lang := range []string{"java", "kotlin"} {
			if render(a[lang]) != render(b[lang]) {
				t.Fatalf("run %d disagreed for %s:\nA:\n%s\nB:\n%s", i, lang, render(a[lang]), render(b[lang]))
			}
		}
	}
}

// TestNumeratorIsTheShippedBinder guards the property that makes this
// measurement worth publishing: the numerator is the SHIPPED walker's output,
// so a fixture the binder really does bind must produce a non-zero numerator.
// Without this the whole suite above could pass with a numerator wired to zero.
func TestNumeratorIsTheShippedBinder(t *testing.T) {
	files := map[string][]byte{
		"a/A.java": []byte("package a;\nclass A { void m() { helper(); helper(); } void helper() {} }\n"),
	}
	r := jvmbindrate.Measure(files)["java"]
	if r.BoundCallSites != 2 {
		t.Fatalf("bound call sites = %d, want 2 — the numerator is not reading the binder", r.BoundCallSites)
	}
	if r.CST.Denominator() != 2 {
		t.Fatalf("denominator = %d, want 2", r.CST.Denominator())
	}
	if r.Rate() != 100 {
		t.Fatalf("rate = %v, want 100 — a fixture the binder fully binds must score 100", r.Rate())
	}
	if r.SkipTotal() != 0 {
		t.Fatalf("histogram = %v, want empty", r.Skips)
	}
}

// TestParseArms_PartitionTheCorpus is FINDING B-0's direction, made measurable.
//
// The first draft declared the bias direction of Kotlin's 10–13 % dirty parses
// "undetermined — both sides of the fraction shrink". That was honest in intent
// and erred against the product, but it was an INCOMPLETE measurement presented
// as an unavailable one: determining the direction never required knowing what
// the grammar dropped, only comparing the two subpopulations.
//
// The property that makes the split a split rather than a second measurement is
// asserted here: the arms must PARTITION. Files, numerator and denominator must
// each reconstitute the published whole-corpus totals exactly. An arm that
// silently rebuilt its own table, or double-counted a file, or lost the sites of
// a file whose path key did not match, would fail this rather than quietly
// publishing two numbers that do not add up.
func TestParseArms_PartitionTheCorpus(t *testing.T) {
	// One clean file and one file the Kotlin grammar cannot parse cleanly. The
	// dirty file's calls still reach the DENOMINATOR (tree-sitter recovers), so
	// this fixture also demonstrates the mechanism itself.
	files := map[string][]byte{
		"clean.kt": []byte("package p\n\nclass Clean {\n    fun a(): Int = b()\n    fun b(): Int = 1\n}\n"),
		"dirty.kt": []byte("package p\n\nclass Dirty { fun a() { alpha(); beta(); gamma(\n"),
	}
	r := jvmbindrate.Measure(files)["kotlin"]

	if r.Clean.Files+r.Dirty.Files != r.CST.Files {
		t.Fatalf("arm files %d + %d != %d total — the arms do not partition the corpus",
			r.Clean.Files, r.Dirty.Files, r.CST.Files)
	}
	if r.Clean.CallSites+r.Dirty.CallSites != r.CST.Denominator() {
		t.Fatalf("arm call sites %d + %d != %d denominator — the split re-measured instead of partitioning",
			r.Clean.CallSites, r.Dirty.CallSites, r.CST.Denominator())
	}
	if r.Clean.BoundCallSites+r.Dirty.BoundCallSites != r.BoundCallSites {
		t.Fatalf("arm bound %d + %d != %d numerator — sites were lost attributing them to a file",
			r.Clean.BoundCallSites, r.Dirty.BoundCallSites, r.BoundCallSites)
	}

	// The arms must actually be non-trivial, or the assertions above are
	// satisfied by an empty partition.
	if r.Clean.Files == 0 || r.Dirty.Files == 0 {
		t.Fatalf("clean/dirty files = %d/%d — the fixture no longer produces both arms",
			r.Clean.Files, r.Dirty.Files)
	}
	if r.Dirty.ParseErrorNodes == 0 {
		t.Fatal("the dirty arm carries no ERROR nodes — the fixture no longer demonstrates a recovered parse")
	}
	if r.Clean.ParseErrorNodes != 0 {
		t.Fatalf("the CLEAN arm carries %d ERROR nodes — the partition is not on what it claims to be on",
			r.Clean.ParseErrorNodes)
	}

	// The mechanism: the recovered file still contributes a denominator. That
	// is precisely why a dirty parse DEPRESSES the rate instead of hiding it.
	if r.Dirty.CallSites == 0 {
		t.Fatal("the dirty arm contributes no denominator — then dirty parses could not depress the rate, and the finding would not hold")
	}
	t.Logf("clean %d files → %s | dirty %d files (%d ERROR nodes) → %s",
		r.Clean.Files, r.Clean.RateText(),
		r.Dirty.Files, r.Dirty.ParseErrorNodes, r.Dirty.RateText())
}

// TestRateText_SaysNotApplicableForAnEmptyDenominator is MINOR-6.
//
// The discipline held in the published document (§7.2 writes `n/a`, §11.8 says
// "0 of 0 is not 0 %") and failed in the harness's own log, which printed
// `RATE 0.00% = 0 bound call sites / 0 CST call sites` for a language with no
// sources at a scope. The CI step's comment calls that log the place "every
// published figure lands", so the log contradicted the document on the exact
// case the story singled out. Both now go through rateText.
func TestRateText_SaysNotApplicableForAnEmptyDenominator(t *testing.T) {
	// A Kotlin-only corpus: the java report exists and has no call sites at all.
	r := jvmbindrate.Measure(map[string][]byte{
		"a.kt": []byte("package p\n\nfun m(): Int = f()\n\nfun f(): Int = 1\n"),
	})["java"]

	if r.CST.Denominator() != 0 {
		t.Fatalf("java denominator = %d, want 0 — the fixture no longer produces the 0/0 case", r.CST.Denominator())
	}
	if got := r.RateText(); !strings.Contains(got, "n/a") {
		t.Fatalf("RateText() = %q, want it to say n/a — 0 of 0 is not 0 %%, and a log that says 0.00%% states a failure that did not happen", got)
	}
	if got := r.WidestRateText(); !strings.Contains(got, "n/a") {
		t.Fatalf("WidestRateText() = %q, want n/a", got)
	}
	if got := (jvmbindrate.ParseArm{}).RateText(); !strings.Contains(got, "n/a") {
		t.Fatalf("ParseArm.RateText() = %q, want n/a", got)
	}

	// And a real rate must still render as a rate.
	k := jvmbindrate.Measure(map[string][]byte{
		"a.kt": []byte("package p\n\nfun m(): Int = f()\n\nfun f(): Int = 1\n"),
	})["kotlin"]
	if got := k.RateText(); strings.Contains(got, "n/a") || !strings.Contains(got, "%") {
		t.Fatalf("kotlin RateText() = %q, want a real percentage", got)
	}
}
