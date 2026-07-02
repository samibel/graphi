package parse

import (
	"context"
	"fmt"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// BashParser is the SW-054 curated tier-1 Bash/Shell parser. It clones the SW-053
// recipe over the pure-Go gotreesitter Bash grammar (CGo-free; default tier green
// under CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_bash).
// Bash has a sparse type system: only `function` and (top-level) `variable` map onto the
// frozen vocabulary; types/methods/constants are absent by design. BashParser carries no
// mutable state and is safe for concurrent use.
type BashParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewBashParser returns a ready BashParser wired to the pure-Go Bash grammar.
func NewBashParser() *BashParser {
	lang := grammars.BashLanguage()
	return &BashParser{lang: lang, extractor: &bashSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*BashParser) Language() string { return "bash" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*BashParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*BashParser) Extensions() []string { return []string{".sh", ".bash"} }

type bashAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *BashParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			res = nil
			err = fmt.Errorf("parse: recovered from panic parsing %q: %v", filename, r)
		}
	}()

	tree, perr := parseTreeSitter(ctx, p.lang, src)
	if perr != nil {
		return nil, fmt.Errorf("parse: bash error in %q: %w", filename, perr)
	}
	root := &bashAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &bashSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: bash extraction in %q: %w", filename, xerr)
	}

	imports := bashImports(root)
	return &ParseResult{
		Meta: SourceMeta{
			Path: filename, Language: p.Language(),
			ContentHash: contentHash(src), Size: len(src),
		},
		Root:        root,
		Nodes:       nodes,
		Edges:       edges,
		PendingRefs: pending,
		Imports:     imports,
		References:  importsToRefs(imports),
	}, nil
}

// Kind mapping (Bash collapses onto {file, function, variable}):
//
//	function ← function_definition (name -> word)
//	variable ← top-level variable_assignment (name -> variable_name)
//
// Absent by design: method, type, constant (no language-level concept). A command whose
// name matches a defined function becomes an intra-file calls edge; an unmapped command
// is a (non-selector) PendingRef. `source`/`.` are recorded as ImportSpecs.

type bashSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*bashSymbolExtractor) Language() string { return "bash" }

// Extract implements SymbolExtractor for Bash.
func (e *bashSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*bashAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: bash extractor: expected non-nil *bashAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "bash"); derr != nil {
		return nil, nil, nil, derr
	}
	bashCollectDefs(w, t.root)
	bashResolveUses(w, t.root)
	return w.finishExtract(filename, "bash")
}

func bashCollectDefs(w *cstWalk, program *gts.Node) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "function_definition":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindFunction, nodePoint(name))
			} else if word := childByType(c, "word", w.lang); word != nil {
				w.addDef(word.Text(w.src), KindFunction, nodePoint(word))
			}
		case "variable_assignment":
			if name := c.ChildByFieldName("name", w.lang); name != nil {
				w.addDef(name.Text(w.src), KindVariable, nodePoint(name))
			} else if vn := childByType(c, "variable_name", w.lang); vn != nil {
				w.addDef(vn.Text(w.src), KindVariable, nodePoint(vn))
			}
		}
	}
}

func bashResolveUses(w *cstWalk, program *gts.Node) {
	for i := 0; i < program.ChildCount(); i++ {
		c := program.Child(i)
		if c == nil || c.Type(w.lang) != "function_definition" {
			continue
		}
		var owner *gts.Node
		if name := c.ChildByFieldName("name", w.lang); name != nil {
			owner = name
		} else {
			owner = childByType(c, "word", w.lang)
		}
		if owner == nil {
			continue
		}
		bashScanBody(w, c, owner.Text(w.src))
	}
}

func bashScanBody(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "command" {
			if cn := childByType(c, "command_name", w.lang); cn != nil {
				if word := childByType(cn, "word", w.lang); word != nil {
					w.callBare(ownerBare, word.Text(w.src), nodePoint(word))
				}
			}
		}
		bashScanBody(w, c, ownerBare)
	}
}

// bashImports records `source x` / `. x` commands as ImportSpecs (the sourced path).
func bashImports(t *bashAST) []ImportSpec {
	if t == nil || t.root == nil {
		return nil
	}
	var out []ImportSpec
	var walk func(n *gts.Node)
	walk = func(n *gts.Node) {
		if n == nil {
			return
		}
		if n.Type(t.lang) == "command" {
			if cn := childByType(n, "command_name", t.lang); cn != nil {
				if word := childByType(cn, "word", t.lang); word != nil {
					name := word.Text(t.src)
					if name == "source" || name == "." {
						// First argument after the command name is the path. It may
						// be a bare word (source lib.sh) or a quoted string
						// (source "lib.sh" / 'lib.sh'); trim surrounding quotes.
						for i := 0; i < n.ChildCount(); i++ {
							arg := n.Child(i)
							if arg == nil {
								continue
							}
							switch arg.Type(t.lang) {
							case "word", "string", "raw_string":
								if path := strings.Trim(arg.Text(t.src), `"'`); path != "" {
									out = append(out, ImportSpec{Path: path})
								}
							default:
								continue
							}
							break
						}
					}
				}
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(t.root)
	return out
}
