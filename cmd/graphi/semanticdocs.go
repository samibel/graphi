package main

import (
	"context"
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/classify"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/internal/rootfile"
)

// fileDocumentSource is the embed.DocumentSource behind `graphi index
// --semantic` (SW-260 AC-8): it cuts SemanticDocument v2 text out of the
// repository's source files. Spans are not persisted (SW-261), so the pass
// re-reads each file through the root-confined reader and re-parses it with
// the default registry to recover ParseResult.Spans; parsers without an exact
// adapter take the window fallback inside embed.BuildDocuments.
//
// Memory is bounded to ONE file at a time: the generation pass asks for
// documents node by node, nodes are sorted by source path (sortNodesByPath),
// and only the current file's documents are held. A file that fails to read
// or parse, or whose content changed since the index (so its node ids no
// longer match), yields no documents for its nodes — they are skipped and
// counted, never embedded from stale or invented text.
//
// This lives in cmd because it does the one thing engine/embed must not: read
// repository files.
type fileDocumentSource struct {
	ctx       context.Context
	root      string
	reg       *parse.Registry
	bounds    parse.ResourceBounds
	admitter  embed.Admission         // active embedder's fail-closed admission (SW-267 AC-2, AC-7)
	tokenizer embed.DocumentTokenizer // legacy fallback when no admission-aware embedder (SW-260 AC-6)

	cur       string
	curDocs   map[model.NodeId]embed.SemanticDocument
	curFailed bool // read/parse failure for cur: its nodes are skipped
	curGen    bool // cur is a generated/vendored path: excluded, never read

	stats      embed.DocumentStats
	unreadable int
}

func newFileDocumentSource(ctx context.Context, root string, emb embed.Embedder) *fileDocumentSource {
	// SW-267 AC-2 / AC-7: prefer the fail-closed Admission interface over
	// the legacy TokenizingEmbedder. The Admission interface returns the
	// exact bytes the model will consume and errors when the body cannot
	// fit — exactly what AC-2 promises ("the adapter holds the exact
	// tokenizer, the usable token limit, the special-token reserve and
	// the preparation policy"). The TokenizingEmbedder fallback covers
	// legacy embedders that have a tokenizer but no Admission surface.
	var adm embed.Admission
	if aa, ok := emb.(embed.Admission); ok {
		adm = aa
	}
	var tok embed.DocumentTokenizer
	if te, ok := emb.(embed.TokenizingEmbedder); ok {
		tok = te.Tokenizer()
	}
	return &fileDocumentSource{
		ctx:       ctx,
		root:      root,
		reg:       parse.NewDefaultRegistry(),
		bounds:    parse.DefaultResourceBounds(),
		admitter:  adm,
		tokenizer: tok,
	}
}

// Result implements embed.ResultDocumentSource (reviewer fix
// Critical 4). The structured Result distinguishes between
// legitimately excluded nodes (file/package/external kinds,
// generated paths, no_span) and nodes the source could not
// produce a document for (read/parse failure, source bytes
// missing, admission failure). The coverage invariant treats
// Failed nodes as build-aborting errors — silently Skipped
// coverage failures is exactly what AC-5 / Critical 4 forbids.
func (s *fileDocumentSource) Result(n model.Node) embed.DocumentResult {
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
	if _, ok := s.curDocs[n.ID()]; !ok {
		return embed.DocumentExcluded // no_span (legitimate)
	}
	return embed.DocumentEmbedded
}

// Document implements embed.DocumentSource.
func (s *fileDocumentSource) Document(n model.Node) (embed.SemanticDocument, bool) {
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
	d, ok := s.curDocs[n.ID()]
	if !ok {
		return s.exclude(embed.ExcludeNoSpan)
	}
	return d, true
}

func (s *fileDocumentSource) exclude(reason string) (embed.SemanticDocument, bool) {
	s.stats.Merge(embed.DocumentStats{Excluded: 1, ExcludedByReason: map[string]int{reason: 1}})
	return embed.SemanticDocument{}, false
}

// load makes path the current file: reads and parses it and builds its
// documents, releasing the previous file's.
func (s *fileDocumentSource) load(path string) {
	s.cur, s.curDocs, s.curFailed, s.curGen = path, nil, false, false
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
	docs, st, _ := embed.BuildDocuments(embed.FileSource{
		Source: embed.Source{Language: res.Meta.Language, Bytes: src, Admitter: s.admitter, Tokenizer: s.tokenizer},
		Path:   path,
		Nodes:  res.Nodes,
		Spans:  res.Spans,
	})
	parse.ReleaseRoot(res)
	// Exclusions are counted per STORE node in Document (the file node, the
	// artefact kinds); only what the builder produced is folded in here.
	s.stats.Merge(embed.DocumentStats{Documents: st.Documents, Truncated: st.Truncated, SpanMethods: st.SpanMethods})
	s.curDocs = make(map[model.NodeId]embed.SemanticDocument, len(docs))
	for _, d := range docs {
		s.curDocs[d.NodeID] = d
	}
}

// sortNodesByPath orders nodes by source path (then id) so the document
// source visits each file exactly once. The embedding order is not part of
// any persisted byte: vectors are keyed by NodeId.
func sortNodesByPath(nodes []model.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if a, b := nodes[i].SourcePath(), nodes[j].SourcePath(); a != b {
			return a < b
		}
		return nodes[i].ID() < nodes[j].ID()
	})
}
