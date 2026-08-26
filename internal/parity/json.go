package parity

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------------------------------------------------------------------------
// W5.n slice (c) — JSON, the one PARSE-ONLY language of the six.
//
// THE HONESTY POINT OF THIS FILE. docs/rc/parity-classes-json.yaml:63 states
// the symbol shape as "nothing at all: a JSON document contributes NO node to
// the graph". Every other language in this family can be checked for vacuity by
// counting node kinds; JSON cannot, because for JSON the CORRECT graph over a
// changed document is the unchanged one, and two empty graphs are
// byte-identical. A row that scored byte equality over an empty graph as PASS
// would be certifying that graphi did nothing — the exact false green the
// hermetic twin's two-half witness exists to prevent.
//
// So this language is marked ParseOnly, and its non-vacuity witness is the
// PARSE BOUNDARY rather than the graph: after the class's edit is applied, the
// document is read BACK OUT OF THE MATERIALIZED TREE and must be exactly what
// the mutation said it would be: for a write class, the bytes on disk equal the
// bytes the planner said it would write; for the delete class, the path is
// gone. An apply that did nothing fails there.
//
// The comparison is BYTE equality and nothing else (parseDetParseBoundary,
// parsedet.go). An earlier version of this comment also claimed a DECODED
// comparison "for every class but the delete"; there is none, and there never
// was — the only json.Unmarshal in this file is in the planner, deciding what
// to write. The claim was harmless because byte equality is strictly stronger
// than decoded equality over the same bytes, but a witness described as doing a
// step it does not do is the defect class this family exists to catch, so it is
// corrected rather than left standing (SW-200 review round 1, minor m1).
//
// What this family therefore proves for JSON, stated so it cannot be
// over-read: that graphi's whole-tree serialisation is byte-stable across two
// independent full passes and across the incremental path WHILE a real JSON
// document in the tree changed underneath it. It proves nothing about a JSON
// symbol, because there is none.
// ---------------------------------------------------------------------------

func parseDetJSON() ParseDetLanguage {
	sh := ParseDetShape{
		Ext: ".json",
		NewFile: func(seed string) []byte {
			return []byte("{\n  \"" + seed + "\": {\n    \"kind\": \"added\"\n  }\n}\n")
		},
		Append:  jsonAppendMember,
		Reorder: jsonReorderMembers,
	}
	return ParseDetLanguage{
		Name:      "json",
		Exts:      []string{".json"},
		ParseOnly: true,
		// Deliberately EMPTY. A JSON document mints no node, so there is no
		// kind whose absence would mean "the parser did nothing" — the witness
		// is the parse boundary instead. Putting "file" here would be a claim
		// that graphi mints a file node for a .json path, which the hermetic
		// twin's rows show it does not rely on.
		//
		// MEASURED, 2026-08-26, because the SW-200 review (minor m2) proposed
		// exactly that stronger witness — "assert the edited path appears as a
		// `file` node in the decoded envelope". It is NOT AVAILABLE for json on
		// this tree: `graphi rebuild` over a two-file tree (main.go +
		// api/schema.json) reported "indexed 2 files", and the snapshot
		// envelope carried two nodes, `file main.go` and `function main.go`.
		// The json path minted NOTHING — not a symbol node, not a file node.
		// So MinSymbolKinds{"file"} would refuse every json row rather than
		// strengthen it. The residual gap the review names is real and stays
		// open (a json PASS is issued without consulting the graph at all) and
		// is filed as PARITY-014; it is latent while json has zero exercised
		// rows in either posture.
		MinSymbolKinds: nil,
		Specs: []ParseDetSpec{
			{ID: "json_add_file", Plan: planParseDetAddFile(sh),
				Note: "A JSON document arrives in a NEW directory. The graph half is an ABSTENTION — a " +
					"parse-only language mints nothing — so the observable half is the parse boundary."},
			{ID: "json_modify_file", Plan: planParseDetModifyFile(sh),
				Note: "An indexed JSON document is rewritten in place with an added member; the " +
					"parse-boundary witness requires the new member alongside every pre-existing one."},
			{ID: "json_delete_file", Plan: planParseDetDeleteFile(sh),
				Note: "STATED RATHER THAN OVERCLAIMED: for a parse-only language a delete has NO " +
					"observable graph consequence. The parse-boundary half asserts the one thing that IS " +
					"observable — the document is GONE from the tree after apply."},
			{ID: "json_nested_document", Plan: planJSONNestedDocument(sh),
				Note: "The document is replaced by a DEEPLY NESTED one. It is the depth-stress row: a " +
					"parser that flattened, truncated or recursed differently between two full passes " +
					"would diverge here and nowhere else in this table."},
			{ID: "json_reorder_members", Plan: planParseDetReorder(sh),
				Note: "The top-level members of one document are permuted with no change to any value. " +
					"JSON object member order is not semantically significant, which is exactly why a " +
					"serialisation that followed parse order rather than a canonical key would diverge here."},
			{ID: "json_reparse_identical_bytes", Plan: planParseDetReparseIdentical(sh),
				Note: "The direct same-bytes-same-AST row. LIMIT, stated rather than claimed away: its " +
					"change is byte-identical to the seed by definition, so the parse-boundary witness " +
					"cannot distinguish it from a change that did nothing. The row's force comes from the " +
					"two independent full passes over the identical tree."},
		},
	}
}

// jsonNestedDepth is the nesting depth of the nested-document row. It is high
// enough to exercise a recursive descent and low enough to stay inside any
// sane parser limit; a row that produced input a conforming parser must reject
// would be testing rejection, not determinism.
const jsonNestedDepth = 24

func planJSONNestedDocument(sh ParseDetShape) ParseDetPlanner {
	return func(m *ParseDetModel) (*Mutation, error) {
		f, err := largestFile(m)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		b.WriteString("{\n  \"" + parseDetSeed + "_nested\": ")
		for i := 0; i < jsonNestedDepth; i++ {
			b.WriteString(fmt.Sprintf("{\"level_%d\": ", i))
		}
		b.WriteString("\"leaf\"")
		b.WriteString(strings.Repeat("}", jsonNestedDepth))
		b.WriteString("\n}\n")
		return &Mutation{
			Desc: fmt.Sprintf("replace %s with a document nested %d levels deep", f.Rel, jsonNestedDepth),
			Ops:  []FileOp{{Kind: opWrite, Path: f.Rel, Data: []byte(b.String())}},
		}, nil
	}
}

// jsonAppendMember adds one member to a top-level OBJECT.
//
// It refuses (errNoTarget) a document whose root is not an object: adding a
// "member" to an array or a scalar is not the same class, and silently
// converting one into the other would make two languages' modify_file rows
// incomparable.
func jsonAppendMember(src []byte, seed string) ([]byte, error) {
	prefix, members, suffix, err := splitJSONTopLevelMembers(src)
	if err != nil {
		return nil, err
	}
	added := "\n  \"" + seed + "_added\": \"appended\""
	if len(members) > 0 {
		added = "," + added
	}
	return []byte(prefix + strings.Join(members, ",") + added + suffix), nil
}

// jsonReorderMembers rotates the top-level members of an object.
func jsonReorderMembers(src []byte) ([]byte, error) {
	prefix, members, suffix, err := splitJSONTopLevelMembers(src)
	if err != nil {
		return nil, err
	}
	rotated := rotateSections(members)
	if rotated == nil {
		return nil, errNoTarget
	}
	return []byte(prefix + strings.Join(rotated, ",") + suffix), nil
}

// splitJSONTopLevelMembers splits an object document into its prefix ("{" plus
// leading whitespace), its top-level members (each INCLUDING surrounding
// whitespace, excluding the separating commas) and its suffix ("}" plus
// trailing bytes).
//
// It VALIDATES the document with encoding/json first. A splitter that operated
// on invalid JSON would happily produce more invalid JSON, and the row would
// then measure how graphi handles a broken file rather than how it handles a
// changed one.
func splitJSONTopLevelMembers(src []byte) (string, []string, string, error) {
	var root any
	if err := json.Unmarshal(src, &root); err != nil {
		return "", nil, "", errNoTarget
	}
	if _, ok := root.(map[string]any); !ok {
		return "", nil, "", errNoTarget
	}
	s := string(src)
	open := strings.IndexByte(s, '{')
	closeAt := strings.LastIndexByte(s, '}')
	if open < 0 || closeAt < open {
		return "", nil, "", errNoTarget
	}
	body := s[open+1 : closeAt]
	var members []string
	depth, start := 0, 0
	inStr, esc := false, false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		case ',':
			if depth == 0 {
				members = append(members, body[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 || inStr {
		return "", nil, "", errNoTarget
	}
	if strings.TrimSpace(body[start:]) != "" || len(members) > 0 {
		members = append(members, body[start:])
	}
	return s[:open+1], members, s[closeAt:], nil
}
