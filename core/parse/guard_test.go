package parse

import (
	"context"
	"sort"
	"testing"
)

// expectedDefaultLanguages is the language set the default-tier guard expects from
// RegisterDefaults. It is DERIVED FROM RegisterDefaults (not the EP-009 21-language
// list): HTML is deliberately absent (deferred to graphi-broad/SW-056 — a security
// positive, it avoids embedding an unregistered blade.bin blob). If a language is
// added/removed in defaults.go without updating this set, the drift test below fails.
var expectedDefaultLanguages = []string{
	"bash", "c", "c_sharp", "cpp", "css", "go", "hcl", "java", "javascript",
	"json", "kotlin", "lua", "markdown", "php", "python", "ruby", "rust", "sql",
	"toml", "tsx", "typescript", "yaml",
}

// TestAssertPureGoDefaults_Positive is the RELEASE-BLOCKING positive guard
// (AC#2/AC#4): every grammar reachable from RegisterDefaults declares a pure-Go
// runtime, so the guard returns NO offenders. The CGo-free guarantee of the default
// tier cannot silently regress: add a CGO-backed parser to defaults.go and this
// fails loudly.
func TestAssertPureGoDefaults_Positive(t *testing.T) {
	r := RegisterDefaults(NewRegistry())

	offenders := AssertPureGoDefaults(r)
	if len(offenders) != 0 {
		t.Fatalf("RELEASE-BLOCKING no-CGO guard FAILED: %s", FormatImpureFailure(offenders))
	}
}

// TestAssertPureGoDefaults_ForestRuntimeAbsent asserts the CGO go-sitter-forest
// runtime is never declared by any registered default parser — the registration
// layer half of "go-sitter-forest is never reachable from the default build". This
// is complementary to (not a replacement for) the import-graph absence assertion in
// internal/cgoconformance.
func TestAssertPureGoDefaults_ForestRuntimeAbsent(t *testing.T) {
	r := RegisterDefaults(NewRegistry())
	for _, rt := range RegisteredRuntimes(r) {
		if rt == RuntimeCGOForest {
			t.Fatalf("CGO go-sitter-forest runtime reachable from the default tier (regression!)")
		}
		if !rt.IsPureGo() {
			t.Fatalf("non-pure-Go runtime %q reachable from the default tier", rt)
		}
	}
}

// TestAssertPureGoDefaults_LanguageSet locks the registered language set to the set
// DERIVED from RegisterDefaults (drift-guard: HTML deliberately absent). It prevents
// the positive guard from passing vacuously against an unexpectedly shrunk default
// tier.
func TestAssertPureGoDefaults_LanguageSet(t *testing.T) {
	got := RegisterDefaults(NewRegistry()).Languages()
	want := append([]string(nil), expectedDefaultLanguages...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("default language set drift: got %d languages %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("default language set drift at %d: got %q, want %q (full got=%v)", i, got[i], want[i], got)
		}
	}
	if contains(got, "html") {
		t.Fatal("html must be ABSENT from the default tier (deferred to graphi-broad/SW-056)")
	}
}

// cgoForestParser is a SYNTHETIC, deliberately-offending parser that declares the
// CGO go-sitter-forest runtime. It exists ONLY for the negative/anti-vacuity test:
// it is registered into a THROWAWAY registry (never the real default registry) to
// prove the guard rejects a CGO-backed parser. It does not compile any cgo — the
// offense is carried purely by its declared Runtime marker, which is exactly what
// the registration-level guard inspects.
type cgoForestParser struct{}

func (*cgoForestParser) Language() string     { return "fortran_cgo" }
func (*cgoForestParser) Extensions() []string { return []string{".f90cgo"} }
func (*cgoForestParser) Runtime() Runtime     { return RuntimeCGOForest }
func (*cgoForestParser) Parse(_ context.Context, filename string, src []byte) (*ParseResult, error) {
	return &ParseResult{Meta: SourceMeta{Path: filename, Language: "fortran_cgo", Size: len(src)}}, nil
}

// TestAssertPureGoDefaults_Negative is the ANTI-VACUITY test: it registers the
// synthetic CGO-marked parser into a throwaway registry and asserts the SAME
// exported guard rejects it (names it as an offender). This proves the guard cannot
// pass vacuously — the code path that accepts the real defaults must also reject a
// planted CGO offender.
func TestAssertPureGoDefaults_Negative(t *testing.T) {
	r := NewRegistry() // throwaway — never the real default registry
	r.Register(&cgoForestParser{})

	offenders := AssertPureGoDefaults(r)
	if len(offenders) != 1 {
		t.Fatalf("negative guard: expected exactly 1 offender, got %d: %v", len(offenders), offenders)
	}
	if offenders[0].Language != "fortran_cgo" || offenders[0].Runtime != RuntimeCGOForest {
		t.Fatalf("negative guard: wrong offender: %v", offenders[0])
	}
	if msg := FormatImpureFailure(offenders); msg == "" {
		t.Fatal("negative guard: FormatImpureFailure returned empty for a real offender")
	}
}

// TestAssertPureGoDefaults_NegativeAlongsideRealDefaults proves the offending CGO
// parser is detected even when mixed into the real default set — the guard is not
// fooled by a majority of pure-Go parsers around one CGO offender.
func TestAssertPureGoDefaults_NegativeAlongsideRealDefaults(t *testing.T) {
	r := RegisterDefaults(NewRegistry())
	r.Register(&cgoForestParser{})

	offenders := AssertPureGoDefaults(r)
	if len(offenders) != 1 || offenders[0].Runtime != RuntimeCGOForest {
		t.Fatalf("expected the single CGO offender among pure-Go defaults, got %v", offenders)
	}
}

// TestAssertPureGoDefaults_EmptyRegistryVacuity is the empty-registry vacuity check
// (architect/QA finding): an empty registry has no offenders AND no registered
// runtimes, so a "no offenders" result is meaningful only paired with the positive
// language-set assertion above (which proves the default tier is non-empty).
func TestAssertPureGoDefaults_EmptyRegistryVacuity(t *testing.T) {
	r := NewRegistry()
	if off := AssertPureGoDefaults(r); len(off) != 0 {
		t.Fatalf("empty registry should yield no offenders, got %v", off)
	}
	if rts := RegisteredRuntimes(r); len(rts) != 0 {
		t.Fatalf("empty registry should yield no runtimes, got %v", rts)
	}
	// Nil registry must not panic.
	if off := AssertPureGoDefaults(nil); off != nil {
		t.Fatalf("nil registry should yield nil offenders, got %v", off)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
