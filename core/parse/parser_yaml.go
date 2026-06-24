package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// YAMLParser is the SW-054 curated tier-1 YAML parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter YAML grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_yaml). YAML
// is a config language with no callables; the node set collapses to {file, variable}
// where each TOP-LEVEL mapping key becomes a `variable` symbol. This exercises the AC#1
// "omit kinds the language lacks" path. YAMLParser carries no mutable state and is safe
// for concurrent use.
type YAMLParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewYAMLParser returns a ready YAMLParser wired to the pure-Go YAML grammar.
func NewYAMLParser() *YAMLParser {
	lang := grammars.YamlLanguage()
	return &YAMLParser{lang: lang, extractor: &yamlSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*YAMLParser) Language() string { return "yaml" }

// Extensions implements Parser.
func (*YAMLParser) Extensions() []string { return []string{".yaml", ".yml"} }

type yamlAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *YAMLParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: yaml error in %q: %w", filename, perr)
	}
	root := &yamlAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &yamlSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: yaml extraction in %q: %w", filename, xerr)
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

// Kind mapping (YAML collapses onto {file, variable}):
//
//	variable ← a TOP-LEVEL block_mapping_pair key
//
// Absent by design: function/method/type/constant; no import system (Imports empty).

type yamlSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*yamlSymbolExtractor) Language() string { return "yaml" }

// Extract implements SymbolExtractor for YAML.
func (e *yamlSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*yamlAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: yaml extractor: expected non-nil *yamlAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	if mapping := yamlTopMapping(t.root, w.lang); mapping != nil {
		for i := 0; i < mapping.ChildCount(); i++ {
			pair := mapping.Child(i)
			if pair == nil || pair.Type(w.lang) != "block_mapping_pair" {
				continue
			}
			key := pair.ChildByFieldName("key", w.lang)
			if key == nil {
				key = yamlFirstFlowNode(pair, w.lang)
			}
			if key != nil {
				name := strings.TrimSpace(key.Text(w.src))
				if name != "" {
					w.addDef(name, KindVariable, nodePoint(key))
				}
			}
		}
	}
	return w.finishExtract(filename, "yaml")
}

// yamlTopMapping descends stream -> document -> block_node -> block_mapping.
func yamlTopMapping(root *gts.Node, lang *gts.Language) *gts.Node {
	doc := childByType(root, "document", lang)
	if doc == nil {
		return nil
	}
	bn := childByType(doc, "block_node", lang)
	if bn == nil {
		return nil
	}
	return childByType(bn, "block_mapping", lang)
}

func yamlFirstFlowNode(pair *gts.Node, lang *gts.Language) *gts.Node {
	return childByType(pair, "flow_node", lang)
}
