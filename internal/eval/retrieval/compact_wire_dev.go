package retrieval

// Development-only compact task_context wire experiment.
//
// This module deliberately does not sit on the production MCP route and does
// not change task_context/2, its 1200-token source budget, or the frozen
// measurement contract. It transforms an already preserved development
// response without consulting judgements. The scorer is a separate seam that
// consults judgements only after the response bytes are final.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

const CompactTaskContextDevVersion = "task_context/compact-dev/3"

// CompactTaskContextDevSource is both the source body and its citation. Source
// order is the read order; removing the separate item/evidence join is the
// largest structural saving while preserving everything an agent needs to
// quote and verify the text.
type CompactTaskContextDevSource struct {
	Path  string `json:"path"`
	Start int    `json:"start_line"`
	End   int    `json:"end_line"`
	Text  string `json:"text"`
}

// CompactTaskContextDevProvenance retains the trust invariants intentionally
// kept by the experiment. InputSHA256 binds all omitted task_context/2 ranking
// and graph metadata to the preserved input. Method identities make the
// retrieval/model/selection implementation auditable. Source order is an
// explicit ranking contract rather than an implicit ref-id join.
type CompactTaskContextDevProvenance struct {
	InputSHA256     string `json:"input_sha256"`
	Method          string `json:"method"`
	Retrieval       string `json:"retrieval"`
	Weights         string `json:"weights"`
	Model           string `json:"model"`
	SourceSelection string `json:"source_selection"`
	SourceOrder     string `json:"source_order"`
	Budget          int    `json:"source_budget"`
	BudgetUnit      string `json:"source_budget_unit"`
}

type CompactTaskContextDevStructured struct {
	Version    string                          `json:"version"`
	Sources    []CompactTaskContextDevSource   `json:"sources"`
	Provenance CompactTaskContextDevProvenance `json:"provenance"`
	Truncated  bool                            `json:"truncated"`
}

type compactTaskContextDevContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type compactTaskContextDevResult struct {
	Content           []compactTaskContextDevContent  `json:"content"`
	StructuredContent CompactTaskContextDevStructured `json:"structuredContent"`
	IsError           bool                            `json:"isError"`
}

type compactTaskContextDevEnvelope struct {
	JSONRPC string                       `json:"jsonrpc"`
	ID      int                          `json:"id"`
	Result  *compactTaskContextDevResult `json:"result"`
}

// BuildCompactTaskContextDev turns one preserved production bundle into a
// single-encoded development response. budget is measured with the same
// whitespace-fields rule as the frozen task_context source budget. Complete
// source lines are admitted by the versioned coherent-region selector; a final
// snippet may be shortened only at a line boundary. No ranking judgement is
// consulted.
func BuildCompactTaskContextDev(query string, input PreservedPayload, budget int, real PayloadCounter) (PreservedPayload, error) {
	return BuildCompactTaskContextDevWithGrepRead(query, input, nil, budget, real)
}

// BuildCompactTaskContextDevWithGrepRead combines the semantic task_context
// bundle with a separately versioned, query-only GrepRead/2 transcript before
// source selection. This is the structural fallback for questions whose
// answer never entered the semantic candidate bytes. The transcript is
// complete before selection and exposes no judgement or target span.
func BuildCompactTaskContextDevWithGrepRead(query string, input PreservedPayload, grepRead *GrepReadV2Transcript, budget int, real PayloadCounter) (PreservedPayload, error) {
	if strings.TrimSpace(query) == "" {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: empty query")
	}
	if budget < 1 || budget > SavingsCandidateBudget {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: source budget %d outside 1..%d", budget, SavingsCandidateBudget)
	}
	if err := validatePayloadCostInput("compact-input", input, real); err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: %w", err)
	}
	bundle, err := taskContextBundleFromCandidateBytes(input.Bytes)
	if err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: %w", err)
	}
	if err := contract.ValidateResult(&bundle); err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: invalid input bundle: %w", err)
	}
	inputSHA := input.SHA256
	if grepRead != nil {
		if err := grepRead.Validate(); err != nil {
			return PreservedPayload{}, fmt.Errorf("compact task_context dev: invalid GrepRead/2 transcript: %w", err)
		}
		if grepRead.Query != query {
			return PreservedPayload{}, fmt.Errorf("compact task_context dev: GrepRead/2 query does not match")
		}
		inputSHA = SHA256Hex([]byte(input.SHA256 + "\n" + grepRead.DigestSHA256()))
	}
	provenance, err := compactTaskContextDevProvenance(bundle.Summary, inputSHA, budget)
	if err != nil {
		return PreservedPayload{}, err
	}

	all := make([]contract.Evidence, 0)
	selectionItems := append([]contract.Item(nil), bundle.Items...)
	seen := make(map[string]bool)
	for _, evidence := range bundle.Evidence {
		if evidence.Snippet == "" {
			continue
		}
		key := evidence.Path + "\x00" + evidence.Span
		if seen[key] {
			continue
		}
		seen[key] = true
		all = append(all, evidence)
	}
	if grepRead != nil {
		additional, err := compactTaskContextDevGrepReadEvidence(*grepRead)
		if err != nil {
			return PreservedPayload{}, err
		}
		for _, evidence := range additional {
			key := evidence.Path + "\x00" + evidence.Span
			if seen[key] {
				continue
			}
			seen[key] = true
			all = append(all, evidence)
			selectionItems = append(selectionItems, contract.Item{RefID: "grepread:" + evidence.RefID, EvidenceRefIDs: []string{evidence.RefID}})
		}
	}
	sources, used, err := compactTaskContextDevSelect(query, all, selectionItems, budget)
	if err != nil {
		return PreservedPayload{}, err
	}
	if len(sources) == 0 {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: budget %d admitted no complete source line", budget)
	}
	summary := fmt.Sprintf("%d ranked source span(s) for %q; read in order (%d/%d source tokens)", len(sources), query, used, budget)
	envelope := compactTaskContextDevEnvelope{
		JSONRPC: "2.0", ID: 1,
		Result: &compactTaskContextDevResult{
			Content: []compactTaskContextDevContent{{Type: "text", Text: summary}},
			StructuredContent: CompactTaskContextDevStructured{
				Version: CompactTaskContextDevVersion, Sources: sources, Provenance: provenance,
				Truncated: len(sources) < len(all) || used < compactTaskContextDevWhitespaceTokens(all),
			},
		},
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: marshal: %w", err)
	}
	raw = append(raw, '\n')
	realTokens, err := real.Count(append([]byte(nil), raw...))
	if err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: tokenize: %w", err)
	}
	return PreservedPayload{
		Sequence: 1, Boundary: PayloadBoundaryCandidate, Operation: CompactTaskContextDevVersion,
		Bytes: raw, SHA256: SHA256Hex(raw), ByteCount: len(raw),
		TokenCounts: []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(raw)))},
			{TokenizerID: real.TokenizerID, VocabularySHA256: real.VocabularySHA256, Tokens: realTokens},
		},
	}, nil
}

func compactTaskContextDevGrepReadEvidence(transcript GrepReadV2Transcript) ([]contract.Evidence, error) {
	makeEvidence := func(refID, path string, start, end int, text string) (contract.Evidence, error) {
		text = strings.TrimSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\r")
		if path == "" || start < 1 || end < start || len(strings.Split(text, "\n")) != end-start+1 {
			return contract.Evidence{}, fmt.Errorf("compact task_context dev: invalid GrepRead/2 source %s:%d-%d", path, start, end)
		}
		return contract.Evidence{
			RefID: "grepread-" + refID, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, end),
			Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
		}, nil
	}

	var out []contract.Evidence
	for index, row := range strings.Split(string(transcript.Ledger.Responses[0].Bytes), "\n") {
		if row == "" || strings.HasPrefix(row, "grep:error:") {
			continue
		}
		fields := strings.SplitN(row, ":", 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf("compact task_context dev: malformed GrepRead/2 grep row")
		}
		line, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("compact task_context dev: malformed GrepRead/2 grep line")
		}
		evidence, err := makeEvidence(fmt.Sprintf("grep-%d", index+1), fields[0], line, line, fields[3])
		if err != nil {
			return nil, err
		}
		out = append(out, evidence)
	}
	for index, read := range transcript.Reads {
		if read.EndLine < read.StartLine {
			continue
		}
		if read.ResponseSequence < 1 || read.ResponseSequence > len(transcript.Ledger.Responses) {
			return nil, fmt.Errorf("compact task_context dev: GrepRead/2 response sequence outside ledger")
		}
		payload := transcript.Ledger.Responses[read.ResponseSequence-1]
		evidence, err := makeEvidence(fmt.Sprintf("read-%d", index+1), read.Path, read.StartLine, read.EndLine, string(payload.Bytes))
		if err != nil {
			return nil, err
		}
		out = append(out, evidence)
	}
	return out, nil
}

type compactTaskContextDevCandidate struct {
	item     contract.Evidence
	lines    []string
	start    int
	anchor   int
	from     int
	to       int
	cost     int
	score    int
	order    int
	fallback bool
}

// compactTaskContextDevSelect retains a small number of coherent regions.
// Ranking considers the whole region and discounts test-name/comment matches
// unless the question explicitly asks about tests. The budget is then split by
// rank before unused quota is returned to the strongest regions. This prevents
// a long tail of one-line anchors from starving the implementation bodies that
// make the response answerable.
func compactTaskContextDevSelect(query string, evidence []contract.Evidence, items []contract.Item, budget int) ([]CompactTaskContextDevSource, int, error) {
	referenced := make(map[string]bool)
	for _, item := range items {
		for _, ref := range item.EvidenceRefIDs {
			referenced[ref] = true
		}
	}
	mode, patterns := grepReadV2QueryPlan(query)
	documentFrequencies := [2]map[string]int{make(map[string]int, len(patterns)), make(map[string]int, len(patterns))}
	for _, item := range evidence {
		channel := 0
		if strings.HasPrefix(item.RefID, "grepread-") {
			channel = 1
		}
		haystack := strings.ToLower(item.Path + "\n" + item.Snippet)
		for _, pattern := range patterns {
			if strings.Contains(haystack, strings.ToLower(pattern)) {
				documentFrequencies[channel][strings.ToLower(pattern)]++
			}
		}
	}
	testIntent := compactTaskContextDevTestIntent(patterns)
	candidates := make([]compactTaskContextDevCandidate, 0, len(evidence))
	for order, item := range evidence {
		if item.ClaimType != "" || item.TextHash != shape.TextHash(item.Snippet) {
			return nil, 0, fmt.Errorf("compact task_context dev: invalid snippet evidence %q", item.RefID)
		}
		start, end, err := exactEvidenceSpan(item.Span)
		if err != nil || item.Line != start || end-start+1 != len(strings.Split(item.Snippet, "\n")) {
			return nil, 0, fmt.Errorf("compact task_context dev: invalid source span %q", item.Span)
		}
		lines := strings.Split(item.Snippet, "\n")
		anchor, lineScore := compactTaskContextDevAnchor(mode, patterns, item.Path, lines)
		fallback := strings.HasPrefix(item.RefID, "grepread-")
		frequencies := documentFrequencies[0]
		if fallback {
			frequencies = documentFrequencies[1]
		}
		score := compactTaskContextDevRegionScore(mode, patterns, frequencies, item, lineScore, testIntent) - order
		if !referenced[item.RefID] {
			// task_context/2's bounded exact-definition bridge deliberately
			// emits its admitted definition as standalone source. It is not
			// an item/ref-id join omission, so retain that query-derived signal.
			if mode == GrepReadV2ExactIdentifier || mode == GrepReadV2ExactPath {
				score += 1_000_000
			} else {
				score += 20_000
			}
		}
		candidates = append(candidates, compactTaskContextDevCandidate{
			item: item, lines: lines, start: start, anchor: anchor,
			from: anchor, to: anchor, score: score, order: order,
			fallback: fallback,
		})
	}
	candidates, err := compactTaskContextDevMergeSameAnchor(candidates)
	if err != nil {
		return nil, 0, err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})

	maxSources := 10
	weights := []int{18, 8, 5, 3, 2, 2, 1, 1, 1, 1}
	if mode == GrepReadV2ExactIdentifier || mode == GrepReadV2ExactPath {
		maxSources = 4
		weights = []int{10, 3, 1, 1}
	} else {
		selected := make([]compactTaskContextDevCandidate, 0, maxSources)
		semantic, fallback := 0, 0
		for _, candidate := range candidates {
			if candidate.fallback {
				if fallback >= 2 {
					continue
				}
				fallback++
			} else {
				if semantic >= 8 {
					continue
				}
				semantic++
			}
			selected = append(selected, candidate)
			if len(selected) == maxSources {
				break
			}
		}
		candidates = selected
	}
	if len(candidates) > maxSources {
		candidates = candidates[:maxSources]
	}

	remaining := budget
	admitted := make([]bool, len(candidates))
	for i := range candidates {
		cost := len(strings.Fields(candidates[i].lines[candidates[i].anchor]))
		if cost == 0 || cost > remaining {
			continue
		}
		admitted[i] = true
		candidates[i].cost = cost
		remaining -= cost
	}
	// A lone comment/signature line is rarely actionable. Give every admitted
	// region one adjacent complete line before weighted depth allocation; this
	// pairs declaration comments with values and signatures with first steps.
	for i := range candidates {
		if admitted[i] && remaining > 0 {
			compactTaskContextDevGrow(&candidates[i], &remaining)
		}
	}
	totalWeight := 0
	for i := range candidates {
		if admitted[i] {
			totalWeight += weights[i]
		}
	}
	for i := range candidates {
		if !admitted[i] {
			continue
		}
		quota := budget * weights[i] / totalWeight
		for candidates[i].cost < quota && remaining > 0 && compactTaskContextDevGrow(&candidates[i], &remaining) {
		}
	}
	// A short declaration or paragraph may not use its quota. Return that space
	// to the strongest regions instead of creating more one-line fragments.
	for remaining > 0 {
		changed := false
		for i := range candidates {
			if admitted[i] && compactTaskContextDevGrow(&candidates[i], &remaining) {
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	out := make([]CompactTaskContextDevSource, 0, len(candidates))
	seenOutput := make(map[string]bool, len(candidates))
	for i, candidate := range candidates {
		if !admitted[i] {
			continue
		}
		text := strings.Join(candidate.lines[candidate.from:candidate.to+1], "\n")
		key := fmt.Sprintf("%s\x00%d\x00%d", candidate.item.Path, candidate.start+candidate.from, candidate.start+candidate.to)
		if seenOutput[key] {
			continue
		}
		seenOutput[key] = true
		out = append(out, CompactTaskContextDevSource{
			Path: candidate.item.Path, Start: candidate.start + candidate.from,
			End: candidate.start + candidate.to, Text: text,
		})
	}
	return out, budget - remaining, nil
}

// Semantic retrieval and GrepRead/2 often land on the same declaration with
// different context windows. Treat that as corroboration of one region, not
// two sources competing for budget. The merge is allowed only at the exact
// same absolute anchor and verifies every overlapping source line.
func compactTaskContextDevMergeSameAnchor(in []compactTaskContextDevCandidate) ([]compactTaskContextDevCandidate, error) {
	byAnchor := make(map[string]int, len(in))
	out := make([]compactTaskContextDevCandidate, 0, len(in))
	for _, candidate := range in {
		absoluteAnchor := candidate.start + candidate.anchor
		key := fmt.Sprintf("%s\x00%d", candidate.item.Path, absoluteAnchor)
		index, ok := byAnchor[key]
		if !ok {
			byAnchor[key] = len(out)
			out = append(out, candidate)
			continue
		}
		prior := &out[index]
		start := min(prior.start, candidate.start)
		end := max(prior.start+len(prior.lines)-1, candidate.start+len(candidate.lines)-1)
		lines := make([]string, end-start+1)
		set := make([]bool, len(lines))
		copyLines := func(source compactTaskContextDevCandidate) error {
			for i, line := range source.lines {
				at := source.start + i - start
				if set[at] && lines[at] != line {
					return fmt.Errorf("compact task_context dev: overlapping source differs at %s:%d", source.item.Path, start+at)
				}
				lines[at], set[at] = line, true
			}
			return nil
		}
		if err := copyLines(*prior); err != nil {
			return nil, err
		}
		if err := copyLines(candidate); err != nil {
			return nil, err
		}
		for i, present := range set {
			if !present {
				return nil, fmt.Errorf("compact task_context dev: source gap at %s:%d", candidate.item.Path, start+i)
			}
		}
		prior.start = start
		prior.lines = lines
		prior.anchor = absoluteAnchor - start
		prior.from, prior.to, prior.cost = prior.anchor, prior.anchor, 0
		prior.score = max(prior.score, candidate.score)
		prior.order = min(prior.order, candidate.order)
		prior.fallback = prior.fallback && candidate.fallback
	}
	return out, nil
}

func compactTaskContextDevRegionScore(mode GrepReadV2Mode, patterns []string, frequencies map[string]int, item contract.Evidence, lineScore int, testIntent bool) int {
	score := lineScore * 1000
	haystack := strings.ToLower(item.Path + "\n" + item.Snippet)
	for _, pattern := range patterns {
		pattern = strings.ToLower(pattern)
		if !strings.Contains(haystack, pattern) {
			continue
		}
		frequency := frequencies[pattern]
		if frequency < 1 {
			frequency = 1
		}
		score += 12_000 / frequency
	}
	lowerPath := strings.ToLower(item.Path)
	isTest := strings.HasSuffix(lowerPath, "_test.go")
	switch {
	case isTest && testIntent:
		score += 5_000
	case isTest:
		score -= 30_000
	case strings.HasSuffix(lowerPath, ".go"):
		score += 30_000
	case strings.HasSuffix(lowerPath, ".md"):
		score += 5_000
	}
	if mode == GrepReadV2ExactPath && strings.EqualFold(item.Path, patterns[0]) {
		score += 1_000_000
	}
	return score
}

func compactTaskContextDevTestIntent(patterns []string) bool {
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, "test") {
			return true
		}
	}
	return false
}

// compactTaskContextDevGrow adds one adjacent complete line. Forward lines are
// preferred because an anchor is normally a declaration or heading; once the
// region reaches its end, preceding documentation/context is added.
func compactTaskContextDevGrow(candidate *compactTaskContextDevCandidate, remaining *int) bool {
	lineIndex := candidate.to + 1
	if lineIndex >= len(candidate.lines) {
		lineIndex = candidate.from - 1
	}
	if lineIndex < 0 || lineIndex >= len(candidate.lines) {
		return false
	}
	cost := len(strings.Fields(candidate.lines[lineIndex]))
	if cost > *remaining {
		return false
	}
	if lineIndex == candidate.to+1 {
		candidate.to = lineIndex
	} else {
		candidate.from = lineIndex
	}
	candidate.cost += cost
	*remaining -= cost
	return true
}

func compactTaskContextDevAnchor(mode GrepReadV2Mode, patterns []string, path string, lines []string) (int, int) {
	bestIndex, bestScore := 0, -1
	lowerPath := strings.ToLower(path)
	for index, line := range lines {
		lower := strings.ToLower(line)
		score := 0
		for _, pattern := range patterns {
			pattern = strings.ToLower(pattern)
			if strings.Contains(lower, pattern) {
				score += 10
			}
			if strings.Contains(lowerPath, pattern) {
				score++
			}
		}
		trimmed := strings.TrimSpace(lower)
		if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "const ") {
			score += 4
		}
		if mode == GrepReadV2ExactPath && index == 0 {
			score += 100
		}
		if score > bestScore {
			bestIndex, bestScore = index, score
		}
	}
	return bestIndex, bestScore
}

func compactTaskContextDevWhitespaceTokens(evidence []contract.Evidence) int {
	total := 0
	for _, item := range evidence {
		total += len(strings.Fields(item.Snippet))
	}
	return total
}

func compactTaskContextDevProvenance(summary, inputSHA string, budget int) (CompactTaskContextDevProvenance, error) {
	if !isLowerHexDigest(inputSHA, 64) {
		return CompactTaskContextDevProvenance{}, fmt.Errorf("compact task_context dev: invalid input digest")
	}
	open, close := strings.LastIndex(summary, " ("), strings.LastIndex(summary, ")")
	if open < 0 || close <= open+2 {
		return CompactTaskContextDevProvenance{}, fmt.Errorf("compact task_context dev: cannot parse input summary provenance")
	}
	fields := strings.Split(summary[open+2:close], "; ")
	if len(fields) < 6 || fields[0] != "task_context/2" {
		return CompactTaskContextDevProvenance{}, fmt.Errorf("compact task_context dev: incomplete input summary provenance")
	}
	value := func(prefix string) string {
		for _, field := range fields {
			if strings.HasPrefix(field, prefix) {
				return strings.TrimPrefix(field, prefix)
			}
		}
		return ""
	}
	p := CompactTaskContextDevProvenance{
		InputSHA256: inputSHA, Method: fields[0], Retrieval: fields[1], Weights: value("weights "),
		Model: compactTaskContextDevModelFingerprint(value("model ")), SourceOrder: "ranked_coherent_regions", Budget: budget,
		BudgetUnit: "whitespace-fields-v1",
	}
	for _, field := range fields {
		if strings.HasPrefix(field, "context-definitions/") {
			p.SourceSelection = field
			break
		}
	}
	if p.Retrieval == "" || p.Weights == "" || p.Model == "" || p.SourceSelection == "" {
		return CompactTaskContextDevProvenance{}, fmt.Errorf("compact task_context dev: incomplete method identity")
	}
	return p, nil
}

// The complete input digest already binds the full model selector. Repeating
// its long implementation identity on every compact response spends source
// capacity without adding an independently checkable invariant.
func compactTaskContextDevModelFingerprint(model string) string {
	if model == "" {
		return ""
	}
	digest := SHA256Hex([]byte(model))
	return "sha256:" + digest[:16]
}

// ParseCompactTaskContextDev validates the exact wire interface. Unknown
// fields fail closed: changing the representation requires a new explicit
// version instead of silently changing the meaning of compact-dev/3.
func ParseCompactTaskContextDev(raw []byte) (string, CompactTaskContextDevStructured, error) {
	var envelope compactTaskContextDevEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: malformed response: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: trailing JSON value")
	} else if err != io.EOF {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: malformed trailing bytes: %w", err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != 1 || envelope.Result == nil || envelope.Result.IsError || len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" || strings.TrimSpace(envelope.Result.Content[0].Text) == "" {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: invalid MCP envelope")
	}
	structured := envelope.Result.StructuredContent
	if structured.Version != CompactTaskContextDevVersion || len(structured.Sources) == 0 {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: invalid structured content identity")
	}
	p := structured.Provenance
	if !isLowerHexDigest(p.InputSHA256, 64) || p.Method != "task_context/2" || !strings.HasPrefix(p.Retrieval, "retrieval/") || p.Weights == "" || !strings.HasPrefix(p.Model, "sha256:") || !isLowerHexDigest(strings.TrimPrefix(p.Model, "sha256:"), 16) || !strings.HasPrefix(p.SourceSelection, "context-definitions/") || p.SourceOrder != "ranked_coherent_regions" || p.Budget < 1 || p.Budget > SavingsCandidateBudget || p.BudgetUnit != "whitespace-fields-v1" {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: invalid provenance")
	}
	seen := make(map[string]bool)
	used := 0
	for _, source := range structured.Sources {
		key := source.Path + "\x00" + strconv.Itoa(source.Start) + "\x00" + strconv.Itoa(source.End)
		if !fs.ValidPath(source.Path) || source.Start < 1 || source.End < source.Start || source.Text == "" || source.End-source.Start+1 != len(strings.Split(source.Text, "\n")) || seen[key] {
			return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: invalid or duplicate source span")
		}
		seen[key] = true
		used += len(strings.Fields(source.Text))
	}
	if used > p.Budget {
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: source budget exceeded")
	}
	return envelope.Result.Content[0].Text, structured, nil
}

// CompactTaskContextDevScore keeps the contract's atomic-overlap result
// separate from the stricter full-span diagnostic. FullSpanCount is not a
// release scorer; it catches bundles that technically touch an answer but omit
// most of the reviewed implementation.
type CompactTaskContextDevScore struct {
	Reached       bool
	Tokens        int
	OverlapSpans  int
	FullSpanCount int
	FirstOverlap  int
}

// ScoreCompactTaskContextDev applies the same atomic overlap target as the
// development equal-recall scorer, after validating every emitted byte against
// the pinned repository.
func ScoreCompactTaskContextDev(repository fs.FS, q Query, raw []byte, target RecallTarget, real PayloadCounter) (CompactTaskContextDevScore, error) {
	if repository == nil || q.Split != SplitDev || q.Stratum == "" || q.Stratum == StratumNoHit {
		return CompactTaskContextDevScore{}, fmt.Errorf("compact task_context dev: invalid development scoring input")
	}
	if err := validateRecallTarget(target); err != nil {
		return CompactTaskContextDevScore{}, err
	}
	_, structured, err := ParseCompactTaskContextDev(raw)
	if err != nil {
		return CompactTaskContextDevScore{}, err
	}
	grade3 := make([]Judgement, 0)
	for _, judgement := range q.Judgements {
		if judgement.Grade == GradeMax {
			grade3 = append(grade3, judgement)
		}
	}
	if len(grade3) != target.TotalSpans {
		return CompactTaskContextDevScore{}, fmt.Errorf("compact task_context dev: target span count drift")
	}
	coveredLines := make([]map[int]bool, len(grade3))
	for i := range coveredLines {
		coveredLines[i] = make(map[int]bool)
	}
	for _, source := range structured.Sources {
		want, err := exactSourceSpan(repository, source.Path, source.Start, source.End)
		if err != nil || want != source.Text {
			return CompactTaskContextDevScore{}, fmt.Errorf("compact task_context dev: source differs from %s:%d-%d", source.Path, source.Start, source.End)
		}
		for i, judgement := range grade3 {
			if source.Path == judgement.Path && source.End >= judgement.StartLine && source.Start <= judgement.EndLine {
				from := max(source.Start, judgement.StartLine)
				to := min(source.End, judgement.EndLine)
				for line := from; line <= to; line++ {
					coveredLines[i][line] = true
				}
			}
		}
	}
	tokens, err := real.Count(append([]byte(nil), raw...))
	if err != nil {
		return CompactTaskContextDevScore{}, err
	}
	score := CompactTaskContextDevScore{Tokens: tokens}
	for sourceIndex, source := range structured.Sources {
		for _, judgement := range grade3 {
			if source.Path == judgement.Path && source.End >= judgement.StartLine && source.Start <= judgement.EndLine {
				score.FirstOverlap = sourceIndex + 1
				break
			}
		}
		if score.FirstOverlap > 0 {
			break
		}
	}
	for i, judgement := range grade3 {
		if len(coveredLines[i]) > 0 {
			score.OverlapSpans++
		}
		if len(coveredLines[i]) == judgement.EndLine-judgement.StartLine+1 {
			score.FullSpanCount++
		}
	}
	score.Reached = score.OverlapSpans >= target.RequiredSpans
	return score, nil
}

// compactTaskContextDevMedian returns the conventional midpoint for an even
// population and is kept here so the report cannot accidentally use an upper
// order statistic.
func compactTaskContextDevMedian(values []int) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int(nil), values...)
	sort.Ints(copyValues)
	mid := len(copyValues) / 2
	if len(copyValues)%2 == 1 {
		return float64(copyValues[mid])
	}
	return float64(copyValues[mid-1]+copyValues[mid]) / 2
}
