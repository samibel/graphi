package parity

import "strings"

// ---------------------------------------------------------------------------
// W5.n slice (e) — TOML.
//
// SYMBOL SHAPE per docs/rc/parity-classes-toml.yaml: each table header becomes
// a `type` symbol and each top-level key/value pair a `variable`, both
// qualified as <directory>.<name>; the node set collapses to
// {file, type, variable}.
// ---------------------------------------------------------------------------

func parseDetTOML() ParseDetLanguage {
	sh := ParseDetShape{
		Ext: ".toml",
		NewFile: func(seed string) []byte {
			return []byte("[" + seed + "]\nname = \"added\"\n")
		},
		Append: func(src []byte, seed string) ([]byte, error) {
			add := "\n[" + seed + "_added]\nname = \"appended\"\n"
			return append(append([]byte(nil), src...), add...), nil
		},
		Rename:  tomlRename,
		Reorder: tomlReorder,
	}
	return ParseDetLanguage{
		Name:           "toml",
		Exts:           []string{".toml"},
		MinSymbolKinds: []string{"type", "variable"},
		Specs: parseDetStandardSpecs("toml", "toml_rename_table", "toml_reorder_tables", sh,
			"A table header is renamed in place. The header text IS the symbol identity, so the rename "+
				"is a delete-plus-add inside one file.",
			"The tables of one document are permuted with no textual change to any table body. TOML "+
				"table order is not semantically significant, which is why a serialisation following "+
				"parse order rather than a canonical key would diverge here."),
	}
}

// isTOMLTableHeader reports whether a line opens a table or an array of tables.
func isTOMLTableHeader(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]")
}

// tomlRename renames the LAST table header.
//
// It refuses an array-of-tables header (`[[x]]`) rather than renaming it: two
// `[[x]]` entries are one array, and renaming the last of them splits the array
// instead of renaming a table — a different change from the one this class
// declares.
func tomlRename(src []byte, seed string) ([]byte, error) {
	lines := strings.SplitAfter(string(src), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		body := strings.TrimRight(lines[i], "\r\n")
		if !isTOMLTableHeader(body) {
			continue
		}
		t := strings.TrimSpace(body)
		if strings.HasPrefix(t, "[[") {
			continue
		}
		tail := lines[i][len(body):]
		lines[i] = "[" + seed + "_renamed]" + tail
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, errNoTarget
}

// tomlReorder rotates the table sections. The PREAMBLE — the top-level
// key/value pairs before the first table header — must stay first: in TOML a
// bare key after a table header belongs to that table, so moving the preamble
// down would re-parent every key in it.
func tomlReorder(src []byte) ([]byte, error) {
	preamble, sections := splitLineSections(src, isTOMLTableHeader)
	rotated := rotateSections(sections)
	if rotated == nil {
		return nil, errNoTarget
	}
	return []byte(preamble + strings.Join(rotated, "")), nil
}
