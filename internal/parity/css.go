package parity

import (
	"bytes"
	"strings"
)

// ---------------------------------------------------------------------------
// W5.n slice (a) — CSS.
//
// SYMBOL SHAPE, taken from docs/rc/parity-classes-css.yaml and not re-decided
// here: each top-level rule's selector becomes a `type` symbol qualified as
// <directory>.<selector>; CSS has no callables, so the node set collapses to
// {file, type}. That is why MinSymbolKinds is {type} and why a graph over a
// real stylesheet tree carrying only `file` nodes is VACUOUS rather than empty.
//
// CSS's OWN spec defines a cross-file construct — the `@import` at-rule — and
// graphi declines to extract it (core/parse/parser_css_test.go's
// TestExtractCSS_NoImports pins the absence). Nothing in this file relies on
// that either way: the witness shape is parse determinism, which is indifferent
// to whether an import edge exists. Recording it so a reader does not infer
// that the absence of import assertions here is an oversight.
// ---------------------------------------------------------------------------

func parseDetCSS() ParseDetLanguage {
	sh := ParseDetShape{
		Ext: ".css",
		NewFile: func(seed string) []byte {
			return []byte("." + strings.ReplaceAll(seed, "_", "-") + " {\n  color: #123456;\n}\n")
		},
		Append: func(src []byte, seed string) ([]byte, error) {
			add := "\n." + strings.ReplaceAll(seed, "_", "-") + "-added {\n  display: block;\n}\n"
			return append(append([]byte(nil), src...), add...), nil
		},
		Rename:  cssRename,
		Reorder: cssReorder,
	}
	return ParseDetLanguage{
		Name:           "css",
		Exts:           []string{".css"},
		MinSymbolKinds: []string{"type"},
		Specs: parseDetStandardSpecs("css", "css_rename_selector", "css_reorder_rules", sh,
			"A selector is renamed in place. The symbol's qualified name is derived from the selector "+
				"text, so the rename is a delete-plus-add inside one file.",
			"The top-level rules of one stylesheet are permuted with no textual change to any rule body. "+
				"The symbol SET is unchanged and only source order and line numbers move, which is the "+
				"sharpest test of the canonical-ordering discipline."),
	}
}

// cssRename renames the LAST top-level rule's selector.
//
// It edits only the selector text that precedes the block's `{`, and it refuses
// (errNoTarget) any block whose selector carries a comma or an at-rule marker —
// a selector list and an `@media` block are different shapes, and renaming
// either by string surgery would produce source whose meaning this harness did
// not intend.
func cssRename(src []byte, seed string) ([]byte, error) {
	blocks, err := splitTopLevelBraceBlocks(src)
	if err != nil {
		return nil, err
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		b := blocks[i]
		open := strings.IndexByte(b, '{')
		if open < 0 {
			continue
		}
		sel := strings.TrimSpace(b[:open])
		if sel == "" || strings.ContainsAny(sel, ",@") {
			continue
		}
		lead := b[:strings.Index(b, sel)]
		renamed := "." + strings.ReplaceAll(seed, "_", "-") + "-renamed"
		blocks[i] = lead + renamed + b[strings.Index(b, sel)+len(sel):]
		return []byte(strings.Join(blocks, "")), nil
	}
	return nil, errNoTarget
}

// cssReorder rotates the top-level rules of a stylesheet.
//
// The at-rule PREAMBLE (`@import`, `@charset`) must stay at the front: CSS
// requires `@charset` first and `@import` before any style rule, so a rotation
// that moved a rule above them would produce source a conforming parser reads
// differently — which would make the row a test of invalid input rather than of
// ordering.
func cssReorder(src []byte) ([]byte, error) {
	blocks, err := splitTopLevelBraceBlocks(src)
	if err != nil {
		return nil, err
	}
	var lead string
	var rules []string
	for _, b := range blocks {
		if strings.TrimSpace(b) == "" {
			lead += b
			continue
		}
		if len(rules) == 0 && !bytes.ContainsAny([]byte(b), "{") {
			lead += b
			continue
		}
		if len(rules) == 0 {
			open := strings.IndexByte(b, '{')
			head := strings.TrimSpace(b[:open])
			if strings.HasPrefix(head, "@") {
				lead += b
				continue
			}
			// The at-rule preamble that precedes this first real rule travels
			// with it inside the block string, so split it off by line.
			cut := strings.LastIndex(b[:open], "\n")
			if cut >= 0 && strings.Contains(b[:cut], "@") {
				lead += b[:cut+1]
				b = b[cut+1:]
			}
		}
		rules = append(rules, b)
	}
	rotated := rotateSections(rules)
	if rotated == nil {
		return nil, errNoTarget
	}
	return []byte(lead + strings.Join(rotated, "")), nil
}
