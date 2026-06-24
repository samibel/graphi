package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// HCLParser is the SW-054 curated tier-1 HCL/Terraform parser. It clones the SW-053
// recipe over the pure-Go gotreesitter HCL grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_hcl). HCL is
// a config language with no callables; the node set collapses to {file, type, variable}
// where each top-level block becomes a `type` (named by its labels) and each top-level
// attribute a `variable`. This exercises the AC#1 "omit kinds the language lacks" path.
// HCLParser carries no mutable state and is safe for concurrent use.
type HCLParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewHCLParser returns a ready HCLParser wired to the pure-Go HCL grammar.
func NewHCLParser() *HCLParser {
	lang := grammars.HclLanguage()
	return &HCLParser{lang: lang, extractor: &hclSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*HCLParser) Language() string { return "hcl" }

// Extensions implements Parser.
func (*HCLParser) Extensions() []string { return []string{".hcl", ".tf"} }

type hclAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *HCLParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: hcl error in %q: %w", filename, perr)
	}
	root := &hclAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &hclSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: hcl extraction in %q: %w", filename, xerr)
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

// Kind mapping (HCL collapses onto {file, type, variable}):
//
//	type     ← a top-level block named by its labels (e.g. resource."aws_instance.web")
//	variable ← a top-level attribute (key = value)
//
// Absent by design: function/method/constant; no import system (Imports empty).

type hclSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*hclSymbolExtractor) Language() string { return "hcl" }

// Extract implements SymbolExtractor for HCL.
func (e *hclSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*hclAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: hcl extractor: expected non-nil *hclAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	body := childByType(t.root, "body", w.lang)
	if body != nil {
		for i := 0; i < body.ChildCount(); i++ {
			c := body.Child(i)
			if c == nil {
				continue
			}
			switch c.Type(w.lang) {
			case "block":
				hclAddBlock(w, c)
			case "attribute":
				if id := childByType(c, "identifier", w.lang); id != nil {
					w.addDef(id.Text(w.src), KindVariable, nodePoint(id))
				}
			}
		}
	}
	return w.finishExtract(filename, "hcl")
}

// hclAddBlock names a block by joining its block-type identifier with its string labels
// (e.g. `resource.aws_instance.web`), recorded as a `type`.
func hclAddBlock(w *cstWalk, block *gts.Node) {
	var typeID *gts.Node
	name := ""
	for i := 0; i < block.ChildCount(); i++ {
		c := block.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "identifier":
			if typeID == nil {
				typeID = c
				name = c.Text(w.src)
			}
		case "string_lit":
			if tl := childByType(c, "template_literal", w.lang); tl != nil {
				name += "." + tl.Text(w.src)
			}
		}
	}
	if typeID != nil && name != "" {
		w.addDef(name, KindType, nodePoint(typeID))
	}
}
