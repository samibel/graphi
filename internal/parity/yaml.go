package parity

import "strings"

// ---------------------------------------------------------------------------
// W5.n slice (f) — YAML.
//
// SYMBOL SHAPE per docs/rc/parity-classes-yaml.yaml: each TOP-LEVEL mapping key
// becomes a `variable` symbol qualified as <directory>.<key>; nested keys are
// deliberately NOT minted, so the node set collapses to {file, variable}. Every
// edit in this file therefore acts on a top-level key — an edit to a nested key
// would change bytes the parser mints nothing for, and the row would then prove
// only that unrelated bytes moved.
//
// MULTI-DOCUMENT STREAMS ARE REFUSED, not handled. A `---` separator starts a
// second document whose top-level keys are a different mapping; rotating across
// that boundary would move a key between documents, which is a semantic change
// wearing a reordering's name.
// ---------------------------------------------------------------------------

func parseDetYAML() ParseDetLanguage {
	sh := ParseDetShape{
		Ext: ".yaml",
		NewFile: func(seed string) []byte {
			return []byte(seed + ":\n  kind: added\n")
		},
		Append: func(src []byte, seed string) ([]byte, error) {
			if yamlIsMultiDocument(src) {
				return nil, errNoTarget
			}
			add := "\n" + seed + "_added:\n  kind: appended\n"
			return append(append([]byte(nil), src...), add...), nil
		},
		Rename:  yamlRename,
		Reorder: yamlReorder,
	}
	return ParseDetLanguage{
		Name:           "yaml",
		Exts:           []string{".yaml", ".yml"},
		MinSymbolKinds: []string{"variable"},
		Specs: parseDetStandardSpecs("yaml", "yaml_rename_key", "yaml_reorder_keys", sh,
			"A TOP-LEVEL mapping key is renamed in place. The key IS the symbol identity, so the rename "+
				"is a delete-plus-add inside one file; nested keys mint nothing and are untouched.",
			"The top-level keys of one document are permuted with no textual change to any value block. "+
				"YAML mapping order is not semantically significant, which is why a serialisation "+
				"following parse order rather than a canonical key would diverge here."),
	}
}

// yamlIsMultiDocument reports whether the stream carries a document separator.
func yamlIsMultiDocument(src []byte) bool {
	for _, ln := range strings.Split(string(src), "\n") {
		t := strings.TrimRight(ln, "\r")
		if t == "---" || strings.HasPrefix(t, "--- ") || t == "..." {
			return true
		}
	}
	return false
}

// isYAMLTopLevelKey reports whether a line declares a top-level mapping key: no
// leading indentation, not a comment, not a sequence entry, and carrying a
// `key:` before any inline comment.
func isYAMLTopLevelKey(line string) bool {
	if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' || line[0] == '-' {
		return false
	}
	i := strings.IndexByte(line, ':')
	if i <= 0 {
		return false
	}
	// A quoted key would need real parsing to split; refuse rather than guess.
	if strings.ContainsAny(line[:i], "\"'") {
		return false
	}
	return i+1 == len(line) || line[i+1] == ' '
}

// yamlRename renames the LAST top-level key.
func yamlRename(src []byte, seed string) ([]byte, error) {
	if yamlIsMultiDocument(src) {
		return nil, errNoTarget
	}
	lines := strings.SplitAfter(string(src), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		body := strings.TrimRight(lines[i], "\r\n")
		if !isYAMLTopLevelKey(body) {
			continue
		}
		tail := lines[i][len(body):]
		colon := strings.IndexByte(body, ':')
		lines[i] = seed + "_renamed" + body[colon:] + tail
		return []byte(strings.Join(lines, "")), nil
	}
	return nil, errNoTarget
}

// yamlReorder rotates the top-level key blocks.
func yamlReorder(src []byte) ([]byte, error) {
	if yamlIsMultiDocument(src) {
		return nil, errNoTarget
	}
	preamble, sections := splitLineSections(src, isYAMLTopLevelKey)
	rotated := rotateSections(sections)
	if rotated == nil {
		return nil, errNoTarget
	}
	return []byte(preamble + strings.Join(rotated, "")), nil
}
