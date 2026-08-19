package jvmgroundtruth

// SW-172 — the toolchain-free half of the signature work: the parsing and
// keying rules, pinned against REAL captured javap output
// (testdata/overloads.javap.txt, testdata/refreturns.javap.txt) so no JDK is
// needed to catch a regression in them. The live differential lives in
// signature_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadOverloads(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "overloads.javap.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// TestParseMethodRef_CarriesDescriptor is AC-3: the descriptor is no longer
// thrown away at parseMethodRef. It is the ONLY place the callee's real,
// javac-chosen signature exists in this output, and discarding it is precisely
// what made the oracle blind to overload mis-binding.
func TestParseMethodRef_CarriesDescriptor(t *testing.T) {
	cases := []struct {
		line              string
		owner, name, desc string
		ok                bool
	}{
		{"2: invokevirtual #7 // Method tax/Rate.apply:(I)I", "tax/Rate", "apply", "(I)I", true},
		{"2: invokevirtual #7 // Method a/Rate.apply:([La/Thing;)I", "a/Rate", "apply", "([La/Thing;)I", true},
		{`1: invokespecial #1 // Method java/lang/Object."<init>":()V`, "java/lang/Object", "<init>", "()V", true},
		{"1: invokevirtual #7 // Method rate:()I", "", "rate", "()I", true},
		// Reference RETURN types — the shape parseMethodHeader used to eat.
		{"1: invokevirtual #7 // Method a/Derived.self:()La/Derived;", "a/Derived", "self", "()La/Derived;", true},
		// INTERFACE refs. javac writes `// InterfaceMethod` for every invoke
		// whose resolved owner is an interface, and matching only `// Method `
		// dropped invokeinterface, interface invokestatic and `X.super.m()`
		// from the truth set entirely — correct code calling through any
		// interface was then accused at every precision.
		{"1: invokeinterface #17,  1 // InterfaceMethod a/HasSeed.seed:()I", "a/HasSeed", "seed", "()I", true},
		{"6: invokestatic  #22 // InterfaceMethod a/HasSeed.base:()I", "a/HasSeed", "base", "()I", true},
		{"0: invokestatic  #1 // InterfaceMethod helper:()I", "", "helper", "()I", true},
		{"1: invokespecial #7 // InterfaceMethod a/HasSeed.seed:()I", "a/HasSeed", "seed", "()I", true},
		{"5: getfield #13 // Field stored:Ltax/Rate;", "", "", "", false},
		{"0: iload_1", "", "", "", false},
	}
	for _, c := range cases {
		owner, name, desc, ok := parseMethodRef(c.line)
		if ok != c.ok || owner != c.owner || name != c.name || desc != c.desc {
			t.Errorf("parseMethodRef(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				c.line, owner, name, desc, ok, c.owner, c.name, c.desc, c.ok)
		}
	}
}

// TestDescriptorParams pins the JVMS 4.3.2 split, including the two ways it
// must DECLINE: a malformed descriptor must never read as an empty parameter
// list, which would forge an arity-0 fact out of nothing.
func TestDescriptorParams(t *testing.T) {
	cases := []struct {
		desc string
		want []string
		ok   bool
	}{
		{"()V", nil, true},
		{"(I)I", []string{"I"}, true},
		{"(II)I", []string{"I", "I"}, true},
		{"(La/Thing;)I", []string{"La/Thing;"}, true},
		{"([La/Thing;)I", []string{"[La/Thing;"}, true},
		{"([[IJLa/B$C;Z)V", []string{"[[I", "J", "La/B$C;", "Z"}, true},
		{"(La/Rate;La/Thing;[La/Thing;)I", []string{"La/Rate;", "La/Thing;", "[La/Thing;"}, true},
		{"I)V", nil, false},     // no opening paren
		{"(I", nil, false},      // no closing paren
		{"(La/X)V", nil, false}, // unterminated object descriptor
		{"(Q)V", nil, false},    // not a field descriptor
	}
	for _, c := range cases {
		got, ok := descriptorParams(c.desc)
		if ok != c.ok {
			t.Errorf("descriptorParams(%q) ok = %v, want %v", c.desc, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("descriptorParams(%q) = %v, want %v", c.desc, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("descriptorParams(%q) = %v, want %v", c.desc, got, c.want)
				break
			}
		}
	}
}

// TestParseClassHeader_GenericBoundIsNotASuperclass pins the bracket-depth
// scan. `class a.Rate<T extends java.lang.Number> extends a.Base` contains the
// keyword `extends` TWICE, and reading the first one would send the JVM
// resolution walk up an imaginary chain rooted at java.lang.Number.
func TestParseClassHeader_GenericBoundIsNotASuperclass(t *testing.T) {
	cases := []struct {
		line        string
		name, super string
		ok          bool
	}{
		{"public class shop.Cart {", "shop.Cart", "", true},
		{"class shop.Cart$Helper {", "shop.Cart$Helper", "", true},
		{"public class a.Derived extends a.Base {", "a.Derived", "a.Base", true},
		{"public class a.Derived extends a.Base implements java.io.Serializable {", "a.Derived", "a.Base", true},
		{"public class a.Rate<T extends java.lang.Number> extends a.Base {", "a.Rate", "a.Base", true},
		{"public interface a.I {", "a.I", "", true},
		{"  public int checkout(tax.Rate);", "", "", false},
	}
	for _, c := range cases {
		h, ok := parseClassHeader(c.line)
		if ok != c.ok || h.name != c.name || h.super != c.super {
			t.Errorf("parseClassHeader(%q) = (%q,%q,%v), want (%q,%q,%v)", c.line, h.name, h.super, ok, c.name, c.super, c.ok)
		}
	}
}

// TestParseMethodHeader_RejectsDescriptorLine pins the fix for a bug the live
// gate caught: `descriptor: (Ltax/Rate;)Ltax/Rate;` ends with ';' and contains
// '(', so it satisfies every other test and was read as a method named
// "descriptor:", which then became the CALLER of the following invokes.
func TestParseMethodHeader_RejectsDescriptorLine(t *testing.T) {
	if name, ok := parseMethodHeader("descriptor: (Ltax/Rate;)Ltax/Rate;"); ok {
		t.Fatalf("a descriptor line is not a method header, got %q", name)
	}
	if name, ok := parseMethodHeader("public int checkout(tax.Rate);"); !ok || name != "checkout" {
		t.Fatalf("parseMethodHeader = (%q,%v), want (checkout,true)", name, ok)
	}
	if name, ok := parseMethodHeader("public shop.Cart();"); !ok || name != "<init>" {
		t.Fatalf("a constructor must normalize to <init>, got (%q,%v)", name, ok)
	}
}

// TestParseMethodHeader_RejectsInvokeLines pins the SIBLING of the bug above,
// which was PRE-EXISTING (it predates SW-172) and strictly worse.
//
// A javap invoke line whose callee RETURNS A REFERENCE TYPE ends with ';' and
// contains '(' — exactly the shape a method header has — and the token before
// '(' carries a '.', so it was read as a CONSTRUCTOR header. In parseInvokes
// that both DROPPED the invoke fact and clobbered the current method to
// `<init>`, re-parenting every later invoke in the method onto a caller that
// never made the call. Missing and mis-attributed truth facts are the
// direction that MANUFACTURES violations, so plain covariant-return Java —
// with every graphi binding correct — was accused at every precision,
// including by-name.
//
// It was invisible because every fixture in the harness returned a primitive
// or void. It is pinned here at the parser, and reproducibly from testdata
// alone in TestParseJavap_ReferenceReturnsAndInterfaceDispatch.
func TestParseMethodHeader_RejectsInvokeLines(t *testing.T) {
	rejected := []string{
		"1: invokevirtual #7                  // Method a/Derived.self:()La/Derived;",
		"5: invokestatic  #9                  // Method a/Rate.make:()La/Rate;",
		"7: invokeinterface #21,  1           // InterfaceMethod a/HasSeed.seed:()La/Seed;",
		"6: invokespecial #27                 // Method a/App$1L.\"<init>\":(La/App;La/Derived;)V",
		"3: getfield      #13                 // Field stored:Ltax/Rate;",
		"2: ldc           #5                  // String (a);",
	}
	for _, line := range rejected {
		if name, ok := parseMethodHeader(strings.TrimSpace(line)); ok {
			t.Errorf("parseMethodHeader(%q) = (%q,true); a javap instruction is not a member declaration", line, name)
		}
	}

	// …and the real headers it must still read, including the ones whose own
	// return type or throws clause is a reference type.
	accepted := map[string]string{
		"public a.Derived self();":                            "self",
		"public a.Base self() throws java.io.IOException;":    "self",
		"public <T extends java.lang.Number> int measure(T);": "measure",
		"public abstract int seed();":                         "seed",
		"public static int base();":                           "base",
		"a.App$1L(a.App, a.Derived);":                         "<init>",
		"public int descriptor(int);":                         "descriptor",
		"public java.util.List<java.lang.String> names(java.util.Map<java.lang.String, java.lang.Integer>);": "names",
	}
	for line, want := range accepted {
		name, ok := parseMethodHeader(line)
		if !ok || name != want {
			t.Errorf("parseMethodHeader(%q) = (%q,%v), want (%q,true)", line, name, ok, want)
		}
	}
}

// TestSyntheticNestedClass pins the discriminator for javac-minted anonymous
// and local classes. It must be EXACT, not a guess: a '$' followed by a digit
// cannot appear in a user-written nested type, because a Java identifier
// cannot begin with a digit.
func TestSyntheticNestedClass(t *testing.T) {
	synthetic := []string{"a/App$1", "a/App$1L", "a/Outer$Inner$2", "a/App$12Body"}
	plain := []string{"a/App", "a/Outer$Inner", "shop/Cart$Helper", "a/A$B$C", "a/App$L1"}
	for _, s := range synthetic {
		if !syntheticNestedClass(s) {
			t.Errorf("syntheticNestedClass(%q) = false, want true", s)
		}
	}
	for _, s := range plain {
		if syntheticNestedClass(s) {
			t.Errorf("syntheticNestedClass(%q) = true, want false", s)
		}
	}
}

// TestParseJavap_ReferenceReturnsAndInterfaceDispatch is the REPRODUCIBLE-FROM-
// TESTDATA proof for the two truth-losing parser defects, so neither needs a
// live JDK to catch. The fixture is real javap output over covariant-return
// overrides, an interface-typed receiver, an interface static call and a local
// class; see testdata/README.md.
func TestParseJavap_ReferenceReturnsAndInterfaceDispatch(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "refreturns.javap.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	// Precondition: the fixture must actually CONTAIN the constructs, or this
	// test is green against nothing — which is how the defects survived.
	if n := strings.Count(string(raw), "// InterfaceMethod "); n < 2 {
		t.Fatalf("fixture lost its interface refs (%d)", n)
	}
	if !strings.Contains(string(raw), "// Method a/Derived.self:()La/Derived;") {
		t.Fatal("fixture lost its reference-returning invoke line")
	}

	calls, err := ParseJavap(raw)
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	type fact struct{ callerFile, callerMethod, calleeFile, callee string }
	got := map[fact]Call{}
	for _, c := range calls {
		got[fact{c.CallerFile, c.CallerMethod, c.CalleeFile, c.Callee}] = c
	}

	// The reference-returning invokes must SURVIVE, under their real caller.
	for _, want := range []fact{
		{"a/App.java", "run", "a/Derived.java", "self"},
		{"a/App.java", "run", "a/Derived.java", "tag"},
		// Interface dispatch: invokeinterface and interface invokestatic.
		{"a/App.java", "viaIface", "a/HasSeed.java", "seed"},
		{"a/App.java", "viaIface", "a/HasSeed.java", "base"},
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing truth fact %+v; parsed: %+v", want, calls)
		}
	}
	// …and nothing may be re-parented onto <init>, which is what the header
	// mis-read did.
	for _, c := range calls {
		if c.CallerMethod == "<init>" && (c.Callee == "tag" || c.Callee == "seed" || c.Callee == "self") {
			t.Errorf("invoke re-parented onto a constructor: %+v", c)
		}
	}

	// The local class's own call is present, and FLAGGED as a caller graphi
	// cannot name (see AbstainBytecodeCallerNotAlignable).
	local, ok := got[fact{"a/App.java", "go", "a/Derived.java", "tag"}]
	if !ok {
		t.Fatalf("the local class's call vanished: %+v", calls)
	}
	if !local.callerSynthetic {
		t.Error("a call made inside a local class must be flagged unalignable")
	}
}

// TestCompareAt_MultiLevelCoarseFallback pins that the coarse rescue walks
// EVERY coarser precision, not one level. A truth set captured without `-s` is
// undecidable at by-arity AND by-signature, so a single-step fallback from
// by-signature looks for a by-arity key the fact never has and ACCUSES the
// confirmed call — a fabricated stop-ship on a question the truth set was
// never able to answer.
func TestCompareAt_MultiLevelCoarseFallback(t *testing.T) {
	truth := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/Rate.java", Callee: "apply",
		CalleeArity: ArityUnknown, CalleeParams: SigUnknown,
		ArityReason: AbstainBytecodeNoDescriptors, ParamsReason: AbstainBytecodeNoDescriptors,
	}}
	confirmed := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/Rate.java", Callee: "apply",
		CalleeArity: 1, CalleeParams: "(I)",
	}}
	for _, p := range []Precision{ByArity, BySignature} {
		res := CompareAt(confirmed, truth, p)
		if !res.Sound() {
			t.Fatalf("a truth set that cannot answer at %s must abstain, not accuse: %+v", p, res.Violations)
		}
		if res.AbstainReasons[AbstainBytecodeNoDescriptors] != 1 {
			t.Fatalf("the abstention must carry the TRUTH side's reason at %s, got %v", p, res.AbstainReasons)
		}
	}
}

// TestCompareAt_OwnerUnresolvedTruthAbstains pins the fourth verdict branch. A
// failed owner walk and an external callee both arrive as CalleeFile "", and
// conflating them made the oracle ACCUSE every interface default method: the
// truth fact was dropped before it could register its own named reason, so a
// correct confirmed call found no match and no rescue.
//
// An owner-unresolved fact is the oracle declining, not the oracle agreeing
// that no such call exists. It stays out of the recall denominator — it is not
// a fact the oracle can claim to have keyed — but it must not let correct code
// be accused.
func TestCompareAt_OwnerUnresolvedTruthAbstains(t *testing.T) {
	truth := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "", Callee: "seed",
		CalleeArity: ArityUnknown, CalleeParams: SigUnknown,
		ArityReason: AbstainBytecodeOwnerUnresolved, ParamsReason: AbstainBytecodeOwnerUnresolved,
	}}
	confirmed := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/HasSeed.java", Callee: "seed",
		CalleeArity: 0, CalleeParams: "()",
	}}
	for _, p := range []Precision{ByName, ByArity, BySignature} {
		res := CompareAt(confirmed, truth, p)
		if !res.Sound() {
			t.Fatalf("an owner-unresolved truth fact must abstain at %s, not accuse: %+v", p, res.Violations)
		}
		if res.AbstainReasons[AbstainBytecodeOwnerUnresolved] != 1 {
			t.Fatalf("the abstention must be NAMED at %s, got %v", p, res.AbstainReasons)
		}
		if res.TruthIntra != 0 {
			t.Fatalf("an unattributable truth fact must stay OUT of the recall denominator at %s, got %d", p, res.TruthIntra)
		}
	}

	// The rescue is narrow: it is keyed on (caller, callee NAME), so a
	// confirmed call to a DIFFERENT name is still a violation.
	ghost := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/HasSeed.java", Callee: "ghost",
		CalleeArity: 0, CalleeParams: "()",
	}}
	if CompareAt(ghost, truth, ByName).Sound() {
		t.Fatal("the owner-unresolved rescue must not excuse a call the truth set never made under that name")
	}
}

// TestEnclosingOfSynthetic pins the lambda-caller normalization and, just as
// importantly, that it leaves anything NOT matching javac's convention alone.
func TestEnclosingOfSynthetic(t *testing.T) {
	cases := map[string]string{
		"lambda$lam$0":  "lam",
		"lambda$run$12": "run",
		// Measured against javac 21: a lambda in a constructor is
		// `lambda$new$0` and belongs to <init>; one in a static initializer is
		// `lambda$static$1` and belongs to <clinit>.
		"lambda$new$0":    "<init>",
		"lambda$static$1": "<clinit>",
		// Left alone: not javac's lambda shape.
		"run":        "run",
		"lambda":     "lambda",
		"lambda$":    "lambda$",
		"access$000": "access$000",
		"<init>":     "<init>",
		// A '$' in the enclosing name is legal in a Java identifier, and the
		// LAST '$' is the index separator — so this is `a$b`, not `lambda$a$b`.
		"lambda$a$b$0": "a$b",
	}
	for in, want := range cases {
		if got := enclosingOfSynthetic(in); got != want {
			t.Errorf("enclosingOfSynthetic(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseJavap_OwnerWalkAndSignatures pins, on real captured bytecode, the
// three facts the finer precisions rest on: the descriptor reaches the Call,
// the arity is read from it, and the SYMBOLIC owner of an inherited call is
// walked to the DECLARING class.
func TestParseJavap_OwnerWalkAndSignatures(t *testing.T) {
	calls, err := ParseJavap(loadOverloads(t))
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}

	// The four `apply` overloads are FOUR distinct facts now — under the
	// pre-SW-172 key they deduped to ONE, which is the blindness in one line.
	applies := 0
	for _, c := range calls {
		if c.Callee == "apply" && c.CallerMethod == "run" {
			applies++
		}
	}
	if applies != 4 {
		t.Fatalf("the four apply overloads must be four distinct facts, got %d: %+v", applies, calls)
	}
	byName := map[callKey]struct{}{}
	for _, c := range calls {
		byName[c.key(ByName)] = struct{}{}
	}
	nameApplies := 0
	for k := range byName {
		if k.callee == "apply" && k.callerMethod == "run" {
			nameApplies++
		}
	}
	if nameApplies != 1 {
		t.Fatalf("at ByName the same four facts must collapse to ONE key (that is the blindness), got %d", nameApplies)
	}

	want := map[string]string{ // params -> descriptor
		"(I)":          "(I)I",
		"(II)":         "(II)I",
		"(La/Thing;)":  "(La/Thing;)I",
		"([La/Thing;)": "([La/Thing;)I",
	}
	for _, c := range calls {
		if c.Callee != "apply" || c.CallerMethod != "run" {
			continue
		}
		desc, ok := want[c.CalleeParams]
		if !ok {
			t.Errorf("unexpected apply signature %q", c.CalleeParams)
			continue
		}
		if c.CalleeDescriptor != desc {
			t.Errorf("apply%s carries descriptor %q, want %q", c.CalleeParams, c.CalleeDescriptor, desc)
		}
		delete(want, c.CalleeParams)
	}
	if len(want) != 0 {
		t.Errorf("missing apply signatures: %v", want)
	}

	// `r.seed()` is declared on a.Base and invoked through a.Rate: javac writes
	// the symbolic owner `a/Rate.seed`, and the walk must land on a/Base.java.
	found := false
	for _, c := range calls {
		if c.Callee != "seed" || c.CallerMethod != "run" {
			continue
		}
		found = true
		if c.CalleeFile != "a/Base.java" {
			t.Errorf("the inherited call must resolve to the DECLARING class a/Base.java, got %q", c.CalleeFile)
		}
	}
	if !found {
		t.Fatal("the inherited seed() call is missing entirely")
	}

	// The lambda body's call is attributed to its enclosing method.
	lam := false
	for _, c := range calls {
		if c.CallerMethod == "lam" && c.Callee == "seed" {
			lam = true
		}
		if len(c.CallerMethod) > 7 && c.CallerMethod[:7] == "lambda$" {
			t.Errorf("a synthetic lambda method survived as a caller: %+v", c)
		}
	}
	if !lam {
		t.Error("the call inside the lambda must be attributed to `lam`")
	}
}

// TestParseJavap_ExternalParamAbstains pins that a descriptor naming a class
// the repository does not declare makes the SIGNATURE undecidable rather than
// pairing `Ljava/lang/String;` with a declaration that only ever wrote
// `String`. Arity survives — it does not depend on knowing the type.
func TestParseJavap_ExternalParamAbstains(t *testing.T) {
	calls, err := ParseJavap(loadOverloads(t))
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	for _, c := range calls {
		if c.Callee != "tag" {
			continue
		}
		if c.CalleeArity != 1 {
			t.Errorf("arity must survive an external parameter, got %d", c.CalleeArity)
		}
		if c.CalleeParams != SigUnknown || c.ParamsReason != AbstainBytecodeExternalParam {
			t.Errorf("an external parameter must abstain by NAME, got params=%q reason=%q", c.CalleeParams, c.ParamsReason)
		}
	}
}

// TestParseJavap_NoDescriptorsDegradesLegibly pins the -s-less path against the
// legacy fixture: everything still parses at ByName — the pre-SW-172 key,
// unchanged — and the finer precisions DECLINE under a named reason rather
// than offering an answer built on an unwalked symbolic owner.
func TestParseJavap_NoDescriptorsDegradesLegibly(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "cart.javap.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	calls, err := ParseJavap(raw)
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}
	intra := 0
	for _, c := range calls {
		if c.CalleeFile == "" {
			continue // external
		}
		intra++
		if c.CalleeArity != ArityUnknown || c.ArityReason != AbstainBytecodeNoDescriptors {
			t.Errorf("without -s the finer precisions must decline, got %+v", c)
		}
	}
	if intra == 0 {
		t.Fatal("the legacy fixture must still yield intra-repo facts at ByName")
	}

	// And the declared-method index is empty, so Verify confirms nothing —
	// declining, never waving through.
	d, err := ParseDeclaredMethods(raw)
	if err != nil {
		t.Fatalf("ParseDeclaredMethods: %v", err)
	}
	if len(d) != 0 {
		t.Fatalf("output without -s carries no descriptors; got %v", d)
	}
	got := d.Verify([]Call{{CalleeFile: "shop/Cart.java", Callee: "checkout", CalleeParams: "(I)"}})
	if got[0].CalleeParams != SigUnknown || got[0].ParamsReason != AbstainBinderSignatureUnverified {
		t.Fatalf("an unconfirmable rendering must be demoted, got %+v", got[0])
	}
}

// TestCompareAt_AbstainsWhenTheTRUTHCannotAnswer pins the third verdict branch,
// the one that keeps the oracle honest: "the truth set disagrees with you" and
// "the truth set cannot answer at this precision" are different findings, and
// reporting the second as a stop-ship counterexample would be a fabricated
// defect. Here the confirmed side is fully decidable and the TRUTH is not.
func TestCompareAt_AbstainsWhenTheTRUTHCannotAnswer(t *testing.T) {
	truth := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/Rate.java", Callee: "tag",
		CalleeArity: 1, CalleeParams: SigUnknown, ParamsReason: AbstainBytecodeExternalParam,
	}}
	confirmed := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/Rate.java", Callee: "tag",
		CalleeArity: 1, CalleeParams: "(La/Other;)",
	}}

	byArity := CompareAt(confirmed, truth, ByArity)
	if !byArity.Sound() || byArity.Matched != 1 {
		t.Fatalf("arity agrees, so this must match: %s", byArity.Format())
	}

	res := CompareAt(confirmed, truth, BySignature)
	if !res.Sound() {
		t.Fatalf("a truth fact that cannot answer must not produce a counterexample: %+v", res.Violations)
	}
	if len(res.Abstained) != 1 || res.AbstainReasons[AbstainBytecodeExternalParam] != 1 {
		t.Fatalf("the abstention must carry the TRUTH side's reason, got %+v / %v", res.Abstained, res.AbstainReasons)
	}
	if res.TruthUndecidable != 1 {
		t.Fatalf("the undecidable truth fact must be reported outside the denominator, got %d", res.TruthUndecidable)
	}
	if res.TruthIntra != 0 {
		t.Fatalf("an unkeyable truth fact must not sit in the recall denominator, got %d", res.TruthIntra)
	}
}

// TestCompareAt_CoarseViolationSurvivesEveryPrecision pins that the precisions
// are strictly nested: a fabricated call is a violation at ByName and stays one
// at every finer key. A "finer" comparison that lost a coarse counterexample
// would be a regression dressed as an improvement.
func TestCompareAt_CoarseViolationSurvivesEveryPrecision(t *testing.T) {
	truth := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/Rate.java", Callee: "rate",
		CalleeArity: 0, CalleeParams: "()",
	}}
	ghost := []Call{{
		CallerFile: "a/App.java", CallerMethod: "run",
		CalleeFile: "a/Rate.java", Callee: "ghost",
		CalleeArity: 0, CalleeParams: "()",
	}}
	for _, p := range []Precision{ByName, ByArity, BySignature} {
		res := CompareAt(ghost, truth, p)
		if res.Sound() {
			t.Fatalf("a fabricated call must be a violation at %s", p)
		}
		if len(res.Abstained) != 0 {
			t.Fatalf("a fully decidable disagreement is a violation, not an abstention, at %s: %+v", p, res.Abstained)
		}
	}
}
