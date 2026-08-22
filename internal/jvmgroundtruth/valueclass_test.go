package jvmgroundtruth

// JVMHARN-001 — Kotlin value-class (inline-class) name mangling, fixed in the
// HARNESS, which is where the defect lives: javap prints the mangled
// DECLARATION name, so nothing in engine/jvmresolve can change what the truth
// set says.
//
// These are the compiler-free half of the closure: the recognition rule, the
// same-class-bridge guard, and both application sites through ParseJavap on a
// capture-shaped fixture. What no test in this package can do without a pinned
// kotlinc — absent from this sandbox, see
// signature_test.go::TestKotlinAbstainsAtFinerPrecisions — is re-measure the
// kotlinx.serialization by-name differential. That is a `jvm-groundtruth` CI
// dispatch and is disclosed as outstanding rather than assumed.

import (
	"strings"
	"testing"
)

// TestValueClassBridgeName_MirrorsJVMResolve pins this package's copy of the
// recognition rule on the ten cases
// engine/jvmresolve/hierarchy_test.go::TestValueClassBridgeName runs, plus one
// more — `append-7apg3OU$main`, the compound `internal`-visibility residual —
// for eleven in all.
//
// It is a BEHAVIOURAL pin on the measured kotlinx shapes, and it is explicitly
// NOT the drift guard: the table is a transcribed snapshot with no link to the
// jvmresolve table, so a maintainer who changes the product rule and the
// product rule's own table together leaves this green while the two rules
// disagree. TestValueClassRule_IdenticalToJVMResolve is the guard; see its doc
// for the measurement that shows why a shared table would not have been one.
func TestValueClassBridgeName_MirrorsJVMResolve(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// The measured kotlinx.serialization shapes (capture digest
		// e6d39791…), transcribed from the jvmresolve table unchanged.
		{"serialize-2TYgG_w", "serialize"},
		{"toBuilder-GBYM_sE", "toBuilder"},
		{"writeContent-Coi6ktg", "writeContent"},
		{"computeIfAbsent-gIAlu-s", "computeIfAbsent"},
		// No mangling: ordinary name.
		{"apply", ""},
		// Too-short hash suffix (the rule requires ≥4 base-64 chars).
		{"foo-ab", ""},
		// Hash with a non-base-64 character.
		{"foo-abcd!", ""},
		// Prefix starts with a digit: not a valid Kotlin identifier.
		{"1foo-abcd", ""},
		// Just a dash and a hash, no prefix.
		{"-abcd", ""},
		// Just a dash, no suffix.
		{"foo-", ""},
		// The fifth measured kotlinx shape, and the compound one: `internal`
		// visibility mangling on top of the value-class hash. `$` is not
		// base-64, so the rule declines. This is cluster C of
		// docs/rc/jvm-corpus-compile-strategy.md §6.2 — pinned as a residual,
		// not as a bug.
		{"append-7apg3OU$main", ""},
	}
	for _, tc := range cases {
		if got := valueClassBridgeName(tc.in); got != tc.want {
			t.Errorf("valueClassBridgeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// kotlinClass builds a classInfo the way parseClasses would for a Kotlin
// source with the given declared member names. Descriptors are irrelevant to
// the guard, which keys on the name half only.
func kotlinClass(source string, members ...string) *classInfo {
	ci := &classInfo{source: source, decls: map[string]struct{}{}}
	for _, m := range members {
		ci.decls[m+":()V"] = struct{}{}
	}
	return ci
}

// TestDemangleValueClass_Guards attacks the guard rather than the happy path.
// The recognition rule alone matches a name a Kotlin user can legally
// hand-write in backquotes, so every case asks the same question: does the
// rewrite still fire when the capture does not prove kotlinc minted the name?
//
// The last two cases are the over-reach the doc discloses, pinned so the
// disclosure cannot quietly narrow.
func TestDemangleValueClass_Guards(t *testing.T) {
	cases := []struct {
		why      string
		classes  map[string]*classInfo
		name     string
		owner    string
		wantName string
		wantOK   bool
	}{
		{
			why: "mangled name + the plain bridge in the same Kotlin class: rewritten",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "serialize-2TYgG_w", "serialize"),
			},
			name: "serialize-2TYgG_w", owner: "k/Codec",
			wantName: "serialize", wantOK: true,
		},
		{
			why: "no plain bridge in the class: a hand-written backquoted name, left alone",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "serialize-2TYgG_w"),
			},
			name: "serialize-2TYgG_w", owner: "k/Codec",
			wantName: "serialize-2TYgG_w", wantOK: false,
		},
		{
			why: "the bridge is in a DIFFERENT class: same-class is the rule, not same-capture",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "serialize-2TYgG_w"),
				"k/Other": kotlinClass("k/Other.kt", "serialize"),
			},
			name: "serialize-2TYgG_w", owner: "k/Codec",
			wantName: "serialize-2TYgG_w", wantOK: false,
		},
		{
			why: "owner compiled from .java: kotlinc did not lower it, so this is not that scheme",
			classes: map[string]*classInfo{
				"j/Codec": {source: "j/Codec.java", decls: map[string]struct{}{
					"serialize-2TYgG_w:()V": {}, "serialize:()V": {},
				}},
			},
			name: "serialize-2TYgG_w", owner: "j/Codec",
			wantName: "serialize-2TYgG_w", wantOK: false,
		},
		{
			why:     "owner absent from the capture: external, nothing proves the pair",
			classes: map[string]*classInfo{},
			name:    "serialize-2TYgG_w", owner: "k/Codec",
			wantName: "serialize-2TYgG_w", wantOK: false,
		},
		{
			why: "owner with an EMPTY decl table (a capture taken without -s): the guard cannot be evaluated, so decline",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt"),
			},
			name: "serialize-2TYgG_w", owner: "k/Codec",
			wantName: "serialize-2TYgG_w", wantOK: false,
		},
		{
			why: "multifile FACADE owner (no SourceFile header, so no path): declines rather than guessing",
			classes: map[string]*classInfo{
				"k/SerializersKt": kotlinClass("", "serialize-2TYgG_w", "serialize"),
			},
			name: "serialize-2TYgG_w", owner: "k/SerializersKt",
			wantName: "serialize-2TYgG_w", wantOK: false,
		},
		{
			why: "cluster C, the compound internal+value-class residual: NOT rewritten",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "append-7apg3OU$main", "append"),
			},
			name: "append-7apg3OU$main", owner: "k/Codec",
			wantName: "append-7apg3OU$main", wantOK: false,
		},
		{
			why: "an ordinary name is never touched",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "serialize"),
			},
			name: "serialize", owner: "k/Codec",
			wantName: "serialize", wantOK: false,
		},
		{
			why: "a backquoted user name whose suffix is too short: the rule rejects before the guard",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "foo-ab", "foo"),
			},
			name: "foo-ab", owner: "k/Codec",
			wantName: "foo-ab", wantOK: false,
		},
		{
			why: "THE DISCLOSED OVER-REACH: a hand-written backquoted `foo-abcd` beside a " +
				"plain `foo` in the same Kotlin class IS rewritten. No corpus instance — but reachable.",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "foo-abcd", "foo"),
			},
			name: "foo-abcd", owner: "k/Codec",
			wantName: "foo", wantOK: true,
		},
		{
			why: "THE SAME OVER-REACH AT ITS REAL WIDTH: isBase64Char accepts `-`, so nothing " +
				"has to look hash-shaped — a backquoted `parse-json-value` beside a plain `parse` " +
				"is rewritten to `parse`.",
			classes: map[string]*classInfo{
				"k/Codec": kotlinClass("k/ValueClasses.kt", "parse-json-value", "parse"),
			},
			name: "parse-json-value", owner: "k/Codec",
			wantName: "parse", wantOK: true,
		},
	}
	for _, tc := range cases {
		got, ok := demangleValueClass(tc.classes, tc.name, tc.owner)
		if got != tc.wantName || ok != tc.wantOK {
			t.Errorf("%s:\n demangleValueClass(%q, %q) = (%q, %v), want (%q, %v)",
				tc.why, tc.name, tc.owner, got, ok, tc.wantName, tc.wantOK)
		}
	}
}

// valueClassCapture is a `javap -c -p -s`-shaped disassembly of the shape
// kotlinc emits for a value-class parameter: the real function under the
// mangled name, and a bridge under the plain name that forwards to it.
//
// It is HAND-WRITTEN against the documented shape, not compiler output —
// kotlinc is absent from this sandbox. The compiler-backed fixtures live in
// demangle_test.go and skip locally. So this pins the parse-and-rewrite path;
// it does not certify the shape itself.
const valueClassCapture = `Compiled from "ValueClasses.kt"
public final class k.Codec {
  public final void serialize-2TYgG_w(k.Enc, int);
    descriptor: (Lk/Enc;I)V
    Code:
       0: aload_1
       1: invokevirtual #7                  // Method k/Enc.emit:()V
       4: return

  public final void serialize(k.Enc, k.Wrapped);
    descriptor: (Lk/Enc;Lk/Wrapped;)V
    Code:
       0: aload_0
       1: aload_1
       2: aload_2
       3: invokevirtual #11                 // Method serialize-2TYgG_w:(Lk/Enc;I)V
       6: return
}
Compiled from "Enc.kt"
public final class k.Enc {
  public void emit();
    descriptor: ()V
    Code:
       0: return
}
`

// TestParseJavap_ValueClassCallerAndCallee exercises both application sites on
// one capture.
//
//   - CALLER side — the invoke inside the mangled function must be attributed
//     to `serialize`, the name ValueClasses.kt declares, not to
//     `serialize-2TYgG_w`. This is the 22-counterexample shape on
//     kotlinx.serialization: the oracle accusing correct code.
//   - CALLEE side — the bridge's forward call names `serialize-2TYgG_w` in the
//     constant pool and must be recorded as `serialize`. That fact is the
//     self-call the doc names: it is a truth fact the source did not write, and
//     it is asserted here rather than left to be discovered.
func TestParseJavap_ValueClassCallerAndCallee(t *testing.T) {
	calls, err := ParseJavap([]byte(valueClassCapture))
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	var sawCaller, sawCallee bool
	for _, c := range calls {
		if strings.Contains(c.CallerMethod, "-") {
			t.Errorf("caller attribution still carries the mangled name: %+v", c)
		}
		if strings.Contains(c.Callee, "-") {
			t.Errorf("callee still carries the mangled name: %+v", c)
		}
		switch {
		case c.Callee == "emit":
			sawCaller = true
			if c.CallerMethod != "serialize" {
				t.Errorf("caller of emit = %q, want %q (ValueClasses.kt declares serialize)", c.CallerMethod, "serialize")
			}
			if c.CallerFile != "k/ValueClasses.kt" || c.CalleeFile != "k/Enc.kt" {
				t.Errorf("emit fact files = (%q, %q), want (k/ValueClasses.kt, k/Enc.kt)", c.CallerFile, c.CalleeFile)
			}
		case c.Callee == "serialize" && c.CallerMethod == "serialize":
			sawCallee = true
			if c.CalleeFile != "k/ValueClasses.kt" {
				t.Errorf("bridge→real callee file = %q, want k/ValueClasses.kt", c.CalleeFile)
			}
		}
	}
	if !sawCaller {
		t.Errorf("no fact for the invoke inside the mangled function; got %+v", calls)
	}
	if !sawCallee {
		t.Errorf("no fact for the bridge's forward call; got %+v", calls)
	}
}

// TestParseJavap_BackquotedDashNameIsNotRewritten is the same capture with the
// bridge REMOVED — which is what a hand-written backquoted `foo-abcd` looks
// like in bytecode. The oracle must leave the name the source wrote alone;
// SW-173's round-1 lesson is that rewriting a name that was never mangled is
// the direction that accuses correct code.
func TestParseJavap_BackquotedDashNameIsNotRewritten(t *testing.T) {
	capture := `Compiled from "Names.kt"
public final class k.Names {
  public final void weird-2TYgG_w(k.Enc);
    descriptor: (Lk/Enc;)V
    Code:
       0: aload_1
       1: invokevirtual #7                  // Method k/Enc.emit:()V
       4: return
}
Compiled from "Enc.kt"
public final class k.Enc {
  public void emit();
    descriptor: ()V
    Code:
       0: return
}
`
	calls, err := ParseJavap([]byte(capture))
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("expected the emit fact")
	}
	for _, c := range calls {
		if c.Callee == "emit" && c.CallerMethod != "weird-2TYgG_w" {
			t.Errorf("caller = %q, want the name the source wrote (weird-2TYgG_w): no bridge proves a mangling here", c.CallerMethod)
		}
	}
}
