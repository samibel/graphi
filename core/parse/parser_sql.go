package parse

import (
	"context"
	"fmt"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"

	"github.com/samibel/graphi/core/model"
)

// SQLParser is the SW-054 curated tier-1 SQL parser. It clones the SW-053 recipe over
// the pure-Go gotreesitter SQL grammar (CGo-free; default tier green under
// CGO_ENABLED=0; grammar blob Go-embedded behind subset tag grammar_subset_sql). SQL is
// statement-oriented with no OO functions and NO import system, so the node set
// collapses to {file, type} (tables/views) and there are NO ImportSpecs — the absence
// is documented rather than fabricated. SQLParser carries no mutable state and is safe
// for concurrent use.
type SQLParser struct {
	lang      *gts.Language
	extractor SymbolExtractor
}

// NewSQLParser returns a ready SQLParser wired to the pure-Go SQL grammar.
func NewSQLParser() *SQLParser {
	lang := grammars.SqlLanguage()
	return &SQLParser{lang: lang, extractor: &sqlSymbolExtractor{lang: lang}}
}

// Language implements Parser.
func (*SQLParser) Language() string { return "sql" }

// Runtime implements Parser: pure-Go gotreesitter tree-sitter runtime (CGo-free).
func (*SQLParser) Runtime() Runtime { return RuntimeGoTreeSitter }

// Extensions implements Parser.
func (*SQLParser) Extensions() []string { return []string{".sql"} }

type sqlAST struct {
	root *gts.Node
	src  []byte
	lang *gts.Language
}

// Parse implements Parser.
func (p *SQLParser) Parse(ctx context.Context, filename string, src []byte) (res *ParseResult, err error) {
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
		return nil, fmt.Errorf("parse: sql error in %q: %w", filename, perr)
	}
	root := &sqlAST{root: tree.RootNode(), src: src, lang: p.lang}

	extractor := p.extractor
	if extractor == nil {
		extractor = &sqlSymbolExtractor{lang: p.lang}
	}
	nodes, edges, pending, xerr := extractor.Extract(filename, root)
	if xerr != nil {
		return nil, fmt.Errorf("parse: sql extraction in %q: %w", filename, xerr)
	}

	// SQL has no import system; Imports/References are intentionally empty.
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

// Kind mapping (SQL collapses onto {file, type}):
//
//	type ← create_table_statement / create_view_statement (the named relation)
//
// Absent by design: function/method/variable/constant (no OO callables; SQL is
// statement-oriented). A view's FROM clause referencing an in-file table becomes an
// intra-file references edge; an unresolved table reference becomes a (non-selector)
// PendingRef. There is NO import system — Imports/References stay empty by design.

type sqlSymbolExtractor struct{ lang *gts.Language }

// Language implements SymbolExtractor.
func (*sqlSymbolExtractor) Language() string { return "sql" }

// Extract implements SymbolExtractor for SQL.
func (e *sqlSymbolExtractor) Extract(filename string, root any) ([]model.Node, []model.Edge, []PendingRef, error) {
	t, ok := root.(*sqlAST)
	if !ok || t == nil || t.root == nil {
		return nil, nil, nil, fmt.Errorf("parse: sql extractor: expected non-nil *sqlAST root for %q, got %T", filename, root)
	}
	w := newCSTWalk(t.lang, t.src, langPackage(filename))
	// SW-055 AC#6: fail-closed parse-depth guard on untrusted input (skips the
	// file with structured, source-free provenance if nesting exceeds the bound).
	if derr := w.guardDepth(t.root, filename, "sql"); derr != nil {
		return nil, nil, nil, derr
	}
	sqlCollectDefs(w, t.root)
	sqlResolveUses(w, t.root)
	return w.finishExtract(filename, "sql")
}

// sqlRelationName returns the first identifier child of a create_* statement (the
// table/view name).
func sqlRelationName(stmt *gts.Node, lang *gts.Language) *gts.Node {
	for i := 0; i < stmt.ChildCount(); i++ {
		c := stmt.Child(i)
		if c != nil && c.Type(lang) == "identifier" {
			return c
		}
	}
	return nil
}

func sqlCollectDefs(w *cstWalk, unit *gts.Node) {
	for i := 0; i < unit.ChildCount(); i++ {
		c := unit.Child(i)
		if c == nil {
			continue
		}
		switch c.Type(w.lang) {
		case "create_table_statement", "create_view_statement":
			if name := sqlRelationName(c, w.lang); name != nil {
				w.addDef(name.Text(w.src), KindType, nodePoint(name))
			}
		}
	}
}

func sqlResolveUses(w *cstWalk, unit *gts.Node) {
	for i := 0; i < unit.ChildCount(); i++ {
		c := unit.Child(i)
		if c == nil || c.Type(w.lang) != "create_view_statement" {
			continue
		}
		owner := sqlRelationName(c, w.lang)
		if owner == nil {
			continue
		}
		sqlScanFrom(w, c, owner.Text(w.src))
	}
}

// sqlScanFrom records every table named in a FROM clause inside a view body as a
// references edge (intra-file) or a (non-selector) PendingRef (unresolved).
func sqlScanFrom(w *cstWalk, n *gts.Node, ownerBare string) {
	if n == nil {
		return
	}
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		if c.Type(w.lang) == "from_clause" {
			for j := 0; j < c.ChildCount(); j++ {
				id := c.Child(j)
				if id != nil && id.Type(w.lang) == "identifier" {
					w.typeRef(ownerBare, id.Text(w.src), nodePoint(id))
				}
			}
		}
		sqlScanFrom(w, c, ownerBare)
	}
}
