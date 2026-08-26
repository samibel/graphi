package parity

import "strings"

// ---------------------------------------------------------------------------
// W5.n slice (d) — Markdown.
//
// SYMBOL SHAPE per docs/rc/parity-classes-markdown.yaml: each ATX heading
// becomes a `type` symbol qualified as <directory>.<heading text>; Markdown has
// no callables, so the node set collapses to {file, type}. The heading TEXT is
// the identity, which is why the rename-shaped class rewrites a heading's text
// and not its level.
// ---------------------------------------------------------------------------

func parseDetMarkdown() ParseDetLanguage {
	sh := ParseDetShape{
		Ext: ".md",
		NewFile: func(seed string) []byte {
			return []byte("# " + seed + "\n\nAdded by the W5.n parse-determinism matrix.\n")
		},
		Append: func(src []byte, seed string) ([]byte, error) {
			add := "\n## " + seed + " added\n\nAppended by the W5.n parse-determinism matrix.\n"
			return append(append([]byte(nil), src...), add...), nil
		},
		Rename:  markdownRename,
		Reorder: markdownReorder,
	}
	return ParseDetLanguage{
		Name:           "markdown",
		Exts:           []string{".md", ".markdown"},
		MinSymbolKinds: []string{"type"},
		Specs: parseDetStandardSpecs("markdown", "markdown_rename_heading", "markdown_reorder_sections", sh,
			"A heading's TEXT is rewritten in place. The heading text IS the symbol identity, so the "+
				"rename is a delete-plus-add inside one file; the heading LEVEL is untouched.",
			"The heading sections of one document are permuted with no textual change to any section "+
				"body. The symbol SET is unchanged and only source order and line numbers move."),
	}
}

// isATXHeading reports whether a line opens an ATX heading. It requires the
// space after the hashes that CommonMark requires, so `#hashtag` is not read as
// a heading — treating it as one would make the row edit a line the parser
// never minted a symbol for.
func isATXHeading(line string) bool {
	t := strings.TrimLeft(line, " ")
	if !strings.HasPrefix(t, "#") {
		return false
	}
	n := 0
	for n < len(t) && t[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(t) && t[n] == ' '
}

// markdownRename rewrites the LAST heading's text.
func markdownRename(src []byte, seed string) ([]byte, error) {
	lines := strings.SplitAfter(string(src), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		body := strings.TrimRight(lines[i], "\r\n")
		if !isATXHeading(body) {
			continue
		}
		tail := lines[i][len(body):]
		hashes := body[:strings.Index(body, " ")]
		lines[i] = hashes + " " + seed + " renamed" + tail
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, errNoTarget
}

// markdownReorder rotates the heading sections. Content before the first
// heading is a PREAMBLE (front matter, a lede paragraph) and stays first:
// moving it below a heading would attach it to a section it was never part of,
// which is a content change and not a reordering.
func markdownReorder(src []byte) ([]byte, error) {
	preamble, sections := splitLineSections(src, isATXHeading)
	rotated := rotateSections(sections)
	if rotated == nil {
		return nil, errNoTarget
	}
	return []byte(preamble + strings.Join(rotated, "")), nil
}
