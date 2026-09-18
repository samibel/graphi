package retrieval

import (
	"fmt"
	"sort"
	"strings"
)

// The qualification gate is a fail-closed, first-error validator by design:
// ValidateQualificationDataset exists to refuse a population, and the cheapest
// correct refusal names one reason and stops. That is the right shape for the
// gate and the wrong shape for the operator standing in front of it. Curating
// the 64-query development population is an iterative job, and a validator that
// surfaces one defect per attempt turns it into dozens of edit-run cycles - each
// one paid for with a `measure` run that indexes a repository four times.
//
// This file is the pre-flight lamp next to that gate. It walks the same rules
// over the same loaded bytes, but collects every violation instead of returning
// at the first, so an operator can close all the gaps in a single pass. It is
// deliberately NOT a second source of truth: the authoritative verdict in the
// report is whatever ValidateQualificationDataset says, run on the very same
// *Loaded value. The enumeration below can only ever be advisory detail hung off
// that verdict, which is why every report prints the authority's own words and
// why TestQualificationDatasetCheckAgreesWithTheAuthority pins the two together.

// QualificationDatasetCheckOptions selects the file to pre-flight and how much
// of it counts as the candidate population.
type QualificationDatasetCheckOptions struct {
	// DatasetPath is the candidate development dataset JSON.
	DatasetPath string
	// DevSplitOnly treats the file as a curation source pool rather than as a
	// finished dataset: holdout rows are dropped before the population is
	// judged instead of being reported as split violations. An existing mixed
	// dataset (cobra-v2.json, say) is the natural place to shop for development
	// queries, and 66 "this row is holdout" lines would bury the six numbers the
	// operator actually came for. The digest of a filtered population is NOT the
	// digest of any file on disk, so a report produced this way can never be
	// pasted into a preregistration; the report says so in as many words.
	DevSplitOnly bool
}

// QualificationStratumGap is one row of the per-stratum table: how many queries
// in this stratum survive every rule, how many the gate demands, and the signed
// distance between them.
type QualificationStratumGap struct {
	Stratum  string
	Usable   int
	Required int
	// Gap is Usable-Required: negative is a shortfall the operator must write
	// new queries to close, positive is surplus that must be trimmed, because
	// the gate wants an exact count and not a minimum.
	Gap int
}

// Needed is how many brand-new queries this stratum still wants. Surplus is not
// negative need - a stratum that is over quota is a different edit (delete rows)
// and is reported as a positive Gap instead.
func (g QualificationStratumGap) Needed() int {
	if g.Gap < 0 {
		return -g.Gap
	}
	return 0
}

// QualificationQueryFault names one query and the offending value, so the
// operator can jump straight to the row without grepping for it.
type QualificationQueryFault struct {
	QueryID string
	Detail  string
}

// QualificationFamilyCollision is one family_id claimed by more than one query.
// Every colliding query ID is listed, not just the two the gate would have
// named, because a family reused three times needs two edits and the gate's
// pairwise message hides the third.
type QualificationFamilyCollision struct {
	FamilyID string
	QueryIDs []string
}

// QualificationDatasetDiagnosis is the complete pre-flight view of one candidate
// development population: every violation at once, plus the authority's verdict.
type QualificationDatasetDiagnosis struct {
	Path      string
	DatasetID string
	// SHA256 digests the bytes actually judged. With DevSplitOnly it is the
	// digest of the filtered population and matches no file on disk; Filtered
	// records which of the two it is.
	SHA256          string
	Filtered        bool
	QueriesInFile   int
	QueriesExamined int
	DroppedNonDev   int

	Strata      []QualificationStratumGap
	UsableTotal int

	NonDevSplit       []QualificationQueryFault
	ForbiddenStrata   []QualificationQueryFault
	BlankFamilyID     []string
	BlankProvenance   []string
	MissingGradeThree []string
	DuplicateFamilies []QualificationFamilyCollision

	// SchemaError is Dataset.Validate's first error, if any. It stays a single
	// string on purpose: the schema validator is upstream of this one and its
	// own first-error contract is not this file's to reinterpret.
	SchemaError string
	// AuthorityError is ValidateQualificationDataset's verdict on the same
	// *Loaded value - empty when the gate accepts the population. This, and
	// only this, decides Conformant.
	AuthorityError string
	Conformant     bool
}

// RequiredTotal is the exact development population size the gate demands.
func (d QualificationDatasetDiagnosis) RequiredTotal() int {
	total := 0
	for _, row := range d.Strata {
		total += row.Required
	}
	return total
}

// NewQueriesNeeded is how many queries must still be written across all strata.
// It is a sum of per-stratum shortfalls and not RequiredTotal-UsableTotal,
// because a surplus in one stratum cannot be spent in another: the gate checks
// each stratum against an exact count.
func (d QualificationDatasetDiagnosis) NewQueriesNeeded() int {
	total := 0
	for _, row := range d.Strata {
		total += row.Needed()
	}
	return total
}

// CheckQualificationDataset pre-flights one candidate development dataset and
// reports every problem it can find in one pass.
//
// An unreadable or unparseable file is the one failure that still stops
// everything: there is no population to enumerate faults over, so it is returned
// as an error rather than folded into a diagnosis. A file that parses but fails
// Dataset.Validate is NOT an error - a half-curated population is exactly the
// state this tool exists to serve, and refusing to describe it would reproduce
// the one-error-at-a-time loop it was written to end.
func CheckQualificationDataset(options QualificationDatasetCheckOptions) (QualificationDatasetDiagnosis, error) {
	loaded, err := decodeStrictQualificationDataset(options.DatasetPath)
	if err != nil {
		return QualificationDatasetDiagnosis{}, err
	}

	diagnosis := QualificationDatasetDiagnosis{
		Path:          loaded.Path,
		DatasetID:     strings.TrimSpace(loaded.Dataset.ID),
		QueriesInFile: len(loaded.Dataset.Queries),
	}

	if options.DevSplitOnly {
		kept := make([]Query, 0, len(loaded.Dataset.Queries))
		for _, query := range loaded.Dataset.Queries {
			if query.Split == SplitDev {
				kept = append(kept, query)
			}
		}
		diagnosis.DroppedNonDev = len(loaded.Dataset.Queries) - len(kept)
		diagnosis.Filtered = diagnosis.DroppedNonDev > 0
		loaded.Dataset.Queries = kept
		// ValidateQualificationDataset binds Raw to SHA256 and refuses a
		// mismatch, so a filtered population has to be re-sealed before the
		// authority can speak about it at all. Re-sealing is what makes the
		// digest file-less, and Filtered is what stops the report pretending
		// otherwise.
		if diagnosis.Filtered {
			if err := resealQualificationDataset(loaded); err != nil {
				return QualificationDatasetDiagnosis{}, err
			}
		}
	}
	diagnosis.SHA256 = loaded.SHA256
	diagnosis.QueriesExamined = len(loaded.Dataset.Queries)

	usable := make(map[string]int, len(qualificationStratumCounts))
	byFamily := make(map[string][]string, len(loaded.Dataset.Queries))
	claimed := make(map[string]bool, len(loaded.Dataset.Queries))
	for _, query := range loaded.Dataset.Queries {
		conformant := true
		if query.Split != SplitDev {
			diagnosis.NonDevSplit = append(diagnosis.NonDevSplit, QualificationQueryFault{QueryID: query.ID, Detail: query.Split})
			conformant = false
		}
		if _, known := qualificationStratumCounts[query.Stratum]; !known {
			diagnosis.ForbiddenStrata = append(diagnosis.ForbiddenStrata, QualificationQueryFault{QueryID: query.ID, Detail: query.Stratum})
			conformant = false
		}
		family := strings.TrimSpace(query.FamilyID)
		if family == "" {
			diagnosis.BlankFamilyID = append(diagnosis.BlankFamilyID, query.ID)
			conformant = false
		} else {
			byFamily[family] = append(byFamily[family], query.ID)
		}
		if strings.TrimSpace(query.Provenance) == "" {
			diagnosis.BlankProvenance = append(diagnosis.BlankProvenance, query.ID)
			conformant = false
		}
		hasGrade3 := false
		for _, judgement := range query.Judgements {
			if judgement.Grade == GradeMax {
				hasGrade3 = true
				break
			}
		}
		if !hasGrade3 {
			diagnosis.MissingGradeThree = append(diagnosis.MissingGradeThree, query.ID)
			conformant = false
		}
		// A family is claimed by the first otherwise-conformant query that
		// carries it, in file order. Only conformant queries may claim, so that
		// repairing an unrelated defect - adding a missing grade-3 span, say -
		// cannot silently move the winner of a family and shuffle the stratum
		// table underneath the operator between two runs.
		if !conformant {
			continue
		}
		if claimed[family] {
			continue
		}
		claimed[family] = true
		usable[query.Stratum]++
		diagnosis.UsableTotal++
	}

	for family, ids := range byFamily {
		if len(ids) > 1 {
			diagnosis.DuplicateFamilies = append(diagnosis.DuplicateFamilies, QualificationFamilyCollision{FamilyID: family, QueryIDs: ids})
		}
	}
	sort.Slice(diagnosis.DuplicateFamilies, func(i, j int) bool {
		return diagnosis.DuplicateFamilies[i].FamilyID < diagnosis.DuplicateFamilies[j].FamilyID
	})

	strata := make([]string, 0, len(qualificationStratumCounts))
	for stratum := range qualificationStratumCounts {
		strata = append(strata, stratum)
	}
	sort.Strings(strata)
	for _, stratum := range strata {
		required := qualificationStratumCounts[stratum]
		diagnosis.Strata = append(diagnosis.Strata, QualificationStratumGap{
			Stratum: stratum, Usable: usable[stratum], Required: required, Gap: usable[stratum] - required,
		})
	}

	if err := loaded.Dataset.Validate(); err != nil {
		diagnosis.SchemaError = err.Error()
	}
	if err := ValidateQualificationDataset(loaded); err != nil {
		diagnosis.AuthorityError = err.Error()
	}
	diagnosis.Conformant = diagnosis.AuthorityError == ""
	return diagnosis, nil
}

// Render formats the diagnosis as the operator-facing report. Every section is
// printed even when it is empty: an absent section reads as "the checker did not
// look", and the whole point of a pre-flight is to be able to say "nothing else
// is wrong" with confidence.
func (d QualificationDatasetDiagnosis) Render() string {
	var b strings.Builder
	b.WriteString("QUALIFICATION DATASET PRE-FLIGHT\n")
	fmt.Fprintf(&b, "  file              %s\n", d.Path)
	fmt.Fprintf(&b, "  dataset id        %s\n", d.DatasetID)
	if d.Filtered {
		fmt.Fprintf(&b, "  sha256            %s  (filtered population; matches NO file on disk - not preregistrable)\n", d.SHA256)
	} else {
		fmt.Fprintf(&b, "  sha256            %s\n", d.SHA256)
	}
	fmt.Fprintf(&b, "  queries in file   %d\n", d.QueriesInFile)
	if d.DroppedNonDev > 0 {
		fmt.Fprintf(&b, "  queries examined  %d  (%d non-dev rows dropped by -dev-only)\n", d.QueriesExamined, d.DroppedNonDev)
	} else {
		fmt.Fprintf(&b, "  queries examined  %d\n", d.QueriesExamined)
	}

	b.WriteString("\nSTRATUM COVERAGE\n")
	fmt.Fprintf(&b, "  %-20s %8s %9s %7s %8s\n", "stratum", "usable", "required", "gap", "needed")
	for _, row := range d.Strata {
		fmt.Fprintf(&b, "  %-20s %8d %9d %+7d %8d\n", row.Stratum, row.Usable, row.Required, row.Gap, row.Needed())
	}
	fmt.Fprintf(&b, "  %-20s %8s %9s %7s %8s\n", "", "--------", "---------", "-------", "--------")
	fmt.Fprintf(&b, "  %-20s %8d %9d %+7d %8d\n", "TOTAL", d.UsableTotal, d.RequiredTotal(), d.UsableTotal-d.RequiredTotal(), d.NewQueriesNeeded())

	renderFaultIDs(&b, "DUPLICATE family_id", len(d.DuplicateFamilies), func(line func(string)) {
		for _, collision := range d.DuplicateFamilies {
			line(fmt.Sprintf("%s reused by %s", collision.FamilyID, strings.Join(collision.QueryIDs, ", ")))
		}
	})
	renderFaultIDs(&b, "BLANK family_id", len(d.BlankFamilyID), func(line func(string)) {
		for _, id := range d.BlankFamilyID {
			line(id)
		}
	})
	renderFaultIDs(&b, "BLANK provenance", len(d.BlankProvenance), func(line func(string)) {
		for _, id := range d.BlankProvenance {
			line(id)
		}
	})
	renderFaultIDs(&b, "NO GRADE-3 ANSWER SPAN", len(d.MissingGradeThree), func(line func(string)) {
		for _, id := range d.MissingGradeThree {
			line(id)
		}
	})
	renderFaultIDs(&b, "FORBIDDEN STRATUM", len(d.ForbiddenStrata), func(line func(string)) {
		for _, fault := range d.ForbiddenStrata {
			line(fmt.Sprintf("%s has stratum %q", fault.QueryID, fault.Detail))
		}
	})
	renderFaultIDs(&b, "SPLIT IS NOT dev", len(d.NonDevSplit), func(line func(string)) {
		for _, fault := range d.NonDevSplit {
			line(fmt.Sprintf("%s has split %q", fault.QueryID, fault.Detail))
		}
	})

	b.WriteString("\nSCHEMA (Dataset.Validate, reports its first error only)\n")
	if d.SchemaError == "" {
		b.WriteString("  ok\n")
	} else {
		fmt.Fprintf(&b, "  %s\n", d.SchemaError)
	}

	b.WriteString("\nAUTHORITY (ValidateQualificationDataset - this verdict, not the table above, is the gate)\n")
	if d.AuthorityError == "" {
		b.WriteString("  ACCEPTED\n")
	} else {
		fmt.Fprintf(&b, "  REJECTED: %s\n", d.AuthorityError)
	}

	b.WriteString("\nSUMMARY\n")
	fmt.Fprintf(&b, "  %d of %d queries are conformant; %d new queries needed", d.UsableTotal, d.RequiredTotal(), d.NewQueriesNeeded())
	shortfalls := make([]string, 0, len(d.Strata))
	for _, row := range d.Strata {
		if row.Needed() > 0 {
			shortfalls = append(shortfalls, fmt.Sprintf("%s +%d", row.Stratum, row.Needed()))
		}
	}
	if len(shortfalls) == 0 {
		b.WriteString(".\n")
	} else {
		fmt.Fprintf(&b, ": %s.\n", strings.Join(shortfalls, ", "))
	}
	return b.String()
}

// renderFaultIDs prints one report section with its count in the heading and an
// explicit "none" body when it is empty, so a clean section is visibly checked
// rather than merely absent.
func renderFaultIDs(b *strings.Builder, heading string, count int, emit func(line func(string))) {
	fmt.Fprintf(b, "\n%s (%d)\n", heading, count)
	if count == 0 {
		b.WriteString("  none\n")
		return
	}
	emit(func(line string) { fmt.Fprintf(b, "  %s\n", line) })
}
