package coverage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// budgetFixture builds a synthetic readme with `total` source lines and a spine
// of `spine` lines. The spine ends at the SECOND `## ` heading, which is the
// structural boundary readmeSpine uses.
func budgetFixture(spine, total int) string {
	if spine < 2 || total < spine+2 {
		panic("budgetFixture: spine must be >= 2 and total >= spine+2")
	}
	lines := make([]string, 0, total)
	lines = append(lines, "# graphi", "")
	for len(lines) < spine-1 {
		lines = append(lines, "spine prose")
	}
	lines = append(lines, "## Quick start") // first H2, still inside the spine
	if len(lines) != spine {
		panic("budgetFixture: spine arithmetic")
	}
	lines = append(lines, "")        // trimmed by readmeSpine
	lines = append(lines, "## Body") // second H2 — ends the spine
	for len(lines) < total {
		lines = append(lines, "body prose")
	}
	return strings.Join(lines, "\n") + "\n"
}

// TestReadmeBudgetFixtureIsHonest proves the fixture builder measures what it
// claims before any assertion is drawn from it — a fixture that miscounts would
// make every kill test below vacuous.
func TestReadmeBudgetFixtureIsHonest(t *testing.T) {
	src := budgetFixture(55, 400)
	if got := strings.Count(src, "\n"); got != 400 {
		t.Fatalf("fixture has %d newlines (`wc -l` semantics), want 400", got)
	}
	rep, err := CheckReadmeBudget(src)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if rep.Lines != 400 || rep.Spine != 55 {
		t.Fatalf("measured lines=%d spine=%d, want 400/55", rep.Lines, rep.Spine)
	}
	if rep.SpineBoundary != "Body" {
		t.Fatalf("spine boundary %q, want %q", rep.SpineBoundary, "Body")
	}
}

// TestReadmeBudgetCeilingBites is AC-8 for assertion (1): the ceiling must go
// RED one line over and GREEN on the line back. A gate nobody has seen fail is
// not known to work.
func TestReadmeBudgetCeilingBites(t *testing.T) {
	at := budgetFixture(55, ReadmeCeiling)
	rep, err := CheckReadmeBudget(at)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if !rep.Pass() {
		t.Fatalf("exactly at the ceiling must PASS, got:\n%s", rep.Format())
	}

	over := budgetFixture(55, ReadmeCeiling+1)
	rep, err = CheckReadmeBudget(over)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if rep.Pass() {
		t.Fatal("one line OVER the ceiling passed — the ceiling does not bite")
	}
	if !strings.Contains(rep.Format(), "readme-budget check FAILED") {
		t.Errorf("FAILED banner missing:\n%s", rep.Format())
	}
	want := "is 401 source lines; the ceiling is 400 (1 over)"
	if !strings.Contains(rep.Format(), want) {
		t.Errorf("output does not name the breach %q:\n%s", want, rep.Format())
	}

	// Revert: back at the ceiling it is green again, so the red above was the
	// mutation and not some standing failure.
	rep, err = CheckReadmeBudget(at)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if !rep.Pass() {
		t.Fatalf("reverted fixture is red — the kill test proved nothing:\n%s", rep.Format())
	}
}

// TestReadmeBudgetSpineBites is AC-8 for assertion (2). A spine line is added
// WITHOUT changing the total, so only the spine assertion can fire — a mutation
// that broke both would not tell the two assertions apart.
func TestReadmeBudgetSpineBites(t *testing.T) {
	at := budgetFixture(ReadmeSpineBudget, 300)
	rep, err := CheckReadmeBudget(at)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if !rep.Pass() {
		t.Fatalf("spine exactly at budget must PASS, got:\n%s", rep.Format())
	}

	over := budgetFixture(ReadmeSpineBudget+1, 300)
	rep, err = CheckReadmeBudget(over)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if rep.Pass() {
		t.Fatal("a 56-line spine passed — the spine assertion does not bite")
	}
	if rep.Lines != 300 {
		t.Fatalf("the mutation changed the total to %d; it must isolate the spine", rep.Lines)
	}
	if len(rep.Violations) != 1 {
		t.Fatalf("want exactly the spine violation, got %v", rep.Violations)
	}
	if !strings.Contains(rep.Violations[0], "SW-212 AC-7's budget is 55 (1 over)") {
		t.Errorf("violation does not name the spine budget: %q", rep.Violations[0])
	}

	rep, err = CheckReadmeBudget(at)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if !rep.Pass() {
		t.Fatalf("reverted fixture is red — the kill test proved nothing:\n%s", rep.Format())
	}
}

// TestReadmeBudgetFailsClosed: every document this cannot delimit is an error,
// never a quiet pass. A size gate that skips is worse than no size gate.
func TestReadmeBudgetFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"empty", "", "is empty"},
		{"no trailing newline", "# graphi\n## a\n\n## b\nbody", "does not end in a newline"},
		{"no H2 at all", "# graphi\n\nbody\n", "has 0 `## ` headings"},
		{"only one H2", "# graphi\n\n## Quick start\n\nbody\n", "has 1 `## ` headings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CheckReadmeBudget(tc.src)
			if err == nil {
				t.Fatal("want an error, got a pass — the gate did not fail closed")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

// TestReadmeBudgetIgnoresDeeperHeadings pins the boundary to `## ` exactly: an
// H3 inside the spine must not end it, or the gate would move under an ordinary
// edit that adds a subheading.
func TestReadmeBudgetIgnoresDeeperHeadings(t *testing.T) {
	src := "# graphi\n\n### sub\n\n## Quick start\n\ntext\n\n## Body\n\ntail\n"
	rep, err := CheckReadmeBudget(src)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	// Spine = lines 1..7 ("tail" excluded), trailing blank at :8 trimmed.
	if rep.Spine != 7 {
		t.Errorf("spine = %d, want 7 (the H3 must not end the spine)", rep.Spine)
	}
	if rep.SpineBoundary != "Body" {
		t.Errorf("spine boundary %q, want %q", rep.SpineBoundary, "Body")
	}
}

// TestReadmeBudgetLiveDocument binds the CHECKED-IN readme.md to both budgets,
// so a breach fails `go test` as well as `go run ./cmd/coverage -check`, and
// then KILL-TESTS both assertions against the real document: one appended line
// reddens the ceiling, one line inserted into the spine reddens the spine, and
// the unmutated bytes are green on both sides of each mutation.
func TestReadmeBudgetLiveDocument(t *testing.T) {
	root, err := ModuleRoot()
	if err != nil {
		t.Fatalf("module root: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ReadmePath)))
	if err != nil {
		t.Fatalf("read readme: %v", err)
	}
	src := string(b)

	rep, err := CheckReadmeBudget(src)
	if err != nil {
		t.Fatalf("CheckReadmeBudget: %v", err)
	}
	if !rep.Pass() {
		t.Fatalf("checked-in %s is over budget:\n%s", ReadmePath, rep.Format())
	}
	t.Logf("live %s: %d/%d source lines, spine %d/%d (boundary %q)",
		ReadmePath, rep.Lines, ReadmeCeiling, rep.Spine, ReadmeSpineBudget, rep.SpineBoundary)

	// Ceiling kill test, on the live document.
	mutated := src + "one line too many\n"
	mrep, err := CheckReadmeBudget(mutated)
	if err != nil {
		t.Fatalf("CheckReadmeBudget(mutated): %v", err)
	}
	if mrep.Pass() {
		t.Errorf("appending a line to the live %s did not redden the ceiling", ReadmePath)
	}
	if mrep.Lines != rep.Lines+1 {
		t.Errorf("mutation moved the count to %d, want %d", mrep.Lines, rep.Lines+1)
	}

	// Spine kill test, on the live document: insert a line at the top, which
	// grows the spine and the total together, then assert the SPINE violation
	// is present by name.
	spineMutated := "an extra spine line\n" + src
	srep, err := CheckReadmeBudget(spineMutated)
	if err != nil {
		t.Fatalf("CheckReadmeBudget(spine-mutated): %v", err)
	}
	if srep.Spine != rep.Spine+1 {
		t.Fatalf("spine moved to %d, want %d", srep.Spine, rep.Spine+1)
	}
	if srep.Pass() {
		t.Errorf("a %d-line spine passed on the live document", srep.Spine)
	}
	var sawSpine bool
	for _, v := range srep.Violations {
		if strings.Contains(v, "SW-212 AC-7's budget") {
			sawSpine = true
		}
	}
	if !sawSpine {
		t.Errorf("spine violation missing from %v", srep.Violations)
	}

	// Revert: the unmutated bytes are green again.
	rep2, err := CheckReadmeBudget(src)
	if err != nil {
		t.Fatalf("CheckReadmeBudget(reverted): %v", err)
	}
	if !rep2.Pass() {
		t.Fatalf("reverted %s is red — the kill tests proved nothing:\n%s", ReadmePath, rep2.Format())
	}
}
