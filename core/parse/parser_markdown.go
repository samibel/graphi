package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// MarkdownParser is the SW-054 curated tier-1 Markdown parser. It clones the SW-053
// recipe over the pure-Go gotreesitter Markdown grammar (CGo-free; default tier green
// under CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_markdown).
// Markdown is a markup language with no callables; the node set collapses to {file, type}
// where each ATX heading becomes a `type` symbol (the document's section anchors). This
// exercises the AC#1 "omit kinds the language lacks" path. MarkdownParser carries no
// mutable state and is safe for concurrent use.
type MarkdownParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewMarkdownParser returns a ready MarkdownParser wired to the pure-Go Markdown grammar.
func NewMarkdownParser() *MarkdownParser {
	lang := grammars.MarkdownLanguage()
	return &MarkdownParser{lang: lang, extractor: &mdSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*MarkdownParser) Language() string { return "markdown" }

// Extensions implements Parser.
func (*MarkdownParser) Extensions() []string { return []string{".md", ".markdown"} }

type mdAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *MarkdownParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	parser := gts.NewParser(p.lang)
	tree, perr := parser.Parse(src)
	if perr != nil {
		return nil, fmt.Errorf("parse: markdown error in %q: %w", filename, perr)
	}
	root := &mdAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &mdSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: markdown extraction in %q: %w", filename, xerr)
	}

	return &ParseResult{
		Meta: SourceMeta{
			Path: filename, Language: p.Language(),
			ContentHash: contentHash(src), Size: len(src),
		},
		Root:        root,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
	}, nil
}

// Kind mapping (Markdown collapses onto {file, type}):
//
//	type ← an ATX heading (# .. ######) — the document's section anchors
//
// Absent by design: function/method/variable/constant; no import system (Imports empty).

type mdSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*mdSymbolExtractor) Language() string { return "markdown" }

// Extract implements SymbolExtractor for Markdown.
func (e *mdSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*mdAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: markdown extractor: expected non-nil *mdAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	mdCollectHeadings(w, t.root)
	return w.finishExtract(filename, "markdown")
}

// mdCollectHeadings walks the document recording each ATX heading's inline text as a
// `type` symbol.
func mdCollectHeadings(w *cstWalk, n *gts.Node) {
	if n == nil {
		return
	}
	if n.Type(w.lang) == "atx_heading" {
		if inline := childByType(n, "inline", w.lang); inline != nil {
			name := strings.TrimSpace(inline.Text(w.src))
			if name != "" {
				w.addDef(name, KindType, nodePoint(n))
			}
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		mdCollectHeadings(w, n.Child(i))
	}
}
