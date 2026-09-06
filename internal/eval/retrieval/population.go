package retrieval

// The answerable population (SW-282 AC-1).
//
// SW-266 AC-2 defines "answerable" as: the query has at least one
// independently reviewed, repository-resolving grade-3 answer span. `no_hit`
// queries are counted separately and satisfy no minimum. Two readings of that
// sentence exist in the tree's history — "not no_hit" and "carries a grade-3
// span" — and on cobra-v2 they disagree about exactly one query (`cb-31`,
// ambiguous, five grade-2 judgements and no grade-3 span). SW-279's approval
// recorded 41 development queries under the loose reading; the contractual
// definition yields 40.
//
// AnswerableQueries below is the ONE place that reading is applied. Everything
// that needs a population count — the dataset shape test, the composition
// record, any future savings statistic — calls it, so the count cannot drift
// between callers.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AnswerablePopulationRecordPath is the checked-in composition and exclusion
// record recomputed from the frozen release dataset by
// TestAnswerablePopulation_RecordMatchesDataset.
const AnswerablePopulationRecordPath = "docs/eval/retrieval/answerable-population.json"

// AnswerableDefinition is the contractual definition, written out so the
// record carries it rather than pointing at a story file.
const AnswerableDefinition = "SW-266 AC-2: a query is answerable when it is in the requested split, its stratum is not no_hit, " +
	"and it carries at least one judgement at grade 3 (retrieval.GradeMax). no_hit queries are counted separately and satisfy no minimum."

// AnswerableQueries returns the answerable queries of one split, in dataset
// order, under the contractual definition.
//
// It RESOLVES the disagreement between the two readings in favour of the
// contractual one (grade-3 required) rather than refusing, which is the
// opposite of what AnswerableHoldout does on the same disagreement. That
// asymmetry is deliberate and is explained beside AnswerableHoldout in
// blindeval.go: the holdout's population size fixes a pre-registered passing
// count `k` before any response is opened, so an ambiguous holdout population
// must stop the run; the development split pre-registers nothing, so a
// development population that is merely ambiguous is resolved by reading the
// contract rather than by halting every development measurement.
//
// Queries excluded by the disagreement are returned separately so a caller can
// name them; a population that silently drops a row is how the miscount
// survived SW-279.
func AnswerableQueries(ds *Dataset, split string) (answerable []Query, excluded []ExcludedQuery, err error) {
	if ds == nil {
		return nil, nil, fmt.Errorf("retrieval: no dataset")
	}
	if split != SplitDev && split != SplitHoldout {
		return nil, nil, fmt.Errorf("retrieval: split %q is neither %s nor %s", split, SplitDev, SplitHoldout)
	}
	for _, q := range ds.Queries {
		if q.Split != split {
			continue
		}
		if q.Stratum == StratumNoHit {
			continue
		}
		if HasGradeMaxJudgement(q) {
			answerable = append(answerable, q)
			continue
		}
		excluded = append(excluded, ExcludedQuery{
			QueryID: q.ID, Split: q.Split, Stratum: q.Stratum,
			Grades: judgementGrades(q),
			Reason: "not no_hit but carries no grade-3 answer span, so it is not answerable under " + AnswerableDefinition,
		})
	}
	if len(answerable) == 0 {
		return nil, excluded, fmt.Errorf("retrieval: dataset %s has no answerable query in split %s", ds.ID, split)
	}
	return answerable, excluded, nil
}

// HasGradeMaxJudgement reports whether the query carries an answer span.
func HasGradeMaxJudgement(q Query) bool {
	for _, j := range q.Judgements {
		if j.Grade == GradeMax {
			return true
		}
	}
	return false
}

func judgementGrades(q Query) []int {
	out := make([]int, 0, len(q.Judgements))
	for _, j := range q.Judgements {
		out = append(out, j.Grade)
	}
	return out
}

// ExcludedQuery is one query the contractual definition removes from a
// population, with everything a reader needs to check the removal.
type ExcludedQuery struct {
	QueryID string `json:"query_id"`
	Split   string `json:"split"`
	Stratum string `json:"stratum"`
	Grades  []int  `json:"judgement_grades"`
	Reason  string `json:"reason"`
}

// SplitComposition is one split's answerable size and its stratum breakdown.
type SplitComposition struct {
	Answerable int            `json:"answerable"`
	NoHit      int            `json:"no_hit"`
	ByStratum  map[string]int `json:"answerable_by_stratum"`
	// ConfigDocs is broken out because it is the concentration that binds
	// what the holdout may be used to claim.
	ConfigDocs         int    `json:"config_docs"`
	ConfigDocsFraction string `json:"config_docs_fraction"`
	ConfigDocsRendered string `json:"config_docs_rendered"`
}

// AnswerablePopulationRecord is docs/eval/retrieval/answerable-population.json:
// the corrected counts, the excluded queries named by ID, and the stratum
// composition, all recomputed from the dataset by a test that fails on drift.
type AnswerablePopulationRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Story         string `json:"story"`
	Definition    string `json:"definition"`
	DatasetID     string `json:"dataset_id"`
	DatasetFile   string `json:"dataset_file"`
	DatasetSHA256 string `json:"dataset_sha256"`

	Dev     SplitComposition `json:"dev"`
	Holdout SplitComposition `json:"holdout"`

	// SavingsPopulation is dev + holdout: the full answerable population any
	// savings statistic is computed over.
	SavingsPopulation            int    `json:"savings_population"`
	SavingsConfigDocs            int    `json:"savings_config_docs"`
	SavingsConfigDocsFraction    string `json:"savings_config_docs_fraction"`
	SavingsConfigDocsRendered    string `json:"savings_config_docs_rendered"`
	SupersededDevCount           int    `json:"superseded_dev_count"`
	SupersededDevCountRecordedIn string `json:"superseded_dev_count_recorded_in"`

	Excluded []ExcludedQuery `json:"excluded"`

	BindingConstraint string `json:"binding_constraint"`
	Notes             string `json:"notes"`
}

// AnswerablePopulationSchemaVersion pins the record file.
const AnswerablePopulationSchemaVersion = 1

// HoldoutBindingConstraint is the constraint the composition puts on any claim
// resting on this holdout. It is recorded rather than made executable: making
// it executable inside the claim grammar would change allowedClaimShape and
// therefore the frozen measurement contract version, which SW-282 may not do.
const HoldoutBindingConstraint = "The answerable holdout is dominated by one stratum: 57 of its 64 queries are config_docs. " +
	"Any future claim resting on this holdout is either narrowed to config_docs or requires a fresh holdout stratified before it is sealed. " +
	"A mostly-config_docs sample may not be presented as generic developer questions. " +
	"This constraint is recorded, not executed: expressing it inside the claim grammar would change allowedClaimShape and therefore the frozen contract version, so it is left to the claim story."

// ComputeAnswerablePopulation builds the record from a loaded dataset.
func ComputeAnswerablePopulation(ds *Loaded, datasetFile string) (*AnswerablePopulationRecord, error) {
	if ds == nil || ds.Dataset == nil {
		return nil, fmt.Errorf("retrieval: no dataset")
	}
	rec := &AnswerablePopulationRecord{
		SchemaVersion: AnswerablePopulationSchemaVersion,
		Story:         "SW-282",
		Definition:    AnswerableDefinition,
		DatasetID:     ds.Dataset.ID,
		DatasetFile:   datasetFile,
		DatasetSHA256: ds.SHA256,
		Excluded:      []ExcludedQuery{},
	}
	for _, split := range []string{SplitDev, SplitHoldout} {
		answerable, excluded, err := AnswerableQueries(ds.Dataset, split)
		if err != nil {
			return nil, err
		}
		comp := SplitComposition{Answerable: len(answerable), ByStratum: map[string]int{}}
		for _, q := range answerable {
			comp.ByStratum[q.Stratum]++
		}
		for _, q := range ds.Dataset.Queries {
			if q.Split == split && q.Stratum == StratumNoHit {
				comp.NoHit++
			}
		}
		comp.ConfigDocs = comp.ByStratum[StratumConfigDocs]
		comp.ConfigDocsFraction = fmt.Sprintf("%d/%d", comp.ConfigDocs, comp.Answerable)
		comp.ConfigDocsRendered = renderPercent(comp.ConfigDocs, comp.Answerable)
		rec.Excluded = append(rec.Excluded, excluded...)
		if split == SplitDev {
			rec.Dev = comp
		} else {
			rec.Holdout = comp
		}
	}
	rec.SavingsPopulation = rec.Dev.Answerable + rec.Holdout.Answerable
	rec.SavingsConfigDocs = rec.Dev.ConfigDocs + rec.Holdout.ConfigDocs
	rec.SavingsConfigDocsFraction = fmt.Sprintf("%d/%d", rec.SavingsConfigDocs, rec.SavingsPopulation)
	rec.SavingsConfigDocsRendered = renderPercent(rec.SavingsConfigDocs, rec.SavingsPopulation)
	rec.SupersededDevCount = rec.Dev.Answerable + len(rec.Excluded)
	rec.SupersededDevCountRecordedIn = "projects/graphi/stories/SW-279/approval.md recorded " +
		fmt.Sprintf("%d", rec.SupersededDevCount) +
		" answerable development queries under the looser \"not no_hit\" reading; SW-282 corrects it to the contractual count."
	rec.BindingConstraint = HoldoutBindingConstraint
	rec.Notes = "Recomputed from the dataset bytes by TestAnswerablePopulation_RecordMatchesDataset. " +
		"Counts are exact integers; percentages are rendered at whole percentage points because one query moves the holdout share by 1/64 and the savings share by 1/104."
	sort.Slice(rec.Excluded, func(i, j int) bool { return rec.Excluded[i].QueryID < rec.Excluded[j].QueryID })
	return rec, nil
}

// renderPercent renders k/n at whole percentage points; the authoritative
// value is the fraction beside it (methodology.md, "Resolution and rendering").
func renderPercent(k, n int) string {
	if n == 0 {
		return "UNKNOWN"
	}
	// Round half away from zero on integer arithmetic; no float formatting.
	return fmt.Sprintf("%d%%", (200*k+n)/(2*n))
}

// MarshalAnswerablePopulation renders the record file.
func MarshalAnswerablePopulation(r *AnswerablePopulationRecord) ([]byte, error) {
	return marshalStable(r)
}

// ReadAnswerablePopulationRecord reads the checked-in record.
func ReadAnswerablePopulationRecord(repoRoot string) (*AnswerablePopulationRecord, []byte, error) {
	p := filepath.Join(repoRoot, filepath.FromSlash(AnswerablePopulationRecordPath))
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, fmt.Errorf("retrieval: read %s: %w", AnswerablePopulationRecordPath, err)
	}
	var rec AnswerablePopulationRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, nil, fmt.Errorf("retrieval: parse %s: %w", AnswerablePopulationRecordPath, err)
	}
	return &rec, raw, nil
}

// SelectDevSplit derives the development-only slice of a loaded dataset.
//
// It exists so a derivation report can be produced over development queries
// alone: DeriveTargets REFUSES a report carrying a holdout row (it does not
// filter one out), and the only honest way to satisfy that is to never measure
// the holdout in the first place. There is deliberately no split parameter —
// this function cannot be asked for the holdout.
func SelectDevSplit(source *Loaded) (*Loaded, error) {
	if source == nil || source.Dataset == nil {
		return nil, fmt.Errorf("retrieval: nil source dataset")
	}
	queries := make([]Query, 0, len(source.Dataset.Queries))
	for _, q := range source.Dataset.Queries {
		if q.Split == SplitDev {
			queries = append(queries, q)
		}
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("retrieval: dataset %s has no %s query", source.Dataset.ID, SplitDev)
	}
	ds := &Dataset{
		SchemaVersion:    source.Dataset.SchemaVersion,
		ID:               source.Dataset.ID + "-dev",
		Repo:             source.Dataset.Repo,
		RepoSHA:          source.Dataset.RepoSHA,
		Language:         source.Dataset.Language,
		EvidenceClass:    source.Dataset.EvidenceClass,
		RelevantMinGrade: source.Dataset.RelevantMinGrade,
		Notes: "Development-only slice of " + source.Dataset.ID + " at sha256 " + source.SHA256 +
			": every and only split=" + SplitDev + " query, judgements unchanged. The holdout is not measured here.",
		Queries: queries,
	}
	for _, q := range ds.Queries {
		if q.Split != SplitDev {
			return nil, fmt.Errorf("retrieval: dev slice widened to query %s (%s)", q.ID, q.Split)
		}
	}
	if err := ds.Validate(); err != nil {
		return nil, fmt.Errorf("retrieval: dev slice: %w", err)
	}
	raw, err := marshalStable(ds)
	if err != nil {
		return nil, err
	}
	ext := filepath.Ext(source.Path)
	p := strings.TrimSuffix(source.Path, ext) + "-dev" + ext
	return &Loaded{Dataset: ds, Path: p, Raw: raw, SHA256: SHA256Hex(raw)}, nil
}
