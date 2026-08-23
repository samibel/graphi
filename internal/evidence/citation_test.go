package evidence

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ── hermetic fixture repository ─────────────────────────────────────────────
//
// Every git-dependent rule is exercised against a repository this test builds,
// never against graphi's own HEAD. Asserting against the live tree would make the
// suite fail whenever the repo changes — which is the failure mode this story
// exists to fix, re-created inside the test suite.

type fixtureRepo struct {
	t    *testing.T
	root string
}

func newFixtureRepo(t *testing.T) *fixtureRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := &fixtureRepo{t: t, root: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "gate@example.invalid")
	r.git("config", "user.name", "gate")
	return r
}

func (r *fixtureRepo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *fixtureRepo) write(rel, body string) {
	r.t.Helper()
	p := filepath.Join(r.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fixtureRepo) commit(msg string) {
	r.t.Helper()
	r.git("add", "-A")
	r.git("commit", "-q", "-m", msg)
}

func (r *fixtureRepo) blobSHA(rel string) string {
	r.t.Helper()
	return r.git("rev-parse", "HEAD:"+rel)
}

// check runs the whole citation sweep over the fixture repo.
func (r *fixtureRepo) check(idx Index, gf Grandfather) CitationReport {
	r.t.Helper()
	rep, err := CheckCitations(r.root, idx, gf)
	if err != nil {
		r.t.Fatalf("CheckCitations: %v", err)
	}
	return rep
}

func gate(id, uri, sha string) Gate {
	return Gate{ID: id, Gate: id, Section: "plan §1", Status: StatusPass, EvidenceURI: uri, SHA: sha}
}

func hasViolation(rep CitationReport, rule CitationRule, targetContains string) bool {
	for _, v := range rep.Violations {
		if v.Rule == rule && strings.Contains(v.Target, targetContains) {
			return true
		}
	}
	return false
}

func violationDetail(rep CitationReport, rule CitationRule) string {
	for _, v := range rep.Violations {
		if v.Rule == rule {
			return v.Detail
		}
	}
	return ""
}

// ── AC-1: a recorded sha must be the sha of the thing it cites ──────────────

// The literal reproduction of failure mode 1: a blob sha is recorded, the file is
// then reformatted and re-committed, and the row keeps the old sha. Presence
// checking cannot see this; the sha comparison must.
func TestAC1_StaleBlobSHAFails(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/conformance/xparity_matrix_test.go", "package conformance\n\nfunc TestX(t *testing.T) {}\n")
	r.commit("add twin")
	stale := r.blobSHA("engine/conformance/xparity_matrix_test.go")

	r.write("engine/conformance/xparity_matrix_test.go", "package conformance\n\nfunc TestX(t *testing.T) {\n}\n")
	r.commit("gofmt drift")

	idx := Index{Gates: []Gate{gate("G3", "engine/conformance/xparity_matrix_test.go", stale)}}
	rep := r.check(idx, Grandfather{})
	if rep.Pass() {
		t.Fatalf("expected FAIL: the recorded sha is a blob that is no longer the cited file's blob\n%s", rep.FormatCitations())
	}
	if !hasViolation(rep, RuleSHAMismatch, "xparity_matrix_test.go") {
		t.Fatalf("want a sha-mismatch violation, got:\n%s", rep.FormatCitations())
	}
	d := violationDetail(rep, RuleSHAMismatch)
	if !strings.Contains(d, stale) || !strings.Contains(d, r.blobSHA("engine/conformance/xparity_matrix_test.go")) {
		t.Fatalf("violation must name BOTH the recorded and the actual sha, got: %s", d)
	}
}

// AC-1: a matching sha passes, and an abbreviated prefix of it passes too.
func TestAC1_MatchingAndAbbreviatedSHAPass(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")
	full := r.blobSHA("engine/x.go")

	for _, sha := range []string{full, full[:8]} {
		rep := r.check(Index{Gates: []Gate{gate("G", "engine/x.go", sha)}}, Grandfather{})
		if !rep.Pass() {
			t.Fatalf("sha %q must pass:\n%s", sha, rep.FormatCitations())
		}
	}
}

// AC-1: a prefix that disagrees in the characters it does record must NOT pass.
func TestAC1_WrongPrefixFails(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")
	full := r.blobSHA("engine/x.go")
	wrong := "f" + full[1:8]
	if wrong == full[:8] {
		wrong = "0" + full[1:8]
	}
	rep := r.check(Index{Gates: []Gate{gate("G", "engine/x.go", wrong)}}, Grandfather{})
	if rep.Pass() {
		t.Fatalf("a prefix that disagrees must fail:\n%s", rep.FormatCitations())
	}
}

// ── AC-2: a cited path must exist, and that is its OWN violation text ───────

func TestAC2_MissingPathIsItsOwnViolation(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")
	sha := r.blobSHA("engine/x.go")

	rep := r.check(Index{Gates: []Gate{gate("G", "docs/rc/parity-matrix-adr0013-run-a.json", sha)}}, Grandfather{})
	if rep.Pass() {
		t.Fatal("expected FAIL: the cited path does not exist at HEAD")
	}
	if !hasViolation(rep, RuleMissingPath, "parity-matrix-adr0013-run-a.json") {
		t.Fatalf("want missing-path, got:\n%s", rep.FormatCitations())
	}
	if hasViolation(rep, RuleSHAMismatch, "") {
		t.Fatalf("\"the file moved\" must not be reported as \"the file changed\":\n%s", rep.FormatCitations())
	}
}

// ── AC-3: worktree drift is disclosed, not absorbed ─────────────────────────

func TestAC3_WorktreeDriftIsADistinctViolation(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")
	sha := r.blobSHA("engine/x.go")
	r.write("engine/x.go", "package engine\n\n// uncommitted\n")

	rep := r.check(Index{Gates: []Gate{gate("G", "engine/x.go", sha)}}, Grandfather{})
	if rep.Pass() {
		t.Fatal("expected FAIL: a row may not claim a sha for bytes that are not committed")
	}
	if !hasViolation(rep, RuleWorktreeDrift, "engine/x.go") {
		t.Fatalf("want worktree-drift, got:\n%s", rep.FormatCitations())
	}
}

// ── AC-4: classification is declared, explicit, and never silent ────────────

func TestAC4_ClassifierRuleSet(t *testing.T) {
	cases := []struct {
		uri  string
		want []CitationKind
	}{
		{"engine/x.go", []CitationKind{KindRepoPath}},
		{"engine/a.go + engine/b.go", []CitationKind{KindRepoPath, KindRepoPath}},
		{"docs/rc/m.md (19/19 section)", []CitationKind{KindRepoPath}},
		{"docs/rc/m.md#some-anchor", []CitationKind{KindDocAnchor}},
		{"engine/x_test.go::TestThing", []CitationKind{KindTestSymbol}},
		{"https://github.com/samibel/graphi/actions/runs/1", []CitationKind{KindExternal}},
		{"corpus/fixtures/hero-<lang>", []CitationKind{KindTemplate}},
		{"corpus/hero/*.yaml", []CitationKind{KindTemplate}},
		{"cmd/coverage -check", []CitationKind{KindCommand}},
		{"docs/rc/parity-classes{,-jvm}.yaml", []CitationKind{KindRepoPath, KindRepoPath}},
		{"matching test files", []CitationKind{KindProse}},
		{"vendor/other/thing.go", []CitationKind{KindUnclassified}},
	}
	for _, c := range cases {
		got := ClassifyURI(c.uri)
		if len(got) != len(c.want) {
			t.Errorf("%q: got %d citations, want %d (%v)", c.uri, len(got), len(c.want), got)
			continue
		}
		for i := range got {
			if got[i].Kind != c.want[i] {
				t.Errorf("%q[%d]: got kind %q, want %q", c.uri, i, got[i].Kind, c.want[i])
			}
		}
	}
}

// AC-4: an unclassifiable URI is a violation, not a pass.
func TestAC4_UnclassifiableURIIsAViolation(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")
	rep := r.check(Index{Gates: []Gate{gate("G", "vendor/somewhere/else.go", "")}}, Grandfather{})
	if !hasViolation(rep, RuleUnclassifiedURI, "vendor/somewhere/else.go") {
		t.Fatalf("want unclassified-uri, got:\n%s", rep.FormatCitations())
	}
}

// AC-4: a classified-but-unresolvable URI is reported, and NOT counted as verified.
func TestAC4_TemplateIsReportedNotVerified(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")
	rep := r.check(Index{Gates: []Gate{gate("G6", "corpus/fixtures/hero-<lang>", "deadbeef")}}, Grandfather{})
	if !rep.Pass() {
		t.Fatalf("a declared template classification is not itself a violation:\n%s", rep.FormatCitations())
	}
	if rep.Census[KindTemplate] != 1 {
		t.Fatalf("the template must be counted in the census, got %v", rep.Census)
	}
	if len(rep.UnbackedPASS) != 1 || rep.UnbackedPASS[0] != "G6" {
		t.Fatalf("a PASS row citing only a template must be named in the report, got %v", rep.UnbackedPASS)
	}
	if !strings.Contains(rep.FormatCitations(), "NOT counted as verified") {
		t.Fatalf("the skip must be visible in the report:\n%s", rep.FormatCitations())
	}
}

// ── AC-5: a cited test symbol must resolve to a real declaration ────────────

func TestAC5_SymbolResolution(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("internal/jvmgroundtruth/signature_test.go", "package jvmgroundtruth\n\nimport \"testing\"\n\nfunc TestSomethingElse(t *testing.T) {}\n")
	r.commit("add")

	// mode 4: the file exists, the symbol never did.
	rep := r.check(Index{Gates: []Gate{gate("G", "internal/jvmgroundtruth/signature_test.go::TestKotlinValueClassMangledName_JVMHARN001", "")}}, Grandfather{})
	if !hasViolation(rep, RuleSymbolUnresolved, "TestKotlinValueClassMangledName_JVMHARN001") {
		t.Fatalf("a symbol that was never written must fail:\n%s", rep.FormatCitations())
	}

	// the symbol's file does not exist at all.
	rep = r.check(Index{Gates: []Gate{gate("G", "internal/gone/absent_test.go::TestX", "")}}, Grandfather{})
	if !hasViolation(rep, RuleMissingPath, "internal/gone/absent_test.go::TestX") {
		t.Fatalf("a symbol whose file does not exist must fail:\n%s", rep.FormatCitations())
	}

	// a symbol that IS declared resolves.
	rep = r.check(Index{Gates: []Gate{gate("G", "internal/jvmgroundtruth/signature_test.go::TestSomethingElse", "")}}, Grandfather{})
	if !rep.Pass() {
		t.Fatalf("a declared symbol must resolve:\n%s", rep.FormatCitations())
	}
}

// AC-5: resolution is over the COMMITTED bytes — an uncommitted test does not
// satisfy a citation.
func TestAC5_UncommittedSymbolDoesNotSatisfy(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("internal/x/x_test.go", "package x\n")
	r.commit("add")
	r.write("internal/x/x_test.go", "package x\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) {}\n")

	rep := r.check(Index{Gates: []Gate{gate("G", "internal/x/x_test.go::TestNew", "")}}, Grandfather{})
	if !hasViolation(rep, RuleSymbolUnresolved, "TestNew") {
		t.Fatalf("an uncommitted symbol must not satisfy a citation:\n%s", rep.FormatCitations())
	}
}

func TestDeclaredSymbols_CoversEveryTopLevelShape(t *testing.T) {
	src := []byte("package p\n\nimport \"testing\"\n\ntype T struct{}\n\nconst C = 1\n\nvar V = 2\n\nfunc F() {}\n\nfunc (T) M() {}\n\nfunc TestX(t *testing.T) {}\n")
	got, err := DeclaredSymbols("p.go", src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"T", "C", "V", "F", "M", "TestX"} {
		if !got[want] {
			t.Errorf("DeclaredSymbols missed %q", want)
		}
	}
	if got["Nope"] {
		t.Error("DeclaredSymbols invented a symbol")
	}
}

// ── AC-6: the same rule covers the prose records ────────────────────────────

func TestAC6_GovernedDocSweep(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/real.go", "package engine\n")
	r.write("docs/decisions/2026-08-record.md", strings.Join([]string{
		"# A record",
		"",
		"## Body",
		"",
		"The two-dispatch re-measure is recorded in",
		"`docs/rc/parity-matrix-adr0013-run-{a,b}.json` (PUBLISHED in this commit).",
		"It also cites `engine/real.go`, which does exist.",
		"",
	}, "\n"))
	r.commit("add")

	rep := r.check(Index{}, Grandfather{})
	if rep.Pass() {
		t.Fatalf("expected FAIL: the record cites two files that were never published\n%s", rep.FormatCitations())
	}
	for _, want := range []string{"parity-matrix-adr0013-run-a.json", "parity-matrix-adr0013-run-b.json"} {
		if !hasViolation(rep, RuleMissingPath, want) {
			t.Fatalf("brace-expanded citation %s must be checked, got:\n%s", want, rep.FormatCitations())
		}
	}
	if rep.Verified == 0 {
		t.Fatal("the citation that DOES resolve must be counted as verified")
	}
}

func TestAC6_TestSymbolCitationInProse(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("internal/jvmgroundtruth/signature_test.go", "package jvmgroundtruth\n")
	r.write("docs/adr/0013-closure.md", "# ADR\n\n## Body\n\nPinned by `internal/jvmgroundtruth/signature_test.go::TestKotlinValueClassMangledName_JVMHARN001`.\n")
	r.commit("add")

	rep := r.check(Index{}, Grandfather{})
	if !hasViolation(rep, RuleSymbolUnresolved, "TestKotlinValueClassMangledName_JVMHARN001") {
		t.Fatalf("AC-5 must apply inside governed prose too:\n%s", rep.FormatCitations())
	}
}

// AC-6: the governed set is declared, not inferred — a record outside it is not swept.
func TestAC6_UngovernedDocIsNotSwept(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.write("docs/notes/scratch.md", "# Scratch\n\nCites `docs/rc/never-existed.json`.\n")
	r.commit("add")

	rep := r.check(Index{}, Grandfather{})
	if !rep.Pass() {
		t.Fatalf("only the declared governed set is swept:\n%s", rep.FormatCitations())
	}
}

// ── AC-7: a superseded section is exempt, and the exemption is counted ──────

func TestAC7_StaleSectionIsExemptAndCounted(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.write("docs/rc/matrix.md", strings.Join([]string{
		"# Matrix",
		"",
		"## Live section",
		"",
		"Cites `docs/rc/live-missing.json`.",
		"",
		"## Superseded measurement — the ADR 0009 candidate",
		"",
		"Cites `docs/rc/old-missing.json`.",
		"",
		"### A subsection of the superseded one",
		"",
		"Cites `docs/rc/older-missing.json`.",
		"",
		"## 9. Change-control and the STALE rule",
		"",
		"Cites `docs/rc/also-live-missing.json`.",
		"",
	}, "\n"))
	r.commit("add")

	rep := r.check(Index{}, Grandfather{})
	if !hasViolation(rep, RuleMissingPath, "live-missing.json") {
		t.Fatalf("a live section is not exempt:\n%s", rep.FormatCitations())
	}
	if hasViolation(rep, RuleMissingPath, "old-missing.json") || hasViolation(rep, RuleMissingPath, "older-missing.json") {
		t.Fatalf("a superseded section and its subsections are exempt:\n%s", rep.FormatCitations())
	}
	// The marker is position-anchored: "the STALE rule" is not a STALE section.
	if !hasViolation(rep, RuleMissingPath, "also-live-missing.json") {
		t.Fatalf("the marker must be matched structurally, not by containment:\n%s", rep.FormatCitations())
	}
	if rep.Exempt != 2 {
		t.Fatalf("the exempt count must be reported: got %d, want 2", rep.Exempt)
	}
	if !strings.Contains(rep.FormatCitations(), "exempted by a declared") {
		t.Fatalf("the exemption must be visible in the report:\n%s", rep.FormatCitations())
	}
}

// AC-7: a CORRECTION banner opening a record declares the whole body below it as
// published — that is what "corrections go on top, the record is never rewritten"
// means structurally. A correction that appears LATER is scoped to its own section.
func TestAC7_CorrectionBannerScope(t *testing.T) {
	r := newFixtureRepo(t)
	head := strings.Join([]string{
		"# A record",
		"",
		"## CORRECTION — 2026-08-23 (SW-205): two statements were not true",
		"",
		"`docs/rc/never-a.json` was never published.",
		"",
		"## The body as published",
		"",
		"Recorded in `docs/rc/never-b.json` (PUBLISHED in this commit).",
		"",
	}, "\n")
	late := strings.Join([]string{
		"# A record",
		"",
		"## The body as published",
		"",
		"Recorded in `docs/rc/never-c.json` (PUBLISHED in this commit).",
		"",
		"## CORRECTION — 2026-08-23: that was not true",
		"",
		"`docs/rc/never-d.json` was never published.",
		"",
	}, "\n")
	r.write("docs/decisions/head.md", head)
	r.write("docs/decisions/late.md", late)
	r.commit("add")

	rep := r.check(Index{}, Grandfather{})
	for _, exempt := range []string{"never-a.json", "never-b.json", "never-d.json"} {
		if hasViolation(rep, RuleMissingPath, exempt) {
			t.Fatalf("%s must be exempt:\n%s", exempt, rep.FormatCitations())
		}
	}
	if !hasViolation(rep, RuleMissingPath, "never-c.json") {
		t.Fatalf("a live section ABOVE a late correction is not exempt:\n%s", rep.FormatCitations())
	}
}

// ── AC-11: the grandfather list is a ratchet that can only shrink ───────────

func TestAC11_EntrySuppressesExactlyItsOwnViolation(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("docs/rc/rec.md", "# R\n\n## B\n\nCites `docs/rc/gone-a.json` and `docs/rc/gone-b.json`.\n")
	r.commit("add")

	gf := Grandfather{Entries: []GrandfatherEntry{{
		Target: "docs/rc/rec.md :: missing-path :: docs/rc/gone-a.json",
		Reason: "owned by an in-flight story",
		Owner:  "SW-193",
		Line:   2,
	}}}
	rep := r.check(Index{}, gf)
	if hasViolation(rep, RuleMissingPath, "gone-a.json") {
		t.Fatalf("the grandfathered key must be suppressed:\n%s", rep.FormatCitations())
	}
	if !hasViolation(rep, RuleMissingPath, "gone-b.json") {
		t.Fatalf("a NON-grandfathered violation must still stand:\n%s", rep.FormatCitations())
	}
	if rep.GrandfatherOK != 1 {
		t.Fatalf("the used-entry count must be reported, got %d", rep.GrandfatherOK)
	}
}

func TestAC11_UnusedEntryFailsAndSaysDelete(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("engine/x.go", "package engine\n")
	r.commit("add")

	gf := Grandfather{Entries: []GrandfatherEntry{{
		Target: "docs/rc/rec.md :: missing-path :: docs/rc/already-fixed.json",
		Reason: "drained",
		Owner:  "SW-193",
		Line:   2,
	}}}
	rep := r.check(Index{}, gf)
	if rep.Pass() {
		t.Fatal("an entry whose target now passes must FAIL the check — the list is a ratchet")
	}
	if !strings.Contains(violationDetail(rep, RuleGrandfatherUnused), "DELETE THIS ENTRY") {
		t.Fatalf("the failure must say to delete the entry, got: %q", violationDetail(rep, RuleGrandfatherUnused))
	}
}

func TestAC11_EntryWithoutOwnerIsAViolation(t *testing.T) {
	r := newFixtureRepo(t)
	r.write("docs/rc/rec.md", "# R\n\n## B\n\nCites `docs/rc/gone-a.json`.\n")
	r.commit("add")

	gf := Grandfather{Entries: []GrandfatherEntry{{
		Target: "docs/rc/rec.md :: missing-path :: docs/rc/gone-a.json",
		Reason: "because",
		Owner:  "",
		Line:   2,
	}}}
	rep := r.check(Index{}, gf)
	if !hasViolation(rep, RuleGrandfatherNoOwner, "gone-a.json") {
		t.Fatalf("an entry with no owner story must be a violation:\n%s", rep.FormatCitations())
	}
}

func TestAC11_ParseAndValidate(t *testing.T) {
	gf, err := parseGrandfather("entries:\n  - target: \"A :: missing-path :: b\"\n    reason: \"r\"\n    owner: SW-193\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(gf.Entries) != 1 || gf.Entries[0].Owner != "SW-193" {
		t.Fatalf("parsed wrong: %+v", gf.Entries)
	}
	if vs := gf.Validate(); len(vs) != 0 {
		t.Fatalf("a well-formed entry must validate, got %+v", vs)
	}
	bad, err := parseGrandfather("entries:\n  - target: \"\"\n    reason: \"\"\n    owner: nope\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(bad.Validate()) == 0 {
		t.Fatal("a blank target and a non-story owner must both be violations")
	}
}

// ── AC-9 / AC-10 are wired in cmd/evidence; the package-level contract they
// rest on is that CheckCitations is the single rule set both call. ───────────

func TestReportKeyIsStableAcrossLineDrift(t *testing.T) {
	a := CitationViolation{Scope: "docs/rc/x.md:12", Rule: RuleMissingPath, Target: "docs/rc/y.json"}
	b := CitationViolation{Scope: "docs/rc/x.md:900", Rule: RuleMissingPath, Target: "docs/rc/y.json"}
	if a.Key() != b.Key() {
		t.Fatalf("a grandfather key must not drift with the line number: %q vs %q", a.Key(), b.Key())
	}
	if strings.Contains(a.Key(), ":12") {
		t.Fatalf("key must not carry a line number: %q", a.Key())
	}
}
