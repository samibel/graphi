package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// parityClassesPath is docs/rc/parity-classes.yaml relative to this package's
// directory, which is `go test`'s working directory.
const parityClassesPath = "../../docs/rc/parity-classes.yaml"

// WHY yaml.Unmarshal AND NOT A HAND-ROLLED PARSER — recorded here so nobody
// "corrects" it back later.
//
// internal/coverage/matrix.go:63-64 states, for docs/coverage-matrix.yaml, that
// it "intentionally does NOT pull in a general YAML dependency (graphi stays lean
// and CGo-free)". That rationale is about the SHIPPED BINARY and it does not
// reach this file: this guard lives in an external _test package, so the import
// is a test-only import. It cannot be linked into cmd/graphi and cannot fatten
// the shipped artifact.
//
// The precise dependency facts, because two earlier statements of them in
// SW-156 were imprecise and were corrected in review:
//
//   - `go list -deps ./cmd/graphi | grep yaml` returns NOTHING. gopkg.in/yaml.v3
//     does not reach the product binary. The claim "already in the product tree"
//     is therefore too loose to justify anything on its own.
//   - yaml.v3 DOES reach cmd/eval, via engine/scenario/scenario.go:26, which
//     imports it unconditionally and calls yaml.Unmarshal at :259 on the checked-in
//     corpus/hero/*.yaml scenarios. So the dependency is real, first-party, and
//     already used for exactly this job: parsing a checked-in source-of-truth YAML.
//   - go.mod:47's `// indirect` marker on yaml.v3 is stale for that reason. It is
//     not evidence either way.
//
// Conclusion: a hand-rolled parser here would buy nothing and would add a parser
// to the review surface. docs/rc/parity-classes.yaml is nevertheless kept inside
// the same flat, scalar-only subset internal/coverage/matrix.go:90 parseMatrixYAML
// accepts, so moving this guard into a cmd/coverage-style binary stays possible
// without reshaping the file.

// parityRow mirrors one row of docs/rc/parity-classes.yaml. Field names track
// the YAML keys exactly; the file's header block documents each one.
type parityRow struct {
	ID          string `yaml:"id"`
	Kind        string `yaml:"kind"`
	Label       string `yaml:"label"`
	PRDSource   string `yaml:"prd_source"`
	DeltaSource string `yaml:"delta_source"`
	Verdict     string `yaml:"verdict"`
	TestFile    string `yaml:"test_file"`
	TestLine    string `yaml:"test_line"`
	TestName    string `yaml:"test_name"`
	Fixture     string `yaml:"fixture"`
	Store       string `yaml:"store"`
	Assertion   string `yaml:"assertion"`
	HarnessRow  string `yaml:"harness_row"`
	KnownDefect string `yaml:"known_defect"`
	DeferredTo  string `yaml:"deferred_to"`
	Owner       string `yaml:"owner"`
	Note        string `yaml:"note"`
}

// Vocabulary constants for the VOCABULARY guard direction. Every closed-set
// field's legal values live here and nowhere else.
const (
	kindChangeClass    = "change_class"
	kindCrashCondition = "crash_condition"

	verdictProven  = "PROVEN"
	verdictPartial = "PARTIAL"
	verdictAbsent  = "ABSENT"

	harnessRequired = "required"
	harnessDeferred = "deferred"

	storeNone = "none"

	assertionSnapshotBytes = "snapshot bytes"
	assertionEnvelopeBytes = "envelope bytes"
)

var (
	legalKinds       = []string{kindChangeClass, kindCrashCondition}
	legalVerdicts    = []string{verdictProven, verdictPartial, verdictAbsent}
	legalFixtures    = []string{"synthetic stub parser", "production Go parser", "real pinned repository"}
	legalStores      = []string{"MemStore", "SQLite", "both", storeNone}
	legalAssertions  = []string{assertionSnapshotBytes, assertionEnvelopeBytes, "spot query"}
	legalHarnessRows = []string{harnessRequired, harnessDeferred}

	// requiredRowOwners is the CLOSED set of stories permitted to own a
	// harness_row: "required" class. SW-157 built the harness and owns the FR-7
	// rows; every later entry names the work package that ADDED a required row,
	// because attributing someone else's row to SW-157 would be false
	// provenance. Keeping it closed (rather than "any non-empty string") is what
	// still stops a row arriving with an empty or invented owner.
	requiredRowOwners = map[string]bool{
		"SW-157": true, // the harness and FR-7's 15 change classes
		"W0.f":   true, // change_colliding_package_dir — the PARITY-002 reproduction
	}
)

// requiredRowOwnerList renders requiredRowOwners deterministically for failure
// messages.
func requiredRowOwnerList() []string {
	out := make([]string, 0, len(requiredRowOwners))
	for k := range requiredRowOwners {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadParityClasses parses docs/rc/parity-classes.yaml, the machine-readable
// source of truth SW-156 landed. It is deliberately the ONLY place this harness
// learns which change classes exist: the class list is not duplicated in Go, so
// a class added to the YAML cannot be quietly absent here (the MISSING
// direction), and a harness row for a class nobody declared cannot be quietly
// present (the PHANTOM direction).
//
// Note the boundary this respects: internal/evalreport.RequiredChangeClasses
// feeds cmd/eval/changeseq.go:36-39 changeSequenceCycle, so adding a class there
// would reshape the freshness/update instrument. This harness keeps its own
// binding, to this file, and never touches internal/evalreport.
func loadParityClasses(t *testing.T) []parityRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.FromSlash(parityClassesPath))
	if err != nil {
		t.Fatalf("read %s: %v", parityClassesPath, err)
	}
	var doc struct {
		ParityClasses []parityRow `yaml:"parity_classes"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", parityClassesPath, err)
	}
	if len(doc.ParityClasses) == 0 {
		t.Fatalf("%s declared no parity_classes rows", parityClassesPath)
	}
	return doc.ParityClasses
}

// changeClassIDs returns the declared ids whose kind is change_class, in file
// order. Crash conditions are excluded BY CONSTRUCTION — see the KIND direction.
func changeClassIDs(rows []parityRow) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.Kind == kindChangeClass {
			out = append(out, r.ID)
		}
	}
	return out
}

// TestParityMatrix_DriftGuard is the AC-2 drift guard. It binds the declarative
// change-class table in this package to docs/rc/parity-classes.yaml in SIX
// directions. Five are the contract SW-156 wrote into the YAML header; the sixth
// (VOCABULARY) was added by SW-157 and the reason is recorded on it.
//
//	MISSING     every row with harness_row: "required" has a table row with the
//	            same id. A required class with no table row FAILS.
//	PHANTOM     every table row's id appears in the YAML. A table row for an id
//	            nobody declared FAILS.
//	KIND        a row with kind: "crash_condition" is not a change class, and the
//	            harness may not count one among its change classes.
//	VERDICT     a required row whose table row exists and passes may not read
//	            verdict: "ABSENT".
//	OWNER       harness_row: "required" implies owner "SW-157";
//	            harness_row: "deferred" implies owner == deferred_to.
//	VOCABULARY  every closed-set field holds a declared value, and store: "none"
//	            is illegal on any row claiming a byte assertion.
func TestParityMatrix_DriftGuard(t *testing.T) {
	rows := loadParityClasses(t)
	table := changeClassTable()

	declared := make(map[string]parityRow, len(rows))
	for _, r := range rows {
		if r.ID == "" {
			t.Fatalf("VOCABULARY: a row has an empty id; id is the frozen join key")
		}
		if _, dup := declared[r.ID]; dup {
			t.Fatalf("VOCABULARY: duplicate id %q in %s", r.ID, parityClassesPath)
		}
		declared[r.ID] = r
	}

	inTable := make(map[string]changeClassRow, len(table))
	for _, row := range table {
		if _, dup := inTable[row.id]; dup {
			t.Fatalf("PHANTOM: duplicate harness table row id %q", row.id)
		}
		inTable[row.id] = row
	}

	t.Run("MISSING", func(t *testing.T) {
		var missing []string
		for _, r := range rows {
			if r.HarnessRow != harnessRequired {
				continue
			}
			if _, ok := inTable[r.ID]; !ok {
				missing = append(missing, r.ID)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Fatalf("MISSING: %s declares harness_row: %q for %d class(es) with NO row in the "+
				"harness table: %s\nAdd a changeClassRow with that exact id to changeClassTable(), "+
				"or change the class's harness_row in the YAML and say why.",
				parityClassesPath, harnessRequired, len(missing), strings.Join(missing, ", "))
		}
	})

	t.Run("PHANTOM", func(t *testing.T) {
		var phantom []string
		for _, row := range table {
			if _, ok := declared[row.id]; !ok {
				phantom = append(phantom, row.id)
			}
		}
		sort.Strings(phantom)
		if len(phantom) > 0 {
			t.Fatalf("PHANTOM: the harness table declares %d row(s) with no matching id in %s: %s\n"+
				"`id` is a frozen wire identifier — declare the class in the YAML or fix the typo.",
				len(phantom), parityClassesPath, strings.Join(phantom, ", "))
		}
	})

	t.Run("KIND", func(t *testing.T) {
		// A crash condition is NOT a change class. This is the exact conflation
		// that turned FR-7's 15 classes into backlog.md:55's "16", and it is now
		// mechanically unrepresentable: the harness's change-class count is
		// derived from kind, never from len(rows).
		for _, row := range table {
			d := declared[row.id]
			if d.ID == "" {
				continue // PHANTOM's problem, already reported there.
			}
			if row.kind != d.Kind {
				t.Errorf("KIND: harness row %q declares kind %q; %s declares %q",
					row.id, row.kind, parityClassesPath, d.Kind)
			}
			if d.Kind == kindCrashCondition && row.kind != kindCrashCondition {
				t.Errorf("KIND: %q is a crash_condition in the YAML but the harness counts it as a change class", row.id)
			}
		}
		gotClasses := 0
		gotCrash := 0
		for _, r := range rows {
			switch r.Kind {
			case kindChangeClass:
				gotClasses++
			case kindCrashCondition:
				gotCrash++
			}
		}
		// 17 + 2: PRD FR-7's 15 change classes + Delta PRD §9's 2 crash
		// conditions, PLUS exactly two classes that FR-7 does not name, both
		// born of PARITY-002 (ADR 0009): `change_colliding_package_dir` (added
		// 2026-08-16 as the defect's first hermetic reproduction, now a real
		// parity assertion) and `add_nested_gomod` (pins the module-map cache
		// invalidation the fix itself depends on). Both are real change classes
		// but exist to prove a defect and its fix, not to discharge an FR-7
		// requirement, and their `prd_source` says so.
		// The count stays PINNED rather than becoming a `>=` bound: the guard's
		// job is that no row can be re-kinded or added unnoticed, and a bound
		// would forfeit exactly that. Adding another class means updating this
		// number and saying why, here.
		if gotClasses != 17 || gotCrash != 2 {
			t.Errorf("KIND: %s has %d change_class + %d crash_condition rows; want 17 + 2",
				parityClassesPath, gotClasses, gotCrash)
		}
	})

	t.Run("VERDICT", func(t *testing.T) {
		// The YAML header's rule, verbatim: "the commit that adds a harness row
		// for class X MUST, in the same commit, update X's verdict and its
		// test_file/test_line/test_name/fixture/store/assertion to point at the
		// harness row that now proves it."
		//
		// HOW THE DIRECTION'S "EXISTS AND PASSES" CONJUNCT IS DISCHARGED, stated
		// precisely because a naive reading of it opens a real trap.
		//
		// EXISTS is checked here, mechanically.
		//
		// PASSES is NOT checked here — this guard never runs a table row. For an
		// ordinary row it is discharged DYNAMICALLY, by the suite being green: a
		// row whose parity assertion fails makes TestFullVsIncremental_ByteParity
		// red, and a red suite is the report. That discharge is sound only while
		// "green" actually means "parity held".
		//
		// THE TRAP: a knownDefect row is GREEN AND DOES NOT ASSERT PARITY. It pins
		// the current wrong behaviour instead (runKnownDefectRow). So for such a
		// row the greenness discharge is VACUOUS — it would license promoting the
		// class to PROVEN on the strength of a suite that never checked parity at
		// all. The same hole would open under a bare `-skip`.
		//
		// THE CLOSURE: for any row carrying a known_defect, PASSES is not inferred
		// from anything. The verdict is constrained STRUCTURALLY and
		// unconditionally — known_defect != "" implies the class may not read
		// PROVEN, no matter what the suite reports. Promotion is therefore
		// impossible while the defect is declared, and clearing the declaration is
		// a deliberate, reviewable edit in two files at once (this YAML and the
		// harness row), which the cross-check below enforces.
		//
		// A class that genuinely fails parity therefore does NOT read ABSENT — it
		// has a harness row, so ABSENT is rejected two checks down. It reads
		// PARTIAL, carries its defect id in known_defect, and the details live in
		// `note`.
		for _, r := range rows {
			if r.HarnessRow != harnessRequired {
				continue
			}
			row, ok := inTable[r.ID]
			if !ok || row.deferredTo != "" {
				continue
			}
			if r.Verdict == verdictAbsent {
				t.Errorf("VERDICT: %q has a harness table row but still reads verdict: %q in %s — "+
					"update the verdict and the test_file/test_line/test_name/fixture/store/assertion "+
					"citations in the SAME commit that adds the row", r.ID, r.Verdict, parityClassesPath)
			}
			// The closure. Unconditional, and independent of suite state.
			if r.KnownDefect != "" && r.Verdict == verdictProven {
				t.Errorf("VERDICT: %q declares known_defect %q, so it may NOT read verdict: %q. A class "+
					"with a tracked parity defect is not proven, and its harness row pins the defect "+
					"rather than asserting parity — so a green suite says nothing about this class's "+
					"parity and cannot be used to promote it.", r.ID, r.KnownDefect, verdictProven)
			}
			// The harness and the matrix must agree about the defect, in both
			// directions, so neither file can be edited alone to slip a promotion
			// through.
			if row.knownDefect != r.KnownDefect {
				t.Errorf("VERDICT: harness row %q declares knownDefect %q but %s declares known_defect %q — "+
					"a tracked defect must be declared in BOTH files or in neither",
					r.ID, row.knownDefect, parityClassesPath, r.KnownDefect)
			}
			if r.TestFile != harnessTestFile {
				t.Errorf("VERDICT: %q is proven by the harness table but test_file reads %q; "+
					"want %q (legacy citations belong in `note` as pre-harness provenance)",
					r.ID, r.TestFile, harnessTestFile)
			}
			if r.TestName != harnessTestName {
				t.Errorf("VERDICT: %q test_name reads %q; want %q", r.ID, r.TestName, harnessTestName)
			}
		}
		// A deferred row has no harness row to pin anything, so a defect id on one
		// would name a reproduction that does not exist.
		for _, r := range rows {
			if r.HarnessRow == harnessDeferred && r.KnownDefect != "" {
				t.Errorf("VERDICT: %q is harness_row: %q but declares known_defect %q; a deferred class "+
					"has no row that could publish the defect", r.ID, harnessDeferred, r.KnownDefect)
			}
		}

		// CITATION — added 2026-07-30 by SW-144, closing finding F9 of SW-157's
		// review. It is ten lines and it shuts a real hole.
		//
		// THE ATTACK IT KILLS. Every direction above keys off `harness_row`, and
		// `harness_row` is a DECLARATION rather than an observation. Five
		// coordinated edits therefore bought a free PROVEN: set
		// harness_row: "deferred", set deferred_to and owner to "SW-158", clear
		// known_defect, and keep verdict: "PROVEN". MISSING skips deferred rows.
		// VERDICT skips them (`r.HarnessRow != harnessRequired` continues).
		// OWNER is satisfied because owner == deferred_to. The suite stays green,
		// the matrix reads PROVEN, and NOTHING RUNS THE CLASS AT ALL.
		//
		// THE CLOSURE, and why it is sound rather than merely narrowing. A row
		// that cites the harness DRIVER is claiming to be proven BY the harness.
		// Only a row with a harness table row can be, so citing the driver and
		// declaring `deferred` is a self-contradiction — and it is exactly the
		// contradiction the forged row must produce, because it wants to keep
		// PROVEN and PROVEN demands a real citation.
		//
		// WHY IT DOES NOT FALSELY ACCUSE THE THREE LEGITIMATE DEFERRED ROWS: none
		// of them cites this driver. branch_switch is ABSENT and cites nothing at
		// all; interrupted_full_pass and restart_and_recovery are PROVEN by
		// engine/ingest/faultmatrix_test.go, a different file, which is precisely
		// why they may be deferred while still reading PROVEN. The rule passes
		// all three unchanged and is not a special case for any of them.
		for _, r := range rows {
			if r.HarnessRow == harnessRequired {
				continue
			}
			if r.TestFile == harnessTestFile || r.TestName == harnessTestName {
				t.Errorf("CITATION: %q is harness_row: %q yet cites the harness driver "+
					"(test_file=%q test_name=%q). A row proven BY the harness must be "+
					"harness_row: %q — otherwise `deferred` becomes a way to claim a verdict "+
					"the harness never produced, because MISSING, VERDICT and OWNER all skip "+
					"deferred rows. Either give the class a real table row and mark it %q, or "+
					"cite the proof that actually covers it.",
					r.ID, r.HarnessRow, r.TestFile, r.TestName, harnessRequired, harnessRequired)
			}
		}
	})

	t.Run("OWNER", func(t *testing.T) {
		for _, r := range rows {
			switch r.HarnessRow {
			case harnessRequired:
				// SW-157 built the harness and owns the FR-7 rows it landed
				// with. A later story that ADDS a required row owns that row
				// instead — attributing it to SW-157 would be false provenance,
				// which is the opposite of what this guard exists for. The set
				// stays CLOSED and enumerated, so a row still cannot arrive with
				// an empty or invented owner; adding one means naming it in
				// requiredRowOwners.
				if !requiredRowOwners[r.Owner] {
					t.Errorf("OWNER: %q is harness_row: %q so owner must be one of %v; got %q",
						r.ID, harnessRequired, requiredRowOwnerList(), r.Owner)
				}
				if r.DeferredTo != "" {
					t.Errorf("OWNER: %q is harness_row: %q so deferred_to must be empty; got %q",
						r.ID, harnessRequired, r.DeferredTo)
				}
			case harnessDeferred:
				if r.DeferredTo == "" {
					t.Errorf("OWNER: %q is harness_row: %q with an empty deferred_to", r.ID, harnessDeferred)
				}
				if r.Owner != r.DeferredTo {
					t.Errorf("OWNER: %q is deferred to %q but owner reads %q — the file would name one "+
						"story as responsible and ask a different one to deliver", r.ID, r.DeferredTo, r.Owner)
				}
			}
		}
		// The harness must agree about who owns a deferred row, so a placeholder
		// cannot silently drift away from the story that has to replace it.
		for _, row := range table {
			d := declared[row.id]
			if d.HarnessRow == harnessDeferred && row.deferredTo != d.DeferredTo {
				t.Errorf("OWNER: harness row %q defers to %q; %s defers to %q",
					row.id, row.deferredTo, parityClassesPath, d.DeferredTo)
			}
			if d.HarnessRow == harnessRequired && row.deferredTo != "" {
				t.Errorf("OWNER: harness row %q is deferred to %q but %s marks it %q",
					row.id, row.deferredTo, parityClassesPath, harnessRequired)
			}
		}
	})

	t.Run("VOCABULARY", func(t *testing.T) {
		// ADDED BY SW-157, and the reason is worth recording because it is a hole
		// SW-156's own review found and had no instruction for.
		//
		// docs/rc/parity-class-matrix.md:434 (Finding 11) asserts, in the present
		// tense, that "The YAML's validator now also rejects `store: none` on any
		// row claiming a byte assertion, since bytes can only come from a store."
		// No such validator existed, and that rule is not among the five
		// directions the YAML header declares — so the assertion was true of
		// nothing. Rather than delete the sentence or leave it unbacked, the rule
		// is implemented here as a sixth direction, generalized to the obvious
		// superset: every closed-set field must hold a declared value.
		//
		// It is a real guard, not a formality: `none` was introduced by review
		// finding m2 for add_implementation, whose primary proof opened no store
		// at all. The moment that row's assertion becomes "snapshot bytes" — which
		// is exactly what SW-157 does to it — leaving store: none behind would be
		// a self-contradicting row, and this direction is what catches it.
		oneOf := func(v string, legal []string) bool {
			for _, l := range legal {
				if v == l {
					return true
				}
			}
			return false
		}
		for _, r := range rows {
			check := func(field, value string, legal []string, allowEmpty bool) {
				if value == "" && allowEmpty {
					return
				}
				if !oneOf(value, legal) {
					t.Errorf("VOCABULARY: %q has %s: %q, which is outside the declared vocabulary %v",
						r.ID, field, value, legal)
				}
			}
			check("kind", r.Kind, legalKinds, false)
			check("verdict", r.Verdict, legalVerdicts, false)
			check("harness_row", r.HarnessRow, legalHarnessRows, false)
			// fixture/store/assertion are empty exactly when the row is ABSENT:
			// the YAML header specifies `test_file` as "" when ABSENT, and the
			// same holds for the fields describing that (non-existent) proof.
			absent := r.Verdict == verdictAbsent
			check("fixture", r.Fixture, legalFixtures, absent)
			check("store", r.Store, legalStores, absent)
			check("assertion", r.Assertion, legalAssertions, absent)
			if absent {
				if r.Fixture != "" || r.Store != "" || r.Assertion != "" {
					t.Errorf("VOCABULARY: %q reads verdict: %q but still cites fixture=%q store=%q assertion=%q",
						r.ID, verdictAbsent, r.Fixture, r.Store, r.Assertion)
				}
			} else if r.Fixture == "" || r.Store == "" || r.Assertion == "" {
				t.Errorf("VOCABULARY: %q reads verdict: %q so fixture/store/assertion must all be set; "+
					"got fixture=%q store=%q assertion=%q", r.ID, r.Verdict, r.Fixture, r.Store, r.Assertion)
			}
			// known_defect is an open string (a defect id), not a closed set, so
			// VOCABULARY checks only that it looks like an ID rather than prose —
			// it is a join key for the backlog entry and the harness row.
			if r.KnownDefect != "" && strings.ContainsAny(r.KnownDefect, " \t") {
				t.Errorf("VOCABULARY: %q has known_defect %q; it must be a bare defect id, not prose "+
					"(the explanation belongs in `note`)", r.ID, r.KnownDefect)
			}
			// The m9 rule itself.
			if r.Store == storeNone &&
				(r.Assertion == assertionSnapshotBytes || r.Assertion == assertionEnvelopeBytes) {
				t.Errorf("VOCABULARY: %q claims assertion: %q with store: %q — bytes can only come from a store",
					r.ID, r.Assertion, storeNone)
			}
		}
	})
}

// TestParityMatrix_HarnessRowsCoverEveryRequiredClass is the human-readable
// companion to the MISSING direction: it prints the join so a reviewer can read
// the coverage without re-deriving it from two files. It asserts only what
// MISSING/PHANTOM already assert, so it can never be the only thing that fails.
func TestParityMatrix_HarnessRowsCoverEveryRequiredClass(t *testing.T) {
	rows := loadParityClasses(t)
	table := changeClassTable()
	inTable := make(map[string]changeClassRow, len(table))
	for _, row := range table {
		inTable[row.id] = row
	}
	var lines []string
	implemented, deferred := 0, 0
	for _, r := range rows {
		row, ok := inTable[r.ID]
		state := "NO TABLE ROW"
		switch {
		case ok && row.deferredTo != "":
			state = "deferred -> " + row.deferredTo
			deferred++
		case ok:
			state = "implemented"
			implemented++
		}
		lines = append(lines, fmt.Sprintf("  %-24s %-16s %-8s %s", r.ID, r.Kind, r.Verdict, state))
	}
	t.Logf("parity class table (%d implemented, %d deferred, %d declared):\n%s",
		implemented, deferred, len(rows), strings.Join(lines, "\n"))
	if implemented+deferred != len(rows) {
		t.Fatalf("harness table covers %d of %d declared rows", implemented+deferred, len(rows))
	}
}
