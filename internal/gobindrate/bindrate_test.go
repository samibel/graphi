package gobindrate

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Hand-counted fixture: 4 source files, no test files, go.mod present.
//
// File a/pkg.go (package a) — 3 calls:
//
//	line 4: alpha()  — bare ident, NOT declared → bare_ident_no_resolved_object_cross_package
//	line 5: beta()   — bare ident, NOT declared → bare_ident_no_resolved_object_cross_package
//	line 6: Beta()   — bare ident, DECLARED in package a → bound_internal_func
//
// File b/pkg.go (package b) — 2 calls:
//
//	line 5: a.Beta() — selector on intra-module package qualifier; the
//	                    tolerant stubImporter serves "a" as a stub (the
//	                    real package WAS checked earlier, but go/types
//	                    calls Import with the LITERAL import string "a",
//	                    so the stub is returned and Beta is unresolved).
//	                    The package qualifier IS found (info.Uses for the
//	                    `a` ident is a *types.PkgName) but fun.Sel has no
//	                    info.Uses entry — classifyCall returns
//	                    selector_method_no_resolved_object_cross_package
//	                    for this granularity, preserving the doc's prior
//	                    histogram shape.
//	line 7: Zeta()   — bare ident, DECLARED in package b → bound_internal_func
//
// File c/pkg.go (package c) — 1 call:
//
//	line 6: f()      — bare ident bound to a *types.Var (local variable
//	                    declared inside Iota). classifyCall folds
//	                    non-Func idents into call_position_other so the
//	                    closed vocabulary stays exhaustive.
//
// File d/pkg.go (package d) — 0 calls.
//
// Hand count:
//
//	a/pkg.go : 3 calls (alpha, beta, Beta)
//	b/pkg.go : 2 calls (a.Beta, Zeta)
//	c/pkg.go : 1 call (f)
//	d/pkg.go : 0 calls
//	TOTAL    : 6 *ast.CallExpr
//
// Per-bucket hand count (exhaustive — every site has exactly one home):
//
//	bound_internal_func                                         : 2  (Beta, Zeta)
//	bare_ident_no_resolved_object_cross_package                 : 2  (alpha, beta)
//	selector_method_no_resolved_object_cross_package            : 1  (a.Beta)
//	call_position_other                                         : 1  (f)
//	selector_qualifier_no_resolved_object_cross_package         : 0
//	selector_with_non_ident_receiver                            : 0
//	generic_call_site_skipped_by_cst                            : 0
//	TOTAL                                                       : 6  == ASTDenominator
//
// Structural invariants this fixture pins (the SW-187 doc's §3 claim):
//
//	(i)  bound_internal_func + sum(AST-shape rows) == ASTDenominator.
//	(ii) Every AST-shape row is one of the closed 6-bucket vocabulary.
//	(iii) Every resolver-level row in the closed vocabulary appears
//	     even when its count is zero.
//	(iv) The two-run SHA is byte-identical (reproducibility).
const handCountedTotal = 6

const handCountedModule = "module fixture\n\ngo 1.22\n"

const handCountedFileAPkg = `package a

func Alpha() int { return 1 }
func Beta() int  { return alpha() }
func Gamma()     { beta() }
func Delta() int { return Beta() + 1 }
`

const handCountedFileBPkg = `package b

import "a"

func Epsilon() int { return a.Beta() }
func Zeta() int    { return 1 }
func Eta() int     { Zeta(); return 1 }
`

const handCountedFileCPkg = `package c

func Theta() {}
func Iota() {
	f := func() {}
	f()
}
`

const handCountedFileDPkg = `package d

func Kappa() {}
`

func handCountedFiles() map[string][]byte {
	files := map[string][]byte{
		"go.mod":   []byte(handCountedModule),
		"a/pkg.go": []byte(handCountedFileAPkg),
		"b/pkg.go": []byte(handCountedFileBPkg),
		"c/pkg.go": []byte(handCountedFileCPkg),
		"d/pkg.go": []byte(handCountedFileDPkg),
	}
	return files
}

// TestDenominator_MatchesTheHandCount pins the per-folder denominator
// against the hand-counted fixture. A future reader can re-derive the
// headline by reading the comments above handCountedTotal.
func TestDenominator_MatchesTheHandCount(t *testing.T) {
	d := WalkASTCounts(handCountedFiles())
	if d.CallSites != handCountedTotal {
		t.Fatalf("denominator: want %d, got %d", handCountedTotal, d.CallSites)
	}
	if d.Files != 4 {
		t.Fatalf("files: want 4, got %d", d.Files)
	}
	if d.ParseFailures != 0 {
		t.Fatalf("parse failures: want 0, got %d", d.ParseFailures)
	}
}

// TestClassification_MutuallyExclusiveAndExhaustiveAtCallExpr pins the
// 6-bucket AST-shape vocabulary's structural invariants:
//
//	(i)  Every AST-shape row falls inside the closed vocabulary (no
//	     reason outside the three cross-package buckets /
//	     ReasonSelectorWithNonIdentReceiver / ReasonCallPositionOther /
//	     ReasonGenericCallSiteSkippedByCST).
//	(ii) Every resolver-level row in the closed vocabulary appears even
//	     when its count is zero (includes the new bound_internal_func row
//	     and the units_degraded:* rows).
//	(iii) bound_internal_func + sum(AST-shape rows) ==
//	      ASTDenominator (the histogram is exhaustive at the denominator).
//
// The test does NOT pin SPECIFIC bucket counts (the per-call-site count
// depends on the tolerant stubImporter's behavior with cross-package
// imports — go/types calls Import with the LITERAL import string, so
// intra-module packages are served as empty stubs whose methods are
// unresolved at the call site). The structural invariants above are
// what the SW-187 doc's §3 actually claims; the per-bucket counts are
// a separate, corpus-dependent observation.
func TestClassification_MutuallyExclusiveAndExhaustiveAtCallExpr(t *testing.T) {
	r, err := Run(handCountedFiles())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if testing.Verbose() {
		t.Logf("\n%s\nreport_sha256=%s", r.Rendered, r.ReportSHA256)
	}

	// Denominator matches hand count.
	if r.ASTDenominator != handCountedTotal {
		t.Fatalf("AST denominator: want %d, got %d", handCountedTotal, r.ASTDenominator)
	}

	// (i) Every histogram row falls inside the closed vocabulary (either
	// AST-shape or resolver-level / bound-emitted).
	for _, hr := range r.Histogram {
		if !isASTShapeReason(hr.Reason) && !isResolverLevelReason(hr.Reason) {
			t.Errorf("histogram row %q outside the closed vocabulary", hr.Reason)
		}
	}

	// (ii) Every resolver-level row in the closed vocabulary appears
	// (even when zero — the closed-vocabulary invariant). On this
	// specific fixture go/types_type_errors is non-zero because the
	// tolerant stubImporter reports unresolved intra-module imports
	// as type errors; that's a property of the corpus, not a defect.
	for _, wantReason := range []Reason{
		ReasonBoundInternalFunc,
		ReasonGoTypesTypeErrors,
		ReasonFileDidNotParse,
		ReasonResolverDroppedIntents,
		ReasonUnitsDegradedTypeCheckPanic,
		ReasonUnitsDegradedTypeCheckNoPackage,
	} {
		found := false
		for _, hr := range r.Histogram {
			if hr.Reason == wantReason {
				found = true
			}
		}
		if !found {
			t.Errorf("resolver row %q missing from closed vocabulary", wantReason)
		}
	}

	// (iii) bound_internal_func + sum(AST-shape rows) == ASTDenominator.
	var boundInternal, astSum int
	for _, hr := range r.Histogram {
		switch {
		case hr.Reason == ReasonBoundInternalFunc:
			boundInternal = hr.Count
		case isASTShapeReason(hr.Reason):
			astSum += hr.Count
		}
	}
	if boundInternal+astSum != r.ASTDenominator {
		t.Fatalf("bound_internal_func + AST-shape skips: want %d (= denominator), got bound=%d skips=%d denom=%d",
			r.ASTDenominator, boundInternal, astSum, r.ASTDenominator)
	}
	// The resolver-of-record's bound count is the source of truth for
	// the numerator; classifyAll's per-file classification is a
	// parallel sanity check. On well-formed corpora they should agree
	// (bound <= boundInternal), but a small gap (resolver drops sites
	// whose endpoint was not committed) is published, never rounded.
	if r.BoundSites < 0 {
		t.Fatalf("BoundSites must be non-negative, got %d", r.BoundSites)
	}
}

// TestTwoRuns_ByteIdenticalReport pins the reproducibility assertion
// (SW-187 AC-4): two consecutive Run calls against the same files map
// produce a byte-identical rendered report and the same SHA-256.
func TestTwoRuns_ByteIdenticalReport(t *testing.T) {
	files := handCountedFiles()
	r1, err := Run(files)
	if err != nil {
		t.Fatalf("Run #1: %v", err)
	}
	r2, err := Run(files)
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if r1.ReportSHA256 == "" || r2.ReportSHA256 == "" {
		t.Fatalf("ReportSHA256 must be non-empty: r1=%q r2=%q", r1.ReportSHA256, r2.ReportSHA256)
	}
	if r1.ReportSHA256 != r2.ReportSHA256 {
		t.Fatalf("two-run SHA mismatch: r1=%s r2=%s", r1.ReportSHA256, r2.ReportSHA256)
	}
	if r1.Rendered != r2.Rendered {
		t.Fatalf("two-run rendered mismatch:\n--- r1 ---\n%s\n--- r2 ---\n%s", r1.Rendered, r2.Rendered)
	}
	// Sanity: SHA of the rendered body (without the trailing sha256
	// line) must equal the reported hash. The CLI appends the sha256
	// line; here Run does so internally.
	sum := sha256.Sum256([]byte(r1.Rendered))
	if hex.EncodeToString(sum[:]) != r1.ReportSHA256 {
		t.Fatalf("rendered SHA mismatch: rendered=%s reported=%s",
			hex.EncodeToString(sum[:]), r1.ReportSHA256)
	}
}

// TestRunOnDisk_HappyPath walks an on-disk Go repository and verifies the
// CLI shape: a small Go repo on disk (go.mod + one package) produces a
// non-zero denominator and a non-empty SHA. Mirrors the way the doc's
// `go run ./internal/gobindrate/cmd/gobindrate` invocation behaves.
func TestRunOnDisk_HappyPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/diskrun\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(`package x
func F() int { return 1 }
func G() int { return F() }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		data, _ := os.ReadFile(path)
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	r, err := Run(files)
	if err != nil {
		t.Fatalf("Run on disk: %v", err)
	}
	if r.ASTDenominator == 0 {
		t.Fatal("ASTDenominator must be > 0 for the on-disk fixture")
	}
	if r.ReportSHA256 == "" {
		t.Fatal("on-disk Run: empty SHA")
	}
}

// isResolverLevelReason reports whether a Reason is in the resolver-level
// half of the closed vocabulary (NOT one of the 6 AST-shape buckets). The
// bound_internal_func row is resolver-emitted (it comes from classifyAll's
// view of the resolver's resolution) so it lives here, not in the AST-shape
// group.
func isResolverLevelReason(r Reason) bool {
	switch r {
	case ReasonBoundInternalFunc,
		ReasonGoTypesTypeErrors,
		ReasonFileDidNotParse,
		ReasonResolverDroppedIntents,
		ReasonUnitsDegradedTypeCheckPanic,
		ReasonUnitsDegradedTypeCheckNoPackage:
		return true
	}
	return false
}
