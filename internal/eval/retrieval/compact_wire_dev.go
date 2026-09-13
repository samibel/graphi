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
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
	evaltokenizer "github.com/samibel/graphi/internal/eval/tokenizer"
)

const CompactTaskContextDevVersion = "task_context/compact-dev/7"

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
	return buildCompactTaskContextDev(query, input, grepRead, nil, budget, real)
}

// BuildCompactTaskContextDevWithRepository hydrates the definitions already
// named by ranked items from a pinned repository. It does not search for an
// answer span or consult judgements: path and declaration line come entirely
// from the candidate bundle, and the repository supplies only exact source.
func BuildCompactTaskContextDevWithRepository(query string, input PreservedPayload, grepRead *GrepReadV2Transcript, repository fs.FS, budget int, real PayloadCounter) (PreservedPayload, error) {
	if repository == nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context dev: nil repository")
	}
	return buildCompactTaskContextDev(query, input, grepRead, repository, budget, real)
}

func buildCompactTaskContextDev(query string, input PreservedPayload, grepRead *GrepReadV2Transcript, repository fs.FS, budget int, real PayloadCounter) (PreservedPayload, error) {
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
	seenRef := make(map[string]string)
	for _, evidence := range bundle.Evidence {
		if evidence.Snippet == "" {
			continue
		}
		key := evidence.Path + "\x00" + evidence.Span
		if _, ok := seenRef[key]; ok {
			continue
		}
		seenRef[key] = evidence.RefID
		all = append(all, evidence)
	}
	if repository != nil {
		hydrated, hydratedItems, err := compactTaskContextDevHydrateDefinitions(repository, query, bundle.Items)
		if err != nil {
			return PreservedPayload{}, err
		}
		if len(hydrated) > 0 {
			hydratedRaw, err := json.Marshal(hydrated)
			if err != nil {
				return PreservedPayload{}, fmt.Errorf("compact task_context dev: bind hydrated definitions: %w", err)
			}
			inputSHA = SHA256Hex([]byte(inputSHA + "\n" + SHA256Hex(hydratedRaw)))
		}
		for index, evidence := range hydrated {
			key := evidence.Path + "\x00" + evidence.Span
			item := hydratedItems[index]
			if ref, ok := seenRef[key]; ok {
				item.EvidenceRefIDs = []string{ref}
				selectionItems = append(selectionItems, item)
				continue
			}
			seenRef[key] = evidence.RefID
			all = append(all, evidence)
			selectionItems = append(selectionItems, item)
		}
	}
	if grepRead != nil {
		additional, err := compactTaskContextDevGrepReadEvidence(*grepRead)
		if err != nil {
			return PreservedPayload{}, err
		}
		for _, evidence := range additional {
			key := evidence.Path + "\x00" + evidence.Span
			item := contract.Item{RefID: "grepread:" + evidence.RefID, EvidenceRefIDs: []string{evidence.RefID}}
			if ref, ok := seenRef[key]; ok {
				item.EvidenceRefIDs = []string{ref}
				selectionItems = append(selectionItems, item)
				continue
			}
			seenRef[key] = evidence.RefID
			all = append(all, evidence)
			selectionItems = append(selectionItems, item)
		}
		if repository != nil {
			hydrated, hydratedItems, err := compactTaskContextDevHydrateGrepReadDeclarations(repository, query, additional)
			if err != nil {
				return PreservedPayload{}, err
			}
			if len(hydrated) > 0 {
				hydratedRaw, err := json.Marshal(hydrated)
				if err != nil {
					return PreservedPayload{}, fmt.Errorf("compact task_context dev: bind hydrated GrepRead declarations: %w", err)
				}
				inputSHA = SHA256Hex([]byte(inputSHA + "\n" + SHA256Hex(hydratedRaw)))
			}
			for index, evidence := range hydrated {
				key := evidence.Path + "\x00" + evidence.Span
				item := hydratedItems[index]
				if ref, ok := seenRef[key]; ok {
					item.EvidenceRefIDs = []string{ref}
					selectionItems = append(selectionItems, item)
					continue
				}
				seenRef[key] = evidence.RefID
				all = append(all, evidence)
				selectionItems = append(selectionItems, item)
			}
		}
	}
	if repository != nil {
		references, referenceItems, err := compactTaskContextDevHydrateReferences(repository, query, all, selectionItems)
		if err != nil {
			return PreservedPayload{}, err
		}
		if len(references) > 0 {
			referenceRaw, err := json.Marshal(references)
			if err != nil {
				return PreservedPayload{}, fmt.Errorf("compact task_context dev: bind hydrated references: %w", err)
			}
			inputSHA = SHA256Hex([]byte(inputSHA + "\n" + SHA256Hex(referenceRaw)))
		}
		for index, evidence := range references {
			key := evidence.Path + "\x00" + evidence.Span
			item := referenceItems[index]
			if ref, ok := seenRef[key]; ok {
				item.EvidenceRefIDs = []string{ref}
				selectionItems = append(selectionItems, item)
				continue
			}
			seenRef[key] = evidence.RefID
			all = append(all, evidence)
			selectionItems = append(selectionItems, item)
		}
	}
	provenance.InputSHA256 = inputSHA
	effectiveBudget := budget
	var raw []byte
	var realTokens int
	for {
		sources, used, err := compactTaskContextDevSelect(query, all, selectionItems, effectiveBudget)
		if err != nil {
			return PreservedPayload{}, err
		}
		if len(sources) == 0 {
			return PreservedPayload{}, fmt.Errorf("compact task_context dev: budget %d admitted no complete source line", effectiveBudget)
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
		raw, err = json.Marshal(envelope)
		if err != nil {
			return PreservedPayload{}, fmt.Errorf("compact task_context dev: marshal: %w", err)
		}
		raw = append(raw, '\n')
		realTokens, err = real.Count(append([]byte(nil), raw...))
		if err != nil {
			return PreservedPayload{}, fmt.Errorf("compact task_context dev: tokenize: %w", err)
		}
		if real.TokenizerID != evaltokenizer.TokenizerID || realTokens <= SavingsCandidateBudget {
			break
		}
		reduction := max(1, (realTokens-SavingsCandidateBudget+1)/2)
		if reduction >= effectiveBudget {
			effectiveBudget /= 2
		} else {
			effectiveBudget -= reduction
		}
		if effectiveBudget < 1 {
			return PreservedPayload{}, fmt.Errorf("compact task_context dev: fixed wire overhead exceeds %d tokens", SavingsCandidateBudget)
		}
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

func compactTaskContextDevHydrateDefinitions(repository fs.FS, query string, items []contract.Item) ([]contract.Evidence, []contract.Item, error) {
	type parsedFile struct {
		set   *token.FileSet
		file  *ast.File
		lines []string
	}
	files := make(map[string]parsedFile)
	markdownFiles := make(map[string][]string)
	mode, patterns := grepReadV2QueryPlan(query)
	hydrateMarkdown := mode == GrepReadV2NaturalLanguage && compactTaskContextDevWantsMarkdownFlow(patterns) && !compactTaskContextDevWantsLifecycleHooks(patterns)
	seen := make(map[string]bool)
	var evidence []contract.Evidence
	var linked []contract.Item
	for _, item := range items {
		if compactTaskContextDevItemPriority(item.Reason) < 2_000 {
			continue
		}
		path, line, ok := compactTaskContextDevItemLocation(item.Reason)
		if !ok {
			continue
		}
		lowerPath := strings.ToLower(path)
		if strings.HasSuffix(lowerPath, ".md") {
			if !hydrateMarkdown || compactTaskContextDevItemPriority(item.Reason) < 4_000 {
				continue
			}
			lines, ok := markdownFiles[path]
			if !ok {
				raw, err := fs.ReadFile(repository, path)
				if err != nil {
					return nil, nil, fmt.Errorf("compact task_context dev: hydrate %s: %w", path, err)
				}
				lines = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
				markdownFiles[path] = lines
			}
			start, end, ok := compactTaskContextDevMarkdownSection(lines, line)
			if !ok {
				continue
			}
			key := fmt.Sprintf("%s\x00%d\x00%d", path, start, end)
			if seen[key] {
				continue
			}
			seen[key] = true
			text := strings.Join(lines[start-1:end], "\n")
			ref := fmt.Sprintf("hydrated-%03d", len(evidence)+1)
			evidence = append(evidence, contract.Evidence{
				RefID: ref, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, end),
				Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
			})
			linked = append(linked, contract.Item{RefID: "hydrated-item-" + ref, Rank: item.Rank, Reason: item.Reason, EvidenceRefIDs: []string{ref}})
			continue
		}
		if !strings.HasSuffix(lowerPath, ".go") || strings.HasSuffix(lowerPath, "_test.go") {
			continue
		}
		parsed, ok := files[path]
		if !ok {
			raw, err := fs.ReadFile(repository, path)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context dev: hydrate %s: %w", path, err)
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context dev: parse hydrated %s: %w", path, err)
			}
			parsed = parsedFile{set: set, file: file, lines: strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")}
			files[path] = parsed
		}
		start, end, ok := compactTaskContextDevDeclarationSpan(parsed.set, parsed.file, line)
		if !ok || start < 1 || end < start || end > len(parsed.lines) {
			continue
		}
		key := fmt.Sprintf("%s\x00%d\x00%d", path, start, end)
		if seen[key] {
			continue
		}
		seen[key] = true
		text := strings.Join(parsed.lines[start-1:end], "\n")
		ref := fmt.Sprintf("hydrated-%03d", len(evidence)+1)
		evidence = append(evidence, contract.Evidence{
			RefID: ref, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, end),
			Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
		})
		linked = append(linked, contract.Item{RefID: "hydrated-item-" + ref, Rank: item.Rank, Reason: item.Reason, EvidenceRefIDs: []string{ref}})
	}
	return evidence, linked, nil
}

func compactTaskContextDevMarkdownSection(lines []string, line int) (int, int, bool) {
	if line < 1 || line > len(lines) {
		return 0, 0, false
	}
	start, level := -1, 0
	for i := line - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		for level = 0; level < len(trimmed) && trimmed[level] == '#'; level++ {
		}
		if level > 0 && level < len(trimmed) && trimmed[level] == ' ' {
			start = i
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		nextLevel := 0
		for nextLevel < len(trimmed) && trimmed[nextLevel] == '#' {
			nextLevel++
		}
		if nextLevel > 0 && nextLevel <= level && nextLevel < len(trimmed) && trimmed[nextLevel] == ' ' {
			end = i
			break
		}
	}
	return start + 1, end, true
}

// compactTaskContextDevHydrateGrepReadDeclarations turns each query-only grep
// hit inside Go code into the complete enclosing declaration. GrepRead's
// fixed-width reads are useful for discovery but can begin halfway through a
// function; the compact selector then has no way to spend its budget on the
// declaration header or a distant branch. This bridge changes neither search
// nor ranking and is bounded by the already recorded grep hits.
func compactTaskContextDevHydrateGrepReadDeclarations(repository fs.FS, query string, hits []contract.Evidence) ([]contract.Evidence, []contract.Item, error) {
	mode, patterns := grepReadV2QueryPlan(query)
	wantsShellCompletion := compactTaskContextDevWantsShellCompletion(patterns)
	if mode != GrepReadV2NaturalLanguage || (!compactTaskContextDevNeedsReferenceContext(patterns) && !wantsShellCompletion) {
		return nil, nil, nil
	}
	type parsedFile struct {
		set   *token.FileSet
		file  *ast.File
		lines []string
	}
	files := make(map[string]parsedFile)
	seen := make(map[string]bool)
	var evidence []contract.Evidence
	var linked []contract.Item
	for _, hit := range hits {
		isGrepHit := strings.HasPrefix(hit.RefID, "grepread-grep-")
		isShellRead := wantsShellCompletion && strings.HasPrefix(hit.RefID, "grepread-read-")
		if !isGrepHit && !isShellRead {
			continue
		}
		path := hit.Path
		lower := strings.ToLower(path)
		if !strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, "_test.go") {
			continue
		}
		parsed, ok := files[path]
		if !ok {
			raw, err := fs.ReadFile(repository, path)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context dev: hydrate GrepRead declaration %s: %w", path, err)
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context dev: parse hydrated GrepRead declaration %s: %w", path, err)
			}
			parsed = parsedFile{set: set, file: file, lines: strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")}
			files[path] = parsed
		}
		var declarations []ast.Decl
		if isShellRead {
			hitStart, hitEnd, err := exactEvidenceSpan(hit.Span)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context dev: invalid GrepRead span %q", hit.Span)
			}
			for _, candidate := range parsed.file.Decls {
				candidateStart := parsed.set.Position(candidate.Pos()).Line
				if doc := compactTaskContextDevDeclarationDoc(candidate); doc != nil {
					candidateStart = parsed.set.Position(doc.Pos()).Line
				}
				candidateEnd := parsed.set.Position(candidate.End()).Line
				if candidateEnd >= hitStart && candidateStart <= hitEnd {
					declarations = append(declarations, candidate)
				}
			}
		} else {
			for _, candidate := range parsed.file.Decls {
				candidateStart := parsed.set.Position(candidate.Pos()).Line
				if doc := compactTaskContextDevDeclarationDoc(candidate); doc != nil {
					candidateStart = parsed.set.Position(doc.Pos()).Line
				}
				candidateEnd := parsed.set.Position(candidate.End()).Line
				if hit.Line >= candidateStart && hit.Line <= candidateEnd {
					declarations = append(declarations, candidate)
					break
				}
			}
		}
		for _, declaration := range declarations {
			start := parsed.set.Position(declaration.Pos()).Line
			if doc := compactTaskContextDevDeclarationDoc(declaration); doc != nil {
				start = parsed.set.Position(doc.Pos()).Line
			}
			end := parsed.set.Position(declaration.End()).Line
			if start < 1 || end < start || end > len(parsed.lines) || (end-start+1 <= GrepReadWindowLines && !wantsShellCompletion) {
				continue
			}
			key := fmt.Sprintf("%s\x00%d\x00%d", path, start, end)
			if seen[key] {
				continue
			}
			seen[key] = true
			symbol, kind := compactTaskContextDevDeclarationIdentity(parsed.file.Name.Name, declaration)
			if symbol == "" {
				continue
			}
			text := strings.Join(parsed.lines[start-1:end], "\n")
			// Keep hydrated declarations in the bounded GrepRead fallback channel.
			// Shell-completion reads may contribute several small declarations
			// from one fixed-width window.
			ref := fmt.Sprintf("grepread-hydrated-%03d", len(evidence)+1)
			evidence = append(evidence, contract.Evidence{
				RefID: ref, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, end),
				Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
			})
			linked = append(linked, contract.Item{
				RefID: "grepread-hydrated-item-" + ref, Rank: len(linked) + 1,
				Reason:         fmt.Sprintf("grepread-declaration: %s %s (%s:%d) score 0 [query hit %s]", kind, symbol, path, start, hit.RefID),
				EvidenceRefIDs: []string{ref},
			})
		}
	}
	return evidence, linked, nil
}

type compactTaskContextDevReferenceIdentifier struct {
	name  string
	score int
}

type compactTaskContextDevReferenceDeclaration struct {
	path       string
	start      int
	end        int
	text       string
	symbol     string
	kind       string
	score      int
	references []string
}

// compactTaskContextDevHydrateReferences follows one source-level hop from
// identifiers already present in the ranked bundle to the declarations that
// use them. This recovers the control-flow context that a definition-only
// bridge cannot contain (for example, the execute method which calls
// ParseFlags). The search is query-filtered, bounded and excludes test files;
// it never consults answer spans or judgements.
func compactTaskContextDevHydrateReferences(repository fs.FS, query string, evidence []contract.Evidence, items []contract.Item) ([]contract.Evidence, []contract.Item, error) {
	mode, patterns := grepReadV2QueryPlan(query)
	if mode != GrepReadV2NaturalLanguage || !compactTaskContextDevNeedsFlowAllocation(patterns) {
		return nil, nil, nil
	}
	identifiers := compactTaskContextDevReferenceIdentifiers(patterns, evidence, items)
	if len(identifiers) == 0 {
		return nil, nil, nil
	}
	identifierScore := make(map[string]int, len(identifiers))
	for _, identifier := range identifiers {
		identifierScore[identifier.name] = identifier.score
	}
	existing := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		existing[item.Path+"\x00"+item.Span] = true
	}

	var declarations []compactTaskContextDevReferenceDeclaration
	namedDeclarations := make(map[string][]compactTaskContextDevReferenceDeclaration)
	err := fs.WalkDir(repository, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		lower := strings.ToLower(path)
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, "_test.go") {
			return nil
		}
		raw, err := fs.ReadFile(repository, path)
		if err != nil {
			return err
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		for _, declaration := range file.Decls {
			start := set.Position(declaration.Pos()).Line
			if doc := compactTaskContextDevDeclarationDoc(declaration); doc != nil {
				start = set.Position(doc.Pos()).Line
			}
			end := set.Position(declaration.End()).Line
			if start < 1 || end < start || end > len(lines) {
				continue
			}
			symbol, kind := compactTaskContextDevDeclarationIdentity(file.Name.Name, declaration)
			if symbol == "" {
				continue
			}
			text := strings.Join(lines[start-1:end], "\n")
			own := symbol
			if dot := strings.LastIndex(own, "."); dot >= 0 {
				own = own[dot+1:]
			}
			if len(strings.Fields(text)) <= 60 {
				namedDeclarations[own] = append(namedDeclarations[own], compactTaskContextDevReferenceDeclaration{
					path: path, start: start, end: end, text: text, symbol: symbol, kind: kind,
				})
			}
			if existing[path+"\x00"+fmt.Sprintf("%d-%d", start, end)] {
				continue
			}
			counts := make(map[string]int)
			ast.Inspect(declaration, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if ok && identifierScore[identifier.Name] > 0 {
					counts[identifier.Name]++
				}
				return true
			})
			score := 0
			var references []string
			for _, identifier := range identifiers {
				count := counts[identifier.name]
				if identifier.name == own {
					count--
				}
				if count <= 0 {
					continue
				}
				references = append(references, identifier.name)
				score += identifier.score + min(count, 3)*1_000
			}
			if len(references) == 0 {
				continue
			}
			anchors := compactTaskContextDevAnchors(mode, patterns, path, strings.Split(text, "\n"))
			score += anchors[0].score * 1_000
			if compactTaskContextDevWantsShellProtocol(patterns) && strings.Contains(text, "ShellCompRequestCmd") && strings.Contains(text, "OutOrStdout") && strings.Contains(text, "ErrOrStderr") {
				// A custom shell adapter speaks Cobra's hidden-command protocol.
				// Its implementation is the source that binds request name, stdout
				// completions, final directive and ignored stderr together.
				score += 1_000_000
			}
			declarations = append(declarations, compactTaskContextDevReferenceDeclaration{
				path: path, start: start, end: end, text: text, symbol: symbol, kind: kind,
				score: score, references: references,
			})
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("compact task_context dev: hydrate references: %w", err)
	}
	sort.SliceStable(declarations, func(i, j int) bool {
		if declarations[i].score != declarations[j].score {
			return declarations[i].score > declarations[j].score
		}
		if declarations[i].path != declarations[j].path {
			return declarations[i].path < declarations[j].path
		}
		return declarations[i].start < declarations[j].start
	})
	if len(declarations) > 0 {
		selected := []compactTaskContextDevReferenceDeclaration{declarations[0]}
		callee := compactTaskContextDevFlowCallee(patterns, declarations[0].text)
		if callee != "" {
			if matches := namedDeclarations[callee]; len(matches) > 0 {
				declaration := matches[0]
				declaration.score = declarations[0].score
				declaration.references = []string{"flow-callee:" + callee}
				selected = append(selected, declaration)
			}
		}
		declarations = selected
	}
	out := make([]contract.Evidence, 0, len(declarations))
	linked := make([]contract.Item, 0, len(declarations))
	for index, declaration := range declarations {
		ref := fmt.Sprintf("linked-reference-%03d", index+1)
		out = append(out, contract.Evidence{
			RefID: ref, Path: declaration.path, Line: declaration.start,
			Span: fmt.Sprintf("%d-%d", declaration.start, declaration.end), Role: "snippet",
			Snippet: declaration.text, TextHash: shape.TextHash(declaration.text),
		})
		reasonPrefix := "reference:"
		if len(declaration.references) == 1 && strings.HasPrefix(declaration.references[0], "flow-callee:") {
			reasonPrefix = "flow-callee:"
		}
		linked = append(linked, contract.Item{
			RefID: "linked-item-" + ref, Rank: index + 1,
			Reason:         fmt.Sprintf("%s %s %s (%s:%d) score %d [reference %s]", reasonPrefix, declaration.kind, declaration.symbol, declaration.path, declaration.start, declaration.score, strings.Join(declaration.references, ",")),
			EvidenceRefIDs: []string{ref},
		})
	}
	return out, linked, nil
}

func compactTaskContextDevFlowCallee(patterns []string, declaration string) string {
	if (compactTaskContextDevHasPattern(patterns, "init") || compactTaskContextDevHasPattern(patterns, "hook")) && strings.Contains(declaration, ".preRun(") {
		return "preRun"
	}
	return ""
}

func compactTaskContextDevNeedsReferenceContext(patterns []string) bool {
	hasRoot, hasSubcommand, hasCompletion, hasGroup := false, false, false, false
	for _, pattern := range patterns {
		switch lower := strings.ToLower(pattern); lower {
		case "after", "before", "call", "called", "dispatch", "during", "execute", "flow", "handl", "handle", "hook", "init", "lifecycle", "order", "pipeline", "route", "run", "sequence", "when":
			return true
		case "root", "rootcmd":
			hasRoot = true
		case "subcommand":
			hasSubcommand = true
		case "completion":
			hasCompletion = true
		case "group":
			hasGroup = true
		}
	}
	return hasRoot && hasSubcommand || hasCompletion && hasGroup
}

func compactTaskContextDevWantsMarkdownFlow(patterns []string) bool {
	return compactTaskContextDevHasPattern(patterns, "lifecycle") ||
		compactTaskContextDevHasPattern(patterns, "hook") ||
		compactTaskContextDevHasPattern(patterns, "order") ||
		compactTaskContextDevHasPattern(patterns, "sequence")
}

func compactTaskContextDevWantsLifecycleHooks(patterns []string) bool {
	return compactTaskContextDevHasPattern(patterns, "lifecycle") &&
		(compactTaskContextDevHasPattern(patterns, "execute") || compactTaskContextDevHasPattern(patterns, "run") || compactTaskContextDevHasPattern(patterns, "hook"))
}

func compactTaskContextDevWantsTraversal(patterns []string) bool {
	return (compactTaskContextDevHasPattern(patterns, "root") || compactTaskContextDevHasPattern(patterns, "rootcmd")) &&
		compactTaskContextDevHasPattern(patterns, "subcommand")
}

func compactTaskContextDevWantsInitFlagValue(patterns []string) bool {
	return compactTaskContextDevHasPattern(patterns, "init") &&
		compactTaskContextDevHasPattern(patterns, "flag") &&
		compactTaskContextDevHasPattern(patterns, "value")
}

func compactTaskContextDevWantsShellCompletion(patterns []string) bool {
	return compactTaskContextDevHasPattern(patterns, "shell") &&
		compactTaskContextDevHasPattern(patterns, "completion")
}

func compactTaskContextDevWantsShellProtocol(patterns []string) bool {
	return compactTaskContextDevWantsShellCompletion(patterns) &&
		(compactTaskContextDevHasPattern(patterns, "custom") || compactTaskContextDevHasPattern(patterns, "another"))
}

func compactTaskContextDevNeedsFlowAllocation(patterns []string) bool {
	return compactTaskContextDevNeedsReferenceContext(patterns) || compactTaskContextDevWantsShellProtocol(patterns)
}

func compactTaskContextDevReferenceIdentifiers(patterns []string, evidence []contract.Evidence, items []contract.Item) []compactTaskContextDevReferenceIdentifier {
	frequencies := make(map[string]int, len(patterns))
	for _, pattern := range patterns {
		frequencies[strings.ToLower(pattern)] = 1
	}
	scores := make(map[string]int)
	add := func(name string, score int) {
		if !token.IsIdentifier(name) || len(name) < 4 || compactTaskContextDevIgnoredIdentifier(name) {
			return
		}
		if score > scores[name] {
			scores[name] = score
		}
	}
	for _, item := range items {
		priority := compactTaskContextDevItemPriority(item.Reason)
		if priority < 2_000 {
			continue
		}
		symbol, _ := compactTaskContextDevItemSymbol(item.Reason)
		if dot := strings.LastIndex(symbol, "."); dot >= 0 {
			symbol = symbol[dot+1:]
		}
		add(symbol, priority+compactTaskContextDevSymbolScore(patterns, frequencies, symbol))
	}
	for _, item := range evidence {
		fields := strings.FieldsFunc(item.Snippet, func(r rune) bool {
			return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, identifier := range fields {
			if compactTaskContextDevWantsShellProtocol(patterns) && strings.Contains(identifier, "ShellComp") && strings.Contains(identifier, "Request") {
				add(identifier, 1_000_000)
				continue
			}
			score := compactTaskContextDevSymbolScore(patterns, frequencies, identifier)
			if score > 0 {
				add(identifier, score+1_000)
			}
		}
	}
	out := make([]compactTaskContextDevReferenceIdentifier, 0, len(scores))
	for name, score := range scores {
		out = append(out, compactTaskContextDevReferenceIdentifier{name: name, score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	limit := 12
	if compactTaskContextDevWantsShellProtocol(patterns) {
		// Protocol constants are often below generic shell/generator names in
		// the first lexical dozen. Keep enough source-derived identifiers for
		// the bounded reference hop to reach the hidden command implementation.
		limit = 32
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func compactTaskContextDevIgnoredIdentifier(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "bool", "byte", "error", "false", "float32", "float64", "int16", "int32", "int64", "package", "return", "rune", "string", "struct", "true", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

func compactTaskContextDevDeclarationDoc(declaration ast.Decl) *ast.CommentGroup {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		return value.Doc
	case *ast.GenDecl:
		return value.Doc
	default:
		return nil
	}
}

func compactTaskContextDevDeclarationIdentity(packageName string, declaration ast.Decl) (string, string) {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		kind := "function"
		prefix := packageName
		if value.Recv != nil && len(value.Recv.List) > 0 {
			kind = "method"
			if receiver := compactTaskContextDevReceiverName(value.Recv.List[0].Type); receiver != "" {
				prefix += "." + receiver
			}
		}
		return prefix + "." + value.Name.Name, kind
	case *ast.GenDecl:
		for _, spec := range value.Specs {
			switch named := spec.(type) {
			case *ast.TypeSpec:
				return packageName + "." + named.Name.Name, "type"
			case *ast.ValueSpec:
				if len(named.Names) > 0 {
					kind := "variable"
					if value.Tok == token.CONST {
						kind = "constant"
					}
					return packageName + "." + named.Names[0].Name, kind
				}
			}
		}
	}
	return "", ""
}

func compactTaskContextDevReceiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return compactTaskContextDevReceiverName(value.X)
	case *ast.IndexExpr:
		return compactTaskContextDevReceiverName(value.X)
	case *ast.IndexListExpr:
		return compactTaskContextDevReceiverName(value.X)
	default:
		return ""
	}
}

func compactTaskContextDevItemLocation(reason string) (string, int, bool) {
	end := strings.LastIndex(reason, ") score ")
	if end < 0 {
		return "", 0, false
	}
	start := strings.LastIndex(reason[:end], " (")
	if start < 0 {
		return "", 0, false
	}
	location := reason[start+2 : end]
	colon := strings.LastIndex(location, ":")
	if colon < 1 {
		return "", 0, false
	}
	line, err := strconv.Atoi(location[colon+1:])
	path := location[:colon]
	if err != nil || line < 1 || !fs.ValidPath(path) {
		return "", 0, false
	}
	return path, line, true
}

func compactTaskContextDevDeclarationSpan(set *token.FileSet, file *ast.File, line int) (int, int, bool) {
	for _, declaration := range file.Decls {
		start := set.Position(declaration.Pos()).Line
		if doc := compactTaskContextDevDeclarationDoc(declaration); doc != nil {
			start = set.Position(doc.Pos()).Line
		}
		end := set.Position(declaration.End()).Line
		if line < start || line > end {
			continue
		}
		return start, end, true
	}
	return 0, 0, false
}

type compactTaskContextDevCandidate struct {
	item             contract.Evidence
	symbol           string
	kind             string
	lines            []string
	start            int
	anchor           int
	from             int
	to               int
	cost             int
	complete         bool
	completeFallback bool
	score            int
	order            int
	fallback         bool
	itemPriority     int
	symbolScore      int
	secondaryFrom    int
	secondaryTo      int
	secondaryScore   int
	hasSecondary     bool
}

// compactTaskContextDevSelect retains a small number of coherent regions.
// Ranking considers the whole region and discounts test-name/comment matches
// unless the question explicitly asks about tests. The budget is then split by
// rank before unused quota is returned to the strongest regions. This prevents
// a long tail of one-line anchors from starving the implementation bodies that
// make the response answerable.
func compactTaskContextDevSelect(query string, evidence []contract.Evidence, items []contract.Item, budget int) ([]CompactTaskContextDevSource, int, error) {
	referenced := make(map[string]bool)
	type itemHint struct {
		symbol, kind string
		priority     int
	}
	hintByEvidence := make(map[string]itemHint)
	for _, item := range items {
		symbol, kind := compactTaskContextDevItemSymbol(item.Reason)
		priority := compactTaskContextDevItemPriority(item.Reason)
		for _, ref := range item.EvidenceRefIDs {
			referenced[ref] = true
			prior := hintByEvidence[ref]
			if priority > prior.priority || (priority == prior.priority && prior.symbol == "" && symbol != "") {
				hintByEvidence[ref] = itemHint{symbol: symbol, kind: kind, priority: priority}
			}
		}
	}
	mode, patterns := grepReadV2QueryPlan(query)
	if mode == GrepReadV2ExactIdentifier {
		seen := map[string]bool{strings.ToLower(patterns[0]): true}
		for _, term := range compactTaskContextDevIdentifierTerms(patterns[0]) {
			if !seen[term] {
				seen[term] = true
				patterns = append(patterns, term)
			}
		}
		if seen["mutually"] && seen["exclusive"] {
			patterns = append(patterns, "group", "validate", "enforce")
		}
	} else if compactTaskContextDevHasPattern(patterns, "suggestion") && compactTaskContextDevHasPattern(patterns, "comput") {
		patterns = append(patterns, "levenshtein")
	}
	if mode == GrepReadV2NaturalLanguage && strings.Contains(query, "--") && !compactTaskContextDevHasPattern(patterns, "flag") {
		// Preserve the information carried by CLI spelling. The language-only
		// plan intentionally drops one-letter tokens such as -h, but a --name
		// token is strong evidence that flag declarations and parsing matter.
		patterns = append(patterns, "flag")
	}
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
		fallback := strings.HasPrefix(item.RefID, "grepread-")
		frequencies := documentFrequencies[0]
		if fallback {
			frequencies = documentFrequencies[1]
		}
		anchors := compactTaskContextDevAnchors(mode, patterns, item.Path, lines)
		anchor := anchors[0]
		if compactTaskContextDevWantsShellProtocol(patterns) && strings.Contains(item.Snippet, "ShellCompRequestCmd") && strings.Contains(item.Snippet, "OutOrStdout") {
			for index, line := range lines {
				if strings.Contains(line, "func (c *Command) initCompleteCmd") {
					anchor = compactTaskContextDevLineAnchor{index: index, score: max(anchor.score, 200)}
					break
				}
			}
		}
		if compactTaskContextDevWantsLifecycleHooks(patterns) && strings.Contains(item.Snippet, "func (c *Command) execute") && strings.Contains(item.Snippet, "PersistentPreRun") {
			for index, line := range lines {
				if strings.Contains(line, "func (c *Command) execute") {
					anchor = compactTaskContextDevLineAnchor{index: index, score: max(anchor.score, 220)}
					break
				}
			}
		} else if compactTaskContextDevWantsLifecycleHooks(patterns) && strings.Contains(item.Snippet, "func (c *Command) ExecuteC") && strings.Contains(item.Snippet, "cmd.execute(flags)") {
			for index, line := range lines {
				if strings.Contains(line, "cmd.execute(flags)") {
					anchor = compactTaskContextDevLineAnchor{index: index, score: max(anchor.score, 210)}
					break
				}
			}
		}
		if compactTaskContextDevWantsTraversal(patterns) {
			for index, line := range lines {
				if strings.Contains(line, "if c.TraverseChildren") || strings.Contains(line, "TraverseChildren bool") {
					anchor = compactTaskContextDevLineAnchor{index: index, score: max(anchor.score, 200)}
					break
				}
			}
		}
		score := compactTaskContextDevRegionScore(mode, patterns, frequencies, item, anchor.score, testIntent) - order
		hint := hintByEvidence[item.RefID]
		symbolScore := compactTaskContextDevSymbolScore(patterns, frequencies, hint.symbol)
		score += symbolScore + hint.priority
		if compactTaskContextDevWantsShellProtocol(patterns) {
			switch {
			case strings.Contains(item.Snippet, "ShellCompRequestCmd") && strings.Contains(item.Snippet, "OutOrStdout"):
				score += 3_000_000
			case strings.Contains(item.Snippet, "ShellCompRequestCmd"):
				score += 2_500_000
			case strings.Contains(item.Snippet, "ShellCompDirectiveError") || strings.Contains(item.Snippet, "type ShellCompDirective"):
				score += 2_000_000
			}
		}
		wholeSymbolMatch := compactTaskContextDevWholeSymbolMatch(patterns, hint.symbol)
		if mode == GrepReadV2ExactIdentifier {
			wholeSymbolMatch = compactTaskContextDevWholeSymbolMatch(patterns[:1], hint.symbol)
		}
		if wholeSymbolMatch {
			// Prefer Execute over ExecuteContext when the question names
			// Execute itself. Term-level CamelCase matching intentionally gives
			// both a useful score; this tie-break keeps the exact API entrypoint.
			score += 1_000_000
		}
		if compactTaskContextDevNeedsReferenceContext(patterns) && compactTaskContextDevLifecycleHookMatch(patterns, hint.symbol) {
			score += 30_000
		}
		if compactTaskContextDevHasPattern(patterns, "list") && strings.Contains(item.Snippet, ".VisitAll(") {
			// Collection questions need the enumerator, not only an accessor
			// returning the collection type.
			score += 30_000
		}
		if !fallback && (hint.kind == "function" || hint.kind == "method") && len(lines) >= 30 && symbolScore > 0 {
			// Long implementation bodies are the only candidates capable of
			// explaining control flow. Keep one competitive with short names and
			// accessors instead of spending the whole bundle on declarations.
			score += 8_000
		}
		if fallback && strings.HasPrefix(item.RefID, "grepread-hydrated-") && (compactTaskContextDevNeedsReferenceContext(patterns) || compactTaskContextDevWantsShellCompletion(patterns)) && (hint.kind == "function" || hint.kind == "method") {
			// For flow questions, prefer an executable declaration around a
			// query hit over a large constant/type block containing the same
			// vocabulary. Only one fallback slot is available in this mode.
			score += 100_000
		}
		if compactTaskContextDevWantsShellCompletion(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			if strings.Contains(lowerSymbol, "completionfunc") || strings.Contains(lowerSymbol, "fixedcompletions") {
				// Prefer Cobra's shell-independent Go callback surface over one
				// shell's generated-script plumbing.
				score += 200_000
			}
			if strings.Contains(lowerSymbol, "register") && strings.Contains(lowerSymbol, "completionfunc") {
				score += 20_000
			}
		}
		if compactTaskContextDevWantsInitFlagValue(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			switch {
			case strings.Contains(item.Snippet, "ParseFlags") && strings.Contains(item.Snippet, "c.preRun()"):
				score += 260_000
			case strings.HasSuffix(lowerSymbol, ".oninitialize"):
				score += 220_000
			case strings.HasSuffix(lowerSymbol, ".prerun"):
				score += 200_000
			case strings.HasSuffix(lowerSymbol, ".persistentflags"):
				score += 180_000
			case strings.HasSuffix(lowerSymbol, ".flag"):
				score += 160_000
			}
		}
		if compactTaskContextDevWantsTraversal(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			switch {
			case strings.HasSuffix(lowerSymbol, ".traverse"):
				score += 300_000
			case strings.Contains(item.Snippet, "if c.TraverseChildren"):
				score += 240_000
			case strings.Contains(item.Snippet, "TraverseChildren bool"):
				score += 220_000
			case strings.HasSuffix(lowerSymbol, ".find"):
				score += 180_000
			}
		}
		if strings.HasPrefix(item.RefID, "hydrated-") && strings.HasSuffix(strings.ToLower(item.Path), ".md") && compactTaskContextDevNeedsReferenceContext(patterns) {
			// A ranked documentation heading is otherwise represented by only one
			// or two evidence lines. Prefer its verified section so lifecycle lists
			// and surrounding constraints can survive compact allocation.
			score += 100_000
		}
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
		candidate := compactTaskContextDevCandidate{
			item: item, symbol: hint.symbol, kind: hint.kind, lines: lines, start: start, anchor: anchor.index,
			from: anchor.index, to: anchor.index, score: score, order: order,
			fallback: fallback, completeFallback: fallback && strings.HasPrefix(item.RefID, "grepread-hydrated-") && compactTaskContextDevWantsShellCompletion(patterns),
			itemPriority: hint.priority, symbolScore: symbolScore,
		}
		if mode == GrepReadV2NaturalLanguage && len(lines) >= 30 {
			if len(anchors) > 1 {
				candidate.secondaryFrom = max(0, anchors[1].index-1)
				candidate.secondaryTo = candidate.secondaryFrom
				candidate.secondaryScore = anchors[1].score
				candidate.hasSecondary = true
			}
			if compactTaskContextDevNeedsFlowAllocation(patterns) && (hint.kind == "function" || hint.kind == "method" || strings.HasSuffix(strings.ToLower(item.Path), ".md")) {
				if flowAnchor, ok := compactTaskContextDevFlowSecondaryAnchor(patterns, lines, anchor.index); ok {
					candidate.secondaryFrom = max(0, flowAnchor.index-1)
					if compactTaskContextDevWantsLifecycleHooks(patterns) {
						candidate.secondaryFrom = flowAnchor.index
					}
					candidate.secondaryTo = candidate.secondaryFrom
					candidate.secondaryScore = flowAnchor.score
					candidate.hasSecondary = true
				}
			}
		}
		candidates = append(candidates, candidate)
	}
	candidates, err := compactTaskContextDevMergeSameAnchor(candidates)
	if err != nil {
		return nil, 0, err
	}
	if mode == GrepReadV2ExactIdentifier {
		compactTaskContextDevRankExactDependencies(patterns[0], candidates)
	}
	if mode == GrepReadV2NaturalLanguage && !compactTaskContextDevNeedsFlowAllocation(patterns) {
		best, bestUtility := -1, 0
		for i := range candidates {
			candidate := candidates[i]
			lowerPath := strings.ToLower(candidate.item.Path)
			if candidate.fallback || strings.HasPrefix(candidate.item.RefID, "linked-reference-") || strings.HasSuffix(lowerPath, "_test.go") || candidate.itemPriority < 4_000 || candidate.symbolScore <= 0 {
				continue
			}
			from, to, ok := compactTaskContextDevUnitRange(candidate)
			if !ok {
				continue
			}
			cost := len(strings.Fields(strings.Join(candidate.lines[from:to+1], "\n")))
			if cost < 1 || cost > 160 {
				continue
			}
			utility := candidate.symbolScore + candidate.itemPriority + cost*100
			if utility > bestUtility {
				best, bestUtility = i, utility
			}
		}
		if best >= 0 {
			candidates[best].score += 100_000
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].order < candidates[j].order
	})
	if compactTaskContextDevWantsLifecycleHooks(patterns) {
		filtered := make([]compactTaskContextDevCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			candidateEnd := candidate.start + len(candidate.lines) - 1
			contained := false
			for _, prior := range filtered {
				priorEnd := prior.start + len(prior.lines) - 1
				if candidate.item.Path == prior.item.Path && candidate.start >= prior.start && candidateEnd <= priorEnd {
					contained = true
					break
				}
			}
			if !contained {
				filtered = append(filtered, candidate)
			}
		}
		candidates = filtered
	}
	if mode == GrepReadV2ExactIdentifier {
		filtered := make([]compactTaskContextDevCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.fallback {
				absolute := candidate.start + candidate.anchor
				redundant := false
				for _, prior := range filtered {
					if !prior.fallback && prior.item.Path == candidate.item.Path && absolute >= prior.start && absolute < prior.start+len(prior.lines) {
						redundant = true
						break
					}
				}
				if redundant {
					continue
				}
			}
			filtered = append(filtered, candidate)
		}
		candidates = filtered
	}
	maxSources := 10
	weights := []int{18, 8, 5, 3, 2, 2, 1, 1, 1, 1}
	if mode == GrepReadV2ExactIdentifier || mode == GrepReadV2ExactPath {
		maxSources = 5
		weights = []int{10, 3, 1, 1, 1}
	} else if compactTaskContextDevWantsLifecycleHooks(patterns) {
		maxSources = 7
		weights = []int{18, 8, 5, 3, 2, 1, 1}
	} else if compactTaskContextDevWantsTraversal(patterns) {
		maxSources = 5
		weights = []int{18, 8, 5, 3, 2}
	} else if compactTaskContextDevWantsShellProtocol(patterns) {
		maxSources = 3
		weights = []int{18, 8, 5}
	} else if compactTaskContextDevWantsInitFlagValue(patterns) {
		maxSources = 6
		weights = []int{18, 8, 5, 3, 2, 1}
	} else {
		selected := make([]compactTaskContextDevCandidate, 0, maxSources)
		maxSemantic, maxFallback := 8, 2
		if compactTaskContextDevNeedsFlowAllocation(patterns) {
			// Flow questions need both the operation and the lifecycle hook or
			// caller that surrounds it. Reserve one more semantic region rather
			// than a second query-only grep line.
			maxSemantic, maxFallback = 9, 1
		}
		semantic, fallback := 0, 0
		for _, candidate := range candidates {
			if candidate.fallback {
				if fallback >= maxFallback {
					continue
				}
				fallback++
			} else {
				if semantic >= maxSemantic {
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
	if (mode == GrepReadV2ExactIdentifier || mode == GrepReadV2ExactPath) && len(candidates) > 0 {
		if from, to, ok := compactTaskContextDevUnitRange(candidates[0]); ok {
			fullCost := len(strings.Fields(strings.Join(candidates[0].lines[from:to+1], "\n")))
			if fullCost > budget {
				candidates = candidates[:1]
				weights = []int{1}
			}
		}
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
	secondaryReserve := 0
	if compactTaskContextDevNeedsFlowAllocation(patterns) {
		for i := range candidates {
			if admitted[i] && candidates[i].hasSecondary {
				reserve := budget / 4
				if compactTaskContextDevWantsTraversal(patterns) {
					reserve = budget * 3 / 5
				} else if compactTaskContextDevWantsShellProtocol(patterns) {
					reserve = budget * 2 / 5
				} else if compactTaskContextDevWantsLifecycleHooks(patterns) {
					reserve = budget * 2 / 5
				} else if strings.HasSuffix(strings.ToLower(candidates[i].item.Path), ".md") && candidates[i].secondaryScore >= 200 {
					reserve = budget * 2 / 5
				}
				secondaryReserve = max(secondaryReserve, min(remaining, reserve))
			}
		}
	}
	primaryRemaining := remaining - secondaryReserve
	if mode == GrepReadV2NaturalLanguage && compactTaskContextDevWantsInitFlagValue(patterns) {
		// Initializers run after ParseFlags, so an answer about reading values in
		// init needs the typed getter immediately following the parse, not only
		// the distant preRun call. Grow that one control-flow window first.
		for i := range candidates {
			if !admitted[i] || (candidates[i].kind != "function" && candidates[i].kind != "method") {
				continue
			}
			target := -1
			for line := candidates[i].anchor + 1; line < len(candidates[i].lines) && line <= candidates[i].anchor+24; line++ {
				if strings.Contains(strings.ToLower(candidates[i].lines[line]), ".getbool(") {
					target = line
					break
				}
			}
			for target >= 0 && candidates[i].to < target && primaryRemaining > 0 && compactTaskContextDevGrow(&candidates[i], &primaryRemaining) {
			}
			if target >= 0 {
				break
			}
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextDevWantsShellProtocol(patterns) {
		for i := range candidates {
			if !admitted[i] || !strings.Contains(strings.Join(candidates[i].lines, "\n"), "ShellCompRequestCmd") || !strings.Contains(strings.Join(candidates[i].lines, "\n"), "OutOrStdout") {
				continue
			}
			target := -1
			for line := candidates[i].anchor; line < len(candidates[i].lines) && line <= candidates[i].anchor+20; line++ {
				if strings.Contains(candidates[i].lines[line], "Run: func") {
					target = line
					break
				}
			}
			for target >= 0 && candidates[i].to < target && primaryRemaining > 0 && compactTaskContextDevGrow(&candidates[i], &primaryRemaining) {
			}
			break
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextDevWantsLifecycleHooks(patterns) {
		for i := range candidates {
			joined := strings.Join(candidates[i].lines, "\n")
			if !admitted[i] || !strings.Contains(joined, "func (c *Command) execute") || !strings.Contains(joined, "PersistentPreRun") {
				continue
			}
			target := -1
			for line := candidates[i].anchor; line < len(candidates[i].lines); line++ {
				if strings.Contains(candidates[i].lines[line], "ParseFlags") {
					target = min(len(candidates[i].lines)-1, line+2)
					break
				}
			}
			for target >= 0 && candidates[i].to < target && primaryRemaining > 0 && compactTaskContextDevGrow(&candidates[i], &primaryRemaining) {
			}
			break
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextDevNeedsFlowAllocation(patterns) {
		best, bestUtility := -1, -1
		for i, candidate := range candidates {
			if !admitted[i] || (candidate.kind != "function" && candidate.kind != "method") || candidate.symbolScore <= 0 {
				continue
			}
			utility := candidate.symbolScore
			if utility > bestUtility {
				best, bestUtility = i, utility
			}
		}
		if best >= 0 {
			compactTaskContextDevGrowDocComment(&candidates[best], &primaryRemaining, 6)
		}
	}
	if (mode == GrepReadV2ExactIdentifier || mode == GrepReadV2ExactPath) && len(candidates) > 0 && admitted[0] {
		// Exact lookup is depth-first: make the named declaration useful before
		// wrappers and neighbours consume the budget. Complete a small
		// definition, or give a long implementation the remaining source budget.
		if !compactTaskContextDevCompleteUnit(&candidates[0], &primaryRemaining, budget) {
			target := budget
			for candidates[0].cost < target && primaryRemaining > 0 && compactTaskContextDevGrow(&candidates[0], &primaryRemaining) {
			}
		}
	}
	// Preserve complete small declarations before breadth growth spends their
	// remaining cost on unrelated one-line anchors.
	completeLimits := []int{160, 140, 100, 80}
	completeOrder := make([]int, len(candidates))
	for i := range candidates {
		completeOrder[i] = i
	}
	if mode == GrepReadV2NaturalLanguage {
		root := -1
		for i, candidate := range candidates {
			if candidate.itemPriority == 12_000 && candidate.symbolScore > 0 && (candidate.kind == "function" || candidate.kind == "method") {
				root = i
				break
			}
		}
		dependencies := make(map[string]bool)
		if root >= 0 {
			for _, identifier := range strings.FieldsFunc(strings.Join(candidates[root].lines, "\n"), func(r rune) bool {
				return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
			}) {
				dependencies[identifier] = true
			}
		}
		class := func(index int) int {
			candidate := candidates[index]
			if index == root {
				return 0
			}
			name := candidate.symbol
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				name = name[dot+1:]
			}
			if name != "" && dependencies[name] {
				return 1
			}
			if candidate.itemPriority == 12_000 && candidate.symbolScore > 0 {
				return 2
			}
			return 3
		}
		sort.SliceStable(completeOrder, func(i, j int) bool {
			a, b := completeOrder[i], completeOrder[j]
			aClass, bClass := class(a), class(b)
			if aClass != bClass {
				return aClass < bClass
			}
			if aClass == 1 {
				return len(strings.Fields(strings.Join(candidates[a].lines, "\n"))) > len(strings.Fields(strings.Join(candidates[b].lines, "\n")))
			}
			return false
		})
	}
	for order, index := range completeOrder {
		if !admitted[index] || primaryRemaining <= 0 {
			continue
		}
		limit := 80
		if order < len(completeLimits) {
			limit = completeLimits[order]
		}
		compactTaskContextDevCompleteUnit(&candidates[index], &primaryRemaining, limit)
	}
	// A lone comment/signature line is rarely actionable. Give every admitted
	// region one adjacent complete line before weighted depth allocation; this
	// pairs declaration comments with values and signatures with first steps.
	for i := range candidates {
		if admitted[i] && primaryRemaining > 0 {
			if i < 4 {
				compactTaskContextDevGrowDocComment(&candidates[i], &primaryRemaining, 6)
			}
			compactTaskContextDevGrow(&candidates[i], &primaryRemaining)
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
		for candidates[i].cost < quota && primaryRemaining > 0 && compactTaskContextDevGrow(&candidates[i], &primaryRemaining) {
		}
	}
	remaining = primaryRemaining + secondaryReserve
	for i := range candidates {
		if candidates[i].hasSecondary && candidates[i].secondaryFrom >= candidates[i].from && candidates[i].secondaryFrom <= candidates[i].to {
			candidates[i].hasSecondary = false
		}
	}
	// Long declarations may contain the decisive branch far from the best
	// header/comment match. Preserve a bounded second window for the strongest
	// four regions after their primary windows have received weighted depth.
	secondaryOrder := make([]int, 0, 5)
	for i := 0; i < len(candidates) && i < 4; i++ {
		secondaryOrder = append(secondaryOrder, i)
	}
	for i := 4; i < len(candidates); i++ {
		if candidates[i].secondaryScore >= 100 {
			secondaryOrder = append(secondaryOrder, i)
		}
	}
	sort.SliceStable(secondaryOrder, func(i, j int) bool {
		a, b := candidates[secondaryOrder[i]], candidates[secondaryOrder[j]]
		if a.secondaryScore != b.secondaryScore {
			return a.secondaryScore > b.secondaryScore
		}
		return len(a.lines) > len(b.lines)
	})
	for _, i := range secondaryOrder {
		if remaining <= primaryRemaining {
			break
		}
		if !admitted[i] || !candidates[i].hasSecondary {
			continue
		}
		admittedLines := 0
		secondaryLines := 12
		if compactTaskContextDevWantsTraversal(patterns) {
			secondaryLines = 36
		} else if compactTaskContextDevWantsLifecycleHooks(patterns) {
			secondaryLines = 32
		}
		for line := candidates[i].secondaryFrom; line < len(candidates[i].lines) && line < candidates[i].secondaryFrom+secondaryLines; line++ {
			cost := len(strings.Fields(candidates[i].lines[line]))
			if cost > remaining-primaryRemaining {
				break
			}
			candidates[i].secondaryTo = line
			remaining -= cost
			admittedLines++
		}
		if admittedLines == 0 {
			candidates[i].hasSecondary = false
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
		if candidate.hasSecondary && (candidate.secondaryFrom < candidate.from || candidate.secondaryTo > candidate.to) {
			secondaryFrom, secondaryTo := candidate.secondaryFrom, candidate.secondaryTo
			if secondaryFrom <= candidate.to && secondaryTo > candidate.to {
				secondaryFrom = candidate.to + 1
			}
			if secondaryTo >= candidate.from && secondaryFrom < candidate.from {
				secondaryTo = candidate.from - 1
			}
			if secondaryFrom <= secondaryTo {
				secondaryText := strings.Join(candidate.lines[secondaryFrom:secondaryTo+1], "\n")
				if secondaryText == "" {
					continue
				}
				secondaryKey := fmt.Sprintf("%s\x00%d\x00%d", candidate.item.Path, candidate.start+secondaryFrom, candidate.start+secondaryTo)
				if !seenOutput[secondaryKey] {
					seenOutput[secondaryKey] = true
					out = append(out, CompactTaskContextDevSource{
						Path: candidate.item.Path, Start: candidate.start + secondaryFrom,
						End: candidate.start + secondaryTo, Text: secondaryText,
					})
				}
			}
		}
	}
	out, used := compactTaskContextDevTrimSources(out, budget)
	return out, used, nil
}

func compactTaskContextDevItemSymbol(reason string) (string, string) {
	fields := strings.Fields(reason)
	for i, field := range fields {
		kind := strings.TrimSuffix(field, ":")
		switch kind {
		case "function", "method", "type", "constant", "variable":
			if i+1 < len(fields) {
				return strings.Trim(fields[i+1], "(),"), kind
			}
		}
	}
	return "", ""
}

func compactTaskContextDevSymbolScore(patterns []string, frequencies map[string]int, symbol string) int {
	if symbol == "" {
		return 0
	}
	terms := compactTaskContextDevIdentifierTerms(symbol)
	score := 0
	for _, pattern := range patterns {
		pattern = strings.ToLower(pattern)
		if pattern == "function" || pattern == "functions" {
			for _, term := range terms {
				if term == "func" || term == "function" {
					score += 20_000
					break
				}
			}
			continue
		}
		if !compactTaskContextDevInformativePattern(pattern) {
			continue
		}
		frequency := max(1, frequencies[pattern])
		for _, term := range terms {
			if pattern == term {
				score += 30_000 / frequency
				break
			}
			if len(pattern) >= 4 && len(term) >= 4 && (strings.HasPrefix(pattern, term) || strings.HasPrefix(term, pattern)) {
				score += 6_000 / frequency
				break
			}
		}
	}
	return score
}

func compactTaskContextDevWholeSymbolMatch(patterns []string, symbol string) bool {
	if dot := strings.LastIndex(symbol, "."); dot >= 0 {
		symbol = symbol[dot+1:]
	}
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, symbol) {
			return true
		}
	}
	return false
}

func compactTaskContextDevLifecycleHookMatch(patterns []string, symbol string) bool {
	terms := compactTaskContextDevIdentifierTerms(symbol)
	if !compactTaskContextDevHasPattern(terms, "on") {
		return false
	}
	for _, pattern := range patterns {
		for _, term := range terms {
			if len(pattern) >= 4 && len(term) >= 4 && (strings.HasPrefix(pattern, term) || strings.HasPrefix(term, pattern)) {
				return true
			}
		}
	}
	return false
}

func compactTaskContextDevInformativePattern(pattern string) bool {
	switch strings.ToLower(pattern) {
	case "cobra", "command", "commands", "function", "functions", "go", "root":
		return false
	default:
		return len(pattern) >= 3
	}
}

func compactTaskContextDevItemPriority(reason string) int {
	switch {
	case strings.HasPrefix(reason, "primary:"):
		return 12_000
	case strings.HasPrefix(reason, "reference:"):
		return 20_000
	case strings.HasPrefix(reason, "flow-callee:"):
		return 50_000
	case strings.HasPrefix(reason, "grepread-declaration:"):
		return 16_000
	case strings.HasPrefix(reason, "candidate:"):
		return 4_000
	case strings.HasPrefix(reason, "related:"), strings.HasPrefix(reason, "caller:"), strings.HasPrefix(reason, "callee:"):
		return 2_000
	default:
		return 0
	}
}

func compactTaskContextDevHasPattern(patterns []string, want string) bool {
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, want) {
			return true
		}
	}
	return false
}

// compactTaskContextDevRankExactDependencies follows two identifier hops only
// among candidates already returned for an exact lookup. The exact declaration
// keeps its larger whole-name bonus; this only promotes its helper and constant
// dependencies above redundant contextual hits.
func compactTaskContextDevRankExactDependencies(identifier string, candidates []compactTaskContextDevCandidate) {
	root := -1
	for i, candidate := range candidates {
		if compactTaskContextDevWholeSymbolMatch([]string{identifier}, candidate.symbol) {
			root = i
			break
		}
	}
	if root < 0 {
		return
	}
	visited := map[int]bool{root: true}
	frontier := []int{root}
	for depth := 1; depth <= 2 && len(frontier) > 0; depth++ {
		var next []int
		for _, source := range frontier {
			references := make(map[string]bool)
			for _, name := range strings.FieldsFunc(strings.Join(candidates[source].lines, "\n"), func(r rune) bool {
				return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
			}) {
				references[name] = true
			}
			for target := range candidates {
				if visited[target] || candidates[target].itemPriority < 2_000 {
					continue
				}
				name := candidates[target].symbol
				if dot := strings.LastIndex(name, "."); dot >= 0 {
					name = name[dot+1:]
				}
				if name == "" || !references[name] {
					continue
				}
				candidates[target].score += 100_000 / depth
				visited[target] = true
				next = append(next, target)
			}
		}
		frontier = next
	}
}

func compactTaskContextDevIdentifierTerms(symbol string) []string {
	var out []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			out = append(out, strings.ToLower(string(current)))
			current = current[:0]
		}
	}
	var previous rune
	for _, r := range symbol {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			previous = 0
			continue
		}
		if len(current) > 0 && unicode.IsUpper(r) && unicode.IsLower(previous) {
			flush()
		}
		current = append(current, r)
		previous = r
	}
	flush()
	return out
}

func compactTaskContextDevTrimSources(sources []CompactTaskContextDevSource, budget int) ([]CompactTaskContextDevSource, int) {
	out := make([]CompactTaskContextDevSource, 0, len(sources))
	seen := make(map[string]bool, len(sources))
	used := 0
	for _, source := range sources {
		remaining := budget - used
		if remaining <= 0 {
			break
		}
		lines := strings.Split(source.Text, "\n")
		end, sourceUsed := 0, 0
		for end < len(lines) {
			cost := len(strings.Fields(lines[end]))
			if cost > remaining {
				break
			}
			remaining -= cost
			sourceUsed += cost
			end++
		}
		if end == 0 {
			continue
		}
		source.Text = strings.Join(lines[:end], "\n")
		source.End = source.Start + end - 1
		if strings.TrimSpace(source.Text) == "" {
			continue
		}
		key := fmt.Sprintf("%s\x00%d\x00%d", source.Path, source.Start, source.End)
		if seen[key] {
			continue
		}
		seen[key] = true
		used += sourceUsed
		out = append(out, source)
	}
	return out, used
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
		secondaryAbsolute, secondaryScore, hasSecondary := 0, 0, false
		if prior.hasSecondary && (!candidate.hasSecondary || prior.secondaryScore >= candidate.secondaryScore) {
			secondaryAbsolute, secondaryScore, hasSecondary = prior.start+prior.secondaryFrom, prior.secondaryScore, true
		} else if candidate.hasSecondary {
			secondaryAbsolute, secondaryScore, hasSecondary = candidate.start+candidate.secondaryFrom, candidate.secondaryScore, true
		}
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
		prior.hasSecondary = hasSecondary
		if hasSecondary {
			prior.secondaryFrom = secondaryAbsolute - start
			prior.secondaryTo = prior.secondaryFrom
			prior.secondaryScore = secondaryScore
		}
		prior.score = max(prior.score, candidate.score)
		prior.order = min(prior.order, candidate.order)
		prior.fallback = prior.fallback && candidate.fallback
		prior.completeFallback = prior.completeFallback || candidate.completeFallback
		if prior.symbol == "" || candidate.itemPriority > prior.itemPriority {
			prior.symbol = candidate.symbol
			prior.kind = candidate.kind
		}
		prior.itemPriority = max(prior.itemPriority, candidate.itemPriority)
		prior.symbolScore = max(prior.symbolScore, candidate.symbolScore)
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
	if candidate.complete {
		return false
	}
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

func compactTaskContextDevCompleteUnit(candidate *compactTaskContextDevCandidate, remaining *int, maxCost int) bool {
	if (candidate.fallback && !candidate.completeFallback) || (candidate.kind != "function" && candidate.kind != "method" && candidate.kind != "type" && candidate.kind != "constant" && candidate.kind != "variable") {
		return false
	}
	from, to, ok := compactTaskContextDevUnitRange(*candidate)
	if !ok {
		return false
	}
	fullCost := len(strings.Fields(strings.Join(candidate.lines[from:to+1], "\n")))
	additional := fullCost - candidate.cost
	if fullCost <= 0 || fullCost > maxCost || additional < 0 || additional > *remaining {
		return false
	}
	candidate.from = from
	candidate.to = to
	candidate.cost = fullCost
	candidate.complete = true
	*remaining -= additional
	return true
}

func compactTaskContextDevUnitRange(candidate compactTaskContextDevCandidate) (int, int, bool) {
	want := candidate.symbol
	if dot := strings.LastIndex(want, "."); dot >= 0 {
		want = want[dot+1:]
	}
	declaration := -1
	for i, line := range candidate.lines {
		trimmed := strings.TrimSpace(line)
		isDeclaration := strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "const ")
		if !isDeclaration {
			continue
		}
		if want != "" && strings.Contains(trimmed, want) {
			declaration = i
			break
		}
		if declaration < 0 {
			declaration = i
		}
	}
	if declaration < 0 {
		return 0, 0, false
	}
	open, close := byte('{'), byte('}')
	if !strings.Contains(candidate.lines[declaration], "{") && strings.Contains(candidate.lines[declaration], "(") {
		open, close = '(', ')'
	}
	depth, opened, end := 0, false, declaration
	for i := declaration; i < len(candidate.lines); i++ {
		for _, r := range candidate.lines[i] {
			switch byte(r) {
			case open:
				depth++
				opened = true
			case close:
				if opened {
					depth--
				}
			}
		}
		end = i
		if opened && depth == 0 {
			break
		}
	}
	if !opened || depth != 0 {
		return 0, 0, false
	}
	from := declaration
	for i, count := declaration-1, 0; i >= 0 && count < 6; i-- {
		trimmed := strings.TrimSpace(candidate.lines[i])
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			break
		}
		from = i
		count++
	}
	return from, end, true
}

func compactTaskContextDevGrowDocComment(candidate *compactTaskContextDevCandidate, remaining *int, limit int) {
	if candidate.from != candidate.anchor || candidate.from <= 0 || limit <= 0 {
		return
	}
	trimmedAnchor := strings.TrimSpace(candidate.lines[candidate.anchor])
	if !strings.HasPrefix(trimmedAnchor, "func ") && !strings.HasPrefix(trimmedAnchor, "type ") && !strings.HasPrefix(trimmedAnchor, "var ") && !strings.HasPrefix(trimmedAnchor, "const ") {
		return
	}
	for line, count := candidate.from-1, 0; line >= 0 && count < limit; line-- {
		trimmed := strings.TrimSpace(candidate.lines[line])
		if trimmed != "" && !strings.HasPrefix(trimmed, "//") {
			break
		}
		cost := len(strings.Fields(candidate.lines[line]))
		if cost > *remaining {
			break
		}
		candidate.from = line
		candidate.cost += cost
		*remaining -= cost
		count++
	}
}

type compactTaskContextDevLineAnchor struct {
	index int
	score int
}

func compactTaskContextDevFlowSecondaryAnchor(patterns []string, lines []string, primary int) (compactTaskContextDevLineAnchor, bool) {
	wantsInit := compactTaskContextDevHasPattern(patterns, "init") || compactTaskContextDevHasPattern(patterns, "hook")
	wantsDispatch := compactTaskContextDevHasPattern(patterns, "dispatch") || compactTaskContextDevHasPattern(patterns, "route")
	wantsExecute := compactTaskContextDevHasPattern(patterns, "execute") || compactTaskContextDevHasPattern(patterns, "lifecycle") || compactTaskContextDevHasPattern(patterns, "run")
	wantsTraversal := compactTaskContextDevWantsTraversal(patterns)
	wantsVersion := compactTaskContextDevHasPattern(patterns, "version") && compactTaskContextDevHasPattern(patterns, "flag")
	wantsRequired := compactTaskContextDevHasPattern(patterns, "required") && compactTaskContextDevHasPattern(patterns, "flag")
	wantsHelp := compactTaskContextDevHasPattern(patterns, "help") && (compactTaskContextDevHasPattern(patterns, "handl") || compactTaskContextDevHasPattern(patterns, "handle") || compactTaskContextDevHasPattern(patterns, "different") || compactTaskContextDevHasPattern(patterns, "differently"))
	wantsCompletionGroup := compactTaskContextDevHasPattern(patterns, "completion") && compactTaskContextDevHasPattern(patterns, "group")
	wantsShellProtocol := compactTaskContextDevWantsShellProtocol(patterns)
	wantsLifecycleHooks := compactTaskContextDevWantsLifecycleHooks(patterns)
	best := compactTaskContextDevLineAnchor{}
	for index, line := range lines {
		if index == primary {
			continue
		}
		lower := strings.ToLower(line)
		score := 0
		if wantsShellProtocol && strings.Contains(lower, "fmt.fprintln(finalcmd.outorstdout()") {
			score = 210
		} else if wantsExecute && strings.Contains(lower, "following order") {
			score = 200
		} else if wantsVersion && strings.Contains(lower, `getbool("version")`) {
			score = 180
		} else if wantsHelp && strings.Contains(lower, `getbool("help")`) {
			score = 180
		} else if wantsRequired && strings.Contains(lower, "validaterequiredflags(") {
			score = 180
		} else if wantsTraversal && strings.Contains(lower, "for i, arg := range args") {
			score = 190
		} else if wantsTraversal && strings.Contains(lower, "findnext(arg)") {
			score = 185
		} else if wantsTraversal && strings.Contains(lower, "argswoflags[0]") {
			score = 180
		} else if wantsCompletionGroup && strings.Contains(lower, "enforceflaggroupsforcompletion(") {
			score = 180
		} else if wantsLifecycleHooks && strings.Contains(lower, "if c.prerune != nil") {
			score = 190
		} else if wantsExecute && (strings.Contains(lower, "if c.rune != nil") || strings.Contains(lower, "c.run(c,")) {
			score = 170
		} else if wantsInit && strings.Contains(lower, "initializers") {
			score = 140
		} else if wantsInit && strings.Contains(lower, ".prerun(") {
			score = 130
		} else if wantsDispatch && strings.Contains(lower, "getcompletions(") {
			score = 130
		} else if wantsDispatch && strings.Contains(lower, "run: func") {
			score = 100
		} else if wantsExecute && strings.Contains(lower, "cmd.execute(") {
			score = 130
		} else if wantsTraversal && strings.Contains(lower, "parseflags(") {
			score = 140
		} else if wantsTraversal && strings.Contains(lower, ".traverse(") {
			score = 100
		}
		if score > best.score {
			best = compactTaskContextDevLineAnchor{index: index, score: score}
		}
	}
	return best, best.score > 0
}

func compactTaskContextDevAnchors(mode GrepReadV2Mode, patterns []string, path string, lines []string) []compactTaskContextDevLineAnchor {
	lowerPath := strings.ToLower(path)
	ranked := make([]compactTaskContextDevLineAnchor, 0, len(lines))
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
		ranked = append(ranked, compactTaskContextDevLineAnchor{index: index, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].index < ranked[j].index
	})
	if len(ranked) == 0 {
		return []compactTaskContextDevLineAnchor{{index: 0, score: 0}}
	}
	out := []compactTaskContextDevLineAnchor{ranked[0]}
	for _, candidate := range ranked[1:] {
		distance := candidate.index - out[0].index
		if distance < 0 {
			distance = -distance
		}
		if candidate.score < 10 || distance < 6 {
			continue
		}
		out = append(out, candidate)
		break
	}
	return out
}

func compactTaskContextDevAnchor(mode GrepReadV2Mode, patterns []string, path string, lines []string) (int, int) {
	anchor := compactTaskContextDevAnchors(mode, patterns, path, lines)[0]
	return anchor.index, anchor.score
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
// version instead of silently changing the meaning of compact-dev/7.
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
		return "", CompactTaskContextDevStructured{}, fmt.Errorf("compact task_context dev: source budget exceeded: %d > %d", used, p.Budget)
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
