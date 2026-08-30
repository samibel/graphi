package embed

import (
	"strings"
	"unicode/utf8"

	"github.com/cespare/xxhash/v2"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/classify"
)

// SemanticDocument v2 (SW-260): the text unit the `--semantic` path embeds,
// built from a node plus its NON-identity SourceSpan. It replaces the v1
// name-only text (NodeText) with the declaration body, doc comment and path,
// assembled in one fixed order so identical source yields byte-identical
// documents. Nothing here is persisted or fingerprinted yet — that is SW-261;
// this file only defines the document and builds it deterministically.

// DocumentSchema tags the document shape and text-assembly rule. It enters
// document_id, so a schema change mints new ids rather than silently reusing
// v1 identities.
const DocumentSchema = "v2"

// MaxDocumentTokens bounds a document's text in tokens of the active
// embedder's tokenizer when one is known, else whitespace tokens (AC-6). 512
// matches the truncation length of the model pinned for the production static
// embedder (SW-259 PINNED.md on the sw-259 branch); a longer document would be
// silently cut by the model anyway, so the bound is stated here where it can
// be reported as `truncated` instead.
const MaxDocumentTokens = 512

// MaxDocumentBytes is the hard byte cap applied after the token bound: a
// document never exceeds it regardless of tokenizer (16 KiB).
const MaxDocumentBytes = 16 << 10

// SemanticDocument is one node's v2 embedding document. Field names are the
// wire names (AC-4). Byte offsets/lines describe the span the body was cut
// from and are unchanged by truncation; `truncated` says the text was bounded.
// `bound` records which bound closed the gap: "tokens" (the embedder's own
// tokenizer), "bytes" (only the byte cap ran — no embedder tokenizer was
// available), or "none" (the document fit every bound).
type SemanticDocument struct {
	DocumentID     string       `json:"document_id"`
	NodeID         model.NodeId `json:"node_id"`
	Language       string       `json:"language"`
	Kind           string       `json:"kind"`
	QualifiedName  string       `json:"qualified_name"`
	Path           string       `json:"path"`
	StartByte      int          `json:"start_byte"`
	EndByte        int          `json:"end_byte"`
	StartLine      int          `json:"start_line"`
	EndLine        int          `json:"end_line"`
	SpanMethod     string       `json:"span_method"`
	TextHash       string       `json:"text_hash"`
	DocumentSchema string       `json:"document_schema"`
	Text           string       `json:"text"`
	Truncated      bool         `json:"truncated"`
	Bound          string       `json:"bound"`
}

// DocumentTokenizer is the OPTIONAL capability through which the active
// embedder's own tokenizer bounds documents (AC-6). Truncate returns text cut
// to at most maxTokens of the embedder's tokens and whether it cut anything.
// When no tokenizer is known the builder counts whitespace tokens instead.
type DocumentTokenizer interface {
	Truncate(text string, maxTokens int) (string, bool)
}

// Source is the file-level context a document is cut from: the canonical
// parser language id (ParseResult.Meta.Language), the file's bytes the span
// indexes into, and the embedder tokenizer when known (nil = byte-cap only).
type Source struct {
	Language  string
	Bytes     []byte
	Tokenizer DocumentTokenizer
}

// FileSource is one parsed file's worth of input to BuildDocuments: its
// source, repo-relative path, extracted nodes and the parser's span sidecar
// (nil when the parser emits none — the window fallback then applies).
type FileSource struct {
	Source
	Path  string
	Nodes []model.Node
	Spans map[model.NodeId]parse.SourceSpan
}

// Exclusion reasons recorded in DocumentStats.ExcludedByReason (AC-5).
const (
	// ExcludeGeneratedPath: the path matches the shared vendor/generated
	// classification (engine/classify.IsGeneratedPath — the one classifier).
	ExcludeGeneratedPath = "generated_path"
	// ExcludeFile: a file node is not a declaration and gets no document.
	ExcludeFile = "file"
	// ExcludePackage: an interned package node has no source.
	ExcludePackage = "package"
	// ExcludeExternal: an interned external-symbol node has no source.
	ExcludeExternal = "external"
	// ExcludeNoSpan: the node has neither an exact span nor a derivable window.
	ExcludeNoSpan = "no_span"
)

// DocumentStats counts what a build produced and what it left out, so the
// window share and the exclusions are reportable rather than silent.
type DocumentStats struct {
	Documents        int
	Excluded         int
	Truncated        int
	ExcludedByReason map[string]int
	SpanMethods      map[string]int
}

// Merge adds o's counts into s.
func (s *DocumentStats) Merge(o DocumentStats) {
	s.Documents += o.Documents
	s.Excluded += o.Excluded
	s.Truncated += o.Truncated
	for k, v := range o.ExcludedByReason {
		s.exclude(k, v)
	}
	for k, v := range o.SpanMethods {
		s.span(k, v)
	}
}

func (s *DocumentStats) exclude(reason string, n int) {
	if s.ExcludedByReason == nil {
		s.ExcludedByReason = map[string]int{}
	}
	s.ExcludedByReason[reason] += n
}

func (s *DocumentStats) span(method string, n int) {
	if s.SpanMethods == nil {
		s.SpanMethods = map[string]int{}
	}
	s.SpanMethods[method] += n
}

// SpanMethodShare is the fraction of documents per span method (AC-9). Both
// keys are always present; with no documents both read 0.
func (s DocumentStats) SpanMethodShare() map[string]float64 {
	out := map[string]float64{string(parse.SpanMethodAST): 0, string(parse.SpanMethodWindow): 0}
	total := s.SpanMethods[string(parse.SpanMethodAST)] + s.SpanMethods[string(parse.SpanMethodWindow)]
	if total == 0 {
		return out
	}
	for k := range out {
		out[k] = float64(s.SpanMethods[k]) / float64(total)
	}
	return out
}

// BuildDocument assembles the v2 document of node from span and source
// (AC-4). Text order is fixed: kind + qualified name, the normalised path
// segments (split on "/", joined by spaces), the node's annotations
// (docs/annotations — the leading doc comment itself rides at the head of the
// body because the span starts there), then the body (the span's bytes with
// trailing whitespace trimmed). The text is then bounded (AC-6): to
// MaxDocumentTokens of source.Tokenizer when the embedder exposes one, else
// the byte cap alone (no whitespace-token approximation), and ALWAYS to
// MaxDocumentBytes. A cut sets Truncated and Bound records which bound closed
// the gap ("tokens" / "bytes" / "none"). The document stays ONE document —
// multi-chunk is backlog. A zero span yields a header-only document.
func BuildDocument(node model.Node, span parse.SourceSpan, source Source) SemanticDocument {
	var b strings.Builder
	b.WriteString(node.Kind())
	b.WriteByte(' ')
	b.WriteString(node.QualifiedName())
	if segs := pathSegments(node.SourcePath()); segs != "" {
		b.WriteByte('\n')
		b.WriteString(segs)
	}
	if ann := node.Meta().Annotations; len(ann) > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Join(ann, " "))
	}
	if body := spanBody(span, source.Bytes); body != "" {
		b.WriteByte('\n')
		b.WriteString(body)
	}
	text, bound := boundText(b.String(), source.Tokenizer)
	textHash := model.FormatID(xxhash.Sum64String(text))
	return SemanticDocument{
		DocumentID:     documentID(node.ID(), textHash, DocumentSchema),
		NodeID:         node.ID(),
		Language:       source.Language,
		Kind:           node.Kind(),
		QualifiedName:  node.QualifiedName(),
		Path:           node.SourcePath(),
		StartByte:      span.StartByte,
		EndByte:        span.EndByte,
		StartLine:      span.StartLine,
		EndLine:        span.EndLine,
		SpanMethod:     string(span.Method),
		TextHash:       textHash,
		DocumentSchema: DocumentSchema,
		Text:           text,
		Truncated:      bound != BoundNone,
		Bound:          bound,
	}
}

// BuildDocuments builds the documents of one parsed file in node order
// (AC-3/AC-5/AC-6). A generated/vendored path (classify.IsGeneratedPath — the
// same classifier the rest of the engine applies; search_hybrid itself applies
// none) excludes every node of the file; file, package and external nodes are
// excluded by kind; when file.Spans is nil the parse.DeriveWindowSpans fallback
// supplies window spans; a node still without a span is excluded as no_span.
// Every exclusion is counted by reason.
func BuildDocuments(file FileSource) ([]SemanticDocument, DocumentStats) {
	var stats DocumentStats
	if classify.IsGeneratedPath(file.Path) {
		for range file.Nodes {
			stats.Excluded++
		}
		if len(file.Nodes) > 0 {
			stats.exclude(ExcludeGeneratedPath, len(file.Nodes))
		}
		return nil, stats
	}
	spans := file.Spans
	if spans == nil {
		spans = parse.DeriveWindowSpans(file.Nodes, file.Bytes)
	}
	docs := make([]SemanticDocument, 0, len(file.Nodes))
	for _, n := range file.Nodes {
		if reason := artefactExclusion(n); reason != "" {
			stats.Excluded++
			stats.exclude(reason, 1)
			continue
		}
		sp, ok := spans[n.ID()]
		if !ok {
			stats.Excluded++
			stats.exclude(ExcludeNoSpan, 1)
			continue
		}
		d := BuildDocument(n, sp, file.Source)
		docs = append(docs, d)
		stats.Documents++
		stats.span(d.SpanMethod, 1)
		if d.Truncated {
			stats.Truncated++
		}
	}
	return docs, stats
}

// artefactExclusion names the exclusion reason for a non-declaration node, or
// "" for a symbol node.
func artefactExclusion(n model.Node) string {
	switch n.Kind() {
	case parse.KindFile:
		return ExcludeFile
	case parse.KindPackage:
		return ExcludePackage
	case parse.KindExternal:
		return ExcludeExternal
	}
	if n.SourcePath() == "" {
		return ExcludeNoSpan
	}
	return ""
}

// documentID is xxhash64 over node_id + text_hash + document_schema.
func documentID(id model.NodeId, textHash, schema string) string {
	return model.FormatID(xxhash.Sum64String(string(id) + textHash + schema))
}

// pathSegments renders a normalised POSIX path as space-joined segments.
func pathSegments(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(model.NormalizePath(p), "/")
	out := parts[:0]
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, " ")
}

// spanBody slices the span out of src, clamping to the source bounds, with
// trailing whitespace trimmed. An empty or inverted span yields "".
func spanBody(sp parse.SourceSpan, src []byte) string {
	start, end := sp.StartByte, sp.EndByte
	if start < 0 {
		start = 0
	}
	if end > len(src) {
		end = len(src)
	}
	if start >= end {
		return ""
	}
	return strings.TrimRight(string(src[start:end]), " \t\r\n")
}

// Bound names the bound applied to a document's text. Bound = "tokens" means
// the embedder's own tokenizer cut the text to MaxDocumentTokens; "bytes"
// means the embedder exposed no tokenizer and ONLY the MaxDocumentBytes cap
// ran; "none" means the document fit every bound (the SW-260 AC-6 invariant).
const (
	BoundNone   = "none"
	BoundTokens = "tokens"
	BoundBytes  = "bytes"
)

// boundText applies the active bound (embedder tokenizer when known, else the
// byte cap only) and reports which bound closed the gap. The whitespace-token
// approximation that previously stood in for a missing tokenizer is removed:
// an unknown tokenizer means the document is bounded by the byte cap alone,
// and the eval harness can read the per-document "bound" field to see which
// path the document took. The byte cap is UTF-8-safe (rune-aligned).
func boundText(text string, tok DocumentTokenizer) (string, string) {
	bound := BoundNone
	if tok != nil {
		var cut bool
		text, cut = tok.Truncate(text, MaxDocumentTokens)
		if cut {
			bound = BoundTokens
		}
	}
	if len(text) > MaxDocumentBytes {
		cut := MaxDocumentBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text = strings.TrimRight(text[:cut], " \t\r\n")
		bound = BoundBytes
	}
	return text, bound
}

// truncateWhitespaceTokens is intentionally REMOVED in SW-260 review round 1:
// the previous default (whitespace tokens when the embedder exposes no
// tokenizer) was an invented approximation. The new contract is "bytes only"
// when no tokenizer is known, so the build harness records the bound it
// actually applied instead of pretending to know tokens.
