package jvmgroundtruth_test

// SW-172 round 1 — the DO-NOT-ACCUSE corpus.
//
// The review bar for an oracle is not that it finds defects; it is that it
// never accuses correct code. Three of the defects fixed in this round were
// found by writing legal-but-unusual Java and looking for a fabricated
// stop-ship, and all three survived only because every fixture in the harness
// happened to avoid the construct that triggered them: reference return types,
// interface dispatch, and a local class as the caller.
//
// So this file is the standing version of that attack. Every entry is CORRECT
// Java. The assertion is uniform and deliberately blunt: at every precision,
// the oracle must produce ZERO violations. It may match, and it may abstain
// with a named reason — an abstention is an honest refusal and is allowed to
// change as the harness learns — but it may never accuse.
//
// A case that becomes a REAL mis-binding does not belong here: it gets its own
// pin with a mechanism and a fix shape (see TestJVMSOUND003_*, TestJVMSOUND004_*).
// Removing a case from this file because it started failing is therefore always
// wrong unless the mis-binding is real and has been filed.
//
// NON-VACUITY, asserted rather than logged (round 2). A violation can only
// arise from a CONFIRMED call, so a case whose confirmed set is EMPTY cannot
// fail the assertion it carries — it is a green that proves nothing. Round 1
// logged the matched/abstained split and trusted a reader to notice; the
// round-2 reviewer noticed instead, and found FOUR such cases, two of which did
// not reach the mechanism their `why` string named. So every case now asserts
// `len(confirmed) > 0`, and the four have been rewritten:
//
//   - nested-versus-toplevel-type-rendering: the overload pair collapsed on
//     TypeRef.Base and the binder forfeited it as ambiguous, so the
//     La/Key;-vs-La/Outer$Key; RENDERING it claimed to test never ran. The
//     methods now carry DISTINCT names, so both renderings are produced and
//     both are checked against javac's table.
//   - variadic-sibling-at-the-same-declared-count: the `why` is restated to
//     name what it actually pins (a binder forfeit UPSTREAM of the oracle), and
//     a non-elastic sibling call makes the case non-vacuous.
//   - anonymous-class-caller / method-reference-invokedynamic: each gains a
//     direct call, so the case pins that the synthetic caller (resp. the
//     invokedynamic bootstrap) does not disturb an ordinary confirmed call
//     beside it, rather than pinning nothing at all.
//
// Each case still logs its matched/abstained split.

import (
	"testing"

	"github.com/samibel/graphi/internal/jvmgroundtruth"
)

func TestAdversarial_CorrectJavaIsNeverAccused(t *testing.T) {
	cases := []struct {
		name  string
		why   string
		files map[string]string
	}{
		{
			name: "cstyle-declarator-with-reference-typed-sibling",
			why:  "the C-style collision in its reference-typed form: (La/Thing;) is really compiled, for the OTHER overload",
			files: map[string]string{
				"a/Thing.java": "package a;\npublic class Thing {}\n",
				"a/Rate.java": `package a;
public class Rate {
    public int apply(Thing ts[]) { return ts.length; }
    public int apply(Thing t)    { return 1; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Rate r, Thing[] ts) { return r.apply(ts); } }\n",
			},
		},
		{
			name: "cstyle-declarator-two-dimensions",
			why:  "int xs[][] renders (I) while javac compiled ([[I); the one-dimensional sibling makes ([I) real too",
			files: map[string]string{
				"a/Rate.java": `package a;
public class Rate {
    public int apply(int xs[][]) { return xs.length; }
    public int apply(int[] ys)   { return ys.length; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Rate r, int[][] xs) { return r.apply(xs); } }\n",
			},
		},
		{
			name: "enum-constant-body-anonymous-subclass",
			why:  "constant-specific bodies compile to a/Op$1, a/Op$2 — synthetic caller classes",
			files: map[string]string{
				"a/Op.java": `package a;
public enum Op {
    ADD { public int f(int x) { return x + 1; } },
    SUB { public int f(int x) { return x - 1; } };
    public abstract int f(int x);
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Op o) { return o.f(1); } }\n",
			},
		},
		{
			name: "nested-type-parameter-and-reference-return",
			why:  "a nested static type as both a parameter type (La/Outer$Key;) and a return type",
			files: map[string]string{
				"a/Outer.java": `package a;
public class Outer {
    public static class Key {}
    public Key make() { return new Key(); }
    public int use(Key k) { return 1; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Outer o) { return o.use(o.make()); } }\n",
			},
		},
		{
			name: "generic-method-erased-return",
			why:  "T get() erases to ()Ljava/lang/Object; — a reference-returning invoke to an external erasure",
			files: map[string]string{
				"a/Box.java": `package a;
public class Box<T> {
    private T v;
    public T get() { return v; }
    public int tag() { return 1; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Box<String> b) { b.get(); return b.tag(); } }\n",
			},
		},
		{
			name: "static-and-instance-initializers-as-callers",
			why:  "javac folds these into <clinit> and <init>, names graphi never mints a node for",
			files: map[string]string{
				"a/Rate.java": `package a;
public class Rate {
    public static int base() { return 1; }
    public int inst() { return 2; }
    static int S;
    int i;
    static { S = base(); }
    { i = inst(); }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Rate r) { return r.inst(); } }\n",
			},
		},
		{
			name: "anonymous-class-caller",
			why:  "the caller class is a/App$1, which graphi mints no node for; the DIRECT call beside it must still be judged (round 2: this case was vacuous without it)",
			files: map[string]string{
				"a/Rate.java": "package a;\npublic class Rate { public int rate() { return 7; } }\n",
				"a/App.java": `package a;
public class App {
    public int run(final Rate r) {
        Runnable x = new Runnable() { public void run() { r.rate(); } };
        x.run();
        return r.rate();
    }
}
`,
			},
		},
		{
			name: "method-reference-invokedynamic",
			why:  "r::rate is an invokedynamic bootstrap, not a method ref; both sides must ignore it AND still judge the ordinary call beside it (round 2: this case was vacuous without it)",
			files: map[string]string{
				"a/Rate.java": "package a;\npublic class Rate { public int rate() { return 7; } public int tag() { return 1; } }\n",
				"a/App.java": `package a;
public class App {
    public int run(Rate r) {
        java.util.function.IntSupplier s = r::rate;
        return s.getAsInt() + r.tag();
    }
}
`,
			},
		},
		{
			name: "record-accessor-and-compact-constructor",
			why:  "javac synthesizes the canonical ctor, accessors, equals/hashCode/toString and an invokedynamic",
			files: map[string]string{
				"a/Pt.java": `package a;
public record Pt(int x, int y) {
    public Pt { if (x < 0) throw new IllegalArgumentException(); }
    public int sum() { return x() + y(); }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Pt p) { return p.sum() + p.x(); } }\n",
			},
		},
		{
			name: "sealed-interface-and-permitted-record",
			why:  "a permits clause in the class header must not be read as a supertype",
			files: map[string]string{
				"a/Shape.java": "package a;\npublic sealed interface Shape permits Sq {\n    int area();\n}\n",
				"a/Sq.java":    "package a;\npublic record Sq(int side) implements Shape {\n    public int area() { return side * side; }\n}\n",
				"a/App.java":   "package a;\npublic class App { public int run(Sq s) { return s.area(); } }\n",
			},
		},
		{
			name: "method-actually-named-descriptor",
			why:  "the descriptor-line guard must key on `descriptor: `, not on the word",
			files: map[string]string{
				"a/Rate.java": `package a;
public class Rate {
    public int descriptor(int x) { return x; }
    public Rate self() { return this; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Rate r) { return r.self().descriptor(1); } }\n",
			},
		},
		{
			name: "throws-clause-and-generic-method-header",
			why:  "both put extra tokens in the javap member header the name scan must survive",
			files: map[string]string{
				"a/Rate.java": `package a;
public class Rate {
    public Rate self() throws java.io.IOException { return this; }
    public <T extends Number> int f(int x) { return x; }
}
`,
				"a/App.java": `package a;
public class App {
    public int run(Rate r) throws java.io.IOException { return r.self().f(1); }
}
`,
			},
		},
		{
			name: "default-package",
			why:  "no package declaration, so the source-path reconstruction has no directory to prepend",
			files: map[string]string{
				"Rate.java": "public class Rate { public Rate self() { return this; } public int tag() { return 1; } }\n",
				"App.java":  "public class App { public int run(Rate r) { return r.self().tag(); } }\n",
			},
		},
		{
			name: "same-simple-name-in-two-packages",
			why:  "a/Thing and b/Thing share a simple name; only the internal name separates them",
			files: map[string]string{
				"a/Thing.java": "package a;\npublic class Thing { public int tag() { return 1; } }\n",
				"b/Thing.java": "package b;\npublic class Thing { public int tag() { return 2; } }\n",
				"a/App.java": `package a;
public class App {
    public int run(a.Thing at, b.Thing bt) { return at.tag() + bt.tag(); }
}
`,
			},
		},
		{
			name: "variadic-sibling-at-the-same-declared-count",
			// RESTATED in round 2. This case does NOT reach the oracle's
			// elastic-member abstention, and claiming it did was the vacuity the
			// reviewer caught: f(int) and f(int...) both declare ONE parameter,
			// but hierarchy.go forfeits the elastic set UPSTREAM, so no confirmed
			// call to `f` is produced at all. That forfeit is the real answer to
			// the ticket's own review question ("can the arity key produce a
			// FALSE counterexample on varargs?") — it cannot, because there is
			// nothing to key. `plain` is the non-elastic sibling that keeps the
			// case non-vacuous and pins that the forfeit does not spread.
			why: "the binder forfeits the elastic set UPSTREAM, so f never reaches the oracle; the non-elastic sibling beside it must still be judged",
			files: map[string]string{
				"a/Rate.java": `package a;
public class Rate {
    public int f(int x)     { return x; }
    public int f(int... xs) { return xs.length; }
    public int plain(int x) { return x; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Rate r) { return r.f(1) + r.plain(2); } }\n",
			},
		},
		{
			name: "covariant-return-chain-three-deep",
			why:  "two levels of bridge methods, each an extra same-name descriptor in javac's table",
			files: map[string]string{
				"a/A.java":   "package a;\npublic class A { public A self() { return this; } public int tag() { return 0; } }\n",
				"a/B.java":   "package a;\npublic class B extends A { @Override public B self() { return this; } }\n",
				"a/C.java":   "package a;\npublic class C extends B { @Override public C self() { return this; } public int tag() { return 2; } }\n",
				"a/App.java": "package a;\npublic class App { public int run(C c) { return c.self().tag(); } }\n",
			},
		},
		{
			name: "interface-constant-initializer-as-caller",
			why:  "an interface field initializer compiles into the interface's own <clinit>",
			files: map[string]string{
				"a/K.java":      "package a;\npublic interface K {\n    int V = Helper.make();\n}\n",
				"a/Helper.java": "package a;\npublic class Helper { public static int make() { return 4; } }\n",
				"a/App.java":    "package a;\npublic class App { public int run() { return Helper.make(); } }\n",
			},
		},
		{
			name: "inner-nonstatic-constructor-and-reference-return",
			why:  "javac gives an inner class's ctor a synthetic enclosing-instance parameter",
			files: map[string]string{
				"a/Outer.java": `package a;
public class Outer {
    public class In { public int v() { return 1; } }
    public In make() { return new In(); }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Outer o) { return o.make().v(); } }\n",
			},
		},
		{
			name: "nested-versus-toplevel-type-rendering",
			// REWRITTEN in round 2. As an OVERLOAD pair (both named `f`) the two
			// signatures collapsed on TypeRef.Base, the binder returned ambiguous,
			// and the rendering this case exists to test never ran. Distinct
			// names keep the binder out of the ambiguity path, so both renderings
			// are really produced and really checked against javac's table.
			why: "La/Key; and La/Outer$Key; differ only in the nesting the rendering has to get right, and BOTH are rendered here",
			files: map[string]string{
				"a/Key.java": "package a;\npublic class Key {}\n",
				"a/Outer.java": `package a;
public class Outer {
    public static class Key {}
    public int viaTop(a.Key k)        { return 1; }
    public int viaNested(Outer.Key k) { return 2; }
}
`,
				"a/App.java": "package a;\npublic class App { public int run(Outer o, a.Key tk, Outer.Key nk) { return o.viaTop(tk) + o.viaNested(nk); } }\n",
			},
		},
		// --- round 2: the empty-extension interface (CRITICAL-4) --------------
		//
		// `interface B extends A {}` compiles to a class file with ZERO declared
		// methods, which used to be read as "this capture was taken without -s"
		// and forged a stop-ship at every precision, by-name included. Three
		// tokens of legal Java, and the commonest shape in the interface-heavy
		// corpora SW-173 points this oracle at. No fixture anywhere contained it,
		// which is exactly how it survived — so it is four fixtures now.
		{
			name: "empty-extension-interface-default-method",
			why:  "an EMPTY interface declares nothing, which is also the shape of an -s-less capture; conflating them accused correct code at by-name",
			files: map[string]string{
				"a/A.java":   "package a;\npublic interface A { default int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic interface B extends A {}\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
		{
			name: "empty-marker-interface-abstract-method",
			why:  "the same shape with a plain ABSTRACT method, so the fix cannot be specific to default methods",
			files: map[string]string{
				"a/A.java":   "package a;\npublic interface A { int seed(); }\n",
				"a/B.java":   "package a;\npublic interface B extends A {}\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
		{
			name: "two-empty-interface-hops",
			why:  "C extends B extends A with both intermediates empty — the walk must decline, not accuse, at every hop",
			files: map[string]string{
				"a/A.java":   "package a;\npublic interface A { default int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic interface B extends A {}\n",
				"a/C.java":   "package a;\npublic interface C extends B {}\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(C c) { return c.seed(); } }\n",
			},
		},
		{
			name: "empty-extension-interface-nonempty-control",
			why:  "the CONTROL: one member makes B non-empty, and the owner walk must then resolve normally — the discriminator is emptiness, nothing else",
			files: map[string]string{
				"a/A.java":   "package a;\npublic interface A { default int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic interface B extends A { int other(); }\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
		// --- round 2: the NINTH forge, found by attacking the round-2 fixes ---
		//
		// A type NAME may contain a type KEYWORD: javap prints
		// `public interface a.Subclass extends a.Base {`, and the old
		// first-substring scan landed inside "Subclass". The interface was
		// never registered, so every call to its methods lost its source path,
		// scored external, and left the truth set — forging at by-name.
		{
			name: "interface-whose-name-ends-in-a-keyword",
			why:  "`class` is reserved only as a WHOLE TOKEN; a type named Subclass must not be read as a type named `extends`",
			files: map[string]string{
				"a/Base.java":     "package a;\npublic interface Base { int seed(); }\n",
				"a/Subclass.java": "package a;\npublic interface Subclass extends Base { int extra(); }\n",
				"a/App.java":      "package a;\npublic class App { public int run(Subclass s) { return s.extra(); } }\n",
			},
		},
		{
			name: "empty-subclass-control",
			why:  "the second CONTROL: an empty CLASS is never genuinely empty (javac gives it <init>), so it must resolve rather than decline",
			files: map[string]string{
				"a/A.java":   "package a;\npublic class A { public int seed() { return 1; } }\n",
				"a/B.java":   "package a;\npublic class B extends A {}\n",
				"a/App.java": "package a;\npublic class App { public int viaSub(B b) { return b.seed(); } }\n",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			confirmed, truth := binderVsBytecode(t, tc.files)
			// NON-VACUITY, first and hard. A violation can only come from a
			// confirmed call, so an empty confirmed set makes every assertion
			// below unfalsifiable — a green that proves nothing. This is an
			// error rather than a skip: if the binder stops binding a construct
			// it used to bind, this corpus must say so out loud instead of
			// quietly ceasing to test it.
			if len(confirmed) == 0 {
				t.Fatalf("VACUOUS CASE: the binder produced no confirmed call, so `zero violations` cannot fail.\nwhy this case exists: %s\nEither restore a confirmed call or restate the case; do not leave it green.", tc.why)
			}
			for _, p := range precisions() {
				res := jvmgroundtruth.CompareAt(confirmed, truth, p)
				if !res.Sound() {
					t.Errorf("FORGED STOP-SHIP on correct Java at %s.\nwhy this case exists: %s\n%s",
						p, tc.why, res.Format())
				}
			}
			fine := jvmgroundtruth.CompareAt(confirmed, truth, jvmgroundtruth.BySignature)
			t.Logf("matched=%d abstained=%d reasons=%v truthIntra=%d",
				fine.Matched, len(fine.Abstained), fine.AbstainReasons, fine.TruthIntra)
		})
	}
}
