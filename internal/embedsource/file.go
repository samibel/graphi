// Package embedsource is the file-backed DocumentSource behind graphi's
// production SemanticDocument build (`graphi index --semantic`) and the
// retrieval eval harness (internal/eval/retrieval). It lives in
// `internal/` for the same reason the original copy lived in `cmd/graphi`:
// it is the one component that reads repository files, and engine/embed
// must not.
//
// Both the `graphi index --semantic` production path (cmd/graphi) and the
// retrieval eval harness (internal/eval/retrieval) build documents through
// the SAME constructor — NewFileDocumentSource. The SW-263 review
// established this as AC-12 (shipped-baseline fidelity): two callers, one
// implementation, divergence was the bug. The SW-267 v3 symbol capsule
// reworked the builder to embed a bounded, structured payload per symbol
// (signature, doc comment, qualified name, kind, path segments, bounded
// body) and added fail-closed admission. This package is the single
// source for that work — there is one implementation of document
// construction, full stop.
//
// Behavioural contract:
//
//   - File / Package / External node kinds are excluded (DocumentExcluded).
//   - Nodes with no SourcePath are excluded (ExcludeNoSpan).
//   - Generated / vendored paths are excluded (ExcludeGeneratedPath).
//   - Files that fail to read or parse contribute no documents; their nodes
//     are DocumentFailed, which aborts the build under the coverage
//     invariant (AC-5, reviewer fix C4). The build never publishes a
//     partial generation as Ready.
//   - Documents whose admission profile rejects the candidate surface an
//     admission error from BuildDocuments; the source marks the entire
//     file as curAdmit and every subsequent declaration in that file is
//     DocumentFailed (reviewer fix C4 — admission failures must not be
//     laundered into legitimate exclusions).
//
// Memory is bounded to ONE file at a time. Callers sort nodes by source
// path (SortNodesByPath) so each file is loaded at most once.
package embedsource

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/classify"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/internal/rootfile"
)

// FileDocumentSource is the file-backed DocumentSource: it cuts
// SemanticDocument v3 capsule text (identity, path, annotations, doc
// comment, signature, bounded body) out of repository files, the way
// `graphi index --semantic` does. Exposed as a value type so callers
// cannot reach into the per-file state; Stats and Unreadable are the
// only observable output besides the documents and the structured
// Result per node.
//
// The exclusion set and document identity are exactly what production
// uses: a test that wires both the eval harness's source and `graphi
// index --semantic`'s source on the same checkout sees byte-identical
// SemanticDocument rows for the same NodeID — the SW-263 AC-12
// "shipped-baseline fidelity" guard.
type FileDocumentSource struct {
	ctx       context.Context
	root      string
	reg       *parse.Registry
	bounds    parse.ResourceBounds
	admitter  embed.Admission         // active embedder's fail-closed admission (SW-267 AC-2, AC-7)
	tokenizer embed.DocumentTokenizer // legacy fallback when no admission-aware embedder

	cur       string
	curDocs   map[model.NodeId]embed.SemanticDocument
	curFailed bool // read/parse failure for cur: its nodes are DocumentFailed
	curAdmit  bool // BuildDocuments surfaced an admission error: every declaration in cur is DocumentFailed
	curGen    bool // cur is a generated/vendored path: excluded, never read

	stats      embed.DocumentStats
	unreadable int
}

// NewFileDocumentSource returns a FileDocumentSource rooted at `root`
// (the already-resolved absolute path of the repository checkout).
//
// SW-267 AC-2 / AC-7: prefer the fail-closed Admission interface over
// the legacy TokenizingEmbedder. The Admission interface returns the
// exact bytes the model will consume and errors when the body cannot
// fit — exactly what AC-2 promises ("the adapter holds the exact
// tokenizer, the usable token limit, the special-token reserve and
// the preparation policy"). The TokenizingEmbedder fallback covers
// legacy embedders that have a tokenizer but no Admission surface.
func NewFileDocumentSource(ctx context.Context, root string, emb embed.Embedder) *FileDocumentSource {
	var adm embed.Admission
	if aa, ok := emb.(embed.Admission); ok {
		adm = aa
	}
	var tok embed.DocumentTokenizer
	if te, ok := emb.(embed.TokenizingEmbedder); ok {
		tok = te.Tokenizer()
	}
	return &FileDocumentSource{
		ctx:       ctx,
		root:      root,
		reg:       parse.NewDefaultRegistry(),
		bounds:    parse.DefaultResourceBounds(),
		admitter:  adm,
		tokenizer: tok,
	}
}

// Result implements embed.ResultDocumentSource (reviewer fix C4). The
// structured Result distinguishes between legitimately excluded nodes
// (file/package/external kinds, generated paths, no_span) and nodes
// the source could not produce a document for (read/parse failure,
// source bytes missing, admission failure). The coverage invariant
// treats DocumentFailed as build-aborting — silently DocumentExcluded
// coverage failures is exactly what AC-5 / Critical 4 forbids.
func (s *FileDocumentSource) Result(n model.Node) embed.DocumentResult {
	switch n.Kind() {
	case parse.KindFile, parse.KindPackage, parse.KindExternal:
		return embed.DocumentExcluded
	}
	path := n.SourcePath()
	if path == "" {
		return embed.DocumentExcluded // no_span (legitimate)
	}
	if path != s.cur {
		s.load(path)
	}
	if s.curGen {
		return embed.DocumentExcluded // generated_path (legitimate)
	}
	if s.curFailed {
		return embed.DocumentFailed // read/parse failure (coverage failure)
	}
	if s.curAdmit {
		// Admission error during BuildDocuments (reviewer fix C4).
		// The previous shape discarded the error and classified the
		// resulting missing declaration as no_span, silently
		// laundering a coverage failure into a legitimate
		// exclusion. The fix surfaces it as DocumentFailed so the
		// build aborts.
		return embed.DocumentFailed
	}
	if _, ok := s.curDocs[n.ID()]; !ok {
		// Genuine no_span: the node id was not produced by the
		// builder and no error surfaced. This is a declared
		// exclusion (the parser did not produce a span for this
		// declaration). Distinguishable from the curAdmit path:
		// when curAdmit is true, EVERY missing id is a failure;
		// here, only a missing id with no source error is a
		// no_span.
		return embed.DocumentExcluded // declared: no_span
	}
	return embed.DocumentEmbedded
}

// Document implements embed.DocumentSource.
func (s *FileDocumentSource) Document(n model.Node) (embed.SemanticDocument, bool) {
	switch n.Kind() {
	case parse.KindFile:
		return s.exclude(embed.ExcludeFile)
	case parse.KindPackage:
		return s.exclude(embed.ExcludePackage)
	case parse.KindExternal:
		return s.exclude(embed.ExcludeExternal)
	}
	path := n.SourcePath()
	if path == "" {
		return s.exclude(embed.ExcludeNoSpan)
	}
	if path != s.cur {
		s.load(path)
	}
	if s.curGen {
		return s.exclude(embed.ExcludeGeneratedPath)
	}
	if s.curFailed {
		s.stats.Excluded++
		s.unreadable++
		return embed.SemanticDocument{}, false
	}
	if s.curAdmit {
		// Admission error during BuildDocuments (reviewer fix C4).
		// The previous shape discarded the error and classified the
		// resulting missing declaration as no_span, silently
		// laundering a coverage failure into a legitimate
		// exclusion. The fix surfaces it as a Failed-shaped
		// (SemanticDocument{}, false) so Result and Document agree.
		s.stats.Excluded++
		s.unreadable++
		return embed.SemanticDocument{}, false
	}
	d, ok := s.curDocs[n.ID()]
	if !ok {
		return s.exclude(embed.ExcludeNoSpan)
	}
	return d, true
}

// Stats returns the running document statistics: counts of produced
// documents, per-reason exclusions, and span-method shares. Callers
// may sample it mid-pass for progress reporting; the caller-visible
// bytes (the produced documents) are not affected by sampling.
func (s *FileDocumentSource) Stats() embed.DocumentStats {
	return s.stats
}

// Unreadable returns the number of distinct files that failed to read
// or parse. A failed file contributes zero documents to the active
// generation; the carry-forward pass cannot reach its nodes either,
// so the count names what the build deliberately left out, never a
// substitute-text side effect.
func (s *FileDocumentSource) Unreadable() int {
	return s.unreadable
}

func (s *FileDocumentSource) exclude(reason string) (embed.SemanticDocument, bool) {
	s.stats.Merge(embed.DocumentStats{Excluded: 1, ExcludedByReason: map[string]int{reason: 1}})
	return embed.SemanticDocument{}, false
}

// load makes path the current file: reads and parses it and builds its
// documents, releasing the previous file's.
func (s *FileDocumentSource) load(path string) {
	s.cur, s.curDocs, s.curFailed, s.curAdmit, s.curGen = path, nil, false, false, false
	if classify.IsGeneratedPath(path) {
		s.curGen = true
		return
	}
	src, err := rootfile.Read(s.root, path, s.bounds.MaxFileSize)
	if err != nil {
		s.curFailed = true
		return
	}
	res, err := s.reg.Parse(s.ctx, path, src)
	if err != nil || res == nil {
		s.curFailed = true
		return
	}
	docs, st, buildErr := embed.BuildDocuments(embed.FileSource{
		Source: embed.Source{Language: res.Meta.Language, Bytes: src, Admitter: s.admitter, Tokenizer: s.tokenizer},
		Path:   path,
		Nodes:  res.Nodes,
		Spans:  res.Spans,
	})
	parse.ReleaseRoot(res)
	// Reviewer fix C4: capture admission errors instead of letting
	// the missing-declaration case launder into a no_span
	// exclusion. A non-admission error is fatal too; we surface it
	// the same way.
	if buildErr != nil {
		s.curAdmit = true
	}

	// Exclusions are counted per STORE node in Document (the file
	// node, the artefact kinds); only what the builder produced is
	// folded in here.
	s.stats.Merge(embed.DocumentStats{Documents: st.Documents, Truncated: st.Truncated, SpanMethods: st.SpanMethods})
	s.curDocs = make(map[model.NodeId]embed.SemanticDocument, len(docs))
	for _, d := range docs {
		s.curDocs[d.NodeID] = d
	}
}

// SortNodesByPath orders nodes by source path (then id) so the document
// source visits each file exactly once. Exposed because callers that
// want to minimise file open/close churn should hand FileDocumentSource
// a path-sorted slice; the embedding order is not part of any persisted
// byte (vectors are keyed by NodeId) but visiting each file exactly
// once avoids re-parsing on cache misses.
func SortNodesByPath(nodes []model.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if a, b := nodes[i].SourcePath(), nodes[j].SourcePath(); a != b {
			return a < b
		}
		return nodes[i].ID() < nodes[j].ID()
	})
}
