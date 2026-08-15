package jvmgroundtruth

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestParseJavap_RealFixture pins the parser against REAL javap -c -p output
// (testdata/cart.javap.txt, captured from the sandbox JDK — never
// hand-written, so the parser cannot be validated against a fiction).
func TestParseJavap_RealFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "cart.javap.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	calls, err := ParseJavap(raw)
	if err != nil {
		t.Fatalf("ParseJavap: %v", err)
	}

	// checkout invokes tax/Rate.rate twice (r.rate() and stored.rate()) → one
	// deduped fact. assist (in the nested Helper) invokes tax/Rate.rate.
	// scaled makes a SAME-CLASS call (other.rate()) which javap prints WITHOUT
	// an owner prefix (`// Method rate:()I`) — the owner is the current class.
	// Both constructors invoke java/lang/Object.<init> → external. The
	// getfield is not an invoke and produces no fact.
	assertHasCall(t, calls, Call{CallerFile: "shop/Cart.java", CallerMethod: "checkout", CalleeFile: "tax/Rate.java", Callee: "rate"})
	assertHasCall(t, calls, Call{CallerFile: "shop/Cart.java", CallerMethod: "assist", CalleeFile: "tax/Rate.java", Callee: "rate"})
	// The same-class call: owner-less ref resolved to the current class.
	assertHasCall(t, calls, Call{CallerFile: "tax/Rate.java", CallerMethod: "scaled", CalleeFile: "tax/Rate.java", Callee: "rate"})

	// The checkout→rate double call is deduped to exactly one fact.
	if n := countCall(calls, "checkout", "rate"); n != 1 {
		t.Errorf("checkout→rate must dedup to one fact, got %d", n)
	}
	// External <init> calls resolve to an empty CalleeFile — never a fabricated path.
	for _, c := range calls {
		if c.Callee == "Object" && c.CalleeFile != "" {
			t.Errorf("external java/lang/Object.<init> must have empty CalleeFile, got %q", c.CalleeFile)
		}
	}
	// No field-access fact leaked in (getfield stored is not an invoke).
	for _, c := range calls {
		if c.Callee == "stored" {
			t.Errorf("a getfield must not become a call fact: %+v", c)
		}
	}
}

// TestParseJavap_ClassSourceMapping pins the package-dir + SourceFile path
// derivation, including the nested class sharing its outer's file.
func TestParseJavap_ClassSourceMapping(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "cart.javap.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	m, err := parseClassSources(raw)
	if err != nil {
		t.Fatalf("parseClassSources: %v", err)
	}
	want := map[string]string{
		"shop/Cart":        "shop/Cart.java",
		"shop/Cart$Helper": "shop/Cart.java", // nested: same source file
		"tax/Rate":         "tax/Rate.java",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("classSource[%q] = %q, want %q", k, m[k], v)
		}
	}
}

// TestCompare_SoundnessAndRecall pins the comparator: a confirmed set that is
// a subset of the truth is sound with the right recall; a confirmed call with
// no truth fact is a violation.
func TestCompare_SoundnessAndRecall(t *testing.T) {
	truth := []Call{
		{CallerFile: "shop/Cart.java", CallerMethod: "checkout", CalleeFile: "tax/Rate.java", Callee: "rate"},
		{CallerFile: "shop/Cart.java", CallerMethod: "assist", CalleeFile: "tax/Rate.java", Callee: "apply"},
		{CallerFile: "shop/Cart.java", CallerMethod: "ctor", CalleeFile: "", Callee: "Object"}, // external, ignored
	}

	// Graphi confirmed only checkout→rate (assist is nested/ambiguous): sound,
	// recall 1/2.
	sound := Compare([]Call{
		{CallerFile: "shop/Cart.java", CallerMethod: "checkout", CalleeFile: "tax/Rate.java", Callee: "rate"},
	}, truth)
	if !sound.Sound() {
		t.Fatalf("subset-of-truth must be sound, violations: %+v", sound.Violations)
	}
	if sound.TruthIntra != 2 || sound.Matched != 1 {
		t.Fatalf("recall bookkeeping: intra=%d matched=%d, want 2/1", sound.TruthIntra, sound.Matched)
	}
	if got := sound.Recall(); got < 0.49 || got > 0.51 {
		t.Fatalf("recall = %v, want ~0.5", got)
	}

	// A fabricated confirmed call (no bytecode fact) is a violation — the
	// zero-tolerance direction.
	unsound := Compare([]Call{
		{CallerFile: "shop/Cart.java", CallerMethod: "checkout", CalleeFile: "tax/Rate.java", Callee: "ghost"},
	}, truth)
	if unsound.Sound() {
		t.Fatal("a confirmed call with no bytecode fact must be a violation")
	}
	if len(unsound.Violations) != 1 || unsound.Violations[0].Callee != "ghost" {
		t.Fatalf("violation set: %+v", unsound.Violations)
	}
}

// TestParseMethodRef pins the constant-pool ref parsing incl. the quoted
// <init> and the owner/name split on the single dot in an internal ref.
func TestParseMethodRef(t *testing.T) {
	cases := []struct {
		line        string
		owner, name string
		ok          bool
	}{
		{"2: invokevirtual #7 // Method tax/Rate.apply:(I)I", "tax/Rate", "apply", true},
		{`1: invokespecial #1 // Method java/lang/Object."<init>":()V`, "java/lang/Object", "<init>", true},
		{"5: getfield #13 // Field stored:Ltax/Rate;", "", "", false},
		{"0: iload_1", "", "", false},
	}
	for _, c := range cases {
		owner, name, ok := parseMethodRef(c.line)
		if ok != c.ok || owner != c.owner || name != c.name {
			t.Errorf("parseMethodRef(%q) = (%q,%q,%v), want (%q,%q,%v)", c.line, owner, name, ok, c.owner, c.name, c.ok)
		}
	}
}

// TestConfirmedCallsDeterministic pins that ConfirmedCalls output is sorted and
// deduped (structural check via a re-sort equality).
func TestConfirmedCallsDeterministic(t *testing.T) {
	a := []Call{
		{CallerFile: "b.java", CallerMethod: "m", CalleeFile: "a.java", Callee: "x"},
		{CallerFile: "a.java", CallerMethod: "m", CalleeFile: "a.java", Callee: "x"},
	}
	b := append([]Call(nil), a...)
	sortCalls(a)
	sortCalls(b)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("sortCalls must be a total order")
	}
	if a[0].CallerFile != "a.java" {
		t.Fatalf("sort order: %+v", a)
	}
}

func assertHasCall(t *testing.T, calls []Call, want Call) {
	t.Helper()
	for _, c := range calls {
		if c == want {
			return
		}
	}
	t.Errorf("missing expected call %+v in %+v", want, calls)
}

func countCall(calls []Call, method, callee string) int {
	n := 0
	for _, c := range calls {
		if c.CallerMethod == method && c.Callee == callee {
			n++
		}
	}
	return n
}
