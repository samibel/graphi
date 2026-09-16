package retrieval

// Qrel-only stage-ceiling controls for the embedded-model qualification.
//
// This file is deliberately confined to internal/eval. Normal candidates are
// frozen before this code receives them; qrels can influence only the three
// explicitly labelled oracle artifacts returned by BuildOracleControls.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
	taskcompact "github.com/samibel/graphi/engine/agenttools/taskctx/compact"
)

const (
	OracleControlCurrentCandidatesOraclePacker  = "current_candidates_oracle_packer"
	OracleControlOracleCandidateCurrentSelector = "oracle_candidate_current_selector"
	OracleControlOracleCandidateOraclePacker    = "oracle_candidate_oracle_packer"
)

// OracleInput is the post-capture boundary. CurrentCandidates must be the
// frozen, qrel-blind task_context/2 result. BuildOracleControls never mutates
// it; candidate injection is performed on a deep copy.
type OracleInput struct {
	Query             Query
	CurrentCandidates contract.Result
	Repository        fs.FS
	RealCounter       PayloadCounter
}

// OracleControls separates retrieval recall, current-selector retention, and
// the fixed representation/budget ceiling.
type OracleControls struct {
	CurrentCandidatesOraclePacker  OracleBundle `json:"current_candidates_oracle_packer"`
	OracleCandidateCurrentSelector OracleBundle `json:"oracle_candidate_current_selector"`
	OracleCandidateOraclePacker    OracleBundle `json:"oracle_candidate_oracle_packer"`
}

// OracleBundle is safe to hand to the existing blind-rater workflow. It
// intentionally contains neither qrels nor answer labels. OutputName and
// CandidateProvenance keep oracle artifacts out of normal capture namespaces.
type OracleBundle struct {
	ControlKind         string           `json:"control_kind"`
	QueryID             string           `json:"query_id"`
	OutputName          string           `json:"output_name"`
	CandidateProvenance string           `json:"candidate_provenance"`
	Injected            bool             `json:"injected"`
	InjectedRows        int              `json:"injected_rows"`
	CompleteGrade3Span  bool             `json:"complete_grade_3_span"`
	TokenCount          int              `json:"token_count"`
	Payload             PreservedPayload `json:"payload"`
}

type oracleSource struct {
	taskcompact.Source
	judgement Judgement
	cost      int
}

// BuildOracleControls constructs all three ceilings only after the caller has
// frozen normal candidates. The current selector is the exported compact/17
// selector itself; it has no oracle callback or alternate ranking path.
func BuildOracleControls(in OracleInput) (OracleControls, error) {
	if strings.TrimSpace(in.Query.ID) == "" || strings.TrimSpace(in.Query.Text) == "" {
		return OracleControls{}, fmt.Errorf("embedded-model qualification oracle: query id and text are required")
	}
	if in.Repository == nil {
		return OracleControls{}, fmt.Errorf("embedded-model qualification oracle: repository is required")
	}
	if in.RealCounter.Count == nil || strings.TrimSpace(in.RealCounter.TokenizerID) == "" || in.RealCounter.TokenizerID == TokenizerID {
		return OracleControls{}, fmt.Errorf("embedded-model qualification oracle: pinned real tokenizer counter is required")
	}

	current, err := cloneOracleCandidates(in.CurrentCandidates)
	if err != nil {
		return OracleControls{}, err
	}
	grade3, err := loadOracleSources(in.Repository, in.Query, in.RealCounter)
	if err != nil {
		return OracleControls{}, err
	}

	currentSelection, err := runOracleCurrentSelector(in.Query.Text, current, in.Repository)
	if err != nil {
		return OracleControls{}, fmt.Errorf("embedded-model qualification oracle: current candidates/current selector: %w", err)
	}
	currentEligible := oracleSourcesPresentInCandidates(grade3, current)
	currentPacked, err := buildOraclePackedBundle(
		in, OracleControlCurrentCandidatesOraclePacker,
		"oracle-current-candidates-oracle-packer.json", "frozen_normal_candidates/oracle_packer",
		false, 0, currentSelection, currentEligible,
	)
	if err != nil {
		return OracleControls{}, err
	}

	injectedCandidates, injectedRows, err := injectOracleCandidates(current, grade3)
	if err != nil {
		return OracleControls{}, err
	}
	injectedSelection, err := runOracleCurrentSelector(in.Query.Text, injectedCandidates, in.Repository)
	if err != nil {
		return OracleControls{}, fmt.Errorf("embedded-model qualification oracle: oracle candidates/current selector: %w", err)
	}
	injectedSelected, err := buildOracleSelectorBundle(
		in, OracleControlOracleCandidateCurrentSelector,
		"oracle-candidate-current-selector.json", "oracle_candidate_copy/current_selector",
		injectedRows, injectedSelection,
	)
	if err != nil {
		return OracleControls{}, err
	}
	injectedPacked, err := buildOraclePackedBundle(
		in, OracleControlOracleCandidateOraclePacker,
		"oracle-candidate-oracle-packer.json", "oracle_candidate_copy/oracle_packer",
		true, injectedRows, injectedSelection, grade3,
	)
	if err != nil {
		return OracleControls{}, err
	}

	return OracleControls{
		CurrentCandidatesOraclePacker:  currentPacked,
		OracleCandidateCurrentSelector: injectedSelected,
		OracleCandidateOraclePacker:    injectedPacked,
	}, nil
}

func cloneOracleCandidates(in contract.Result) (contract.Result, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return contract.Result{}, fmt.Errorf("embedded-model qualification oracle: clone candidates: %w", err)
	}
	var out contract.Result
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return contract.Result{}, fmt.Errorf("embedded-model qualification oracle: clone candidates: %w", err)
	}
	if err := contract.ValidateResult(&out); err != nil {
		return contract.Result{}, fmt.Errorf("embedded-model qualification oracle: invalid frozen candidates: %w", err)
	}
	return out, nil
}

func loadOracleSources(repository fs.FS, query Query, real PayloadCounter) ([]oracleSource, error) {
	seen := make(map[string]bool)
	var out []oracleSource
	for _, judgement := range query.Judgements {
		if judgement.Grade != GradeMax {
			continue
		}
		key := fmt.Sprintf("%s\x00%d\x00%d", judgement.Path, judgement.StartLine, judgement.EndLine)
		if seen[key] {
			continue
		}
		seen[key] = true
		text, err := exactSourceSpan(repository, judgement.Path, judgement.StartLine, judgement.EndLine)
		if err != nil {
			return nil, fmt.Errorf("embedded-model qualification oracle: query %s: %w", query.ID, err)
		}
		source := taskcompact.Source{
			Path: judgement.Path, StartLine: judgement.StartLine,
			EndLine: judgement.EndLine, Text: text,
		}
		raw, err := json.Marshal(source)
		if err != nil {
			return nil, err
		}
		cost, err := real.Count(raw)
		if err != nil {
			return nil, fmt.Errorf("embedded-model qualification oracle: count source %s: %w", judgement.Path, err)
		}
		out = append(out, oracleSource{Source: source, judgement: judgement, cost: cost})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("embedded-model qualification oracle: query %s has no grade-3 judgement", query.ID)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cost != out[j].cost {
			return out[i].cost < out[j].cost
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].StartLine != out[j].StartLine {
			return out[i].StartLine < out[j].StartLine
		}
		return out[i].EndLine < out[j].EndLine
	})
	return out, nil
}

func oracleSourcesPresentInCandidates(sources []oracleSource, candidates contract.Result) []oracleSource {
	referenced := make(map[string]bool)
	for _, item := range candidates.Items {
		for _, ref := range item.EvidenceRefIDs {
			referenced[ref] = true
		}
	}
	var present []oracleSource
	for _, source := range sources {
		for _, evidence := range candidates.Evidence {
			if referenced[evidence.RefID] && SpanMatches(evidence.Path, evidence.Line, source.judgement) {
				present = append(present, source)
				break
			}
		}
	}
	return present
}

func injectOracleCandidates(current contract.Result, sources []oracleSource) (contract.Result, int, error) {
	used := make(map[string]bool, len(current.Items)+len(current.Evidence))
	for _, item := range current.Items {
		used[item.RefID] = true
	}
	for _, evidence := range current.Evidence {
		used[evidence.RefID] = true
	}
	items := make([]contract.Item, 0, len(sources)+len(current.Items))
	evidence := make([]contract.Evidence, 0, len(sources)+len(current.Evidence))
	for i, source := range sources {
		base := fmt.Sprintf("oracle-injected-%03d", i+1)
		itemRef := uniqueOracleRef(base+"-item", used)
		used[itemRef] = true
		evidenceRef := uniqueOracleRef(base+"-evidence", used)
		used[evidenceRef] = true
		items = append(items, contract.Item{
			RefID: itemRef, Rank: i + 1, Reason: "evaluation control candidate",
			EvidenceRefIDs: []string{evidenceRef},
		})
		evidence = append(evidence, contract.Evidence{
			RefID: evidenceRef, Path: source.Path, Line: source.StartLine,
			Span: fmt.Sprintf("%d-%d", source.StartLine, source.EndLine),
			Role: "snippet", Snippet: source.Text, TextHash: shape.TextHash(source.Text),
		})
	}
	current.Items = append(items, current.Items...)
	current.Evidence = append(evidence, current.Evidence...)
	current.Limits.TotalAvailable += len(sources)
	if err := contract.ValidateResult(&current); err != nil {
		return contract.Result{}, 0, fmt.Errorf("embedded-model qualification oracle: inject candidates: %w", err)
	}
	return current, len(sources), nil
}

func uniqueOracleRef(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if !used[candidate] {
			return candidate
		}
	}
}

func runOracleCurrentSelector(query string, candidates contract.Result, repository fs.FS) (taskcompact.Result, error) {
	raw, err := contract.SerializeStable(&candidates)
	if err != nil {
		return taskcompact.Result{}, err
	}
	return taskcompact.Build(context.Background(), query, raw, repository, taskcompact.DefaultSourceBudget)
}

func buildOracleSelectorBundle(in OracleInput, kind, outputName, provenance string, injectedRows int, selected taskcompact.Result) (OracleBundle, error) {
	payload, tokens, err := preserveOraclePayload(in.Query.ID, selected, in.RealCounter)
	if err != nil {
		return OracleBundle{}, err
	}
	return OracleBundle{
		ControlKind: kind, QueryID: in.Query.ID, OutputName: outputName,
		CandidateProvenance: provenance, Injected: true, InjectedRows: injectedRows,
		CompleteGrade3Span: qualificationCompleteGrade3Span(in.Query, selected.Structured.Sources),
		TokenCount:         tokens, Payload: payload,
	}, nil
}

func buildOraclePackedBundle(in OracleInput, kind, outputName, provenance string, injected bool, injectedRows int, template taskcompact.Result, eligible []oracleSource) (OracleBundle, error) {
	packed := template
	packed.Summary = "task_context/2 evaluation control " + kind
	packed.Structured.Sources = nil
	packed.Structured.Followup = nil
	packed.Structured.Provenance.SourceSelection = "context-definitions/oracle-" + strings.ReplaceAll(kind, "_", "-") + "/1"
	packed.Structured.Truncated = len(eligible) > 0

	for _, source := range eligible {
		trial := packed
		trial.Structured.Sources = append(append([]taskcompact.Source(nil), packed.Structured.Sources...), source.Source)
		if oracleWhitespaceTokens(trial.Structured.Sources) > taskcompact.DefaultSourceBudget {
			continue
		}
		raw, err := marshalOracleCompactResult(trial)
		if err != nil {
			return OracleBundle{}, err
		}
		tokens, err := in.RealCounter.Count(raw)
		if err != nil {
			return OracleBundle{}, fmt.Errorf("embedded-model qualification oracle: count %s: %w", kind, err)
		}
		if tokens > SavingsCandidateBudget {
			break
		}
		packed = trial
	}
	packed.Structured.Truncated = len(packed.Structured.Sources) < len(eligible)
	payload, tokens, err := preserveOraclePayload(in.Query.ID, packed, in.RealCounter)
	if err != nil {
		return OracleBundle{}, err
	}
	return OracleBundle{
		ControlKind: kind, QueryID: in.Query.ID, OutputName: outputName,
		CandidateProvenance: provenance, Injected: injected, InjectedRows: injectedRows,
		CompleteGrade3Span: qualificationCompleteGrade3Span(in.Query, packed.Structured.Sources),
		TokenCount:         tokens, Payload: payload,
	}, nil
}

func oracleWhitespaceTokens(sources []taskcompact.Source) int {
	total := 0
	for _, source := range sources {
		total += len(strings.Fields(source.Text))
	}
	return total
}

func preserveOraclePayload(queryID string, result taskcompact.Result, real PayloadCounter) (PreservedPayload, int, error) {
	raw, err := marshalOracleCompactResult(result)
	if err != nil {
		return PreservedPayload{}, 0, err
	}
	tokens, err := real.Count(raw)
	if err != nil {
		return PreservedPayload{}, 0, fmt.Errorf("embedded-model qualification oracle: count %s: %w", queryID, err)
	}
	if tokens > SavingsCandidateBudget {
		return PreservedPayload{}, 0, fmt.Errorf("embedded-model qualification oracle: query %s payload is %d tokens, exceeds %d", queryID, tokens, SavingsCandidateBudget)
	}
	if _, err := ValidateCompactCandidateBundleBytes(queryID, raw); err != nil {
		return PreservedPayload{}, 0, fmt.Errorf("embedded-model qualification oracle: query %s compact payload: %w", queryID, err)
	}
	payload, err := preserveCandidatePayload(queryID, raw, real)
	if err != nil {
		return PreservedPayload{}, 0, err
	}
	return payload, tokens, nil
}

func marshalOracleCompactResult(result taskcompact.Result) ([]byte, error) {
	envelope := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int    `json:"id"`
		Result  struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent taskcompact.Structured `json:"structuredContent"`
			IsError           bool                   `json:"isError"`
		} `json:"result"`
	}{JSONRPC: "2.0", ID: 1}
	envelope.Result.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: result.Summary}}
	envelope.Result.StructuredContent = result.Structured
	raw, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("embedded-model qualification oracle: marshal compact payload: %w", err)
	}
	return append(raw, '\n'), nil
}
