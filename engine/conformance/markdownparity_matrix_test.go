package conformance_test

// SW-199 (W5.m, intra/parse residual, markdown slice d): the Markdown parity-class
// matrix drift guard. Binds docs/rc/parity-classes-markdown.yaml to the harness
// table markdownParityTable() in markdownparity_test.go.
//
// It ships the CLOSED 7-direction shape from the outset — MISSING, PHANTOM,
// KIND, VERDICT, OWNER, AXIS, VOCABULARY — which is the SW-189 / TEMPL-P4 D-11
// lesson applied rather than re-learned: a table that ships only
// MISSING/PHANTOM/KIND/OWNER reproduces D-11 at a new site, and
// parity_matrix_directions_test.go fails the build if any of the seven is
// missing from this file.
//
// It carries an EIGHTH check that the cross-file-heuristic families do not:
// TestMarkdownParityMatrix_Applicability. The G3 threshold this language's evidence
// row publishes (docs/rc/evidence-index.yaml, GA-LANG-markdown-G3) is the byte-parity
// table PLUS "an explicit applicability disposition per Go class — every
// cross-file change class dispositioned as `not_applicable` with a
// language-spec reason, so an inapplicable class is data the drift guard can
// see, not prose in a note". `go_class_applicability:` is that data and this is
// the guard that reads it. Its failure messages are prefixed APPLICABILITY- on
// purpose, so it can never stand in for one of the seven directions.

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/samibel/graphi/core/profile"
)

// markdownParityClassesPath is docs/rc/parity-classes-markdown.yaml relative to this
// package's directory (go test's working directory).
const markdownParityClassesPath = "../../docs/rc/parity-classes-markdown.yaml"

// markdownHarnessTestFile and markdownHarnessTestName are the single citation every
// required row must carry. Keeping them constants is what lets the VERDICT
// direction check the citation instead of trusting it.
const (
	markdownHarnessTestFile = "engine/conformance/markdownparity_test.go"
	markdownHarnessTestName = "TestMarkdownParityDeterminism_ByteStable"
)

// loadMarkdownParityClasses parses the Markdown matrix. Reuses parityRow from
// paritymatrix_test.go and yaml.Unmarshal — a test-only import.
func loadMarkdownParityClasses(t *testing.T) []parityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(markdownParityClassesPath))
	if err != nil {
		t.Fatalf("read %s: %v", markdownParityClassesPath, err)
	}
	var doc struct {
		ParityClasses []parityRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", markdownParityClassesPath, err)
	}
	if len(doc.ParityClasses) == 0 {
		t.Fatalf("%s declared no parity_classes rows", markdownParityClassesPath)
	}
	return doc.ParityClasses
}

// markdownHarnessRowIDs is the id set of the harness table, built once so both the
// MISSING/PHANTOM directions and the applicability guard read the same source.
func markdownHarnessRowIDs(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, row := range markdownParityTable() {
		if out[row.id] {
			t.Fatalf("duplicate harness row id %q", row.id)
		}
		out[row.id] = true
	}
	return out
}

// TestMarkdownParityMatrix_DriftGuard is the MISSING / PHANTOM / DEFERRED direction.
func TestMarkdownParityMatrix_DriftGuard(t *testing.T) {
	rows := loadMarkdownParityClasses(t)

	declared := map[string]parityRow{}
	for _, r := range rows {
		if _, dup := declared[r.ID]; dup {
			t.Fatalf("duplicate id %q in %s", r.ID, markdownParityClassesPath)
		}
		declared[r.ID] = r
	}
	harness := markdownHarnessRowIDs(t)

	var missing, misdeferred []string
	for _, r := range rows {
		switch r.HarnessRow {
		case harnessRequired:
			if !harness[r.ID] {
				missing = append(missing, r.ID)
			}
		case harnessDeferred:
			if harness[r.ID] {
				misdeferred = append(misdeferred, r.ID+" (has a harness row but is marked deferred)")
			}
			if r.DeferredTo == "" {
				misdeferred = append(misdeferred, r.ID+" (deferred with no deferred_to owner)")
			}
		default:
			t.Errorf("row %q has harness_row %q, want required|deferred", r.ID, r.HarnessRow)
		}
	}

	var phantom []string
	for id := range harness {
		if _, ok := declared[id]; !ok {
			phantom = append(phantom, id)
		}
	}

	sort.Strings(missing)
	sort.Strings(phantom)
	sort.Strings(misdeferred)
	if len(missing) > 0 {
		t.Errorf("MISSING — required classes with no harness row: %v", missing)
	}
	if len(phantom) > 0 {
		t.Errorf("PHANTOM — harness rows for undeclared ids: %v", phantom)
	}
	if len(misdeferred) > 0 {
		t.Errorf("DEFERRED — malformed deferred rows: %v", misdeferred)
	}
}

// markdownRequiredRowOwners is the CLOSED set of work packages permitted to own a
// harness_row: "required" row in docs/rc/parity-classes-markdown.yaml. It stays closed
// so a row cannot arrive with an empty or invented owner; adding one means
// naming it here. The suffix letter is this language's slice in SW-199's
// per-language breakdown; the prefix records the language's LEVEL (intra-file-only),
// because labelling a parse-only language as intra-file-only would misstate it.
var markdownRequiredRowOwners = map[string]bool{
	"W5-INTRA.d": true, // the six Markdown parse-determinism rows
}

func markdownRequiredRowOwnerList() []string {
	out := make([]string, 0, len(markdownRequiredRowOwners))
	for k := range markdownRequiredRowOwners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestMarkdownParityMatrix_KindCountAndOwners pins the matrix's row count by KIND
// and its owner set. The count stays a PINNED literal rather than a `>=` bound
// for the same reason every other family's does: the guard's whole job is that
// no row can be added, removed or re-kinded unnoticed, and a bound forfeits
// exactly that.
//
// 6 + 0: the six rows SW-199 added. This matrix carries no crash_condition row.
func TestMarkdownParityMatrix_KindCountAndOwners(t *testing.T) {
	rows := loadMarkdownParityClasses(t)
	var classes, crash int
	for _, r := range rows {
		switch r.Kind {
		case kindChangeClass:
			classes++
		case kindCrashCondition:
			crash++
		default:
			t.Errorf("row %q has kind %q, want %s|%s", r.ID, r.Kind, kindChangeClass, kindCrashCondition)
		}
	}
	const wantClasses, wantCrash = 6, 0
	if classes != wantClasses || crash != wantCrash {
		t.Errorf("KIND: %s has %d change_class + %d crash_condition rows; want %d + %d",
			markdownParityClassesPath, classes, crash, wantClasses, wantCrash)
	}
	for _, r := range rows {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if !markdownRequiredRowOwners[r.Owner] {
			t.Errorf("OWNER: %q is harness_row: %q so owner must be one of %v; got %q — "+
				"a required row may not arrive with an empty or invented owner",
				r.ID, harnessRequired, markdownRequiredRowOwnerList(), r.Owner)
		}
	}
}

// TestMarkdownParityMatrix_Verdict binds the matrix's verdict to what its harness row
// actually proves. Required rows claim PROVEN only when the named driver ran
// them; deferred rows claim ABSENT and may not cite the driver at all.
func TestMarkdownParityMatrix_Verdict(t *testing.T) {
	for _, r := range loadMarkdownParityClasses(t) {
		switch r.HarnessRow {
		case harnessRequired:
			if r.Verdict != verdictProven {
				t.Errorf("VERDICT: markdown %q reads verdict %q, want PROVEN", r.ID, r.Verdict)
			}
			if r.TestFile != markdownHarnessTestFile {
				t.Errorf("VERDICT: markdown %q is proven by the harness table but test_file reads %q; want %q",
					r.ID, r.TestFile, markdownHarnessTestFile)
			}
			if r.TestName != markdownHarnessTestName {
				t.Errorf("VERDICT: markdown %q test_name reads %q; want %q", r.ID, r.TestName, markdownHarnessTestName)
			}
			if r.TestLine == "" {
				t.Errorf("VERDICT: markdown %q is a required row but test_line is empty; cite the line of %s in %s",
					r.ID, markdownHarnessTestName, markdownHarnessTestFile)
			}
			if r.KnownDefect != "" {
				t.Errorf("VERDICT: markdown %q declares known_defect %q so it may NOT read verdict: %q",
					r.ID, r.KnownDefect, verdictProven)
			}
		case harnessDeferred:
			if r.Verdict != verdictAbsent {
				t.Errorf("VERDICT: markdown %q is deferred so it reads verdict %q, want ABSENT", r.ID, r.Verdict)
			}
			if r.TestFile == markdownHarnessTestFile || r.TestName == markdownHarnessTestName {
				t.Errorf("VERDICT: markdown %q is harness_row: %q yet cites the harness driver "+
					"(test_file=%q test_name=%q). A deferred row cannot claim a verdict the harness produced.",
					r.ID, harnessDeferred, r.TestFile, r.TestName)
			}
		}
	}
}

// TestMarkdownParityMatrix_Axis binds the matrix's profile/store claim to the axes the
// harness actually runs. Removing the balanced entry from parityProfiles()
// makes this test go red, by construction.
func TestMarkdownParityMatrix_Axis(t *testing.T) {
	profilesSeen := map[profile.Profile]bool{}
	for _, pr := range parityProfiles() {
		profilesSeen[pr.p] = true
	}
	if !profilesSeen[""] || !profilesSeen[profile.Balanced] {
		t.Errorf("AXIS: parityProfiles() runs {default=%v, balanced=%v}; the markdown matrix claims profile: \"both\" "+
			"on every required row, so both must run", profilesSeen[""], profilesSeen[profile.Balanced])
	}
	if n := len(parityBackends()); n != 2 {
		t.Errorf("AXIS: parityBackends() runs %d backend(s); the markdown matrix claims store: \"both\", so 2 are required", n)
	}
	for _, r := range loadMarkdownParityClasses(t) {
		if r.HarnessRow != harnessRequired {
			continue
		}
		if r.Store != "both" {
			t.Errorf("AXIS: markdown %q is harness_row: %q but reads store: %q; want \"both\"", r.ID, harnessRequired, r.Store)
		}
		if r.Profile != "both" {
			t.Errorf("AXIS: markdown %q is harness_row: %q but reads profile: %q; want \"both\"", r.ID, harnessRequired, r.Profile)
		}
	}
}

// TestMarkdownParityMatrix_Vocabulary enforces the closed vocabularies on every row.
// The shared constants live in paritymatrix_test.go and are the SAME closed set
// the Go guard enforces.
func TestMarkdownParityMatrix_Vocabulary(t *testing.T) {
	for _, r := range loadMarkdownParityClasses(t) {
		check := func(field, value string, legal []string) {
			if !oneOfString(value, legal) {
				t.Errorf("VOCABULARY: markdown %q has %s: %q, which is outside the declared vocabulary %v",
					r.ID, field, value, legal)
			}
		}
		check("kind", r.Kind, legalKinds)
		check("verdict", r.Verdict, legalVerdicts)
		check("harness_row", r.HarnessRow, legalHarnessRows)
		check("fixture", r.Fixture, legalFixtures)
		check("store", r.Store, legalStores)
		check("profile", r.Profile, legalProfiles)
		check("assertion", r.Assertion, legalAssertions)
		if r.Store == storeNone &&
			(r.Assertion == assertionSnapshotBytes || r.Assertion == assertionEnvelopeBytes) {
			t.Errorf("VOCABULARY: markdown %q claims assertion: %q with store: %q — bytes can only come from a store",
				r.ID, r.Assertion, storeNone)
		}
	}
}

// TestMarkdownParityMatrix_Applicability is the G3 threshold's second half: every Go
// change class carries an explicit, guard-readable disposition for this
// language, and every cross-file class is dispositioned not_applicable with a
// language-spec reason. See intrafile_shared_test.go for the rules enforced.
func TestMarkdownParityMatrix_Applicability(t *testing.T) {
	assertGoClassApplicability(t, "markdown", markdownParityClassesPath, loadMarkdownParityClasses(t))
}
