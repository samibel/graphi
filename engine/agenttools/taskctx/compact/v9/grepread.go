package v9

// GrepRead/2 is the bounded, deterministic source-discovery stage used by
// task_context/2 when indexed candidates do not contain the answer span. Its
// only inputs are a repository filesystem and the caller query; no judgement or
// target can alter the completed transcript.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	GrepReadV2Version     = "2-source-discovery/4"
	GrepReadV2SearchLimit = 48
	GrepReadV2MaxReads    = 8
	GrepReadV2MaxFiles    = 20000
	GrepReadV2MaxFileSize = 4 << 20
	GrepReadV2MaxBytes    = 128 << 20

	grepReadV2ContextBefore = 8
	grepReadV2DiverseReads  = 4
	grepReadV2BreadthReads  = 6
)

type GrepReadV2Mode string

const (
	GrepReadV2ExactIdentifier GrepReadV2Mode = "exact_identifier"
	GrepReadV2ExactPath       GrepReadV2Mode = "exact_path"
	GrepReadV2NaturalLanguage GrepReadV2Mode = "natural_language"
)

// GrepReadV2Transcript is the complete output of a judgement-blind /2 run.
// The interface intentionally accepts no qrels, judgements, target span, or
// callback that could alter execution after an answer is observed.
type GrepReadV2Transcript struct {
	Version       string              `json:"version"`
	Query         string              `json:"query"`
	Mode          GrepReadV2Mode      `json:"mode"`
	Patterns      []string            `json:"patterns"`
	IncludedFiles []string            `json:"included_files"`
	Reads         []GrepReadOperation `json:"reads"`
	StopReason    string              `json:"stop_reason"`
	Ledger        PayloadLedger       `json:"ledger"`
}

func (t GrepReadV2Transcript) DigestSHA256() string {
	raw, err := json.Marshal(t)
	if err != nil {
		panic(fmt.Sprintf("retrieval: marshal GrepRead/2 transcript: %v", err))
	}
	return SHA256Hex(raw)
}

func (t GrepReadV2Transcript) Validate() error {
	if t.Version != GrepReadV2Version {
		return fmt.Errorf("retrieval GrepRead/2: version=%q, want %q", t.Version, GrepReadV2Version)
	}
	mode, patterns := grepReadV2QueryPlan(t.Query)
	if t.Mode != mode || !equalStrings(t.Patterns, patterns) {
		return fmt.Errorf("retrieval GrepRead/2: query plan does not match the pinned transformation")
	}
	if !sort.StringsAreSorted(t.IncludedFiles) {
		return fmt.Errorf("retrieval GrepRead/2: included files are not in canonical order")
	}
	for i, name := range t.IncludedFiles {
		if !grepReadIncludes(name) {
			return fmt.Errorf("retrieval GrepRead/2: included file %q violates the inclusion rule", name)
		}
		if i > 0 && name == t.IncludedFiles[i-1] {
			return fmt.Errorf("retrieval GrepRead/2: included file %q is duplicated", name)
		}
	}
	if t.StopReason != SavingsStopExhausted && t.StopReason != SavingsStopMaxReads && t.StopReason != SavingsStopScanLimit {
		return fmt.Errorf("retrieval GrepRead/2: invalid stop reason %q", t.StopReason)
	}
	if len(t.Reads) > GrepReadV2MaxReads {
		return fmt.Errorf("retrieval GrepRead/2: %d reads exceed max %d", len(t.Reads), GrepReadV2MaxReads)
	}
	if err := t.Ledger.Validate(); err != nil {
		return err
	}
	if len(t.Ledger.Responses) != len(t.Reads)+1 {
		return fmt.Errorf("retrieval GrepRead/2: ledger has %d responses for grep plus %d reads", len(t.Ledger.Responses), len(t.Reads))
	}
	for i, response := range t.Ledger.Responses {
		want := PayloadOperationRead
		if i == 0 {
			want = PayloadOperationGrep
		}
		if response.Boundary != PayloadBoundaryGrepRead || response.Operation != want {
			return fmt.Errorf("retrieval GrepRead/2: response %d has boundary/operation %q/%q", response.Sequence, response.Boundary, response.Operation)
		}
	}
	for i, read := range t.Reads {
		if read.Path == "" || read.StartLine < 1 || read.EndLine < read.StartLine-1 || read.ResponseSequence != i+2 {
			return fmt.Errorf("retrieval GrepRead/2: read %d has invalid provenance %+v", i+1, read)
		}
		if read.EndLine-read.StartLine+1 > GrepReadWindowLines {
			return fmt.Errorf("retrieval GrepRead/2: read %d exceeds the %d-line operation limit", i+1, GrepReadWindowLines)
		}
	}
	return nil
}

// GrepReadV2 performs a complete deterministic run before any scorer sees the
// result. Search ranks all matches globally, then the read planner spends its
// first four slots on distinct files before filling remaining slots by rank.
func GrepReadV2(ctx context.Context, repository fs.FS, query string) (GrepReadV2Transcript, error) {
	transcript, _, err := grepReadV2WithFiles(ctx, repository, query)
	return transcript, err
}

// grepReadV2WithFiles retains the one bounded source snapshot used to build
// the transcript. Production selection reuses it for reference hydration so
// one request never performs a second repository-wide walk or read pass.
func grepReadV2WithFiles(ctx context.Context, repository fs.FS, query string) (GrepReadV2Transcript, *grepReadSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	mode, patterns := grepReadV2QueryPlan(query)
	transcript := GrepReadV2Transcript{
		Version:  GrepReadV2Version,
		Query:    query,
		Mode:     mode,
		Patterns: patterns,
	}

	included, matches, response, scanLimited, snapshot, err := grepReadV2Search(ctx, repository, mode, patterns)
	if err != nil {
		return GrepReadV2Transcript{}, nil, err
	}
	transcript.IncludedFiles = included
	transcript.Ledger.capture(PayloadBoundaryGrepRead, PayloadOperationGrep, response)

	planned := grepReadV2PlanReads(matches, mode)
	for _, window := range planned {
		readResponse, endLine := grepReadRead(repository, snapshot, window)
		sequence := transcript.Ledger.capture(PayloadBoundaryGrepRead, PayloadOperationRead, readResponse)
		transcript.Reads = append(transcript.Reads, GrepReadOperation{
			Path: window.Path, StartLine: window.StartLine, EndLine: endLine, ResponseSequence: sequence,
		})
	}
	if scanLimited {
		transcript.StopReason = SavingsStopScanLimit
	} else if len(planned) == GrepReadV2MaxReads && grepReadV2HasUncovered(matches, planned) {
		transcript.StopReason = SavingsStopMaxReads
	} else {
		transcript.StopReason = SavingsStopExhausted
	}
	return transcript, snapshot, nil
}

type grepReadV2Match struct {
	grepReadMatch
	Score            int
	Declaration      bool
	DeclarationStart int
	DeclarationEnd   int
}

type grepReadV2Declaration struct {
	Name     string
	Start    int
	NameLine int
	End      int
}

var grepReadV2Identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// grepReadV2SeparatedIdentifier is a token written the way people write a
// name in prose — "post-run", "pre_run" — whose parts joined are an
// identifier. It is searched as that identifier, ignoring case.
var grepReadV2SeparatedIdentifier = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:[-_][A-Za-z0-9]+)+$`)

func grepReadV2QueryPlan(query string) (GrepReadV2Mode, []string) {
	trimmed := strings.TrimSpace(query)
	cleanPath := cleanGrepReadPath(trimmed)
	if !strings.ContainsAny(trimmed, " \t\r\n") && path.Ext(cleanPath) == ".go" {
		return GrepReadV2ExactPath, []string{cleanPath}
	}
	if grepReadV2Identifier.MatchString(trimmed) || grepReadV2SeparatedIdentifier.MatchString(trimmed) {
		return GrepReadV2ExactIdentifier, []string{trimmed}
	}

	words := grepReadV2Words(trimmed)
	var selected []string
	seen := map[string]bool{}
	for _, rawWord := range words {
		word := strings.ToLower(rawWord)
		if grepReadV2StopWords[word] || utf8.RuneCountInString(word) < 3 {
			continue
		}
		// Preserve identifier-shaped prose tokens before lowercasing erases
		// their structure. Language stemming turns names such as
		// getCompletions into the nonexistent getCompletion and loses the
		// strongest available query signal.
		if !grepReadV2IdentifierShaped(rawWord) {
			word = grepReadV2Stem(word)
		}
		if seen[word] {
			continue
		}
		seen[word] = true
		selected = append(selected, word)
	}
	for _, word := range append([]string(nil), selected...) {
		for _, expansion := range grepReadV2Expansions[word] {
			if !seen[expansion] {
				seen[expansion] = true
				selected = append(selected, expansion)
			}
		}
	}
	if len(selected) == 0 {
		selected = grepReadPatterns(query)
	}
	if len(selected) > 8 {
		selected = selected[:8]
	}
	return GrepReadV2NaturalLanguage, selected
}

func grepReadV2IdentifierShaped(word string) bool {
	if strings.ContainsRune(word, '_') {
		return true
	}
	first := true
	for _, r := range word {
		if !first && unicode.IsUpper(r) {
			return true
		}
		first = false
	}
	return false
}

func grepReadV2Stem(word string) string {
	switch {
	case word == "argument" || word == "arguments":
		return "arg"
	case len(word) > 5 && strings.HasSuffix(word, "ing"):
		return strings.TrimSuffix(word, "ing")
	case len(word) > 4 && strings.HasSuffix(word, "ed"):
		return strings.TrimSuffix(word, "ed")
	case len(word) > 4 && strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss"):
		return strings.TrimSuffix(word, "s")
	default:
		return word
	}
}

var grepReadV2Expansions = map[string][]string{
	"chosen": {"called"},
}

func grepReadV2Words(query string) []string {
	var words []string
	start := -1
	flush := func(end int) {
		if start >= 0 {
			words = append(words, query[start:end])
			start = -1
		}
	}
	for offset, r := range query {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			if start < 0 {
				start = offset
			}
			continue
		}
		flush(offset)
	}
	flush(len(query))
	return words
}

// Language-neutral function words are excluded because their repository-wide
// frequency otherwise spends the bounded hit/read budget before task terms.
var grepReadV2StopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "before": true, "between": true, "by": true, "can": true,
	"do": true, "does": true, "for": true, "from": true, "get": true,
	"given": true, "how": true, "i": true, "in": true, "into": true,
	"is": true, "it": true, "of": true, "on": true, "or": true, "the": true,
	"their": true, "this": true, "to": true, "was": true, "what": true,
	"when": true, "where": true, "which": true, "why": true, "with": true,
	"would": true,
}

func grepReadV2Search(ctx context.Context, repository fs.FS, mode GrepReadV2Mode, patterns []string) ([]string, []grepReadV2Match, []byte, bool, *grepReadSnapshot, error) {
	if len(patterns) == 0 {
		return []string{}, nil, []byte("grep:error:query:no_searchable_pattern\n"), false, &grepReadSnapshot{}, nil
	}
	files, scanLimited, err := grepReadV2Files(ctx, repository)
	if err != nil {
		return nil, nil, nil, false, nil, err
	}
	snapshot := &grepReadSnapshot{files: files}
	included := make([]string, 0, len(files))
	searchFiles := make([]grepReadFile, 0, len(files))
	var errors []grepReadFile
	for _, file := range files {
		if grepReadIncludes(file.Path) {
			included = append(included, file.Path)
			searchFiles = append(searchFiles, file)
			if file.ErrorKind != "" {
				errors = append(errors, file)
			}
		} else if file.Path == "." && file.ErrorKind != "" {
			errors = append(errors, file)
		}
	}

	var matches []grepReadV2Match
	switch mode {
	case GrepReadV2ExactPath:
		matches, err = grepReadV2PathMatches(ctx, searchFiles, patterns[0])
	case GrepReadV2ExactIdentifier:
		matches, err = grepReadV2IdentifierMatches(ctx, searchFiles, patterns[0])
		hasDeclaration := false
		for _, match := range matches {
			hasDeclaration = hasDeclaration || match.Declaration
		}
		if !hasDeclaration {
			matches, err = grepReadV2NLMatches(ctx, searchFiles, []string{strings.ToLower(patterns[0])})
		}
	default:
		matches, err = grepReadV2NLMatches(ctx, searchFiles, patterns)
	}
	if err != nil {
		return nil, nil, nil, false, nil, err
	}
	if len(matches) > GrepReadV2SearchLimit {
		matches = matches[:GrepReadV2SearchLimit]
	}

	var response bytes.Buffer
	for _, file := range errors {
		fmt.Fprintf(&response, "grep:error:%s:%s\n", file.Path, file.ErrorKind)
	}
	for _, match := range matches {
		fmt.Fprintf(&response, "%s:%d:%d:", match.Path, match.Line, match.Column)
		response.Write(match.Text)
		response.WriteByte('\n')
	}
	return included, matches, response.Bytes(), scanLimited, snapshot, nil
}

var errGrepReadV2ScanLimit = errors.New("source discovery scan limit reached")

func grepReadV2Files(ctx context.Context, repository fs.FS) ([]grepReadFile, bool, error) {
	return grepReadV2FilesWithLimits(ctx, repository, grepReadV2Limits{
		maxFiles: GrepReadV2MaxFiles, maxFileSize: GrepReadV2MaxFileSize, maxBytes: GrepReadV2MaxBytes,
	})
}

type grepReadV2Limits struct {
	maxFiles    int
	maxFileSize int64
	maxBytes    int64
}

func grepReadV2FilesWithLimits(ctx context.Context, repository fs.FS, limits grepReadV2Limits) ([]grepReadFile, bool, error) {
	if repository == nil {
		return []grepReadFile{{Path: ".", ErrorKind: "walk_failed"}}, false, nil
	}
	var files []grepReadFile
	totalBytes := int64(0)
	scanLimited := false
	walkErr := fs.WalkDir(repository, ".", func(name string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			files = append(files, grepReadFile{Path: cleanGrepReadPath(name), ErrorKind: "walk_failed"})
			return nil
		}
		if entry.IsDir() {
			if name != "." && grepReadExcludedDirectory(entry.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceSnapshotIncludes(name) || !entry.Type().IsRegular() {
			return nil
		}
		if len(files) >= limits.maxFiles {
			scanLimited = true
			files = append(files, grepReadFile{Path: ".", ErrorKind: "scan_limit"})
			return errGrepReadV2ScanLimit
		}
		file := grepReadFile{Path: cleanGrepReadPath(name)}
		info, infoErr := entry.Info()
		if infoErr != nil {
			file.ErrorKind = "stat_failed"
			files = append(files, file)
			return nil
		}
		if info.Size() < 0 || info.Size() > limits.maxFileSize {
			file.ErrorKind = "file_too_large"
			files = append(files, file)
			return nil
		}
		if totalBytes+info.Size() > limits.maxBytes {
			scanLimited = true
			files = append(files, grepReadFile{Path: ".", ErrorKind: "scan_limit"})
			return errGrepReadV2ScanLimit
		}
		remaining := limits.maxBytes - totalBytes
		readLimit := min(limits.maxFileSize, remaining)
		raw, readBytes, err := readSourceFileLimit(repository, name, readLimit)
		file.Bytes = raw
		// Account at the I/O boundary, including partial bytes returned with
		// errors, before content is classified or discarded.
		totalBytes += int64(readBytes)
		if err != nil {
			if errors.Is(err, errSourceFileLimit) && readLimit < limits.maxFileSize {
				scanLimited = true
				files = append(files, grepReadFile{Path: ".", ErrorKind: "scan_limit"})
				return errGrepReadV2ScanLimit
			}
			if errors.Is(err, errSourceFileLimit) {
				file.ErrorKind = "file_too_large"
			} else {
				file.ErrorKind = "read_failed"
			}
			file.Bytes = nil
		} else if !utf8.Valid(file.Bytes) {
			file.ErrorKind = "invalid_utf8"
			file.Bytes = nil
		}
		files = append(files, file)
		return nil
	})
	if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
		return nil, false, walkErr
	}
	if walkErr != nil && !errors.Is(walkErr, errGrepReadV2ScanLimit) && len(files) == 0 {
		files = append(files, grepReadFile{Path: ".", ErrorKind: "walk_failed"})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, scanLimited, nil
}

func grepReadV2PathMatches(ctx context.Context, files []grepReadFile, queryPath string) ([]grepReadV2Match, error) {
	queryPath = cleanGrepReadPath(queryPath)
	var matches []grepReadV2Match
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.ErrorKind != "" || !(file.Path == queryPath || path.Base(file.Path) == path.Base(queryPath)) {
			continue
		}
		lines := splitGrepReadLines(file.Bytes)
		text := make([]byte, 0)
		if len(lines) > 0 {
			text = append(text, lines[0].Text...)
		}
		score := 100
		if file.Path == queryPath {
			score = 200
		}
		matches = append(matches, grepReadV2Match{
			grepReadMatch:    grepReadMatch{Path: file.Path, Line: 1, Column: 1, Text: text},
			Score:            score,
			Declaration:      true,
			DeclarationStart: 1,
			DeclarationEnd:   len(lines),
		})
	}
	if err := grepReadV2Sort(ctx, matches); err != nil {
		return nil, err
	}
	return matches, nil
}

func grepReadV2IdentifierMatches(ctx context.Context, files []grepReadFile, identifier string) ([]grepReadV2Match, error) {
	var matches []grepReadV2Match
	// A separated token ("post-run") also matches the identifier its parts
	// spell ("PostRun"), as a whole identifier, ignoring case.
	folded := ""
	if strings.ContainsAny(identifier, "-_") {
		folded = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(identifier))
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.ErrorKind != "" {
			continue
		}
		declarations := grepReadV2Declarations(file.Path, file.Bytes)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i, line := range splitGrepReadLines(file.Bytes) {
			if i&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			column, actual := grepReadV2WholeIdentifierColumn(line.Text, identifier), identifier
			if column == 0 && folded != "" {
				column, actual = grepReadV2FoldedIdentifierColumn(line.Text, folded)
			}
			if column == 0 {
				continue
			}
			declarationStart, declarationEnd := 0, 0
			if declaration, ok := grepReadV2NamedDeclarationAt(declarations, i+1, actual); ok {
				declarationStart, declarationEnd = declaration.Start, declaration.End
			}
			declaration := declarationEnd > 0 || grepReadV2DeclarationLine(line.Text, actual)
			if declaration && declarationEnd == 0 {
				declarationStart = i + 1
				declarationEnd = i + 1
			}
			score := 100
			if declaration {
				score += 10000
			}
			if !strings.HasSuffix(file.Path, "_test.go") {
				score += 100
			}
			matches = append(matches, grepReadV2Match{
				grepReadMatch: grepReadMatch{Path: file.Path, Line: i + 1, Column: column, Text: append([]byte(nil), line.Text...)},
				Score:         score, Declaration: declaration, DeclarationStart: declarationStart, DeclarationEnd: declarationEnd,
			})
		}
	}
	if err := grepReadV2Sort(ctx, matches); err != nil {
		return nil, err
	}
	return matches, nil
}

func grepReadV2NLMatches(ctx context.Context, files []grepReadFile, patterns []string) ([]grepReadV2Match, error) {
	type pending struct {
		match     grepReadMatch
		terms     []int
		filename  bool
		decl      bool
		rankDecl  bool
		declStart int
		declEnd   int
	}
	var all []pending
	frequency := make([]int, len(patterns))
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if file.ErrorKind != "" {
			continue
		}
		lowerPath := strings.ToLower(file.Path)
		declarations := grepReadV2Declarations(file.Path, file.Bytes)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for lineIndex, line := range splitGrepReadLines(file.Bytes) {
			if lineIndex&255 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			lower := bytes.ToLower(line.Text)
			firstColumn := 0
			var terms []int
			for i, pattern := range patterns {
				location := bytes.Index(lower, []byte(strings.ToLower(pattern)))
				if location < 0 {
					continue
				}
				terms = append(terms, i)
				frequency[i]++
				if firstColumn == 0 || location+1 < firstColumn {
					firstColumn = location + 1
				}
			}
			if len(terms) == 0 {
				continue
			}
			filename := false
			for _, term := range terms {
				if strings.Contains(lowerPath, strings.ToLower(patterns[term])) {
					filename = true
					break
				}
			}
			declarationStart, declarationEnd := 0, 0
			enclosing := grepReadV2EnclosingDeclaration(declarations, lineIndex+1)
			for _, declaration := range enclosing {
				if declarationEnd == 0 || declaration.End-declaration.Start < declarationEnd-declarationStart {
					declarationStart, declarationEnd = declaration.Start, declaration.End
				}
			}
			declaration := declarationEnd > 0 || grepReadV2AnyDeclarationLine(line.Text, patterns, terms)
			rankDeclaration := false
			for _, candidate := range enclosing {
				if lineIndex+1 <= candidate.NameLine {
					rankDeclaration = true
					break
				}
			}
			all = append(all, pending{
				match: grepReadMatch{Path: file.Path, Line: lineIndex + 1, Column: firstColumn, Text: append([]byte(nil), line.Text...)},
				terms: terms, filename: filename, decl: declaration, rankDecl: rankDeclaration, declStart: declarationStart, declEnd: declarationEnd,
			})
		}
	}
	declarationTerms := map[string]map[int]bool{}
	for index, item := range all {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if item.declStart == 0 || !item.rankDecl {
			continue
		}
		key := fmt.Sprintf("%s:%d:%d", item.match.Path, item.declStart, item.declEnd)
		if declarationTerms[key] == nil {
			declarationTerms[key] = map[int]bool{}
		}
		for _, term := range item.terms {
			declarationTerms[key][term] = true
		}
	}
	matches := make([]grepReadV2Match, 0, len(all))
	for index, item := range all {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		effectiveTerms := item.terms
		if item.declStart > 0 && item.rankDecl {
			key := fmt.Sprintf("%s:%d:%d", item.match.Path, item.declStart, item.declEnd)
			effectiveTerms = effectiveTerms[:0]
			for term := range declarationTerms[key] {
				effectiveTerms = append(effectiveTerms, term)
			}
			sort.Ints(effectiveTerms)
		}
		score := len(effectiveTerms) * 200000
		for _, term := range effectiveTerms {
			score += 100000 / (frequency[term] + 1)
			if grepReadV2WholeIdentifierColumn(item.match.Text, patterns[term]) > 0 {
				score += 2000
			}
		}
		if item.rankDecl {
			score += 250000
		}
		if item.filename {
			score += 10000
		}
		if !strings.HasSuffix(item.match.Path, "_test.go") {
			score += 15000
			if item.rankDecl {
				score += 300000
			}
		}
		matches = append(matches, grepReadV2Match{
			grepReadMatch: item.match, Score: score, Declaration: item.decl,
			DeclarationStart: item.declStart, DeclarationEnd: item.declEnd,
		})
	}
	if err := grepReadV2Sort(ctx, matches); err != nil {
		return nil, err
	}
	return matches, nil
}

func grepReadV2Before(a, b grepReadV2Match) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

// grepReadV2Sort is a stable bottom-up merge sort with bounded cancellation
// latency. A repository may produce a large pre-sort match set, so checking
// context only before and after sort would leave cancellation ineffective
// during one of discovery's most expensive CPU phases.
func grepReadV2Sort(ctx context.Context, matches []grepReadV2Match) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(matches) < 2 {
		return nil
	}
	scratch := make([]grepReadV2Match, len(matches))
	source, target := matches, scratch
	inScratch := false
	for width := 1; width < len(matches); width *= 2 {
		for start := 0; start < len(matches); start += 2 * width {
			if err := ctx.Err(); err != nil {
				return err
			}
			middle := min(start+width, len(matches))
			end := min(start+2*width, len(matches))
			left, right := start, middle
			for out := start; out < end; out++ {
				if out&1023 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				switch {
				case left >= middle:
					target[out] = source[right]
					right++
				case right >= end:
					target[out] = source[left]
					left++
				case grepReadV2Before(source[right], source[left]):
					target[out] = source[right]
					right++
				default:
					// Taking the left value on equality preserves input order.
					target[out] = source[left]
					left++
				}
			}
		}
		source, target = target, source
		inScratch = !inScratch
	}
	if inScratch {
		copy(matches, source)
	}
	return ctx.Err()
}

func grepReadV2PlanReads(matches []grepReadV2Match, mode GrepReadV2Mode) []grepReadWindow {
	var windows []grepReadWindow
	seenPath := map[string]bool{}
	add := func(match grepReadV2Match) bool {
		start := match.Line
		if match.DeclarationStart > 0 {
			start = match.DeclarationStart + ((match.Line-match.DeclarationStart)/GrepReadWindowLines)*GrepReadWindowLines
		} else if mode == GrepReadV2NaturalLanguage && !match.Declaration {
			start = max(1, match.Line-grepReadV2ContextBefore)
		}
		if grepReadCovered(match.Path, match.Line, windows) {
			return false
		}
		windows = append(windows, grepReadWindow{Path: match.Path, StartLine: start, EndLine: start + GrepReadWindowLines - 1})
		seenPath[match.Path] = true
		return true
	}
	addDeclarationTail := func(match grepReadV2Match) {
		startLine := match.DeclarationStart
		if startLine == 0 {
			startLine = match.Line
		}
		for start := startLine + GrepReadWindowLines; start <= match.DeclarationEnd && len(windows) < GrepReadV2MaxReads; start += GrepReadWindowLines {
			if !grepReadCovered(match.Path, start, windows) {
				windows = append(windows, grepReadWindow{Path: match.Path, StartLine: start, EndLine: start + GrepReadWindowLines - 1})
			}
		}
	}
	if mode == GrepReadV2ExactIdentifier || mode == GrepReadV2ExactPath {
		for _, match := range matches {
			if len(windows) == GrepReadV2MaxReads {
				break
			}
			if add(match) {
				addDeclarationTail(match)
			}
		}
		return windows
	}
	for _, match := range matches {
		if len(windows) == grepReadV2DiverseReads {
			break
		}
		if !seenPath[match.Path] {
			add(match)
		}
	}
	for _, match := range matches {
		if len(windows) == grepReadV2BreadthReads {
			break
		}
		add(match)
	}
	tailMatches := append([]grepReadV2Match(nil), matches...)
	sort.SliceStable(tailMatches, func(i, j int) bool {
		iTest := strings.HasSuffix(tailMatches[i].Path, "_test.go")
		jTest := strings.HasSuffix(tailMatches[j].Path, "_test.go")
		if iTest != jTest {
			return !iTest
		}
		return false
	})
	for _, match := range tailMatches {
		if len(windows) == GrepReadV2MaxReads {
			break
		}
		if grepReadCovered(match.Path, match.Line, windows) {
			addDeclarationTail(match)
		}
	}
	for _, match := range matches {
		if len(windows) == GrepReadV2MaxReads {
			break
		}
		add(match)
	}
	return windows
}

func grepReadV2Declarations(filename string, raw []byte) []grepReadV2Declaration {
	var out []grepReadV2Declaration
	fileset := token.NewFileSet()
	parsed, err := parser.ParseFile(fileset, filename, raw, parser.ParseComments)
	if err != nil {
		return out
	}
	add := func(name string, start, namePosition, end token.Pos) {
		startLine := fileset.PositionFor(start, false).Line
		nameLine := fileset.PositionFor(namePosition, false).Line
		endLine := fileset.PositionFor(end, false).Line
		if name != "" && startLine > 0 && endLine >= startLine {
			out = append(out, grepReadV2Declaration{Name: name, Start: startLine, NameLine: nameLine, End: endLine})
		}
	}
	for _, declaration := range parsed.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			start := declaration.Pos()
			if declaration.Doc != nil {
				start = declaration.Doc.Pos()
			}
			add(declaration.Name.Name, start, declaration.Name.Pos(), declaration.End())
		case *ast.GenDecl:
			start := declaration.Pos()
			if declaration.Doc != nil {
				start = declaration.Doc.Pos()
			}
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					add(spec.Name.Name, start, spec.Name.Pos(), declaration.End())
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						add(name.Name, start, name.Pos(), declaration.End())
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		if out[i].End != out[j].End {
			return out[i].End < out[j].End
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func grepReadV2NamedDeclarationAt(declarations []grepReadV2Declaration, line int, name string) (grepReadV2Declaration, bool) {
	for _, declaration := range declarations {
		if declaration.Name == name && line >= declaration.Start && line <= declaration.NameLine {
			return declaration, true
		}
	}
	return grepReadV2Declaration{}, false
}

func grepReadV2EnclosingDeclaration(declarations []grepReadV2Declaration, line int) []grepReadV2Declaration {
	var out []grepReadV2Declaration
	for _, declaration := range declarations {
		if line >= declaration.Start && line <= declaration.End {
			out = append(out, declaration)
		}
	}
	return out
}

func grepReadV2HasUncovered(matches []grepReadV2Match, windows []grepReadWindow) bool {
	for _, match := range matches {
		if !grepReadCovered(match.Path, match.Line, windows) {
			return true
		}
		if match.DeclarationEnd > 0 && !grepReadCovered(match.Path, match.DeclarationEnd, windows) {
			return true
		}
	}
	return false
}

func grepReadV2WholeIdentifierColumn(line []byte, identifier string) int {
	if identifier == "" {
		return 0
	}
	for offset := 0; offset <= len(line)-len(identifier); {
		found := bytes.Index(line[offset:], []byte(identifier))
		if found < 0 {
			return 0
		}
		found += offset
		beforeOK := found == 0 || !grepReadV2IdentifierByte(line[found-1])
		after := found + len(identifier)
		afterOK := after == len(line) || !grepReadV2IdentifierByte(line[after])
		if beforeOK && afterOK {
			return found + 1
		}
		offset = found + 1
	}
	return 0
}

// grepReadV2FoldedIdentifierColumn finds the first whole identifier on the
// line whose lower-case form is folded, returning its 1-based column and the
// identifier as written.
func grepReadV2FoldedIdentifierColumn(line []byte, folded string) (int, string) {
	for offset := 0; offset < len(line); {
		if !grepReadV2IdentifierByte(line[offset]) {
			offset++
			continue
		}
		end := offset
		for end < len(line) && grepReadV2IdentifierByte(line[end]) {
			end++
		}
		if token := line[offset:end]; len(token) == len(folded) && strings.EqualFold(string(token), folded) {
			return offset + 1, string(token)
		}
		offset = end
	}
	return 0, ""
}

func grepReadV2IdentifierByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func grepReadV2DeclarationLine(line []byte, identifier string) bool {
	trimmed := strings.TrimSpace(string(line))
	quoted := regexp.QuoteMeta(identifier)
	patterns := []string{
		`^func(?:\s+\([^)]*\))?\s+` + quoted + `\s*\(`,
		`^type\s+` + quoted + `\b`,
		`^(?:var|const)\s+` + quoted + `\b`,
		// A field or parameter line: the name followed by a type, never by
		// an assignment or a literal key.
		`^` + quoted + `\s+(?:\[\]|\*|map\[|func\s*\(|<-chan\s|chan\s|[A-Za-z_])`,
	}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).MatchString(trimmed) {
			return true
		}
	}
	return false
}

func grepReadV2AnyDeclarationLine(line []byte, patterns []string, terms []int) bool {
	trimmed := strings.TrimSpace(string(line))
	if !(strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "const ")) {
		return false
	}
	for _, term := range terms {
		if strings.Contains(strings.ToLower(trimmed), strings.ToLower(patterns[term])) {
			return true
		}
	}
	return false
}
