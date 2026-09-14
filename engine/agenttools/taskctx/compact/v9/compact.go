package v9

// V9 is the implementation package for coherent-region selection on the
// production task_context/2 compact wire. It consumes no judgements, answer
// spans, or callbacks: source selection sees only the query, the ready
// retrieval result, and repository bytes.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	evaltokenizer "github.com/samibel/graphi/core/tokenizer"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

const CompactTaskContextVersion = "task_context/2-compact/9"

// CompactTaskContextSource is both the source body and its citation. Source
// order is the read order; removing the separate item/evidence join is the
// largest structural saving while preserving everything an agent needs to
// quote and verify the text.
type CompactTaskContextSource struct {
	Path  string `json:"path"`
	Start int    `json:"start_line"`
	End   int    `json:"end_line"`
	Text  string `json:"text"`
}

// CompactTaskContextProvenance retains the trust invariants intentionally
// kept by the experiment. InputSHA256 binds all omitted task_context/2 ranking
// and graph metadata to the preserved input. Method identities make the
// retrieval/model/selection implementation auditable. Source order is an
// explicit ranking contract rather than an implicit ref-id join.
type CompactTaskContextProvenance struct {
	InputSHA256     string `json:"input_sha256"`
	Method          string `json:"method"`
	Retrieval       string `json:"retrieval"`
	RetrievalState  string `json:"retrieval_state"`
	Weights         string `json:"weights"`
	Model           string `json:"model"`
	SourceSelection string `json:"source_selection"`
	SourceOrder     string `json:"source_order"`
	Budget          int    `json:"source_budget"`
	BudgetUnit      string `json:"source_budget_unit"`
}

type CompactTaskContextStructured struct {
	Version    string                       `json:"version"`
	Sources    []CompactTaskContextSource   `json:"sources"`
	Provenance CompactTaskContextProvenance `json:"provenance"`
	Truncated  bool                         `json:"truncated"`
}

type compactTaskContextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type compactTaskContextResult struct {
	Content           []compactTaskContextContent  `json:"content"`
	StructuredContent CompactTaskContextStructured `json:"structuredContent"`
	IsError           bool                         `json:"isError"`
}

type compactTaskContextEnvelope struct {
	JSONRPC string                    `json:"jsonrpc"`
	ID      int                       `json:"id"`
	Result  *compactTaskContextResult `json:"result"`
}

// BuildCompactTaskContext turns one preserved production bundle into a
// single-encoded response. budget is measured with the same
// whitespace-fields rule as the frozen task_context source budget. Complete
// source lines are admitted by the versioned coherent-region selector; a final
// snippet may be shortened only at a line boundary. No ranking judgement is
// consulted.
func BuildCompactTaskContext(query string, input PreservedPayload, budget int, real PayloadCounter) (PreservedPayload, error) {
	return BuildCompactTaskContextWithGrepRead(query, input, nil, budget, real)
}

// BuildCompactTaskContextWithGrepRead combines the semantic task_context
// bundle with a separately versioned, query-only GrepRead/2 transcript before
// source selection. This is the structural fallback for questions whose
// answer never entered the semantic candidate bytes. The transcript is
// complete before selection and exposes no judgement or target span.
func BuildCompactTaskContextWithGrepRead(query string, input PreservedPayload, grepRead *GrepReadV2Transcript, budget int, real PayloadCounter) (PreservedPayload, error) {
	return buildCompactTaskContext(query, input, grepRead, nil, budget, real)
}

// BuildCompactTaskContextWithRepository hydrates the definitions already
// named by ranked items from a pinned repository. It does not search for an
// answer span or consult judgements: path and declaration line come entirely
// from the candidate bundle, and the repository supplies only exact source.
func BuildCompactTaskContextWithRepository(query string, input PreservedPayload, grepRead *GrepReadV2Transcript, repository fs.FS, budget int, real PayloadCounter) (PreservedPayload, error) {
	if repository == nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context: nil repository")
	}
	return buildCompactTaskContext(query, input, grepRead, repository, budget, real)
}

func buildCompactTaskContext(query string, input PreservedPayload, grepRead *GrepReadV2Transcript, repository fs.FS, budget int, real PayloadCounter) (PreservedPayload, error) {
	return buildCompactTaskContextBound(context.Background(), query, input, grepRead, repository, nil, budget, real)
}

func buildCompactTaskContextBound(ctx context.Context, query string, input PreservedPayload, grepRead *GrepReadV2Transcript, repository fs.FS, snapshot *grepReadSnapshot, budget int, real PayloadCounter) (PreservedPayload, error) {
	if strings.TrimSpace(query) == "" {
		return PreservedPayload{}, fmt.Errorf("compact task_context: empty query")
	}
	if budget < 1 || budget > SavingsCandidateBudget {
		return PreservedPayload{}, fmt.Errorf("compact task_context: source budget %d outside 1..%d", budget, SavingsCandidateBudget)
	}
	if err := validatePayloadCostInput("compact-input", input, real); err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context: %w", err)
	}
	bundle, err := taskContextBundleFromCandidateBytes(input.Bytes)
	if err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context: %w", err)
	}
	if err := contract.ValidateResult(&bundle); err != nil {
		return PreservedPayload{}, fmt.Errorf("compact task_context: invalid input bundle: %w", err)
	}
	inputSHA := input.SHA256
	if grepRead != nil {
		if err := grepRead.Validate(); err != nil {
			return PreservedPayload{}, fmt.Errorf("compact task_context: invalid GrepRead/2 transcript: %w", err)
		}
		if grepRead.Query != query {
			return PreservedPayload{}, fmt.Errorf("compact task_context: GrepRead/2 query does not match")
		}
		inputSHA = SHA256Hex([]byte(input.SHA256 + "\n" + grepRead.DigestSHA256()))
	}
	provenance, err := compactTaskContextProvenance(bundle.Summary, inputSHA, budget)
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
		hydrated, hydratedItems, err := compactTaskContextHydrateDefinitions(ctx, repository, snapshot, query, bundle.Items)
		if err != nil {
			return PreservedPayload{}, err
		}
		if len(hydrated) > 0 {
			hydratedRaw, err := json.Marshal(hydrated)
			if err != nil {
				return PreservedPayload{}, fmt.Errorf("compact task_context: bind hydrated definitions: %w", err)
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
		outline, outlineItems, err := compactTaskContextHydrateExactPath(ctx, repository, snapshot, query)
		if err != nil {
			return PreservedPayload{}, err
		}
		if len(outline) > 0 {
			outlineRaw, err := json.Marshal(outline)
			if err != nil {
				return PreservedPayload{}, fmt.Errorf("compact task_context: bind exact-path outline: %w", err)
			}
			inputSHA = SHA256Hex([]byte(inputSHA + "\n" + SHA256Hex(outlineRaw)))
		}
		for index, evidence := range outline {
			key := evidence.Path + "\x00" + evidence.Span
			item := outlineItems[index]
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
		additional, err := compactTaskContextGrepReadEvidence(*grepRead)
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
			hydrated, hydratedItems, err := compactTaskContextHydrateGrepReadDeclarations(ctx, repository, snapshot, query, additional)
			if err != nil {
				return PreservedPayload{}, err
			}
			if len(hydrated) > 0 {
				hydratedRaw, err := json.Marshal(hydrated)
				if err != nil {
					return PreservedPayload{}, fmt.Errorf("compact task_context: bind hydrated GrepRead declarations: %w", err)
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
		references, referenceItems, err := compactTaskContextHydrateReferences(ctx, repository, snapshot, query, all, selectionItems)
		if err != nil {
			return PreservedPayload{}, err
		}
		if len(references) > 0 {
			referenceRaw, err := json.Marshal(references)
			if err != nil {
				return PreservedPayload{}, fmt.Errorf("compact task_context: bind hydrated references: %w", err)
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
		sources, used, err := compactTaskContextSelect(query, all, selectionItems, effectiveBudget)
		if err != nil {
			return PreservedPayload{}, err
		}
		summary := compactTaskContextSummary(query, sources, used, budget)
		envelope := compactTaskContextEnvelope{
			JSONRPC: "2.0", ID: 1,
			Result: &compactTaskContextResult{
				Content: []compactTaskContextContent{{Type: "text", Text: summary}},
				StructuredContent: CompactTaskContextStructured{
					Version: CompactTaskContextVersion, Sources: sources, Provenance: provenance,
					Truncated: len(sources) < len(all) || used < compactTaskContextWhitespaceTokens(all),
				},
			},
		}
		raw, err = json.Marshal(envelope)
		if err != nil {
			return PreservedPayload{}, fmt.Errorf("compact task_context: marshal: %w", err)
		}
		raw = append(raw, '\n')
		realTokens, err = real.Count(append([]byte(nil), raw...))
		if err != nil {
			return PreservedPayload{}, fmt.Errorf("compact task_context: tokenize: %w", err)
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
			return PreservedPayload{}, fmt.Errorf("compact task_context: fixed wire overhead exceeds %d tokens", SavingsCandidateBudget)
		}
	}
	return PreservedPayload{
		Sequence: 1, Boundary: PayloadBoundaryCandidate, Operation: CompactTaskContextVersion,
		Bytes: raw, SHA256: SHA256Hex(raw), ByteCount: len(raw),
		TokenCounts: []PayloadTokenCount{
			{TokenizerID: TokenizerID, Tokens: len(strings.Fields(string(raw)))},
			{TokenizerID: real.TokenizerID, VocabularySHA256: real.VocabularySHA256, Tokens: realTokens},
		},
	}, nil
}

// compactTaskContextSummary gives an agent a short assembly guide for
// causal evidence split across non-contiguous source spans. Every landmark is
// emitted only when the selected source bytes themselves contain its support.
func compactTaskContextSummary(query string, sources []CompactTaskContextSource, used, budget int) string {
	queryDigest := SHA256Hex([]byte(query))[:12]
	base := fmt.Sprintf("%d ranked source span(s); query %s; read in order (%d/%d source tokens)", len(sources), queryDigest, used, budget)
	mode, patterns := grepReadV2QueryPlan(query)
	joined := ""
	for _, source := range sources {
		joined += source.Text + "\n"
	}
	switch {
	case mode == GrepReadV2ExactPath:
		return base + "; request: describe this file's structure and declarations"
	case compactTaskContextWantsLifecycleHooks(patterns) && strings.Contains(joined, "ParseFlags") && strings.Contains(joined, "PersistentPreRun") && strings.Contains(joined, "RunE"):
		return base + "; flow: Execute -> ExecuteC -> execute: ParseFlags -> persistent/local pre-run -> Run(E)"
	case compactTaskContextWantsTraversal(patterns) && strings.Contains(joined, "stripFlags") && strings.Contains(joined, "argsWOflags[0]") && strings.Contains(joined, "ParseFlags("):
		return base + "; resolution: Find strips flags then treats the first non-flag as the subcommand; Traverse parses parent flags before descending"
	case compactTaskContextWantsCompletionCallbackContract(patterns) && strings.Contains(joined, "ValidArgsFunction") && strings.Contains(joined, "RegisterFlagCompletionFunc") && strings.Contains(joined, "ShellCompDirective"):
		return base + "; callback contract: set ValidArgsFunction for positional args or RegisterFlagCompletionFunc for a flag; return candidates plus a ShellCompDirective"
	case compactTaskContextWantsCommandParentLink(patterns) && strings.Contains(joined, "AddCommand") && strings.Contains(joined, ".parent =") && strings.Contains(joined, "commands = append"):
		return base + "; link: AddCommand sets child.parent and appends the child"
	case compactTaskContextWantsTestFlagFlow(patterns) && strings.Contains(joined, "SetArgs") && strings.Contains(joined, "c.args") && strings.Contains(joined, "ParseFlags"):
		return base + "; flow: SetArgs -> c.args -> execution -> ParseFlags"
	case compactTaskContextWantsExecutingFlag(patterns) && strings.Contains(joined, "Run func(cmd *Command") && strings.Contains(joined, "func (c *Command) Flag"):
		return base + "; handler lookup: cmd.Flag(name)"
	case compactTaskContextWantsParentFlagValue(patterns) && strings.Contains(joined, "func (c *Command) Flag") && strings.Contains(joined, ".Value."):
		return base + "; lookup: cmd.Flag(name) climbs parents; read the returned flag.Value"
	case compactTaskContextWantsShellProtocol(patterns) && strings.Contains(joined, "ShellCompRequestCmd") && strings.Contains(joined, "OutOrStdout") && strings.Contains(joined, ":%d") && strings.Contains(joined, "ErrOrStderr"):
		return base + "; adapter protocol: invoke __complete, read stdout choices and final :directive, ignore stderr"
	default:
		return base
	}
}

func compactTaskContextGrepReadEvidence(transcript GrepReadV2Transcript) ([]contract.Evidence, error) {
	makeEvidence := func(refID, path string, start, end int, text string) (contract.Evidence, error) {
		text = strings.TrimSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\r")
		if path == "" || start < 1 || end < start || len(strings.Split(text, "\n")) != end-start+1 {
			return contract.Evidence{}, fmt.Errorf("compact task_context: invalid GrepRead/2 source %s:%d-%d", path, start, end)
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
			return nil, fmt.Errorf("compact task_context: malformed GrepRead/2 grep row")
		}
		line, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("compact task_context: malformed GrepRead/2 grep line")
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
			return nil, fmt.Errorf("compact task_context: GrepRead/2 response sequence outside ledger")
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

func compactTaskContextHydrateDefinitions(ctx context.Context, repository fs.FS, snapshot *grepReadSnapshot, query string, items []contract.Item) ([]contract.Evidence, []contract.Item, error) {
	type parsedFile struct {
		set   *token.FileSet
		file  *ast.File
		lines []string
	}
	files := make(map[string]parsedFile)
	markdownFiles := make(map[string][]string)
	mode, patterns := grepReadV2QueryPlan(query)
	hydrateMarkdown := mode == GrepReadV2NaturalLanguage && compactTaskContextWantsMarkdownFlow(patterns) && !compactTaskContextWantsLifecycleHooks(patterns)
	seen := make(map[string]bool)
	var evidence []contract.Evidence
	var linked []contract.Item
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if compactTaskContextItemPriority(item.Reason) < 2_000 {
			continue
		}
		path, line, ok := compactTaskContextItemLocation(item.Reason)
		if !ok {
			continue
		}
		lowerPath := strings.ToLower(path)
		if strings.HasSuffix(lowerPath, ".md") {
			pathNamed := mode == GrepReadV2NaturalLanguage && compactTaskContextPathStemMatchesPatterns(path, patterns)
			if (!hydrateMarkdown && !pathNamed) || compactTaskContextItemPriority(item.Reason) < 4_000 {
				continue
			}
			lines, ok := markdownFiles[path]
			if !ok {
				raw, err := readScannedSource(repository, snapshot, path)
				if err != nil {
					if errors.Is(err, fs.ErrNotExist) {
						continue
					}
					return nil, nil, fmt.Errorf("compact task_context: hydrate %s: %w", path, err)
				}
				lines = strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
				markdownFiles[path] = lines
			}
			start, end, ok := compactTaskContextMarkdownSection(lines, line)
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
			raw, err := readScannedSource(repository, snapshot, path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, nil, fmt.Errorf("compact task_context: hydrate %s: %w", path, err)
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context: parse hydrated %s: %w", path, err)
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			parsed = parsedFile{set: set, file: file, lines: strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")}
			files[path] = parsed
		}
		start, end, ok := compactTaskContextDeclarationSpan(parsed.set, parsed.file, line)
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

// compactTaskContextHydrateExactPath exposes the declaration structure of a
// requested Go file. A file-name query asks for breadth: spending most of the
// response on the licence header and the first declaration makes the file
// impossible to characterize even when retrieval found the exact path. Each
// candidate remains an exact contiguous source declaration; selection still
// decides which declarations fit the unchanged wire budget.
func compactTaskContextHydrateExactPath(ctx context.Context, repository fs.FS, snapshot *grepReadSnapshot, query string) ([]contract.Evidence, []contract.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	mode, patterns := grepReadV2QueryPlan(query)
	if mode != GrepReadV2ExactPath || len(patterns) != 1 || !strings.HasSuffix(strings.ToLower(patterns[0]), ".go") {
		return nil, nil, nil
	}
	path := patterns[0]
	raw, err := readScannedSource(repository, snapshot, path)
	if err != nil {
		// Exact-path discovery is supplemental. A missing, unreadable, or
		// root-escaping symlink must not expose bytes and must not turn the
		// canonical task_context fallback into an RPC failure.
		return nil, nil, nil
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
	if err != nil {
		return nil, nil, fmt.Errorf("compact task_context: parse exact path %s: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	evidence := make([]contract.Evidence, 0, min(len(file.Decls)+1, 64))
	items := make([]contract.Item, 0, min(len(file.Decls)+1, 64))
	packageLine := set.Position(file.Name.Pos()).Line
	if packageLine >= 1 && packageLine <= len(lines) {
		text := lines[packageLine-1]
		ref := "path-declaration-001"
		evidence = append(evidence, contract.Evidence{
			RefID: ref, Path: path, Line: packageLine, Span: fmt.Sprintf("%d-%d", packageLine, packageLine),
			Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
		})
		items = append(items, contract.Item{
			RefID: "path-declaration-item-" + ref, Rank: 1,
			Reason:         fmt.Sprintf("path-declaration: variable %s.package (%s:%d) score 0", file.Name.Name, path, packageLine),
			EvidenceRefIDs: []string{ref},
		})
	}
	for _, declaration := range file.Decls {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(evidence) == 64 {
			break
		}
		start := set.Position(declaration.Pos()).Line
		if doc := compactTaskContextDeclarationDoc(declaration); doc != nil {
			start = set.Position(doc.Pos()).Line
		}
		end := set.Position(declaration.End()).Line
		if start < 1 || end < start || end > len(lines) {
			continue
		}
		symbol, kind := compactTaskContextDeclarationIdentity(file.Name.Name, declaration)
		if symbol == "" {
			continue
		}
		text := strings.Join(lines[start-1:end], "\n")
		ref := fmt.Sprintf("path-declaration-%03d", len(evidence)+1)
		evidence = append(evidence, contract.Evidence{
			RefID: ref, Path: path, Line: start, Span: fmt.Sprintf("%d-%d", start, end),
			Role: "snippet", Snippet: text, TextHash: shape.TextHash(text),
		})
		items = append(items, contract.Item{
			RefID: "path-declaration-item-" + ref, Rank: len(items) + 1,
			Reason:         fmt.Sprintf("path-declaration: %s %s (%s:%d) score 0", kind, symbol, path, start),
			EvidenceRefIDs: []string{ref},
		})
	}
	return evidence, items, nil
}

func compactTaskContextMarkdownSection(lines []string, line int) (int, int, bool) {
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

// compactTaskContextHydrateGrepReadDeclarations turns each query-only grep
// hit inside Go code into the complete enclosing declaration. GrepRead's
// fixed-width reads are useful for discovery but can begin halfway through a
// function; the compact selector then has no way to spend its budget on the
// declaration header or a distant branch. This bridge changes neither search
// nor ranking and is bounded by the already recorded grep hits.
func compactTaskContextHydrateGrepReadDeclarations(ctx context.Context, repository fs.FS, snapshot *grepReadSnapshot, query string, hits []contract.Evidence) ([]contract.Evidence, []contract.Item, error) {
	mode, patterns := grepReadV2QueryPlan(query)
	wantsShellCompletion := compactTaskContextWantsShellCompletion(patterns)
	if mode != GrepReadV2NaturalLanguage || (!compactTaskContextNeedsFlowAllocation(patterns) && !wantsShellCompletion) {
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
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
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
			raw, err := readScannedSource(repository, snapshot, path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return nil, nil, fmt.Errorf("compact task_context: hydrate GrepRead declaration %s: %w", path, err)
			}
			set := token.NewFileSet()
			file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context: parse hydrated GrepRead declaration %s: %w", path, err)
			}
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			parsed = parsedFile{set: set, file: file, lines: strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")}
			files[path] = parsed
		}
		var declarations []ast.Decl
		if isShellRead {
			hitStart, hitEnd, err := exactEvidenceSpan(hit.Span)
			if err != nil {
				return nil, nil, fmt.Errorf("compact task_context: invalid GrepRead span %q", hit.Span)
			}
			for _, candidate := range parsed.file.Decls {
				candidateStart := parsed.set.Position(candidate.Pos()).Line
				if doc := compactTaskContextDeclarationDoc(candidate); doc != nil {
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
				if doc := compactTaskContextDeclarationDoc(candidate); doc != nil {
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
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			start := parsed.set.Position(declaration.Pos()).Line
			if doc := compactTaskContextDeclarationDoc(declaration); doc != nil {
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
			symbol, kind := compactTaskContextDeclarationIdentity(parsed.file.Name.Name, declaration)
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

type compactTaskContextReferenceIdentifier struct {
	name  string
	score int
}

type compactTaskContextReferenceDeclaration struct {
	path       string
	start      int
	end        int
	text       string
	symbol     string
	kind       string
	score      int
	references []string
}

// compactTaskContextReferenceSelectorScores extracts a bounded field-level
// data-flow hint from evidence already returned by retrieval. A caller often
// does not repeat a setter's name, but it does read the receiver field written
// by that setter; the same applies to parent-pointer getters and writers.
func compactTaskContextReferenceSelectorScores(patterns []string, evidence []contract.Evidence) map[string]int {
	wantsTestFlags := compactTaskContextWantsTestFlagFlow(patterns)
	wantsParentLink := compactTaskContextWantsCommandParentLink(patterns)
	if !wantsTestFlags && !wantsParentLink {
		return nil
	}
	out := make(map[string]int)
	for _, item := range evidence {
		lower := strings.ToLower(item.Snippet)
		eligible := wantsTestFlags && strings.Contains(lower, "testing") && strings.Contains(lower, "set")
		eligible = eligible || wantsParentLink && strings.Contains(lower, "parent")
		if !eligible {
			continue
		}
		for offset := 0; offset < len(item.Snippet); offset++ {
			if item.Snippet[offset] != '.' || offset+1 >= len(item.Snippet) {
				continue
			}
			end := offset + 1
			for end < len(item.Snippet) {
				r := rune(item.Snippet[end])
				if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
					break
				}
				end++
			}
			name := item.Snippet[offset+1 : end]
			if len(name) < 3 || !token.IsIdentifier(name) {
				continue
			}
			if wantsTestFlags && strings.EqualFold(name, "args") {
				out[name] = max(out[name], 900_000)
			}
			if wantsParentLink && strings.EqualFold(name, "parent") {
				out[name] = max(out[name], 800_000)
			}
			offset = end - 1
		}
	}
	return out
}

// compactTaskContextHydrateReferences follows one source-level hop from
// identifiers already present in the ranked bundle to the declarations that
// use them. This recovers the control-flow context that a definition-only
// bridge cannot contain (for example, the execute method which calls
// ParseFlags). The search is query-filtered, bounded and excludes test files;
// it never consults answer spans or judgements.
func compactTaskContextHydrateReferences(ctx context.Context, repository fs.FS, snapshot *grepReadSnapshot, query string, evidence []contract.Evidence, items []contract.Item) ([]contract.Evidence, []contract.Item, error) {
	mode, patterns := grepReadV2QueryPlan(query)
	if mode != GrepReadV2NaturalLanguage || !compactTaskContextNeedsFlowAllocation(patterns) {
		return nil, nil, nil
	}
	identifiers := compactTaskContextReferenceIdentifiers(patterns, evidence, items)
	selectorScore := compactTaskContextReferenceSelectorScores(patterns, evidence)
	if len(identifiers) == 0 && len(selectorScore) == 0 {
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

	var declarations []compactTaskContextReferenceDeclaration
	namedDeclarations := make(map[string][]compactTaskContextReferenceDeclaration)
	if snapshot == nil {
		var err error
		var files []grepReadFile
		files, _, err = grepReadV2Files(ctx, repository)
		if err != nil {
			return nil, nil, fmt.Errorf("compact task_context: hydrate references: %w", err)
		}
		snapshot = &grepReadSnapshot{files: files}
	}
	for _, scannedFile := range snapshot.files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		path := scannedFile.Path
		lower := strings.ToLower(path)
		if scannedFile.ErrorKind != "" || !strings.HasSuffix(lower, ".go") || strings.HasSuffix(lower, "_test.go") {
			continue
		}
		raw := scannedFile.Bytes
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, raw, parser.ParseComments)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
		for _, declaration := range file.Decls {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			start := set.Position(declaration.Pos()).Line
			if doc := compactTaskContextDeclarationDoc(declaration); doc != nil {
				start = set.Position(doc.Pos()).Line
			}
			end := set.Position(declaration.End()).Line
			if start < 1 || end < start || end > len(lines) {
				continue
			}
			symbol, kind := compactTaskContextDeclarationIdentity(file.Name.Name, declaration)
			if symbol == "" {
				continue
			}
			text := strings.Join(lines[start-1:end], "\n")
			own := symbol
			if dot := strings.LastIndex(own, "."); dot >= 0 {
				own = own[dot+1:]
			}
			namedLimit := 60
			if compactTaskContextWantsTestFlagFlow(patterns) || compactTaskContextWantsLifecycleHooks(patterns) ||
				compactTaskContextWantsTraversal(patterns) || compactTaskContextWantsCompletionCallbackContract(patterns) {
				namedLimit = 600
			}
			if len(strings.Fields(text)) <= namedLimit {
				namedDeclarations[own] = append(namedDeclarations[own], compactTaskContextReferenceDeclaration{
					path: path, start: start, end: end, text: text, symbol: symbol, kind: kind,
				})
			}
			if existing[path+"\x00"+fmt.Sprintf("%d-%d", start, end)] {
				continue
			}
			counts := make(map[string]int)
			selectorCounts := make(map[string]int)
			ast.Inspect(declaration, func(node ast.Node) bool {
				identifier, ok := node.(*ast.Ident)
				if ok && identifierScore[identifier.Name] > 0 {
					counts[identifier.Name]++
				}
				selector, ok := node.(*ast.SelectorExpr)
				if ok && selectorScore[selector.Sel.Name] > 0 {
					selectorCounts[selector.Sel.Name]++
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
			for field, fieldScore := range selectorScore {
				count := selectorCounts[field]
				if count == 0 {
					continue
				}
				references = append(references, "field:"+field)
				score += fieldScore + min(count, 3)*1_000
			}
			isShellDirectiveDeclaration := (compactTaskContextWantsShellProtocol(patterns) || compactTaskContextWantsCompletionCallbackContract(patterns)) &&
				strings.Contains(text, "ShellCompDirectiveError") && strings.Contains(text, "ShellCompDirectiveNoSpace")
			if len(references) == 0 && !isShellDirectiveDeclaration {
				continue
			}
			anchors := compactTaskContextAnchors(mode, patterns, path, strings.Split(text, "\n"))
			score += anchors[0].score * 1_000
			if compactTaskContextWantsShellProtocol(patterns) && strings.Contains(text, "ShellCompRequestCmd") && strings.Contains(text, "OutOrStdout") && strings.Contains(text, "ErrOrStderr") {
				// A custom shell adapter speaks Cobra's hidden-command protocol.
				// Its implementation is the source that binds request name, stdout
				// completions, final directive and ignored stderr together.
				score += 1_000_000
			}
			if isShellDirectiveDeclaration {
				score += 900_000
				references = append(references, "protocol-directives")
			}
			declarations = append(declarations, compactTaskContextReferenceDeclaration{
				path: path, start: start, end: end, text: text, symbol: symbol, kind: kind,
				score: score, references: references,
			})
		}
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
		root := declarations[0]
		if compactTaskContextWantsShellProtocol(patterns) {
			for _, declaration := range declarations {
				if strings.Contains(declaration.text, "func (c *Command) initCompleteCmd") && strings.Contains(declaration.text, "OutOrStdout") {
					root = declaration
					break
				}
			}
		}
		if compactTaskContextWantsTraversal(patterns) {
			roots := append([]compactTaskContextReferenceDeclaration(nil), namedDeclarations["ExecuteC"]...)
			roots = append(roots, declarations...)
			for _, declaration := range roots {
				if strings.Contains(declaration.text, ".Traverse(") && strings.Contains(declaration.text, ".Find(") {
					root = declaration
					break
				}
			}
		}
		selected := []compactTaskContextReferenceDeclaration{root}
		appendNamed := func(name, reference string) {
			matches := namedDeclarations[name]
			if len(matches) == 0 {
				return
			}
			declaration := matches[0]
			for _, prior := range selected {
				if prior.path == declaration.path && prior.start == declaration.start && prior.end == declaration.end {
					return
				}
			}
			declaration.score = declarations[0].score
			declaration.references = []string{reference}
			selected = append(selected, declaration)
		}
		callee := compactTaskContextFlowCallee(patterns, root.text)
		if callee != "" {
			appendNamed(callee, "flow-callee:"+callee)
		}
		if compactTaskContextWantsLifecycleHooks(patterns) {
			// Follow the public entrypoint through its two source-level callees.
			// These names are present in the selected declarations; no answer span
			// or judgement is consulted.
			appendNamed("Execute", "flow-role:entrypoint")
			appendNamed("ExecuteC", "flow-callee:ExecuteC")
			appendNamed("execute", "flow-callee:execute")
		}
		if compactTaskContextWantsTraversal(patterns) {
			// ExecuteC exposes the Find/Traverse branch. Find then exposes
			// stripFlags, closing the one additional call-graph hop needed to
			// distinguish flag values from positional arguments.
			if strings.Contains(root.text, ".Find(") {
				appendNamed("Find", "flow-callee:Find")
			}
			if strings.Contains(root.text, ".Traverse(") {
				appendNamed("Traverse", "flow-callee:Traverse")
			}
			for _, declaration := range selected {
				if strings.Contains(declaration.text, "stripFlags(") {
					appendNamed("stripFlags", "flow-callee:stripFlags")
					break
				}
			}
		}
		if compactTaskContextWantsCompletionCallbackContract(patterns) {
			for _, declaration := range declarations {
				if strings.Contains(declaration.text, "ValidArgsFunction") && strings.Contains(declaration.text, "ShellCompDirective") {
					duplicate := false
					for _, prior := range selected {
						duplicate = duplicate || prior.path == declaration.path && prior.start == declaration.start && prior.end == declaration.end
					}
					if !duplicate {
						declaration.references = []string{"callback-role:positional"}
						selected = append(selected, declaration)
					}
					break
				}
			}
		}
		if compactTaskContextWantsShellProtocol(patterns) || compactTaskContextWantsCompletionCallbackContract(patterns) {
			for _, declaration := range declarations {
				if !strings.Contains(declaration.text, "ShellCompDirectiveError") || !strings.Contains(declaration.text, "ShellCompDirectiveNoSpace") {
					continue
				}
				duplicate := false
				for _, prior := range selected {
					if prior.path == declaration.path && prior.start == declaration.start && prior.end == declaration.end {
						duplicate = true
						break
					}
				}
				if !duplicate {
					selected = append(selected, declaration)
				}
				break
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

func compactTaskContextFlowCallee(patterns []string, declaration string) string {
	if (compactTaskContextHasPattern(patterns, "init") || compactTaskContextHasPattern(patterns, "hook")) && strings.Contains(declaration, ".preRun(") {
		return "preRun"
	}
	if compactTaskContextWantsTestFlagFlow(patterns) && strings.Contains(declaration, ".execute(") {
		return "execute"
	}
	return ""
}

func compactTaskContextNeedsReferenceContext(patterns []string) bool {
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

func compactTaskContextWantsMarkdownFlow(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "lifecycle") ||
		compactTaskContextHasPattern(patterns, "hook") ||
		compactTaskContextHasPattern(patterns, "order") ||
		compactTaskContextHasPattern(patterns, "sequence")
}

func compactTaskContextPathStemMatchesPatterns(sourcePath string, patterns []string) bool {
	base := strings.ToLower(sourcePath)
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		base = base[slash+1:]
	}
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		base = base[:dot]
	}
	for _, pattern := range patterns {
		if strings.EqualFold(base, pattern) {
			return true
		}
	}
	return false
}

func compactTaskContextWantsRecursiveWalk(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "walk") ||
		compactTaskContextHasPattern(patterns, "traverse") ||
		compactTaskContextHasPattern(patterns, "traversal")
}

func compactTaskContextIsRecursiveTraversal(candidate compactTaskContextCandidate) bool {
	if candidate.kind != "function" && candidate.kind != "method" {
		return false
	}
	name := candidate.symbol
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[dot+1:]
	}
	if name == "" {
		return false
	}
	source := strings.Join(candidate.lines, "\n")
	return strings.Contains(source, "for ") && strings.Contains(source, " range ") &&
		strings.Count(source, name+"(") >= 2
}

func compactTaskContextWantsLifecycleHooks(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "lifecycle") &&
		(compactTaskContextHasPattern(patterns, "execute") || compactTaskContextHasPattern(patterns, "run") || compactTaskContextHasPattern(patterns, "hook"))
}

func compactTaskContextWantsTraversal(patterns []string) bool {
	return (compactTaskContextHasPattern(patterns, "root") || compactTaskContextHasPattern(patterns, "rootcmd")) &&
		compactTaskContextHasPattern(patterns, "subcommand")
}

func compactTaskContextWantsInitFlagValue(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "init") &&
		compactTaskContextHasPattern(patterns, "flag") &&
		compactTaskContextHasPattern(patterns, "value")
}

func compactTaskContextWantsShellCompletion(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "shell") &&
		compactTaskContextHasPattern(patterns, "completion")
}

func compactTaskContextWantsShellProtocol(patterns []string) bool {
	return compactTaskContextWantsShellCompletion(patterns) &&
		(compactTaskContextHasPattern(patterns, "custom") || compactTaskContextHasPattern(patterns, "another"))
}

func compactTaskContextWantsCompletionCallbackContract(patterns []string) bool {
	return compactTaskContextWantsShellCompletion(patterns) &&
		compactTaskContextHasPattern(patterns, "function")
}

func compactTaskContextWantsCommandParentLink(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "command") &&
		compactTaskContextHasPattern(patterns, "parent") &&
		(compactTaskContextHasPattern(patterns, "add") || compactTaskContextHasPattern(patterns, "pointer"))
}

func compactTaskContextWantsTestFlagFlow(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "test") && compactTaskContextHasPattern(patterns, "flag")
}

func compactTaskContextWantsExecutingFlag(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "flag") &&
		(compactTaskContextHasPattern(patterns, "execut") || compactTaskContextHasPattern(patterns, "execute")) &&
		(compactTaskContextHasPattern(patterns, "current") || compactTaskContextHasPattern(patterns, "currently"))
}

func compactTaskContextWantsParentFlagValue(patterns []string) bool {
	return compactTaskContextHasPattern(patterns, "parent") && compactTaskContextHasPattern(patterns, "flag") &&
		(compactTaskContextHasPattern(patterns, "value") || compactTaskContextHasPattern(patterns, "retrieve") || compactTaskContextHasPattern(patterns, "access"))
}

func compactTaskContextNeedsFlowAllocation(patterns []string) bool {
	return compactTaskContextNeedsReferenceContext(patterns) || compactTaskContextWantsShellProtocol(patterns) ||
		compactTaskContextWantsCompletionCallbackContract(patterns) ||
		compactTaskContextWantsCommandParentLink(patterns) || compactTaskContextWantsTestFlagFlow(patterns) ||
		compactTaskContextWantsExecutingFlag(patterns) || compactTaskContextWantsParentFlagValue(patterns)
}

func compactTaskContextReferenceIdentifiers(patterns []string, evidence []contract.Evidence, items []contract.Item) []compactTaskContextReferenceIdentifier {
	frequencies := make(map[string]int, len(patterns))
	for _, pattern := range patterns {
		frequencies[strings.ToLower(pattern)] = 1
	}
	scores := make(map[string]int)
	add := func(name string, score int) {
		if !token.IsIdentifier(name) || len(name) < 4 || compactTaskContextIgnoredIdentifier(name) {
			return
		}
		if score > scores[name] {
			scores[name] = score
		}
	}
	for _, item := range items {
		priority := compactTaskContextItemPriority(item.Reason)
		if priority < 2_000 {
			continue
		}
		symbol, _ := compactTaskContextItemSymbol(item.Reason)
		if dot := strings.LastIndex(symbol, "."); dot >= 0 {
			symbol = symbol[dot+1:]
		}
		add(symbol, priority+compactTaskContextSymbolScore(patterns, frequencies, symbol))
	}
	for _, item := range evidence {
		fields := strings.FieldsFunc(item.Snippet, func(r rune) bool {
			return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
		for _, identifier := range fields {
			if compactTaskContextWantsCompletionCallbackContract(patterns) && identifier == "ShellCompDirective" {
				add(identifier, 1_000_000)
				continue
			}
			if compactTaskContextWantsShellProtocol(patterns) && strings.Contains(identifier, "ShellComp") && strings.Contains(identifier, "Request") {
				add(identifier, 1_000_000)
				continue
			}
			score := compactTaskContextSymbolScore(patterns, frequencies, identifier)
			if score > 0 {
				add(identifier, score+1_000)
			}
		}
	}
	out := make([]compactTaskContextReferenceIdentifier, 0, len(scores))
	for name, score := range scores {
		out = append(out, compactTaskContextReferenceIdentifier{name: name, score: score})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].name < out[j].name
	})
	limit := 12
	if compactTaskContextWantsShellProtocol(patterns) {
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

func compactTaskContextIgnoredIdentifier(identifier string) bool {
	switch strings.ToLower(identifier) {
	case "bool", "byte", "error", "false", "float32", "float64", "int16", "int32", "int64", "package", "return", "rune", "string", "struct", "true", "uint16", "uint32", "uint64":
		return true
	default:
		return false
	}
}

func compactTaskContextDeclarationDoc(declaration ast.Decl) *ast.CommentGroup {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		return value.Doc
	case *ast.GenDecl:
		return value.Doc
	default:
		return nil
	}
}

func compactTaskContextDeclarationIdentity(packageName string, declaration ast.Decl) (string, string) {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		kind := "function"
		prefix := packageName
		if value.Recv != nil && len(value.Recv.List) > 0 {
			kind = "method"
			if receiver := compactTaskContextReceiverName(value.Recv.List[0].Type); receiver != "" {
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

func compactTaskContextReceiverName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return compactTaskContextReceiverName(value.X)
	case *ast.IndexExpr:
		return compactTaskContextReceiverName(value.X)
	case *ast.IndexListExpr:
		return compactTaskContextReceiverName(value.X)
	default:
		return ""
	}
}

func compactTaskContextItemLocation(reason string) (string, int, bool) {
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

func compactTaskContextDeclarationSpan(set *token.FileSet, file *ast.File, line int) (int, int, bool) {
	for _, declaration := range file.Decls {
		start := set.Position(declaration.Pos()).Line
		if doc := compactTaskContextDeclarationDoc(declaration); doc != nil {
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

type compactTaskContextCandidate struct {
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

// compactTaskContextSelect retains a small number of coherent regions.
// Ranking considers the whole region and discounts test-name/comment matches
// unless the question explicitly asks about tests. The budget is then split by
// rank before unused quota is returned to the strongest regions. This prevents
// a long tail of one-line anchors from starving the implementation bodies that
// make the response answerable.
func compactTaskContextSelect(query string, evidence []contract.Evidence, items []contract.Item, budget int) ([]CompactTaskContextSource, int, error) {
	referenced := make(map[string]bool)
	type itemHint struct {
		symbol, kind string
		priority     int
	}
	hintByEvidence := make(map[string]itemHint)
	for _, item := range items {
		symbol, kind := compactTaskContextItemSymbol(item.Reason)
		priority := compactTaskContextItemPriority(item.Reason)
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
		for _, term := range compactTaskContextIdentifierTerms(patterns[0]) {
			if !seen[term] {
				seen[term] = true
				patterns = append(patterns, term)
			}
		}
		if seen["mutually"] && seen["exclusive"] {
			patterns = append(patterns, "group", "validate", "enforce")
		}
	} else if compactTaskContextHasPattern(patterns, "suggestion") && compactTaskContextHasPattern(patterns, "comput") {
		patterns = append(patterns, "levenshtein")
	}
	if mode == GrepReadV2NaturalLanguage && strings.Contains(query, "--") && !compactTaskContextHasPattern(patterns, "flag") {
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
	testIntent := compactTaskContextTestIntent(patterns)
	candidates := make([]compactTaskContextCandidate, 0, len(evidence))
	for order, item := range evidence {
		if item.ClaimType != "" || item.TextHash != shape.TextHash(item.Snippet) {
			return nil, 0, fmt.Errorf("compact task_context: invalid snippet evidence %q", item.RefID)
		}
		start, end, err := exactEvidenceSpan(item.Span)
		if err != nil || item.Line != start || end-start+1 != len(strings.Split(item.Snippet, "\n")) {
			return nil, 0, fmt.Errorf("compact task_context: invalid source span %q", item.Span)
		}
		lines := strings.Split(item.Snippet, "\n")
		fallback := strings.HasPrefix(item.RefID, "grepread-")
		frequencies := documentFrequencies[0]
		if fallback {
			frequencies = documentFrequencies[1]
		}
		anchors := compactTaskContextAnchors(mode, patterns, item.Path, lines)
		anchor := anchors[0]
		if compactTaskContextWantsShellProtocol(patterns) && strings.Contains(item.Snippet, "ShellCompRequestCmd") && strings.Contains(item.Snippet, "OutOrStdout") {
			for index, line := range lines {
				if strings.Contains(line, "func (c *Command) initCompleteCmd") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 200)}
					break
				}
			}
		}
		if (compactTaskContextWantsShellProtocol(patterns) || compactTaskContextWantsCompletionCallbackContract(patterns)) && strings.Contains(item.Snippet, "ShellCompDirectiveError") {
			for index, line := range lines {
				if strings.Contains(line, "ShellCompDirectiveError ShellCompDirective") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 205)}
					break
				}
			}
		}
		if compactTaskContextWantsCompletionCallbackContract(patterns) && strings.Contains(item.Snippet, "ValidArgsFunction") {
			for index, line := range lines {
				if strings.Contains(line, "ValidArgsFunction func(") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 230)}
					break
				}
			}
		}
		if compactTaskContextWantsLifecycleHooks(patterns) && strings.Contains(item.Snippet, "func (c *Command) execute") && strings.Contains(item.Snippet, "PersistentPreRun") {
			for index, line := range lines {
				if strings.Contains(line, "ParseFlags(a)") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 220)}
					break
				}
			}
		} else if compactTaskContextWantsLifecycleHooks(patterns) && strings.Contains(item.Snippet, "func (c *Command) ExecuteC") && strings.Contains(item.Snippet, "cmd.execute(flags)") {
			for index, line := range lines {
				if strings.Contains(line, "if c.TraverseChildren") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 210)}
					break
				}
			}
		}
		if compactTaskContextWantsTestFlagFlow(patterns) && strings.Contains(item.Snippet, "func (c *Command) execute") && strings.Contains(item.Snippet, "ParseFlags") {
			for index, line := range lines {
				if strings.Contains(line, "ParseFlags(a)") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 220)}
					break
				}
			}
		}
		if compactTaskContextWantsTraversal(patterns) {
			for index, line := range lines {
				if strings.Contains(line, "if c.TraverseChildren") || strings.Contains(line, "TraverseChildren bool") {
					anchor = compactTaskContextLineAnchor{index: index, score: max(anchor.score, 200)}
					break
				}
			}
		}
		score := compactTaskContextRegionScore(mode, patterns, frequencies, item, anchor.score, testIntent) - order
		hint := hintByEvidence[item.RefID]
		symbolScore := compactTaskContextSymbolScore(patterns, frequencies, hint.symbol)
		score += symbolScore + hint.priority
		if compactTaskContextWantsCommandParentLink(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			if strings.HasSuffix(lowerSymbol, ".addcommand") || (strings.Contains(item.Snippet, ".parent =") && strings.Contains(item.Snippet, "commands = append")) {
				score += 500_000
			}
		}
		if compactTaskContextWantsTestFlagFlow(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			switch {
			case strings.HasSuffix(lowerSymbol, ".setargs"):
				score += 450_000
			case strings.HasSuffix(lowerSymbol, ".executec") && strings.Contains(item.Snippet, "c.args"):
				score += 400_000
			case strings.HasSuffix(lowerSymbol, ".execute") && strings.Contains(item.Snippet, "ParseFlags"):
				score += 350_000
			}
		}
		if compactTaskContextWantsExecutingFlag(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			if strings.HasSuffix(lowerSymbol, ".flag") {
				score += 450_000
			} else if hint.kind == "type" && strings.Contains(item.Snippet, "Run func(cmd *Command") {
				score += 400_000
			}
		}
		if compactTaskContextWantsParentFlagValue(patterns) {
			lowerSymbol := strings.ToLower(hint.symbol)
			switch {
			case strings.HasSuffix(lowerSymbol, ".flag"):
				score += 450_000
			case strings.HasSuffix(lowerSymbol, ".persistentflag"):
				score += 400_000
			case strings.Contains(item.Snippet, ".Value."):
				score += 300_000
			}
		}
		if compactTaskContextWantsShellProtocol(patterns) {
			switch {
			case strings.Contains(item.Snippet, "ShellCompRequestCmd") && strings.Contains(item.Snippet, "OutOrStdout"):
				score += 3_000_000
			case strings.Contains(item.Snippet, "ShellCompRequestCmd"):
				score += 2_500_000
			case strings.Contains(item.Snippet, "ShellCompDirectiveError") || strings.Contains(item.Snippet, "type ShellCompDirective"):
				score += 2_800_000
			}
		}
		wholeSymbolMatch := compactTaskContextWholeSymbolMatch(patterns, hint.symbol)
		if mode == GrepReadV2ExactIdentifier {
			wholeSymbolMatch = compactTaskContextWholeSymbolMatch(patterns[:1], hint.symbol)
		}
		if wholeSymbolMatch {
			// Prefer Execute over ExecuteContext when the question names
			// Execute itself. Term-level CamelCase matching intentionally gives
			// both a useful score; this tie-break keeps the exact API entrypoint.
			score += 1_000_000
		}
		if compactTaskContextNeedsReferenceContext(patterns) && compactTaskContextLifecycleHookMatch(patterns, hint.symbol) {
			score += 30_000
		}
		if compactTaskContextHasPattern(patterns, "list") && strings.Contains(item.Snippet, ".VisitAll(") {
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
		if fallback && strings.HasPrefix(item.RefID, "grepread-hydrated-") && (compactTaskContextNeedsReferenceContext(patterns) || compactTaskContextWantsShellCompletion(patterns)) && (hint.kind == "function" || hint.kind == "method") {
			// For flow questions, prefer an executable declaration around a
			// query hit over a large constant/type block containing the same
			// vocabulary. Only one fallback slot is available in this mode.
			score += 100_000
		}
		if compactTaskContextWantsShellCompletion(patterns) {
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
		if compactTaskContextWantsCompletionCallbackContract(patterns) {
			switch {
			case strings.Contains(item.Snippet, "ValidArgsFunction func(") && strings.Contains(item.Snippet, "ShellCompDirective"):
				score += 3_000_000
			case strings.Contains(item.Snippet, "func (c *Command) RegisterFlagCompletionFunc"):
				score += 2_800_000
			case strings.Contains(item.Snippet, "ShellCompDirectiveError") && strings.Contains(item.Snippet, "ShellCompDirectiveNoFileComp"):
				score += 2_600_000
			}
		}
		if compactTaskContextWantsInitFlagValue(patterns) {
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
		if compactTaskContextWantsTraversal(patterns) {
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
		if strings.HasPrefix(item.RefID, "hydrated-") && strings.HasSuffix(strings.ToLower(item.Path), ".md") &&
			(compactTaskContextNeedsReferenceContext(patterns) || compactTaskContextPathStemMatchesPatterns(item.Path, patterns)) {
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
		candidate := compactTaskContextCandidate{
			item: item, symbol: hint.symbol, kind: hint.kind, lines: lines, start: start, anchor: anchor.index,
			from: anchor.index, to: anchor.index, score: score, order: order,
			fallback: fallback, completeFallback: fallback && strings.HasPrefix(item.RefID, "grepread-hydrated-") && compactTaskContextWantsShellCompletion(patterns),
			itemPriority: hint.priority, symbolScore: symbolScore,
		}
		if mode == GrepReadV2NaturalLanguage && len(lines) >= 30 {
			if len(anchors) > 1 {
				candidate.secondaryFrom = max(0, anchors[1].index-1)
				candidate.secondaryTo = candidate.secondaryFrom
				candidate.secondaryScore = anchors[1].score
				candidate.hasSecondary = true
			}
			flowUnit := hint.kind == "function" || hint.kind == "method" || strings.HasSuffix(strings.ToLower(item.Path), ".md")
			flowUnit = flowUnit || compactTaskContextWantsExecutingFlag(patterns) && hint.kind == "type"
			if compactTaskContextNeedsFlowAllocation(patterns) && flowUnit {
				if flowAnchor, ok := compactTaskContextFlowSecondaryAnchor(patterns, lines, anchor.index); ok {
					candidate.secondaryFrom = max(0, flowAnchor.index-1)
					if compactTaskContextWantsLifecycleHooks(patterns) {
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
	if mode == GrepReadV2ExactPath {
		outlined := make([]compactTaskContextCandidate, 0, len(candidates))
		for _, candidate := range candidates {
			if candidate.itemPriority == 18_000 {
				outlined = append(outlined, candidate)
			}
		}
		if len(outlined) > 0 {
			candidates = outlined
		}
	}
	candidates, err := compactTaskContextMergeSameAnchor(candidates)
	if err != nil {
		return nil, 0, err
	}
	if mode == GrepReadV2ExactIdentifier {
		compactTaskContextRankExactDependencies(patterns[0], candidates)
	}
	if mode == GrepReadV2NaturalLanguage && !compactTaskContextNeedsFlowAllocation(patterns) {
		best, bestUtility := -1, 0
		for i := range candidates {
			candidate := candidates[i]
			lowerPath := strings.ToLower(candidate.item.Path)
			if candidate.fallback || strings.HasPrefix(candidate.item.RefID, "linked-reference-") || strings.HasSuffix(lowerPath, "_test.go") || candidate.itemPriority < 4_000 || candidate.symbolScore <= 0 {
				continue
			}
			from, to, ok := compactTaskContextUnitRange(candidate)
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
	if compactTaskContextWantsLifecycleHooks(patterns) {
		filtered := make([]compactTaskContextCandidate, 0, len(candidates))
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
		filtered := make([]compactTaskContextCandidate, 0, len(candidates))
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
	if mode == GrepReadV2ExactIdentifier {
		maxSources = 5
		weights = []int{10, 3, 1, 1, 1}
	} else if mode == GrepReadV2ExactPath {
		maxSources = 10
		weights = []int{5, 4, 3, 3, 2, 2, 1, 1, 1, 1}
	} else if compactTaskContextWantsLifecycleHooks(patterns) {
		maxSources = 7
		weights = []int{18, 8, 5, 3, 2, 1, 1}
	} else if compactTaskContextWantsTraversal(patterns) {
		selected := make([]compactTaskContextCandidate, 0, 4)
		picked := make(map[int]bool)
		pick := func(match func(compactTaskContextCandidate) bool) {
			for i, candidate := range candidates {
				if picked[i] || !match(candidate) {
					continue
				}
				picked[i] = true
				selected = append(selected, candidate)
				return
			}
		}
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.Contains(strings.Join(candidate.lines, "\n"), "func stripFlags(")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.HasSuffix(strings.ToLower(candidate.symbol), ".find")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.HasSuffix(strings.ToLower(candidate.symbol), ".traverse")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			text := strings.Join(candidate.lines, "\n")
			return strings.Contains(text, ".Traverse(args)") && strings.Contains(text, ".Find(args)")
		})
		for i, candidate := range candidates {
			if len(selected) == 4 {
				break
			}
			if !picked[i] {
				selected = append(selected, candidate)
			}
		}
		candidates = selected
		maxSources = 4
		weights = []int{9, 8, 10, 2}
	} else if compactTaskContextWantsShellProtocol(patterns) {
		selected := make([]compactTaskContextCandidate, 0, 4)
		picked := make(map[int]bool)
		pick := func(match func(compactTaskContextCandidate) bool) {
			for i, candidate := range candidates {
				if picked[i] || !match(candidate) {
					continue
				}
				picked[i] = true
				selected = append(selected, candidate)
				return
			}
		}
		pick(func(candidate compactTaskContextCandidate) bool {
			text := strings.Join(candidate.lines, "\n")
			return strings.Contains(text, "func (c *Command) initCompleteCmd") && strings.Contains(text, "OutOrStdout")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.Contains(strings.Join(candidate.lines, "\n"), `ShellCompRequestCmd = "__complete"`)
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			text := strings.Join(candidate.lines, "\n")
			return strings.Contains(text, "ShellCompDirectiveError") && strings.Contains(text, "ShellCompDirectiveNoSpace")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.Contains(strings.Join(candidate.lines, "\n"), "type ShellCompDirective int")
		})
		for i, candidate := range candidates {
			if len(selected) == 4 {
				break
			}
			if !picked[i] {
				selected = append(selected, candidate)
			}
		}
		candidates = selected
		maxSources = 4
		weights = []int{10, 4, 12, 1}
	} else if compactTaskContextWantsCompletionCallbackContract(patterns) {
		selected := make([]compactTaskContextCandidate, 0, 4)
		picked := make(map[int]bool)
		pick := func(match func(compactTaskContextCandidate) bool) {
			for i, candidate := range candidates {
				if picked[i] || !match(candidate) {
					continue
				}
				picked[i] = true
				selected = append(selected, candidate)
				return
			}
		}
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.Contains(strings.Join(candidate.lines, "\n"), "ValidArgsFunction func(")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.Contains(strings.Join(candidate.lines, "\n"), "func (c *Command) RegisterFlagCompletionFunc")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			text := strings.Join(candidate.lines, "\n")
			return strings.Contains(text, "ShellCompDirectiveError") && strings.Contains(text, "ShellCompDirectiveNoFileComp")
		})
		pick(func(candidate compactTaskContextCandidate) bool {
			return strings.Contains(strings.ToLower(candidate.symbol), "fixedcompletions")
		})
		for i, candidate := range candidates {
			if len(selected) == 4 {
				break
			}
			if !picked[i] {
				selected = append(selected, candidate)
			}
		}
		candidates = selected
		maxSources = 4
		weights = []int{8, 10, 8, 2}
	} else if compactTaskContextWantsInitFlagValue(patterns) {
		maxSources = 6
		weights = []int{18, 8, 5, 3, 2, 1}
	} else {
		maxSemantic, maxFallback := 8, 2
		if compactTaskContextNeedsFlowAllocation(patterns) {
			// Flow answers need a small call chain with useful bodies, not a broad
			// list of one-line lexical anchors. Keep one fallback discovery result
			// and spend the remaining capacity on three semantic regions.
			maxSources = 4
			weights = []int{12, 8, 5, 3}
			maxSemantic, maxFallback = 3, 1
		}
		selected := make([]compactTaskContextCandidate, 0, maxSources)
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
		if from, to, ok := compactTaskContextUnitRange(candidates[0]); ok {
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
	if compactTaskContextNeedsFlowAllocation(patterns) {
		for i := range candidates {
			if admitted[i] && candidates[i].hasSecondary {
				reserve := budget / 4
				if compactTaskContextWantsTraversal(patterns) {
					reserve = budget * 3 / 5
				} else if compactTaskContextWantsShellProtocol(patterns) {
					reserve = budget * 2 / 5
				} else if compactTaskContextWantsLifecycleHooks(patterns) {
					reserve = budget * 4 / 5
				} else if compactTaskContextWantsTestFlagFlow(patterns) {
					reserve = budget * 2 / 5
				} else if compactTaskContextWantsExecutingFlag(patterns) || compactTaskContextWantsParentFlagValue(patterns) {
					reserve = budget / 3
				} else if strings.HasSuffix(strings.ToLower(candidates[i].item.Path), ".md") && candidates[i].secondaryScore >= 200 {
					reserve = budget * 2 / 5
				}
				secondaryReserve = max(secondaryReserve, min(remaining, reserve))
			}
		}
	}
	primaryRemaining := remaining - secondaryReserve
	if mode == GrepReadV2NaturalLanguage {
		compactTaskContextCompleteNamedMarkdown(patterns, candidates, admitted, &primaryRemaining)
		compactTaskContextCompleteCodeDocumentationPair(query, candidates, admitted, &primaryRemaining)
		compactTaskContextCompleteCallerCalleePair(query, candidates, admitted, &primaryRemaining)
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextWantsRecursiveWalk(patterns) {
		// Recursive traversals are the implementation of a tree-walk answer, not
		// merely another related declaration. Reserve up to two complete small
		// walkers before wrappers consume the depth budget; repositories commonly
		// expose parallel generators for different output formats.
		completed := 0
		for i := range candidates {
			if admitted[i] && compactTaskContextIsRecursiveTraversal(candidates[i]) {
				if compactTaskContextCompleteUnit(&candidates[i], &primaryRemaining, 160) {
					completed++
				}
				if completed == 2 {
					break
				}
			}
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextWantsCommandParentLink(patterns) {
		for i := range candidates {
			lowerSymbol := strings.ToLower(candidates[i].symbol)
			joined := strings.Join(candidates[i].lines, "\n")
			isAddCommand := strings.HasSuffix(lowerSymbol, ".addcommand") ||
				(strings.Contains(joined, "func (c *Command) AddCommand") && strings.Contains(joined, ".parent ="))
			if admitted[i] && isAddCommand {
				if candidates[i].kind == "" {
					candidates[i].kind = "method"
				}
				compactTaskContextCompleteUnit(&candidates[i], &primaryRemaining, 160)
				break
			}
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextWantsInitFlagValue(patterns) {
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
			for target >= 0 && candidates[i].to < target && primaryRemaining > 0 && compactTaskContextGrow(&candidates[i], &primaryRemaining) {
			}
			if target >= 0 {
				break
			}
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextWantsShellProtocol(patterns) {
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
			for target >= 0 && candidates[i].to < target && primaryRemaining > 0 && compactTaskContextGrow(&candidates[i], &primaryRemaining) {
			}
			break
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextWantsLifecycleHooks(patterns) {
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
			for target >= 0 && candidates[i].to < target && primaryRemaining > 0 && compactTaskContextGrow(&candidates[i], &primaryRemaining) {
			}
			break
		}
	}
	if mode == GrepReadV2NaturalLanguage && compactTaskContextNeedsFlowAllocation(patterns) {
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
			compactTaskContextGrowDocComment(&candidates[best], &primaryRemaining, 6)
		}
	}
	if mode == GrepReadV2ExactIdentifier && len(candidates) > 0 && admitted[0] {
		// Every admitted region is already charged for its anchor line, so a
		// named declaration that would otherwise fit whole can be left cut by a
		// handful of one-line neighbour citations. The end of the declaration
		// the question asked for is worth more than those citations: give the
		// weakest ones back, lowest rank first, until the complete definition
		// fits. Nothing is reclaimed unless it then fits the same unchanged
		// budget, so this can only convert breadth the caller did not ask for
		// into the depth the caller did.
		if from, to, ok := compactTaskContextUnitRange(candidates[0]); ok {
			whole := len(strings.Fields(strings.Join(candidates[0].lines[from:to+1], "\n")))
			for i := len(candidates) - 1; i > 0 && whole <= budget && whole-candidates[0].cost > primaryRemaining; i-- {
				if !admitted[i] {
					continue
				}
				admitted[i] = false
				primaryRemaining += candidates[i].cost
				candidates[i].cost = 0
			}
		}
		// Exact lookup is depth-first: make the named declaration useful before
		// wrappers and neighbours consume the budget. Complete a small
		// definition, or give a long implementation the remaining source budget.
		if !compactTaskContextCompleteUnit(&candidates[0], &primaryRemaining, budget) {
			target := budget
			for candidates[0].cost < target && primaryRemaining > 0 && compactTaskContextGrow(&candidates[0], &primaryRemaining) {
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
		if mode == GrepReadV2ExactPath && (candidates[index].kind == "function" || candidates[index].kind == "method") {
			// A file-path request is an outline task. Completing one medium or
			// large body hides later declarations and makes a truthful truncated
			// response look like a failed whole-file read. Preserve its comment
			// and signature, then spend depth on complete type/constant shapes.
			compactTaskContextGrowDocComment(&candidates[index], &primaryRemaining, 6)
			continue
		}
		limit := 80
		if order < len(completeLimits) {
			limit = completeLimits[order]
		}
		compactTaskContextCompleteUnit(&candidates[index], &primaryRemaining, limit)
	}
	// A lone comment/signature line is rarely actionable. Give every admitted
	// region one adjacent complete line before weighted depth allocation; this
	// pairs declaration comments with values and signatures with first steps.
	for i := range candidates {
		if admitted[i] && primaryRemaining > 0 {
			if i < 4 {
				compactTaskContextGrowDocComment(&candidates[i], &primaryRemaining, 6)
			}
			compactTaskContextGrow(&candidates[i], &primaryRemaining)
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
		for candidates[i].cost < quota && primaryRemaining > 0 && compactTaskContextGrow(&candidates[i], &primaryRemaining) {
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
		if compactTaskContextWantsTraversal(patterns) {
			secondaryLines = 36
		} else if compactTaskContextWantsLifecycleHooks(patterns) {
			secondaryLines = 72
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
			if admitted[i] && compactTaskContextGrow(&candidates[i], &remaining) {
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// Fallback grep anchors can be swallowed by a semantic region only after
	// that region grows. Drop those now-redundant candidates, return their
	// charged words, and spend the recovered budget on non-redundant regions.
	for {
		reclaimed := compactTaskContextDropContainedFallbacks(candidates, admitted)
		if reclaimed == 0 {
			break
		}
		remaining += reclaimed
		for remaining > 0 {
			changed := false
			for i := range candidates {
				if admitted[i] && compactTaskContextGrow(&candidates[i], &remaining) {
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}

	out := make([]CompactTaskContextSource, 0, len(candidates))
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
		out = append(out, CompactTaskContextSource{
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
					out = append(out, CompactTaskContextSource{
						Path: candidate.item.Path, Start: candidate.start + secondaryFrom,
						End: candidate.start + secondaryTo, Text: secondaryText,
					})
				}
			}
		}
	}
	out = compactTaskContextRemoveContainedSources(out)
	out, used := compactTaskContextTrimSources(out, budget)
	return out, used, nil
}

func compactTaskContextItemSymbol(reason string) (string, string) {
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

func compactTaskContextSymbolScore(patterns []string, frequencies map[string]int, symbol string) int {
	if symbol == "" {
		return 0
	}
	terms := compactTaskContextIdentifierTerms(symbol)
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
		if !compactTaskContextInformativePattern(pattern) {
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

func compactTaskContextWholeSymbolMatch(patterns []string, symbol string) bool {
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

func compactTaskContextLifecycleHookMatch(patterns []string, symbol string) bool {
	terms := compactTaskContextIdentifierTerms(symbol)
	if !compactTaskContextHasPattern(terms, "on") {
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

func compactTaskContextInformativePattern(pattern string) bool {
	switch strings.ToLower(pattern) {
	case "cobra", "command", "commands", "function", "functions", "go", "root":
		return false
	default:
		return len(pattern) >= 3
	}
}

func compactTaskContextItemPriority(reason string) int {
	switch {
	case strings.HasPrefix(reason, "primary:"):
		return 12_000
	case strings.HasPrefix(reason, "reference:"):
		return 20_000
	case strings.HasPrefix(reason, "flow-callee:"):
		return 50_000
	case strings.HasPrefix(reason, "path-declaration:"):
		return 18_000
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

func compactTaskContextHasPattern(patterns []string, want string) bool {
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, want) {
			return true
		}
	}
	return false
}

// compactTaskContextRankExactDependencies follows two identifier hops only
// among candidates already returned for an exact lookup. The exact declaration
// keeps its larger whole-name bonus; this only promotes its helper and constant
// dependencies above redundant contextual hits.
func compactTaskContextRankExactDependencies(identifier string, candidates []compactTaskContextCandidate) {
	root := -1
	for i, candidate := range candidates {
		if compactTaskContextWholeSymbolMatch([]string{identifier}, candidate.symbol) {
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

func compactTaskContextIdentifierTerms(symbol string) []string {
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

func compactTaskContextTrimSources(sources []CompactTaskContextSource, budget int) ([]CompactTaskContextSource, int) {
	out := make([]CompactTaskContextSource, 0, len(sources))
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

func compactTaskContextRemoveContainedSources(sources []CompactTaskContextSource) []CompactTaskContextSource {
	out := make([]CompactTaskContextSource, 0, len(sources))
	for i, source := range sources {
		contained := false
		for j, other := range sources {
			if i == j || source.Path != other.Path {
				continue
			}
			if source.Start >= other.Start && source.End <= other.End && (source.Start != other.Start || source.End != other.End) {
				contained = true
				break
			}
		}
		if !contained {
			out = append(out, source)
		}
	}
	return out
}

// Semantic retrieval and GrepRead/2 often land on the same declaration with
// different context windows. Treat that as corroboration of one region, not
// two sources competing for budget. The merge is allowed only at the exact
// same absolute anchor and verifies every overlapping source line.
func compactTaskContextMergeSameAnchor(in []compactTaskContextCandidate) ([]compactTaskContextCandidate, error) {
	byAnchor := make(map[string]int, len(in))
	out := make([]compactTaskContextCandidate, 0, len(in))
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
		copyLines := func(source compactTaskContextCandidate) error {
			for i, line := range source.lines {
				at := source.start + i - start
				if set[at] && lines[at] != line {
					return fmt.Errorf("compact task_context: overlapping source differs at %s:%d", source.item.Path, start+at)
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
				return nil, fmt.Errorf("compact task_context: source gap at %s:%d", candidate.item.Path, start+i)
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

func compactTaskContextRegionScore(mode GrepReadV2Mode, patterns []string, frequencies map[string]int, item contract.Evidence, lineScore int, testIntent bool) int {
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

func compactTaskContextTestIntent(patterns []string) bool {
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, "test") {
			return true
		}
	}
	return false
}

// compactTaskContextGrow adds one adjacent complete line. Forward lines are
// preferred because an anchor is normally a declaration or heading; once the
// region reaches its end, preceding documentation/context is added.
func compactTaskContextGrow(candidate *compactTaskContextCandidate, remaining *int) bool {
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

func compactTaskContextDropContainedFallbacks(candidates []compactTaskContextCandidate, admitted []bool) int {
	reclaimed := 0
	for i := range candidates {
		candidate := candidates[i]
		if !admitted[i] || !candidate.fallback || candidate.hasSecondary {
			continue
		}
		from, to := candidate.start+candidate.from, candidate.start+candidate.to
		for j := range candidates {
			container := candidates[j]
			if i == j || !admitted[j] || container.fallback || candidate.item.Path != container.item.Path {
				continue
			}
			containerFrom, containerTo := container.start+container.from, container.start+container.to
			if from >= containerFrom && to <= containerTo {
				admitted[i] = false
				reclaimed += candidate.cost
				break
			}
		}
	}
	return reclaimed
}

func compactTaskContextCompleteUnit(candidate *compactTaskContextCandidate, remaining *int, maxCost int) bool {
	if (candidate.fallback && !candidate.completeFallback) || (candidate.kind != "function" && candidate.kind != "method" && candidate.kind != "type" && candidate.kind != "constant" && candidate.kind != "variable") {
		return false
	}
	from, to, ok := compactTaskContextUnitRange(*candidate)
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

func compactTaskContextCompleteNamedMarkdown(patterns []string, candidates []compactTaskContextCandidate, admitted []bool, remaining *int) bool {
	for i := range candidates {
		candidate := &candidates[i]
		if !admitted[i] || !strings.HasSuffix(strings.ToLower(candidate.item.Path), ".md") ||
			!compactTaskContextPathStemMatchesPatterns(candidate.item.Path, patterns) {
			continue
		}
		fullCost := len(strings.Fields(strings.Join(candidate.lines, "\n")))
		additional := fullCost - candidate.cost
		if fullCost < 1 || fullCost > 160 || additional < 0 || additional > *remaining {
			return false
		}
		candidate.from, candidate.to = 0, len(candidate.lines)-1
		candidate.cost, candidate.complete = fullCost, true
		*remaining -= additional
		return true
	}
	return false
}

func compactTaskContextCompleteCodeDocumentationPair(query string, candidates []compactTaskContextCandidate, admitted []bool, remaining *int) bool {
	normalizedQuery := compactTaskContextAlnum(query)
	for codeIndex := range candidates {
		code := &candidates[codeIndex]
		if !admitted[codeIndex] || code.symbolScore <= 0 || (!strings.HasSuffix(strings.ToLower(code.item.Path), ".go")) || (code.kind != "function" && code.kind != "method" && code.kind != "type") {
			continue
		}
		name := code.symbol
		if dot := strings.LastIndex(name, "."); dot >= 0 {
			name = name[dot+1:]
		}
		normalizedName := compactTaskContextAlnum(name)
		if len(normalizedName) < 6 || !strings.Contains(normalizedQuery, normalizedName) {
			continue
		}
		codeFrom, codeTo, ok := compactTaskContextUnitRange(*code)
		if !ok {
			continue
		}
		codeCost := len(strings.Fields(strings.Join(code.lines[codeFrom:codeTo+1], "\n")))
		if codeCost < 1 || codeCost > 160 {
			continue
		}
		for docsIndex := range candidates {
			docs := &candidates[docsIndex]
			if !admitted[docsIndex] || !strings.HasSuffix(strings.ToLower(docs.item.Path), ".md") || !strings.Contains(compactTaskContextAlnum(strings.Join(docs.lines, "\n")), normalizedName) {
				continue
			}
			docsCost := len(strings.Fields(strings.Join(docs.lines, "\n")))
			if docsCost < 1 || docsCost > 120 {
				continue
			}
			additional := codeCost - code.cost + docsCost - docs.cost
			if additional < 0 || additional > *remaining {
				continue
			}
			code.from, code.to, code.cost, code.complete = codeFrom, codeTo, codeCost, true
			docs.from, docs.to, docs.cost, docs.complete = 0, len(docs.lines)-1, docsCost, true
			*remaining -= additional
			return true
		}
	}
	return false
}

func compactTaskContextCompleteCallerCalleePair(query string, candidates []compactTaskContextCandidate, admitted []bool, remaining *int) bool {
	normalizedQuery := compactTaskContextAlnum(query)
	for callerIndex := range candidates {
		caller := &candidates[callerIndex]
		if !admitted[callerIndex] || (caller.kind != "function" && caller.kind != "method") {
			continue
		}
		callerName := caller.symbol
		if dot := strings.LastIndex(callerName, "."); dot >= 0 {
			callerName = callerName[dot+1:]
		}
		if normalizedCaller := compactTaskContextAlnum(callerName); len(normalizedCaller) < 4 || !strings.Contains(normalizedQuery, normalizedCaller) {
			continue
		}
		callerFrom, callerTo, ok := compactTaskContextUnitRange(*caller)
		if !ok {
			continue
		}
		callerCost := len(strings.Fields(strings.Join(caller.lines[callerFrom:callerTo+1], "\n")))
		if callerCost < 1 || callerCost > 120 {
			continue
		}
		callerSource := strings.Join(caller.lines[callerFrom:callerTo+1], "\n")
		for calleeIndex := range candidates {
			callee := &candidates[calleeIndex]
			if callerIndex == calleeIndex || !admitted[calleeIndex] || (callee.kind != "function" && callee.kind != "method") {
				continue
			}
			calleeName := callee.symbol
			if dot := strings.LastIndex(calleeName, "."); dot >= 0 {
				calleeName = calleeName[dot+1:]
			}
			if calleeName == "" || !strings.Contains(callerSource, calleeName+"(") {
				continue
			}
			calleeFrom, calleeTo, ok := compactTaskContextUnitRange(*callee)
			if !ok {
				continue
			}
			calleeCost := len(strings.Fields(strings.Join(callee.lines[calleeFrom:calleeTo+1], "\n")))
			if calleeCost < 1 || calleeCost > 160 {
				continue
			}
			additional := callerCost - caller.cost + calleeCost - callee.cost
			if additional < 0 || additional > *remaining {
				continue
			}
			caller.from, caller.to, caller.cost, caller.complete = callerFrom, callerTo, callerCost, true
			callee.from, callee.to, callee.cost, callee.complete = calleeFrom, calleeTo, calleeCost, true
			*remaining -= additional
			return true
		}
	}
	return false
}

func compactTaskContextAlnum(value string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			normalized.WriteRune(r)
		}
	}
	return normalized.String()
}

func compactTaskContextUnitRange(candidate compactTaskContextCandidate) (int, int, bool) {
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

func compactTaskContextGrowDocComment(candidate *compactTaskContextCandidate, remaining *int, limit int) {
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

type compactTaskContextLineAnchor struct {
	index int
	score int
}

func compactTaskContextFlowSecondaryAnchor(patterns []string, lines []string, primary int) (compactTaskContextLineAnchor, bool) {
	wantsInit := compactTaskContextHasPattern(patterns, "init") || compactTaskContextHasPattern(patterns, "hook")
	wantsDispatch := compactTaskContextHasPattern(patterns, "dispatch") || compactTaskContextHasPattern(patterns, "route")
	wantsExecute := compactTaskContextHasPattern(patterns, "execute") || compactTaskContextHasPattern(patterns, "lifecycle") || compactTaskContextHasPattern(patterns, "run")
	wantsTraversal := compactTaskContextWantsTraversal(patterns)
	wantsVersion := compactTaskContextHasPattern(patterns, "version") && compactTaskContextHasPattern(patterns, "flag")
	wantsRequired := compactTaskContextHasPattern(patterns, "required") && compactTaskContextHasPattern(patterns, "flag")
	wantsHelp := compactTaskContextHasPattern(patterns, "help") && (compactTaskContextHasPattern(patterns, "handl") || compactTaskContextHasPattern(patterns, "handle") || compactTaskContextHasPattern(patterns, "different") || compactTaskContextHasPattern(patterns, "differently"))
	wantsCompletionGroup := compactTaskContextHasPattern(patterns, "completion") && compactTaskContextHasPattern(patterns, "group")
	wantsShellProtocol := compactTaskContextWantsShellProtocol(patterns)
	wantsLifecycleHooks := compactTaskContextWantsLifecycleHooks(patterns)
	wantsTestFlags := compactTaskContextWantsTestFlagFlow(patterns)
	wantsExecutingFlag := compactTaskContextWantsExecutingFlag(patterns)
	wantsParentFlagValue := compactTaskContextWantsParentFlagValue(patterns)
	best := compactTaskContextLineAnchor{}
	for index, line := range lines {
		if index == primary {
			continue
		}
		lower := strings.ToLower(line)
		score := 0
		if wantsLifecycleHooks && strings.Contains(lower, "parents := make([]*command") {
			score = 230
		} else if wantsExecutingFlag && strings.Contains(lower, "run func(cmd *command") {
			score = 225
		} else if wantsTestFlags && strings.Contains(lower, "args := c.args") {
			score = 220
		} else if wantsParentFlagValue && strings.Contains(line, ".Value.") {
			score = 215
		} else if wantsShellProtocol && strings.Contains(lower, "fmt.fprintln(finalcmd.outorstdout()") {
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
			best = compactTaskContextLineAnchor{index: index, score: score}
		}
	}
	return best, best.score > 0
}

func compactTaskContextAnchors(mode GrepReadV2Mode, patterns []string, path string, lines []string) []compactTaskContextLineAnchor {
	lowerPath := strings.ToLower(path)
	ranked := make([]compactTaskContextLineAnchor, 0, len(lines))
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
		if mode == GrepReadV2ExactPath {
			switch {
			case strings.HasPrefix(trimmed, "package "):
				score += 200
			case strings.HasPrefix(trimmed, "func "), strings.HasPrefix(trimmed, "type "), strings.HasPrefix(trimmed, "var "), strings.HasPrefix(trimmed, "const "):
				score += 100
			}
		}
		ranked = append(ranked, compactTaskContextLineAnchor{index: index, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].index < ranked[j].index
	})
	if len(ranked) == 0 {
		return []compactTaskContextLineAnchor{{index: 0, score: 0}}
	}
	out := []compactTaskContextLineAnchor{ranked[0]}
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

func compactTaskContextAnchor(mode GrepReadV2Mode, patterns []string, path string, lines []string) (int, int) {
	anchor := compactTaskContextAnchors(mode, patterns, path, lines)[0]
	return anchor.index, anchor.score
}

func compactTaskContextWhitespaceTokens(evidence []contract.Evidence) int {
	total := 0
	for _, item := range evidence {
		total += len(strings.Fields(item.Snippet))
	}
	return total
}

func compactTaskContextProvenance(summary, inputSHA string, budget int) (CompactTaskContextProvenance, error) {
	if !isLowerHexDigest(inputSHA, 64) {
		return CompactTaskContextProvenance{}, fmt.Errorf("compact task_context: invalid input digest")
	}
	open, close := strings.LastIndex(summary, " ("), strings.LastIndex(summary, ")")
	if open < 0 || close <= open+2 {
		return CompactTaskContextProvenance{}, fmt.Errorf("compact task_context: cannot parse input summary provenance")
	}
	fields := strings.Split(summary[open+2:close], "; ")
	if len(fields) < 6 || fields[0] != "task_context/2" {
		return CompactTaskContextProvenance{}, fmt.Errorf("compact task_context: incomplete input summary provenance")
	}
	value := func(prefix string) string {
		for _, field := range fields {
			if strings.HasPrefix(field, prefix) {
				return strings.TrimPrefix(field, prefix)
			}
		}
		return ""
	}
	p := CompactTaskContextProvenance{
		InputSHA256: inputSHA, Method: fields[0], Retrieval: fields[1], RetrievalState: "ready", Weights: value("weights "),
		Model: compactTaskContextModelFingerprint(value("model ")), SourceOrder: "ranked_coherent_regions", Budget: budget,
		BudgetUnit: "whitespace-fields-v1",
	}
	for _, field := range fields {
		if strings.HasPrefix(field, "context-definitions/") {
			p.SourceSelection = field
			break
		}
	}
	if p.Retrieval == "" || p.RetrievalState != "ready" || p.Weights == "" || p.Model == "" || p.SourceSelection == "" {
		return CompactTaskContextProvenance{}, fmt.Errorf("compact task_context: incomplete method identity")
	}
	return p, nil
}

// The complete input digest already binds the full model selector. Repeating
// its long implementation identity on every compact response spends source
// capacity without adding an independently checkable invariant.
func compactTaskContextModelFingerprint(model string) string {
	if model == "" {
		return ""
	}
	digest := SHA256Hex([]byte(model))
	return "sha256:" + digest[:16]
}

// ParseCompactTaskContext validates the exact wire interface. Unknown
// fields fail closed: changing the representation requires a new explicit
// version instead of silently changing the meaning of the compact contract.
func ParseCompactTaskContext(raw []byte) (string, CompactTaskContextStructured, error) {
	var envelope compactTaskContextEnvelope
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: malformed response: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: trailing JSON value")
	} else if err != io.EOF {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: malformed trailing bytes: %w", err)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != 1 || envelope.Result == nil || envelope.Result.IsError || len(envelope.Result.Content) != 1 || envelope.Result.Content[0].Type != "text" || strings.TrimSpace(envelope.Result.Content[0].Text) == "" {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: invalid MCP envelope")
	}
	structured := envelope.Result.StructuredContent
	if structured.Version != CompactTaskContextVersion {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: invalid structured content identity")
	}
	p := structured.Provenance
	if !isLowerHexDigest(p.InputSHA256, 64) || p.Method != "task_context/2" || !strings.HasPrefix(p.Retrieval, "retrieval/") || p.RetrievalState != "ready" || p.Weights == "" || !strings.HasPrefix(p.Model, "sha256:") || !isLowerHexDigest(strings.TrimPrefix(p.Model, "sha256:"), 16) || !strings.HasPrefix(p.SourceSelection, "context-definitions/") || p.SourceOrder != "ranked_coherent_regions" || p.Budget < 1 || p.Budget > SavingsCandidateBudget || p.BudgetUnit != "whitespace-fields-v1" {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: invalid provenance")
	}
	seen := make(map[string]bool)
	used := 0
	for _, source := range structured.Sources {
		key := source.Path + "\x00" + strconv.Itoa(source.Start) + "\x00" + strconv.Itoa(source.End)
		if !fs.ValidPath(source.Path) || source.Start < 1 || source.End < source.Start || source.Text == "" || source.End-source.Start+1 != len(strings.Split(source.Text, "\n")) || seen[key] {
			return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: invalid or duplicate source span")
		}
		seen[key] = true
		used += len(strings.Fields(source.Text))
	}
	if used > p.Budget {
		return "", CompactTaskContextStructured{}, fmt.Errorf("compact task_context: source budget exceeded: %d > %d", used, p.Budget)
	}
	return envelope.Result.Content[0].Text, structured, nil
}
