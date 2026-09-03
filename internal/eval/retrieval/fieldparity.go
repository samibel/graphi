package retrieval

import (
	"fmt"
	"path/filepath"
	"strings"
)

// SelectFieldParityDevNLBehaviour derives the fixed SW-272 measurement
// population from a loaded source dataset. There is deliberately no split or
// stratum parameter: callers cannot widen the diagnostic to the reserved
// holdout. The returned raw bytes contain every and only dev/nl_behaviour
// query selected from the cited source dataset.
func SelectFieldParityDevNLBehaviour(source *Loaded) (*Loaded, error) {
	if source == nil || source.Dataset == nil {
		return nil, fmt.Errorf("field-parity eval: nil source dataset")
	}
	selected, err := SelectTaskContextDevNLBehaviour(source.Dataset)
	if err != nil {
		return nil, fmt.Errorf("field-parity eval: %w", err)
	}
	// NDCG intentionally consumes every judgement it receives rather than
	// applying RelevantMinGrade. Derive an exact-grade-3 qrel set here so all
	// metrics, including NDCG, answer the same SW-272 question without changing
	// the shared scorer or any previously recorded run.
	queries := make([]Query, 0, len(selected))
	for _, sourceQuery := range selected {
		query := sourceQuery
		query.Judgements = nil
		for _, judgement := range sourceQuery.Judgements {
			if judgement.Grade == GradeMax {
				query.Judgements = append(query.Judgements, judgement)
			}
		}
		queries = append(queries, query)
	}
	dataset := &Dataset{
		SchemaVersion: source.Dataset.SchemaVersion,
		ID:            source.Dataset.ID + "-dev-nl-behaviour-field-parity",
		Repo:          source.Dataset.Repo,
		RepoSHA:       source.Dataset.RepoSHA,
		Language:      source.Dataset.Language,
		EvidenceClass: source.Dataset.EvidenceClass,
		// SW-272's discriminating field-parity measurement scores exact
		// grade-3 evidence. Do not inherit the source dataset's historical
		// default (grade >= 2), which answers a different question.
		RelevantMinGrade: GradeMax,
		Notes: "Derived exact-grade-3 field-parity population: every and only split=dev, stratum=nl_behaviour query from source dataset " +
			source.Dataset.ID + " at sha256 " + source.SHA256 + "; grade-1/2 qrels and holdout are excluded.",
		Queries: append([]Query(nil), queries...),
	}
	if err := dataset.Validate(); err != nil {
		return nil, fmt.Errorf("field-parity eval: derived dataset: %w", err)
	}
	for _, query := range dataset.Queries {
		if query.Split != SplitDev || query.Stratum != StratumNLBehaviour {
			return nil, fmt.Errorf("field-parity eval: selection widened to query %s (%s/%s)", query.ID, query.Stratum, query.Split)
		}
	}
	raw, err := marshalStable(dataset)
	if err != nil {
		return nil, err
	}
	ext := filepath.Ext(source.Path)
	path := strings.TrimSuffix(source.Path, ext) + "-dev-nl-behaviour-field-parity" + ext
	return &Loaded{Dataset: dataset, Path: path, Raw: raw, SHA256: SHA256Hex(raw)}, nil
}
