package retrieval

// Development-only cost attribution for preserved task_context/2 responses.
//
// Tokenization is not additive across independently tokenized byte slices. The
// report therefore uses an explicit ordered-marginal model which reconciles to
// the complete response exactly:
//
//   empty MCP text -> JSON structure -> metadata -> summary -> source snippets
//
// Each stage is a complete valid JSON-RPC response. A category's token cost is
// the executable tokenizer count at that stage minus the preceding stage. The
// order is part of PayloadCostAttributionVersion; these diagnostics are not a
// release estimand and do not alter the frozen measurement contract.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

const (
	PayloadCostAttributionVersion = "dev-task-context-payload-cost/1"
	payloadCostDevDatasetSHA256   = "2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c"
	payloadCostDevQueries         = 44
)

const (
	payloadCostEnvelope  = "envelope"
	payloadCostStructure = "json_structure_and_escaping"
	payloadCostMetadata  = "provenance_and_metadata"
	payloadCostSummary   = "summary"
	payloadCostSource    = "source_and_evidence_text"
)

var payloadCostOrder = []string{
	payloadCostEnvelope,
	payloadCostStructure,
	payloadCostMetadata,
	payloadCostSummary,
	payloadCostSource,
}

// PayloadCostCategory is one exact ordered marginal. ValueBytes counts the
// decoded scalar values belonging to the category. WireBytes includes JSON
// quoting/escaping and token/byte interactions introduced at this stage.
type PayloadCostCategory struct {
	Name                   string  `json:"name"`
	ValueBytes             int     `json:"decoded_value_bytes"`
	WireBytes              int     `json:"ordered_marginal_wire_bytes"`
	Tokens                 int     `json:"ordered_marginal_tokens"`
	IsolatedValueTokens    *int    `json:"isolated_value_tokens,omitempty"`
	TokenInteraction       *int    `json:"token_interaction_vs_isolated,omitempty"`
	WireExpansionOverValue int     `json:"wire_bytes_minus_decoded_values"`
	ShareFullBytes         float64 `json:"share_of_full_bytes,omitempty"`
	ShareFullTokens        float64 `json:"share_of_full_tokens,omitempty"`
}

// PayloadCostObservation attributes one preserved response without changing
// or re-marshalling the measured full payload.
type PayloadCostObservation struct {
	QueryID       string                `json:"query_id"`
	PayloadSHA256 string                `json:"payload_sha256"`
	FullBytes     int                   `json:"full_bytes"`
	FullTokens    int                   `json:"full_tokens"`
	Categories    []PayloadCostCategory `json:"categories"`
}

// PayloadCostAggregate totals the exact per-query marginals. Totals, rather
// than rounded means, are the authoritative values.
type PayloadCostAggregate struct {
	Queries    int                   `json:"queries"`
	FullBytes  int                   `json:"full_bytes"`
	FullTokens int                   `json:"full_tokens"`
	MeanBytes  float64               `json:"mean_bytes"`
	MeanTokens float64               `json:"mean_tokens"`
	Categories []PayloadCostCategory `json:"categories"`
}

// PayloadCostReport is deliberately bound to the committed development
// capture. It refuses a different dataset, including the sealed holdout.
type PayloadCostReport struct {
	Version                   string                   `json:"version"`
	Scope                     string                   `json:"scope"`
	InputSHA256               string                   `json:"input_sha256"`
	DatasetSHA256             string                   `json:"dataset_sha256"`
	TokenizerID               string                   `json:"tokenizer_id"`
	TokenizerVocabularySHA256 string                   `json:"tokenizer_vocabulary_sha256"`
	AttributionOrder          []string                 `json:"attribution_order"`
	ByteAccounting            string                   `json:"byte_accounting"`
	TokenAccounting           string                   `json:"token_accounting"`
	IndependentBuildsVerified int                      `json:"independent_builds_verified"`
	Observations              []PayloadCostObservation `json:"observations"`
	Aggregate                 PayloadCostAggregate     `json:"aggregate"`
}

// BuildDevPayloadCostReport validates both independent captures and produces
// one observation per development query. The second build is evidence of byte
// reproducibility, not a second sample, and must match the first exactly.
func BuildDevPayloadCostReport(artifactRaw []byte, real PayloadCounter) (PayloadCostReport, error) {
	var artifact struct {
		DatasetSHA256     string `json:"dataset_sha256"`
		Queries           int    `json:"queries"`
		IndependentBuilds [][]struct {
			QueryID string `json:"query_id"`
			Capture struct {
				QueryID string           `json:"query_id"`
				Payload PreservedPayload `json:"payload"`
			} `json:"capture"`
		} `json:"independent_builds"`
	}
	if len(artifactRaw) == 0 {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: empty capture artifact")
	}
	dec := json.NewDecoder(bytes.NewReader(artifactRaw))
	if err := dec.Decode(&artifact); err != nil {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: malformed capture artifact: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: trailing JSON value")
	} else if err != io.EOF {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: malformed trailing bytes: %w", err)
	}
	if artifact.DatasetSHA256 != payloadCostDevDatasetSHA256 {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: dataset sha256=%q, want the committed development-only slice %s", artifact.DatasetSHA256, payloadCostDevDatasetSHA256)
	}
	if artifact.Queries != payloadCostDevQueries {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: artifact declares %d development queries, want %d", artifact.Queries, payloadCostDevQueries)
	}
	if real.TokenizerID != evaltokenizer.TokenizerID || real.VocabularySHA256 != evaltokenizer.PinnedVocabularySHA256 || real.Count == nil {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: executable pinned tokenizer is required")
	}
	if len(artifact.IndependentBuilds) != 2 || len(artifact.IndependentBuilds[0]) != payloadCostDevQueries || len(artifact.IndependentBuilds[1]) != payloadCostDevQueries {
		return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: want two complete %d-query independent development builds", payloadCostDevQueries)
	}
	first := make(map[string]PreservedPayload, len(artifact.IndependentBuilds[0]))
	for build, rows := range artifact.IndependentBuilds {
		seen := make(map[string]bool, len(rows))
		for _, row := range rows {
			if row.QueryID == "" || row.Capture.QueryID != row.QueryID || seen[row.QueryID] {
				return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: build %d has invalid or duplicate query identity %q", build+1, row.QueryID)
			}
			seen[row.QueryID] = true
			if err := validatePayloadCostInput(row.QueryID, row.Capture.Payload, real); err != nil {
				return PayloadCostReport{}, err
			}
			if build == 0 {
				first[row.QueryID] = row.Capture.Payload
				continue
			}
			prior, ok := first[row.QueryID]
			if !ok || !bytes.Equal(prior.Bytes, row.Capture.Payload.Bytes) {
				return PayloadCostReport{}, fmt.Errorf("payload cost diagnostic: query %s differs across independent builds", row.QueryID)
			}
		}
	}

	ids := make([]string, 0, len(first))
	for id := range first {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	report := PayloadCostReport{
		Version:                   PayloadCostAttributionVersion,
		Scope:                     "development_only",
		InputSHA256:               SHA256Hex(artifactRaw),
		DatasetSHA256:             artifact.DatasetSHA256,
		TokenizerID:               real.TokenizerID,
		TokenizerVocabularySHA256: real.VocabularySHA256,
		AttributionOrder:          append([]string(nil), payloadCostOrder...),
		ByteAccounting:            "exact ordered marginals; decoded_value_bytes separately exposes semantic text and wire expansion",
		TokenAccounting:           "exact ordered marginals over complete valid responses; order is material because BPE tokenization is non-additive",
		IndependentBuildsVerified: 2,
	}
	for _, id := range ids {
		obs, err := AttributeTaskContextPayload(id, first[id], real)
		if err != nil {
			return PayloadCostReport{}, err
		}
		report.Observations = append(report.Observations, obs)
	}
	report.Aggregate = aggregatePayloadCosts(report.Observations)
	return report, nil
}

func validatePayloadCostInput(queryID string, payload PreservedPayload, real PayloadCounter) error {
	if payload.Sequence != 1 || payload.Boundary != PayloadBoundaryCandidate || payload.Operation != PayloadOperationTaskContext || payload.Bytes == nil {
		return fmt.Errorf("payload cost diagnostic: query %s is not one preserved task_context/2 MCP response", queryID)
	}
	if payload.SHA256 != SHA256Hex(payload.Bytes) || payload.ByteCount != len(payload.Bytes) {
		return fmt.Errorf("payload cost diagnostic: query %s payload digest or byte count drift", queryID)
	}
	if _, err := ValidateCandidateBundleBytes(queryID, payload.Bytes); err != nil {
		return err
	}
	gotTokens, err := real.Count(append([]byte(nil), payload.Bytes...))
	if err != nil {
		return fmt.Errorf("payload cost diagnostic: query %s tokenizer: %w", queryID, err)
	}
	foundReal, foundWhitespace := false, false
	for _, count := range payload.TokenCounts {
		switch count.TokenizerID {
		case real.TokenizerID:
			foundReal = true
			if count.VocabularySHA256 != real.VocabularySHA256 || count.Tokens != gotTokens {
				return fmt.Errorf("payload cost diagnostic: query %s pinned tokenizer count or vocabulary digest drift", queryID)
			}
		case TokenizerID:
			foundWhitespace = true
			if count.Tokens != len(strings.Fields(string(payload.Bytes))) {
				return fmt.Errorf("payload cost diagnostic: query %s whitespace token count drift", queryID)
			}
		}
	}
	if !foundReal || !foundWhitespace || len(payload.TokenCounts) != 2 {
		return fmt.Errorf("payload cost diagnostic: query %s does not carry exactly the two required token counts", queryID)
	}
	return nil
}

// AttributeTaskContextPayload implements the exact ordered-marginal model for
// one already validated payload.
func AttributeTaskContextPayload(queryID string, payload PreservedPayload, real PayloadCounter) (PayloadCostObservation, error) {
	if err := validatePayloadCostInput(queryID, payload, real); err != nil {
		return PayloadCostObservation{}, err
	}
	root, err := parseJSONTree(payload.Bytes)
	if err != nil {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s outer JSON: %w", queryID, err)
	}
	textNode, err := jsonNodeAt(root, "result", "content", 0, "text")
	if err != nil || textNode.kind != jsonString {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s cannot uniquely locate result.content[0].text", queryID)
	}
	inner := []byte(textNode.text)
	innerRoot, err := parseJSONTree(inner)
	if err != nil {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s inner JSON: %w", queryID, err)
	}
	scalars := collectPayloadScalars(innerRoot)
	outerPrefix := payload.Bytes[:textNode.start]
	outerSuffix := payload.Bytes[textNode.end:]
	originalLexeme, err := marshalJSONStringForTransport(string(inner))
	if err != nil {
		return PayloadCostObservation{}, err
	}
	if !bytes.Equal(originalLexeme, payload.Bytes[textNode.start:textNode.end]) {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s inner text lexeme is not the encoder's canonical JSON string", queryID)
	}

	makeStage := func(include map[payloadScalarClass]bool) ([]byte, error) {
		masked, err := maskPayloadScalars(inner, scalars, include)
		if err != nil {
			return nil, err
		}
		lexeme, err := marshalJSONStringForTransport(string(masked))
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(outerPrefix)+len(lexeme)+len(outerSuffix))
		out = append(out, outerPrefix...)
		out = append(out, lexeme...)
		out = append(out, outerSuffix...)
		return out, nil
	}
	emptyOuter := append(append(append([]byte(nil), outerPrefix...), '"', '"'), outerSuffix...)
	stages := [][]byte{emptyOuter}
	for _, include := range []map[payloadScalarClass]bool{
		{},
		{payloadScalarMetadata: true},
		{payloadScalarMetadata: true, payloadScalarSummary: true},
		{payloadScalarMetadata: true, payloadScalarSummary: true, payloadScalarSource: true},
	} {
		stage, err := makeStage(include)
		if err != nil {
			return PayloadCostObservation{}, err
		}
		stages = append(stages, stage)
	}
	if !bytes.Equal(stages[len(stages)-1], payload.Bytes) {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s attribution stages do not reconstruct the preserved response", queryID)
	}
	counts := make([]int, len(stages))
	for i, stage := range stages {
		counts[i], err = real.Count(append([]byte(nil), stage...))
		if err != nil {
			return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s stage %d tokenizer: %w", queryID, i, err)
		}
	}
	valueBytes, isolatedTokens, err := payloadScalarTotals(scalars, real)
	if err != nil {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s isolated values: %w", queryID, err)
	}
	categories := make([]PayloadCostCategory, 0, len(payloadCostOrder))
	for i, name := range payloadCostOrder {
		wireBytes, tokens := len(stages[i]), counts[i]
		if i > 0 {
			wireBytes -= len(stages[i-1])
			tokens -= counts[i-1]
		}
		class := payloadScalarNone
		switch name {
		case payloadCostMetadata:
			class = payloadScalarMetadata
		case payloadCostSummary:
			class = payloadScalarSummary
		case payloadCostSource:
			class = payloadScalarSource
		}
		category := PayloadCostCategory{Name: name, WireBytes: wireBytes, Tokens: tokens}
		if class != payloadScalarNone {
			category.ValueBytes = valueBytes[class]
			isolated := isolatedTokens[class]
			interaction := tokens - isolated
			category.IsolatedValueTokens = &isolated
			category.TokenInteraction = &interaction
		}
		category.WireExpansionOverValue = wireBytes - category.ValueBytes
		categories = append(categories, category)
	}
	obs := PayloadCostObservation{QueryID: queryID, PayloadSHA256: payload.SHA256, FullBytes: len(payload.Bytes), FullTokens: counts[len(counts)-1], Categories: categories}
	if err := validatePayloadCostObservation(obs); err != nil {
		return PayloadCostObservation{}, fmt.Errorf("payload cost diagnostic: query %s: %w", queryID, err)
	}
	return obs, nil
}

func marshalJSONStringForTransport(value string) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}

func aggregatePayloadCosts(observations []PayloadCostObservation) PayloadCostAggregate {
	agg := PayloadCostAggregate{Queries: len(observations), Categories: make([]PayloadCostCategory, len(payloadCostOrder))}
	for i, name := range payloadCostOrder {
		agg.Categories[i].Name = name
		if name == payloadCostMetadata || name == payloadCostSummary || name == payloadCostSource {
			zeroIsolated, zeroInteraction := 0, 0
			agg.Categories[i].IsolatedValueTokens = &zeroIsolated
			agg.Categories[i].TokenInteraction = &zeroInteraction
		}
	}
	for _, obs := range observations {
		agg.FullBytes += obs.FullBytes
		agg.FullTokens += obs.FullTokens
		for i, category := range obs.Categories {
			a := &agg.Categories[i]
			a.ValueBytes += category.ValueBytes
			a.WireBytes += category.WireBytes
			a.Tokens += category.Tokens
			if category.IsolatedValueTokens != nil {
				*a.IsolatedValueTokens += *category.IsolatedValueTokens
				*a.TokenInteraction += *category.TokenInteraction
			}
			a.WireExpansionOverValue += category.WireExpansionOverValue
		}
	}
	if agg.Queries > 0 {
		agg.MeanBytes = float64(agg.FullBytes) / float64(agg.Queries)
		agg.MeanTokens = float64(agg.FullTokens) / float64(agg.Queries)
		for i := range agg.Categories {
			agg.Categories[i].ShareFullBytes = float64(agg.Categories[i].WireBytes) / float64(agg.FullBytes)
			agg.Categories[i].ShareFullTokens = float64(agg.Categories[i].Tokens) / float64(agg.FullTokens)
		}
	}
	return agg
}

func validatePayloadCostObservation(obs PayloadCostObservation) error {
	if len(obs.Categories) != len(payloadCostOrder) {
		return fmt.Errorf("has %d categories, want %d", len(obs.Categories), len(payloadCostOrder))
	}
	bytesTotal, tokenTotal := 0, 0
	for i, category := range obs.Categories {
		if category.Name != payloadCostOrder[i] {
			return fmt.Errorf("category %d=%q, want %q", i, category.Name, payloadCostOrder[i])
		}
		bytesTotal += category.WireBytes
		tokenTotal += category.Tokens
	}
	if bytesTotal != obs.FullBytes || tokenTotal != obs.FullTokens {
		return fmt.Errorf("ordered marginals do not reconcile: bytes=%d/%d tokens=%d/%d", bytesTotal, obs.FullBytes, tokenTotal, obs.FullTokens)
	}
	return nil
}

type payloadScalarClass uint8

const (
	payloadScalarNone payloadScalarClass = iota
	payloadScalarMetadata
	payloadScalarSummary
	payloadScalarSource
)

type payloadScalar struct {
	start int
	end   int
	kind  jsonKind
	text  string
	class payloadScalarClass
}

func collectPayloadScalars(root *jsonTreeNode) []payloadScalar {
	var out []payloadScalar
	var walk func(*jsonTreeNode, []string)
	walk = func(node *jsonTreeNode, path []string) {
		switch node.kind {
		case jsonObject:
			for _, member := range node.members {
				walk(member.value, append(path, member.key))
			}
		case jsonArray:
			for i, child := range node.elements {
				walk(child, append(path, fmt.Sprintf("[%d]", i)))
			}
		default:
			class := payloadScalarMetadata
			if len(path) == 1 && path[0] == "summary" {
				class = payloadScalarSummary
			} else if len(path) == 3 && path[0] == "evidence" && strings.HasPrefix(path[1], "[") && path[2] == "snippet" {
				class = payloadScalarSource
			}
			out = append(out, payloadScalar{start: node.start, end: node.end, kind: node.kind, text: node.text, class: class})
		}
	}
	walk(root, nil)
	return out
}

func maskPayloadScalars(raw []byte, scalars []payloadScalar, include map[payloadScalarClass]bool) ([]byte, error) {
	out := make([]byte, 0, len(raw))
	last := 0
	for _, scalar := range scalars {
		if scalar.start < last || scalar.end > len(raw) {
			return nil, fmt.Errorf("overlapping or invalid scalar ranges")
		}
		out = append(out, raw[last:scalar.start]...)
		if include[scalar.class] {
			out = append(out, raw[scalar.start:scalar.end]...)
		} else {
			switch scalar.kind {
			case jsonString:
				out = append(out, '"', '"')
			case jsonNumber:
				out = append(out, '0')
			case jsonBool:
				out = append(out, "false"...)
			case jsonNull:
				out = append(out, "null"...)
			default:
				return nil, fmt.Errorf("non-scalar attribution leaf")
			}
		}
		last = scalar.end
	}
	out = append(out, raw[last:]...)
	if !json.Valid(out) {
		return nil, fmt.Errorf("masked stage is not valid JSON")
	}
	return out, nil
}

func payloadScalarTotals(scalars []payloadScalar, real PayloadCounter) (map[payloadScalarClass]int, map[payloadScalarClass]int, error) {
	byteTotals := make(map[payloadScalarClass]int)
	tokenTotals := make(map[payloadScalarClass]int)
	for _, scalar := range scalars {
		var value []byte
		if scalar.kind == jsonString {
			value = []byte(scalar.text)
		} else {
			value = []byte(scalar.text)
		}
		byteTotals[scalar.class] += len(value)
		count, err := real.Count(value)
		if err != nil {
			return nil, nil, err
		}
		tokenTotals[scalar.class] += count
	}
	return byteTotals, tokenTotals, nil
}

// MarshalPayloadCostReport emits stable indented JSON with a terminating LF.
func MarshalPayloadCostReport(report PayloadCostReport) ([]byte, error) {
	if report.Version != PayloadCostAttributionVersion || report.Scope != "development_only" || report.DatasetSHA256 != payloadCostDevDatasetSHA256 || report.IndependentBuildsVerified != 2 {
		return nil, fmt.Errorf("payload cost diagnostic: report identity is invalid")
	}
	if report.TokenizerID != evaltokenizer.TokenizerID || report.TokenizerVocabularySHA256 != evaltokenizer.PinnedVocabularySHA256 || !isLowerHexDigest(report.InputSHA256, 64) || !reflect.DeepEqual(report.AttributionOrder, payloadCostOrder) {
		return nil, fmt.Errorf("payload cost diagnostic: report input, tokenizer, or attribution identity is invalid")
	}
	previous := ""
	for _, obs := range report.Observations {
		if obs.QueryID <= previous {
			return nil, fmt.Errorf("payload cost diagnostic: observations are not uniquely sorted")
		}
		previous = obs.QueryID
		if err := validatePayloadCostObservation(obs); err != nil {
			return nil, fmt.Errorf("payload cost diagnostic: query %s: %w", obs.QueryID, err)
		}
	}
	if len(report.Observations) != payloadCostDevQueries || !reflect.DeepEqual(report.Aggregate, aggregatePayloadCosts(report.Observations)) {
		return nil, fmt.Errorf("payload cost diagnostic: aggregate does not exactly reproduce the complete development observations")
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

type jsonKind uint8

const (
	jsonObject jsonKind = iota + 1
	jsonArray
	jsonString
	jsonNumber
	jsonBool
	jsonNull
)

type jsonMember struct {
	key   string
	value *jsonTreeNode
}

type jsonTreeNode struct {
	kind     jsonKind
	start    int
	end      int
	text     string
	members  []jsonMember
	elements []*jsonTreeNode
}

type rawJSONParser struct {
	raw []byte
	pos int
}

func parseJSONTree(raw []byte) (*jsonTreeNode, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	p := &rawJSONParser{raw: raw}
	node, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.pos != len(raw) {
		return nil, fmt.Errorf("trailing bytes at offset %d", p.pos)
	}
	return node, nil
}

func (p *rawJSONParser) value() (*jsonTreeNode, error) {
	p.space()
	if p.pos >= len(p.raw) {
		return nil, fmt.Errorf("missing value")
	}
	switch p.raw[p.pos] {
	case '{':
		return p.object()
	case '[':
		return p.array()
	case '"':
		return p.stringNode()
	case 't', 'f':
		return p.literal(jsonBool)
	case 'n':
		return p.literal(jsonNull)
	default:
		return p.literal(jsonNumber)
	}
}

func (p *rawJSONParser) object() (*jsonTreeNode, error) {
	node := &jsonTreeNode{kind: jsonObject, start: p.pos}
	p.pos++
	p.space()
	seen := make(map[string]bool)
	if p.take('}') {
		node.end = p.pos
		return node, nil
	}
	for {
		key, err := p.stringNode()
		if err != nil || key.kind != jsonString {
			return nil, fmt.Errorf("object key at offset %d", p.pos)
		}
		if seen[key.text] {
			return nil, fmt.Errorf("duplicate object key %q", key.text)
		}
		seen[key.text] = true
		p.space()
		if !p.take(':') {
			return nil, fmt.Errorf("missing colon at offset %d", p.pos)
		}
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		node.members = append(node.members, jsonMember{key: key.text, value: value})
		p.space()
		if p.take('}') {
			node.end = p.pos
			return node, nil
		}
		if !p.take(',') {
			return nil, fmt.Errorf("missing comma at offset %d", p.pos)
		}
		p.space()
	}
}

func (p *rawJSONParser) array() (*jsonTreeNode, error) {
	node := &jsonTreeNode{kind: jsonArray, start: p.pos}
	p.pos++
	p.space()
	if p.take(']') {
		node.end = p.pos
		return node, nil
	}
	for {
		value, err := p.value()
		if err != nil {
			return nil, err
		}
		node.elements = append(node.elements, value)
		p.space()
		if p.take(']') {
			node.end = p.pos
			return node, nil
		}
		if !p.take(',') {
			return nil, fmt.Errorf("missing comma at offset %d", p.pos)
		}
		p.space()
	}
}

func (p *rawJSONParser) stringNode() (*jsonTreeNode, error) {
	start := p.pos
	if !p.take('"') {
		return nil, fmt.Errorf("missing string at offset %d", p.pos)
	}
	escaped := false
	for p.pos < len(p.raw) {
		b := p.raw[p.pos]
		p.pos++
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' {
			escaped = true
			continue
		}
		if b == '"' {
			var text string
			if err := json.Unmarshal(p.raw[start:p.pos], &text); err != nil {
				return nil, err
			}
			return &jsonTreeNode{kind: jsonString, start: start, end: p.pos, text: text}, nil
		}
	}
	return nil, fmt.Errorf("unterminated string at offset %d", start)
}

func (p *rawJSONParser) literal(kind jsonKind) (*jsonTreeNode, error) {
	start := p.pos
	for p.pos < len(p.raw) && !bytes.ContainsRune([]byte(" \t\r\n,]}"), rune(p.raw[p.pos])) {
		p.pos++
	}
	return &jsonTreeNode{kind: kind, start: start, end: p.pos, text: string(p.raw[start:p.pos])}, nil
}

func (p *rawJSONParser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}

func (p *rawJSONParser) take(want byte) bool {
	if p.pos < len(p.raw) && p.raw[p.pos] == want {
		p.pos++
		return true
	}
	return false
}

func jsonNodeAt(root *jsonTreeNode, objectKey, nestedKey string, nestedIndex int, finalKey string) (*jsonTreeNode, error) {
	node, err := objectMember(root, objectKey)
	if err != nil {
		return nil, err
	}
	node, err = objectMember(node, nestedKey)
	if err != nil || node.kind != jsonArray || nestedIndex < 0 || len(node.elements) <= nestedIndex {
		return nil, fmt.Errorf("invalid array path")
	}
	return objectMember(node.elements[nestedIndex], finalKey)
}

func objectMember(node *jsonTreeNode, key string) (*jsonTreeNode, error) {
	if node == nil || node.kind != jsonObject {
		return nil, fmt.Errorf("%q parent is not an object", key)
	}
	for _, member := range node.members {
		if member.key == key {
			return member.value, nil
		}
	}
	return nil, fmt.Errorf("missing key %q", key)
}
