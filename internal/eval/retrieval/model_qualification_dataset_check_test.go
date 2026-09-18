package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The property under test is not "the checker notices a defect" - the gate
// already does that - but "the checker notices ALL of them in one pass". Every
// case below therefore plants several independent defects and asserts that each
// one is named, and the combined case asserts that no defect is swallowed by an
// earlier one, which is precisely what ValidateQualificationDataset does by
// design and what makes an operator's curation loop so slow.
func TestCheckQualificationDatasetReportsEveryProblemAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name            string
		mutate          func(*Dataset)
		wantConformant  bool
		wantUsableTotal int
		wantGaps        map[string]int
		wantNeeded      int
		wantReport      []string
		wantNotInReport []string
	}{
		{
			name:            "conformant population",
			mutate:          func(*Dataset) {},
			wantConformant:  true,
			wantUsableTotal: 64,
			wantGaps:        map[string]int{},
			wantNeeded:      0,
			wantReport: []string{
				"AUTHORITY", "ACCEPTED",
				"DUPLICATE family_id (0)",
				"BLANK family_id (0)",
				"BLANK provenance (0)",
				"NO GRADE-3 ANSWER SPAN (0)",
				"FORBIDDEN STRATUM (0)",
				"SPLIT IS NOT dev (0)",
				"64 of 64 queries are conformant; 0 new queries needed.",
			},
			wantNotInReport: []string{"REJECTED"},
		},
		{
			// Two collisions, one of them three-way. The gate would have named
			// a single pair and stopped; the report has to name all five rows,
			// otherwise the operator fixes one collision and pays for another
			// round trip to discover the next.
			name: "duplicate families",
			mutate: func(d *Dataset) {
				setFamily(d, "ambiguous-b", "family-ambiguous-a")
				setFamily(d, "exact_path-c", "family-exact_path-a")
				setFamily(d, "exact_path-d", "family-exact_path-a")
			},
			wantConformant:  false,
			wantUsableTotal: 61,
			wantGaps:        map[string]int{StratumAmbiguous: -1, StratumExactPath: -2},
			wantNeeded:      3,
			wantReport: []string{
				"DUPLICATE family_id (2)",
				"family-ambiguous-a reused by ambiguous-a, ambiguous-b",
				"family-exact_path-a reused by exact_path-a, exact_path-c, exact_path-d",
				"ambiguous 9 10 -1 1",
				"exact_path 9 11 -2 2",
				"61 of 64 queries are conformant; 3 new queries needed: ambiguous +1, exact_path +2.",
			},
		},
		{
			// no_hit is a legitimate stratum of the wider dataset schema and an
			// illegitimate one here: a question with no answer cannot take part
			// in a paired grade-3 comparison. The rows keep their judgements so
			// this case also pins the rule that a Dataset.Validate failure is
			// reported, not fatal - a half-curated file must still get a table.
			name: "forbidden no_hit stratum",
			mutate: func(d *Dataset) {
				setStratum(d, "ambiguous-a", StratumNoHit)
				setStratum(d, "nl_behaviour-a", StratumNoHit)
			},
			wantConformant:  false,
			wantUsableTotal: 62,
			wantGaps:        map[string]int{StratumAmbiguous: -1, StratumNLBehaviour: -1},
			wantNeeded:      2,
			wantReport: []string{
				"FORBIDDEN STRATUM (2)",
				`ambiguous-a has stratum "no_hit"`,
				`nl_behaviour-a has stratum "no_hit"`,
				"SCHEMA (Dataset.Validate, reports its first error only)",
				"62 of 64 queries are conformant; 2 new queries needed: ambiguous +1, nl_behaviour +1.",
			},
		},
		{
			// Three strata short at once is the ordinary mid-curation state and
			// the one the gate serves worst: it reports whichever stratum its
			// map iteration reaches first, so the operator cannot even tell how
			// much work is left.
			name: "short in several strata",
			mutate: func(d *Dataset) {
				dropQueries(d, "ambiguous-a", "ambiguous-b", "ambiguous-c", "exact_path-a", "exact_path-b", "nl_behaviour-a", "nl_behaviour-b", "nl_behaviour-c", "nl_behaviour-d")
			},
			wantConformant:  false,
			wantUsableTotal: 55,
			wantGaps:        map[string]int{StratumAmbiguous: -3, StratumExactPath: -2, StratumNLBehaviour: -4},
			wantNeeded:      9,
			wantReport: []string{
				"queries examined 55",
				"ambiguous 7 10 -3 3",
				"exact_path 9 11 -2 2",
				"nl_behaviour 7 11 -4 4",
				"55 of 64 queries are conformant; 9 new queries needed: ambiguous +3, exact_path +2, nl_behaviour +4.",
			},
		},
		{
			// A grade-2 span looks like an answer and is not one: the gate reads
			// exact grade 3, so a row annotated one notch low is invisible work
			// that silently shrinks the population.
			name: "missing grade-3 span",
			mutate: func(d *Dataset) {
				downgrade(d, "config_docs-a")
				downgrade(d, "exact_identifier-b")
			},
			wantConformant:  false,
			wantUsableTotal: 62,
			wantGaps:        map[string]int{StratumConfigDocs: -1, StratumExactIdentifier: -1},
			wantNeeded:      2,
			wantReport: []string{
				"NO GRADE-3 ANSWER SPAN (2)",
				"config_docs-a",
				"exact_identifier-b",
				"62 of 64 queries are conformant; 2 new queries needed: config_docs +1, exact_identifier +1.",
			},
		},
		{
			// The load-bearing case: six unrelated defect kinds in one file. The
			// gate returns exactly one sentence here. The report must name all
			// six, or the pre-flight has not replaced the edit-run loop at all.
			name: "every defect kind at once",
			mutate: func(d *Dataset) {
				setFamily(d, "ambiguous-b", "family-ambiguous-a")
				setFamily(d, "architecture_flow-a", "  ")
				setProvenance(d, "config_docs-b", "")
				setStratum(d, "exact_identifier-c", StratumNoHit)
				downgrade(d, "exact_path-d")
				setSplit(d, "nl_behaviour-e", SplitHoldout)
			},
			wantConformant:  false,
			wantUsableTotal: 58,
			wantGaps: map[string]int{
				StratumAmbiguous: -1, StratumArchitectureFlow: -1, StratumConfigDocs: -1,
				StratumExactIdentifier: -1, StratumExactPath: -1, StratumNLBehaviour: -1,
			},
			wantNeeded: 6,
			wantReport: []string{
				"DUPLICATE family_id (1)",
				"family-ambiguous-a reused by ambiguous-a, ambiguous-b",
				"BLANK family_id (1)",
				"architecture_flow-a",
				"BLANK provenance (1)",
				"config_docs-b",
				"NO GRADE-3 ANSWER SPAN (1)",
				"exact_path-d",
				"FORBIDDEN STRATUM (1)",
				`exact_identifier-c has stratum "no_hit"`,
				"SPLIT IS NOT dev (1)",
				`nl_behaviour-e has split "holdout"`,
				"58 of 64 queries are conformant; 6 new queries needed: ambiguous +1, architecture_flow +1, config_docs +1, exact_identifier +1, exact_path +1, nl_behaviour +1.",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataset := qualificationDatasetFixture().Dataset
			tc.mutate(dataset)
			path := writeQualificationDatasetFile(t, dataset)

			diagnosis, err := CheckQualificationDataset(QualificationDatasetCheckOptions{DatasetPath: path})
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			report := diagnosis.Render()
			// Column widths are presentation, not contract: the assertions below
			// collapse runs of spaces so a future re-alignment of the table does
			// not read as a behaviour change.
			flat := collapseSpaces(report)

			if diagnosis.Conformant != tc.wantConformant {
				t.Errorf("conformant=%t want %t; authority said %q", diagnosis.Conformant, tc.wantConformant, diagnosis.AuthorityError)
			}
			if diagnosis.UsableTotal != tc.wantUsableTotal {
				t.Errorf("usable total=%d want %d", diagnosis.UsableTotal, tc.wantUsableTotal)
			}
			if diagnosis.RequiredTotal() != 64 {
				t.Errorf("required total=%d want 64", diagnosis.RequiredTotal())
			}
			if got := diagnosis.NewQueriesNeeded(); got != tc.wantNeeded {
				t.Errorf("new queries needed=%d want %d", got, tc.wantNeeded)
			}
			for _, row := range diagnosis.Strata {
				if got, want := row.Gap, tc.wantGaps[row.Stratum]; got != want {
					t.Errorf("stratum %s gap=%+d want %+d (usable %d, required %d)", row.Stratum, got, want, row.Usable, row.Required)
				}
			}
			for _, want := range tc.wantReport {
				if !strings.Contains(flat, want) {
					t.Errorf("report is missing %q\n--- report ---\n%s", want, report)
				}
			}
			for _, unwanted := range tc.wantNotInReport {
				if strings.Contains(flat, unwanted) {
					t.Errorf("report unexpectedly contains %q\n--- report ---\n%s", unwanted, report)
				}
			}

			// The checker never gets its own opinion about pass or fail: the
			// verdict it prints must be the gate's verdict on the same bytes,
			// or a dataset it blesses could still cost an operator a measure run.
			loaded, err := decodeStrictQualificationDataset(path)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			authority := ValidateQualificationDataset(loaded)
			if (authority == nil) != diagnosis.Conformant {
				t.Fatalf("verdict drift: authority=%v, diagnosis.Conformant=%t", authority, diagnosis.Conformant)
			}
			if authority != nil && diagnosis.AuthorityError != authority.Error() {
				t.Fatalf("authority error not quoted verbatim:\n got %q\nwant %q", diagnosis.AuthorityError, authority.Error())
			}
		})
	}
}

// A clean report must be a sufficient condition for the gate, not merely a
// necessary one. If the two ever disagree in this direction the pre-flight is
// worse than useless: it would send an operator into an expensive measure run.
func TestCheckQualificationDatasetCleanReportImpliesTheGateAccepts(t *testing.T) {
	path := writeQualificationDatasetFile(t, qualificationDatasetFixture().Dataset)
	diagnosis, err := CheckQualificationDataset(QualificationDatasetCheckOptions{DatasetPath: path})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if n := len(diagnosis.DuplicateFamilies) + len(diagnosis.BlankFamilyID) + len(diagnosis.BlankProvenance) +
		len(diagnosis.MissingGradeThree) + len(diagnosis.ForbiddenStrata) + len(diagnosis.NonDevSplit); n != 0 {
		t.Fatalf("clean fixture reported %d faults:\n%s", n, diagnosis.Render())
	}
	for _, row := range diagnosis.Strata {
		if row.Gap != 0 {
			t.Fatalf("clean fixture has gap %+d in %s", row.Gap, row.Stratum)
		}
	}
	if !diagnosis.Conformant || diagnosis.AuthorityError != "" {
		t.Fatalf("clean report but the gate refused: %q", diagnosis.AuthorityError)
	}
}

// -dev-only exists so an operator can measure a mixed source dataset against the
// development quota without first hand-cutting a file. The filtered digest must
// be visibly marked as file-less, because pasting it into a preregistration
// would bind the experiment to bytes nobody can reproduce.
func TestCheckQualificationDatasetDevOnlyFiltersTheSourcePool(t *testing.T) {
	dataset := qualificationDatasetFixture().Dataset
	for _, id := range []string{"h1", "h2", "h3"} {
		holdout := qualificationQuery(id, StratumAmbiguous)
		holdout.Split = SplitHoldout
		dataset.Queries = append(dataset.Queries, holdout)
	}
	path := writeQualificationDatasetFile(t, dataset)

	unfiltered, err := CheckQualificationDataset(QualificationDatasetCheckOptions{DatasetPath: path})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if unfiltered.Conformant || len(unfiltered.NonDevSplit) != 3 || unfiltered.QueriesExamined != 67 {
		t.Fatalf("unfiltered: conformant=%t non-dev=%d examined=%d", unfiltered.Conformant, len(unfiltered.NonDevSplit), unfiltered.QueriesExamined)
	}

	filtered, err := CheckQualificationDataset(QualificationDatasetCheckOptions{DatasetPath: path, DevSplitOnly: true})
	if err != nil {
		t.Fatalf("check dev-only: %v", err)
	}
	if !filtered.Conformant {
		t.Fatalf("dev-only population refused: %q\n%s", filtered.AuthorityError, filtered.Render())
	}
	if filtered.DroppedNonDev != 3 || filtered.QueriesExamined != 64 || !filtered.Filtered {
		t.Fatalf("dropped=%d examined=%d filtered=%t", filtered.DroppedNonDev, filtered.QueriesExamined, filtered.Filtered)
	}
	if filtered.SHA256 == unfiltered.SHA256 {
		t.Fatal("filtered population kept the file digest; it would look preregistrable")
	}
	if !strings.Contains(filtered.Render(), "not preregistrable") {
		t.Fatalf("filtered report does not warn about its digest:\n%s", filtered.Render())
	}
}

// An unreadable or undecodable file is the one case with nothing to enumerate,
// so it stays an error rather than a diagnosis full of zeroes that an operator
// might read as "the population is empty".
func TestCheckQualificationDatasetRefusesUnreadableInput(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.json")
	if err := os.WriteFile(garbage, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "absent.json"), garbage} {
		if _, err := CheckQualificationDataset(QualificationDatasetCheckOptions{DatasetPath: path}); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}

func writeQualificationDatasetFile(t *testing.T, dataset *Dataset) string {
	t.Helper()
	raw, err := json.MarshalIndent(dataset, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "candidate-development.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func setFamily(d *Dataset, id, family string) {
	for i := range d.Queries {
		if d.Queries[i].ID == id {
			d.Queries[i].FamilyID = family
			return
		}
	}
	panic("fixture has no query " + id)
}

func setProvenance(d *Dataset, id, provenance string) {
	for i := range d.Queries {
		if d.Queries[i].ID == id {
			d.Queries[i].Provenance = provenance
			return
		}
	}
	panic("fixture has no query " + id)
}

func setStratum(d *Dataset, id, stratum string) {
	for i := range d.Queries {
		if d.Queries[i].ID == id {
			d.Queries[i].Stratum = stratum
			return
		}
	}
	panic("fixture has no query " + id)
}

func setSplit(d *Dataset, id, split string) {
	for i := range d.Queries {
		if d.Queries[i].ID == id {
			d.Queries[i].Split = split
			return
		}
	}
	panic("fixture has no query " + id)
}

// downgrade drops every span of one query to grade 2: present, plausible, and
// not the exact answer the gate counts.
func downgrade(d *Dataset, id string) {
	for i := range d.Queries {
		if d.Queries[i].ID == id {
			for j := range d.Queries[i].Judgements {
				d.Queries[i].Judgements[j].Grade = GradeMax - 1
			}
			return
		}
	}
	panic("fixture has no query " + id)
}

func dropQueries(d *Dataset, ids ...string) {
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		drop[id] = true
	}
	kept := d.Queries[:0]
	for _, query := range d.Queries {
		if !drop[query.ID] {
			kept = append(kept, query)
		}
	}
	d.Queries = kept
}

// collapseSpaces squeezes runs of spaces inside each line so report assertions
// pin content rather than column alignment.
func collapseSpaces(report string) string {
	lines := strings.Split(report, "\n")
	for i, line := range lines {
		lines[i] = strings.Join(strings.Fields(line), " ")
	}
	return strings.Join(lines, "\n")
}
