package embed

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/cespare/xxhash/v2"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/classify"
)

// SemanticDocument v3 (SW-267): the bounded, structured symbol capsule
// the `--semantic` path embeds. Built from a node plus its NON-identity
// SourceSpan. v3 differs from v2 in three ways: (1) the joined text is
// assembled from structured capsule fields (kind/qualified-name, path
// segments, signature, doc comment, body) so the embedded bytes admit
// to the active adapter by construction; (2) the body is bounded by
// the adapter's Admission profile, fail-closed (AC-2, AC-7); (3) the
// document carries the admission metadata (token count, limit,
// algorithm id) so the eval harness and carry-forward path can read
// exactly what the model consumed.

// DocumentSchema tags the document shape and text-assembly rule. It enters
// document_id, so a schema change mints new ids rather than silently reusing
// prior identities. SW-267 mints "v3" — the deterministic symbol capsule
// (AC-1), admitted by the active adapter's Admission profile (AC-2, AC-7),
// whose admitted bytes ARE the bytes the model consumes and the bytes
// TextHash describes.
const DocumentSchema = "v3"

// MaxCapsuleBytes is the hard byte cap applied to a v3 capsule's Text field
// AFTER admission: a document never exceeds it regardless of tokenizer
// (16 KiB). The byte cap is the resource bound (AC-6), distinct from the
// model's admission limit (which is in tokens, owned by the adapter).
const MaxCapsuleBytes = 16 << 10

// SemanticDocument is one node's v3 embedding document (SW-267). Field
// names are the wire names (AC-4). The Text field is the EXACT bytes the
// model will consume (the bounded, admitted capsule bytes — AC-1, AC-7):
// TextHash is the xxhash64 of those bytes, so the row's persisted vector
// represents the same input the fingerprint describes (no silent
// truncation under false provenance). Byte offsets/lines describe the
// span the body was cut from and are unchanged by admission. Truncated
// says the body was bounded; Bound records which bound closed the gap:
//
//	"none"   — the document fit every bound, no admission cut;
//	"tokens" — the adapter's admission profile closed the gap;
//	"bytes"  — only the resource cap ran (no admission-aware embedder).
//
// The structured v3 capsule (AC-1) lives in Capsule: it is the bounded
// payload embedded as a single vector per node. Node identity, source
// span and the one-row-per-node schema are unchanged (the capsule is
// the admitted payload; the row's primary key is still NodeID).
//
// Admission metadata (AdmissionTokenCount, AdmissionLimit,
// AdmissionAlgorithmID) is what the eval harness and the carry-forward
// path read to confirm a profile change (AC-3): a stored row's token
// count and limit were what the model actually saw, not the model's
// internal cap or an approximation.
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

	// Capsule is the structured v3 symbol capsule (AC-1) — the bounded,
	// single-vector payload per symbol. Signature is the declaration
	// line; DocComment is the leading comment block; Body is the
	// bounded body (or "" when the symbol has none).
	Capsule Capsule `json:"capsule"`

	// AdmissionTokenCount is the exact number of tokens the model saw
	// for Text (post-preparation). 0 means no admission-aware embedder
	// was available. AdmissionLimit is the model's usable token limit
	// (0 when no admission-aware embedder).
	AdmissionTokenCount int `json:"admission_token_count"`
	AdmissionLimit      int `json:"admission_limit"`

	// AdmissionAlgorithmID is the dotted identifier of the preparation
	// policy ("first-n-tokens@1") so a future algorithm change invalidates
	// the row's profile by name. "" means no admission-aware embedder.
	AdmissionAlgorithmID string `json:"admission_algorithm_id"`
}

// Capsule is the v3 symbol capsule (AC-1): a bounded, structured
// single-vector payload per symbol. The four fields together describe
// one node's identity, provenance and body summary in a fixed
// deterministic order — the same bytes always assemble to the same
// capsule, so the embedded vector represents the same input on every
// run. Body is bounded by the active adapter's Admission profile (or
// the resource cap, when no admission-aware embedder is configured);
// the bounded Body is what gets hashed into TextHash.
type Capsule struct {
	Kind          string `json:"kind"`
	QualifiedName string `json:"qualified_name"`
	PathSegments  string `json:"path_segments"`
	Signature     string `json:"signature"`
	DocComment    string `json:"doc_comment"`
	Body          string `json:"body"`
}

// DocumentTokenizer is the LEGACY OPTIONAL capability through which the
// active embedder's tokenizer bounded documents (SW-260 AC-6). It remains
// for backward compatibility with callers that wired a non-fail-closed
// tokenizer; the v3 builder prefers Admission (fail-closed) and falls
// back to DocumentTokenizer only when Admission is nil. The legacy
// truncate-by-maxTokens shape was never fully honest — Model.InferenceIDs
// silently caps at the tokenizer's max length before the Truncate check
// can ever fire (AC-7) — so the v3 builder calls Admission whenever
// available.
type DocumentTokenizer interface {
	Truncate(text string, maxTokens int) (string, bool)
}

// Source is the file-level context a document is cut from: the canonical
// parser language id (ParseResult.Meta.Language), the file's bytes the
// span indexes into, and the embedder's admission / tokenizer when known.
// Admitter is the FAIL-CLOSED per-document admission (preferred, AC-7);
// Tokenizer is the legacy truncation-only fallback used when no
// admission-aware embedder is configured (the builder then applies the
// byte cap alone).
type Source struct {
	Language  string
	Bytes     []byte
	Admitter  Admission
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

// BuildDocument assembles the v3 symbol capsule of node from span and
// source (AC-1, AC-4). The capsule has structured fields: kind +
// qualified name (identity), normalised path segments, declaration
// signature, leading doc comment, body. The joined Text field — what
// the model consumes and what TextHash names — is the fixed-order
// concatenation:
//
//	kind qualified_name
//	path_segments
//	annotations (space-joined, only if any)
//	doc_comment (only if any)
//	signature (only if any)
//	body (post-signature, only if any)
//
// The ENTIRE rendered capsule is admitted by the adapter's Admission
// profile (AC-2, AC-7, reviewer fix): the budget covers the joined
// text, not just the body, because an oversized signature, doc
// comment, or path header would otherwise exceed the model's
// context. The adapter returns the EXACT bytes the model will
// consume; those bytes ARE Text, ARE what TextHash hashes, ARE what
// the embedder pools. The persisted vector represents those bytes —
// no silent truncation can sit between TextHash and the persisted
// vector.
//
// Mandatory-field overflow policy: kind, qualified name, path, annotations,
// doc comment, and signature form an indivisible prefix. Body bytes may be
// truncated, but that prefix may not. If adapter admission or the final byte
// cap cuts any mandatory byte, BuildDocument returns a typed *AdmissionError;
// it never publishes Text containing half a signature while Capsule.Signature
// retains the full declaration.
//
// When no Admission is provided the builder falls back to the legacy
// Tokenizer path: cut to the legacy 512-token limit when a
// DocumentTokenizer is known, else the byte cap alone (AC-6 — bytes vs model admission
// stay separated). The v3 builder always applies the resource cap
// (MaxCapsuleBytes) as the last bound.
func BuildDocument(node model.Node, span parse.SourceSpan, source Source) (SemanticDocument, error) {
	cap := assembleCapsule(node, span, source.Bytes)
	annotations := joinAnnotations(node.Meta().Annotations)
	// Render the FULL capsule text first (header + annotations + doc
	// comment + signature + body). The admission budget covers this
	// full text, not the body alone.
	full := renderCapsuleText(cap, annotations)
	mandatoryCapsule := cap
	mandatoryCapsule.Body = ""
	mandatory := renderCapsuleText(mandatoryCapsule, annotations)
	admitted, algoID, profile, admitErr := admitFull(full, source)
	if admitErr != nil {
		// Surface the typed admission error with the document identity
		// attached so the build that owns this node fails closed (AC-4)
		// and never publishes a partial generation as ready (AC-5).
		if ae, ok := admitErr.(*AdmissionError); ok {
			ae.NodeID = string(node.ID())
			ae.Path = node.SourcePath()
			return SemanticDocument{}, ae
		}
		return SemanticDocument{}, admitErr
	}
	if !strings.HasPrefix(admitted.Text, mandatory) {
		limit := profile.MaxTokens
		if limit <= 0 {
			limit = -1
		}
		actual := admitted.TokenCount
		if limit >= 0 && actual <= limit {
			actual = limit + 1
		}
		return SemanticDocument{}, &AdmissionError{
			NodeID:  string(node.ID()),
			Path:    node.SourcePath(),
			Limit:   limit,
			Actual:  actual,
			Profile: profile,
			Reason:  "mandatory capsule fields exceed the admission limit",
		}
	}
	// The admitted bytes are EXACTLY what the model consumes and
	// EXACTLY what TextHash hashes (reviewer fix Critical 2). The
	// structured capsule fields still describe the original
	// declaration; the bounded payload is the admitted text.
	admittedText := admitted.Text
	text, byteBound := applyByteCap(admittedText)
	tokenCount := admitted.TokenCount
	if !strings.HasPrefix(text, mandatory) {
		return SemanticDocument{}, &AdmissionError{
			NodeID:  string(node.ID()),
			Path:    node.SourcePath(),
			Limit:   MaxCapsuleBytes,
			Actual:  len(mandatory),
			Profile: profile,
			Reason:  "mandatory capsule fields exceed the byte limit",
		}
	}
	textHash := model.FormatID(xxhash.Sum64String(text))
	bound := admitted.Bound
	if byteBound {
		bound = BoundBytes
	}
	return SemanticDocument{
		DocumentID:           documentID(node.ID(), textHash, DocumentSchema),
		NodeID:               node.ID(),
		Language:             source.Language,
		Kind:                 node.Kind(),
		QualifiedName:        node.QualifiedName(),
		Path:                 node.SourcePath(),
		StartByte:            span.StartByte,
		EndByte:              span.EndByte,
		StartLine:            span.StartLine,
		EndLine:              span.EndLine,
		SpanMethod:           string(span.Method),
		TextHash:             textHash,
		DocumentSchema:       DocumentSchema,
		Text:                 text,
		Truncated:            bound != BoundNone,
		Bound:                bound,
		Capsule:              cap,
		AdmissionTokenCount:  tokenCount,
		AdmissionLimit:       profile.MaxTokens,
		AdmissionAlgorithmID: algoID,
	}, nil
}

// assembleCapsule constructs the structured v3 capsule (AC-1) from a
// node plus its non-identity span. Field order is fixed: kind+qualified
// name, path segments, signature, doc comment, body. A zero span yields
// a header-only capsule; a missing doc comment yields "".
//
// SW-267 parser-provided spans: when span.SignatureSpan or
// span.DocSpan is non-nil, the capsule uses those byte ranges
// verbatim (the parser's view is authoritative) and the heuristic
// fallback is skipped. The heuristic is rigorously tested for
// the shapes it covers; the parser span is the canonical source.
func assembleCapsule(node model.Node, span parse.SourceSpan, src []byte) Capsule {
	body := spanBody(span, src)
	var doc, sig string
	if span.DocSpan != nil {
		doc = bytesRange(src, span.DocSpan.StartByte, span.DocSpan.EndByte)
	} else {
		doc = spanDocComment(body)
	}
	if span.SignatureSpan != nil {
		sig = bytesRange(src, span.SignatureSpan.StartByte, span.SignatureSpan.EndByte)
	} else {
		signatureBody := body
		if doc != "" {
			signatureBody = strings.TrimLeft(strings.TrimPrefix(signatureBody, doc), "\r\n")
		}
		sig = spanSignatureFromBody(signatureBody)
	}
	body = stripDocAndSignature(body, doc, sig)
	return Capsule{
		Kind:          node.Kind(),
		QualifiedName: node.QualifiedName(),
		PathSegments:  pathSegments(node.SourcePath()),
		Signature:     sig,
		DocComment:    doc,
		Body:          body,
	}
}

// bytesRange returns src[a:b] clamped to src's range; safe for out-of-range.
func bytesRange(src []byte, a, b int) string {
	if a < 0 {
		a = 0
	}
	if b > len(src) {
		b = len(src)
	}
	if a >= b {
		return ""
	}
	return strings.TrimRight(string(src[a:b]), " \t\r\n")
}

// spanSignatureFromBody is the same shape as spanSignature but reads
// from a pre-trimmed body string so assembleCapsule can use the value
// without recomputing the byte run. The signature is the declaration
// line that opens the function/type (the first non-comment, non-decorator,
// non-blank line). If that line opens a parenthesized or bracketed declaration,
// continuation lines are included through the opening `{`. A one-line body is
// included through its balanced `}`; a multiline body stops at `{` so the body
// remains disjoint. A pure-doc-comment body yields "".
//
// Decorators (lines starting with `@`, common in TypeScript / Python)
// ride with the body, not the signature — they decorate the
// declaration but are not part of the declaration line itself.
func spanSignatureFromBody(body string) string {
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	firstLine := -1
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "/*") {
			continue
		}
		if strings.HasPrefix(trim, "@") {
			// Decorator: skip; ride with body.
			continue
		}
		firstLine = i
		break
	}
	if firstLine < 0 {
		return ""
	}
	first := lines[firstLine]
	open := strings.Index(first, "{")
	if open < 0 {
		depth := declarationDelimiterDepth(first)
		if depth <= 0 {
			return strings.TrimRight(first, " \t\r\n")
		}
		parts := []string{first}
		for _, line := range lines[firstLine+1:] {
			if open = strings.Index(line, "{"); open >= 0 {
				parts = append(parts, line[:open+1])
				return strings.TrimRight(strings.Join(parts, "\n"), " \t\r\n")
			}
			parts = append(parts, line)
			depth += declarationDelimiterDepth(line)
			if depth <= 0 {
				return strings.TrimRight(strings.Join(parts, "\n"), " \t\r\n")
			}
		}
		return strings.TrimRight(strings.Join(parts, "\n"), " \t\r\n")
	}
	depth := 0
	for i := open; i < len(first); i++ {
		switch first[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return strings.TrimRight(first[:i+1], " \t\r\n")
			}
		}
	}
	// Unmatched `{` on the first line: signature ends at `{` so the
	// body that admission bounds stays disjoint.
	return strings.TrimRight(first[:open+1], " \t\r\n")
}

func declarationDelimiterDepth(line string) int {
	depth := 0
	for _, r := range line {
		switch r {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			depth--
		}
	}
	return depth
}

// stripDocAndSignature removes the leading doc-comment block AND the
// signature line from body, returning the post-signature remainder
// (the Body field the capsule carries). The remainder is what
// admission bounds, so the signature and doc comment survive even on
// a 12 KB body (AC-1). A body without a matching leading doc/signature
// is returned unchanged.
func stripDocAndSignature(body, doc, sig string) string {
	if body == "" {
		return body
	}
	rest := body
	if doc != "" {
		rest = strings.TrimPrefix(rest, doc)
		rest = strings.TrimLeft(rest, "\n")
	}
	if sig != "" {
		rest = strings.TrimPrefix(rest, sig)
		rest = strings.TrimLeft(rest, "\n")
	}
	return rest
}

// admitFull runs the adapter's admission on the FULL rendered capsule
// text (reviewer fix Critical 2: the budget covers the joined text,
// not just the body). When the adapter's Admitter returns admitted
// bytes, those bytes ARE what gets embedded (AC-7) and TextHash hashes
// them. When no admission-aware embedder is configured, the legacy
// DocumentTokenizer path runs (best-effort, may silently mis-state —
// the bound label is "none" when the legacy path was a no-op).
//
// The returned profile is the adapter's AdmissionSpec; the algorithm
// id is the version-tagged identifier the adapter advertises (static:
// "first-n-tokens@1"; Ollama: "server-side-truncate-false@1").
//
// A typed *AdmissionError surfaces when the adapter cannot prove
// admissibility (AC-4): a degenerate input with no recoverable bytes,
// an oversized payload beyond the resource cap, or a server-side
// rejection from a remote embedder.
func admitFull(full string, source Source) (Admitted, string, AdmissionSpec, error) {
	if source.Admitter != nil {
		out, e := source.Admitter.Admit(context.Background(), full)
		if e != nil {
			return Admitted{}, "", AdmissionSpec{}, e
		}
		// Algorithm id: read it from the embedder's admission profile
		// when available; the static embedder reports
		// "first-n-tokens@1"; Ollama reports "server-side-truncate-false@1".
		algoID := admissionAlgorithmFirstN + "@" + admissionAlgorithmVersion
		var profile AdmissionSpec
		if ap, ok := source.Admitter.(AdmissionProfile); ok {
			profile = ap.Profile()
			if profile.Algorithm != "" {
				algoID = profile.Algorithm + "@" + profile.AlgorithmVersion
			}
		}
		// Bound: the adapter either truncated ("tokens") or admitted as-is
		// ("none"). Bytes-bound falls through to applyByteCap.
		return out, algoID, profile, nil
	}
	if source.Tokenizer != nil {
		// Legacy SW-260 path: best-effort, may silently mis-state (AC-7).
		// Bound = "none" when the legacy Truncate was a no-op; "tokens"
		// when it cut. No profile is exposed, so AdmissionLimit stays
		// 0 and the algorithm id is the legacy tokenization fallback.
		cut, wasCut := source.Tokenizer.Truncate(full, 512)
		if wasCut {
			return Admitted{Text: cut, TokenCount: 0, Bound: BoundTokens},
				"legacy-truncate@1", AdmissionSpec{MaxTokens: 512}, nil
		}
		return Admitted{Text: full, TokenCount: 0, Bound: BoundNone},
			"legacy-truncate@1", AdmissionSpec{MaxTokens: 512}, nil
	}
	// No adapter-side knowledge: pass through, the byte cap is the
	// only bound. An embedder with NO admission support must not reach
	// ready — the caller (GenerateAndPersist) catches that case at
	// commit time and fails closed (AC-5 reviewer fix). Here we
	// surface a zero AdmissionLimit so the absence is observable.
	return Admitted{Text: full, TokenCount: 0, Bound: BoundNone},
		"", AdmissionSpec{}, nil
}

// renderCapsuleText joins the bounded capsule into the deterministic
// bytes that Text hashes. Newlines separate the fields; the order is
// fixed so identical inputs always produce the same Text. The body
// and the signature/doc-comment blocks live in the Capsule struct
// separately, so the joined Text is byte-stable even when the body is
// bounded: the signature line rides at the head of the body (or as
// its own line when the body is empty) and the doc-comment block
// precedes it. The annotations line follows the path segments and
// precedes the doc-comment block.
func renderCapsuleText(c Capsule, annotations string) string {
	var b strings.Builder
	b.WriteString(c.Kind)
	b.WriteByte(' ')
	b.WriteString(c.QualifiedName)
	if c.PathSegments != "" {
		b.WriteByte('\n')
		b.WriteString(c.PathSegments)
	}
	if annotations != "" {
		b.WriteByte('\n')
		b.WriteString(annotations)
	}
	if c.DocComment != "" {
		b.WriteByte('\n')
		b.WriteString(c.DocComment)
	}
	if c.Signature != "" {
		b.WriteByte('\n')
		b.WriteString(c.Signature)
	}
	if c.Body != "" {
		b.WriteByte('\n')
		b.WriteString(c.Body)
	}
	return b.String()
}

// joinAnnotations is the space-joined form of node annotations used in
// the joined Text (annotations are NOT part of the structured capsule —
// they live on the node, not on the admitted payload — but they ride
// in the joined text for backward compatibility with the v2 wire form
// and for retrieval that depends on the "Deprecated" / "Beta" tags
// sitting on the symbol). The result is "" when no annotations are
// present.
func joinAnnotations(ann []string) string {
	if len(ann) == 0 {
		return ""
	}
	return strings.Join(ann, " ")
}

// boundLabel resolves the document's bound string: the body-level bound
// wins when set, otherwise "none" (the document was bounded only by the
// byte cap or not at all).
func boundLabel(bodyBound string, truncated bool) string {
	if bodyBound != "" && bodyBound != BoundNone {
		return bodyBound
	}
	if truncated {
		return BoundBytes
	}
	return BoundNone
}

// BuildDocuments builds the documents of one parsed file in node order
// (AC-3/AC-5/AC-6). A generated/vendored path (classify.IsGeneratedPath — the
// same classifier the rest of the engine applies; search_hybrid itself applies
// none) excludes every node of the file; file, package and external nodes are
// excluded by kind; when file.Spans is nil the parse.DeriveWindowSpans fallback
// supplies window spans; a node still without a span is excluded as no_span.
// Every exclusion is counted by reason.
//
// Admission failures (AC-4) surface as a typed *AdmissionError: the
// build that owns this file fails closed, never publishes a partial
// generation as ready (AC-5).
func BuildDocuments(file FileSource) ([]SemanticDocument, DocumentStats, error) {
	var stats DocumentStats
	if classify.IsGeneratedPath(file.Path) {
		for range file.Nodes {
			stats.Excluded++
		}
		if len(file.Nodes) > 0 {
			stats.exclude(ExcludeGeneratedPath, len(file.Nodes))
		}
		return nil, stats, nil
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
		d, err := BuildDocument(n, sp, file.Source)
		if err != nil {
			if IsAdmissionError(err) {
				return nil, stats, err
			}
			return nil, stats, err
		}
		docs = append(docs, d)
		stats.Documents++
		stats.span(d.SpanMethod, 1)
		if d.Truncated {
			stats.Truncated++
		}
	}
	return docs, stats, nil
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

// IsDeclaredExclusion reports whether n's absence is a declared
// legitimate exclusion (file/package/external kind, no source path).
// Reviewer fix C4: the legacy Document fallback in GenerateAndPersist
// treats ok=false as Excluded ONLY when the node is a declared
// exclusion. Anything else (a declaration with a path whose source
// returned false) is a coverage failure — exactly the laundering
// path the previous shape left open.
func IsDeclaredExclusion(n model.Node) bool { return isDeclaredExclusion(n) }

func isDeclaredExclusion(n model.Node) bool {
	switch n.Kind() {
	case parse.KindFile, parse.KindPackage, parse.KindExternal:
		return true
	}
	return n.SourcePath() == ""
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

// spanSignature is the declaration line of the span: the first
// non-comment, non-blank line, including any balanced `{ ... }` pair.
// The signature is the deterministic identifier the capsule carries
// for declaration-shape retrieval. A zero span or a span made entirely
// of comments yields "".
func spanSignature(sp parse.SourceSpan, src []byte) string {
	body := spanBody(sp, src)
	return spanSignatureFromBody(body)
}

// spanDocComment is the leading comment block at the head of the span
// body. It captures the doc comment in any of three shapes:
//
//   - // line comments (Go's standard convention), including consecutive
//     lines, blank lines between, and the leading whitespace;
//   - /* ... */ block comments, possibly spanning multiple lines,
//     including Go-style "* " continuation lines;
//   - The JSDoc/TSDoc /** ... */ block at the head of a declaration.
//
// Heuristic: the leading doc comment is the longest leading run of
// lines whose trimmed form starts with "//", "/*", or "* " (a block-
// comment continuation line) — or blank lines that fit between them.
// The first line that doesn't fit ends the block. Decorator lines
// (starting with "@") ride with the body, not the doc-comment block.
func spanDocComment(body string) string {
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	var docLines []string
	inBlock := false
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" {
			if len(docLines) > 0 {
				docLines = append(docLines, line)
			}
			continue
		}
		if strings.HasPrefix(trim, "//") {
			docLines = append(docLines, line)
			continue
		}
		if strings.HasPrefix(trim, "/*") {
			docLines = append(docLines, line)
			inBlock = true
			continue
		}
		if strings.HasPrefix(trim, "*") && inBlock {
			// Block-comment continuation line (Go: "* foo"; JSDoc: "** foo").
			docLines = append(docLines, line)
			continue
		}
		if strings.HasPrefix(trim, "*/") {
			docLines = append(docLines, line)
			inBlock = false
			continue
		}
		// First non-fitting line ends the block.
		break
	}
	if len(docLines) == 0 {
		return ""
	}
	return strings.TrimRight(strings.Join(docLines, "\n"), " \t\r\n")
}

// applyByteCap applies the hard byte resource cap to the joined capsule
// text (AC-6: bytes are the resource bound, distinct from token
// admission). The cut is rune-aligned and the trimmed trailing
// whitespace is removed so two identical inputs always produce the same
// Text. Returns the (possibly cut) text and whether the cap fired.
func applyByteCap(text string) (string, bool) {
	if len(text) <= MaxCapsuleBytes {
		return text, false
	}
	cut := MaxCapsuleBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimRight(text[:cut], " \t\r\n"), true
}

// Bound names the bound applied to a document's text. SW-267 v3:
// "tokens" means the adapter's admission profile closed the gap; "bytes"
// means only the resource cap ran (no admission-aware embedder); "none"
// means the document fit every bound.
const (
	BoundNone   = "none"
	BoundTokens = "tokens"
	BoundBytes  = "bytes"
)
