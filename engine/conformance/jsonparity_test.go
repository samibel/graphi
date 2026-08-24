package conformance_test

// SW-199 (W5.m, intra/parse residual, json slice c): the JSON
// parse-determinism gate, bound to docs/rc/parity-classes-json.yaml by the
// drift guard in jsonparity_matrix_test.go.
//
// JSON IS THE ONE PARSE-ONLY LANGUAGE OF THE SIX, AND THAT CHANGES THE WITNESS.
// core/parse/parser_json.go:25-29 states it plainly: JSONParser wires NO
// SymbolExtractor, so it emits no symbol nodes and no intra-file edges. It does
// not even mint a `file` node. A JSON document therefore contributes NOTHING to
// the graph, and a graph-level witness over a JSON change would pass whether or
// not the document was parsed — the vacuous outcome SW-185 AC-6 rejects
// explicitly for this language ("a vacuous gate that passes regardless of the
// graph's contents is worse than no gate").
//
// So this family's rows witness in TWO halves, and the YAML row notes say which
// half carries which claim:
//
//	PARSE-BOUNDARY HALF (where JSON's AST actually is). The production JSON
//	parser is run over the row's document bytes THREE times, and the canonical
//	re-encoding of the resulting structural root — together with the parse
//	metadata (language, content hash, size) — must be byte-identical across all
//	three. This is AC-4's "same input bytes -> same AST, asserted at byte level
//	on the AST's serialization" for a language whose AST the store never sees.
//	Each row also carries a `docWitness` over the parsed value, so a row whose
//	document changed shape fails rather than passing on an empty tree.
//
//	GRAPH HALF (where the abstention lives). The row's change is applied to a
//	real fixture tree on BOTH stores and BOTH profile axes through the shared
//	runner, which asserts two independent full passes agree byte for byte and
//	that the incremental graph equals the full graph. The row's graph witness
//	asserts the PARSE-ONLY ABSTENTION — no node in the graph came from the .json
//	path — together with a CONTROL symbol from a sibling Markdown document, so
//	the abstention cannot be satisfied by a tree that was never indexed at all.
//
// WHY A MARKDOWN CONTROL AND NOT A SECOND JSON FILE. Stated so it is not read
// as contamination: the control's only job is to prove the ingest ran. A JSON
// sibling could not do that job, because a JSON sibling is invisible too. The
// control is named in every graph witness below and in every YAML note.
//
// THE LANGUAGE-SPEC POINT, because it is the load-bearing one. JSON's own
// specification (RFC 8259 / ECMA-404) defines objects, arrays, numbers,
// strings, and the three literals — and NOTHING ELSE. It has no include, no
// import, and no reference of any kind. `$ref` is a construct of JSON SCHEMA
// and JSON Reference (draft specifications layered ON json), not of JSON: to
// json's grammar `"$ref": "other.json#/x"` is an ordinary member whose name
// happens to start with a dollar sign. So JSON's own answer to "does the
// language define a cross-file reference?" is no, and that — not graphi's
// parser comment — is the spec-level abstention this family records.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/samibel/graphi/core/parse"
)

// jsonBaseTree is the JSON fixture every row starts from: one JSON document
// (invisible to the graph) and one Markdown document whose heading is the
// CONTROL symbol every graph witness requires.
func jsonBaseTree() map[string]string {
	return map[string]string{
		"api/schema.json": `{
  "name": "demo",
  "version": 1
}
`,
		"api/notes.md": `# ApiNotes

the control anchor: this heading proves the tree was indexed
`,
	}
}

// jsonParityRow is the JSON family's row shape. It is NOT changeClassRow
// because a parse-only language needs a witness over the PARSED VALUE as well
// as one over the graph, and folding a parse-boundary witness into the shared
// change-class row would hand every other family a field it must not use.
type jsonParityRow struct {
	id          string
	kind        string
	description string
	// apply is the change over the fixture tree (the graph half).
	apply func(f *fixture)
	// witness is the graph-half predicate: the parse-only abstention plus the
	// control symbol.
	witness func(g *graphView) error
	// docPath / doc are the JSON document the parse-boundary half parses. doc
	// is empty exactly for the delete row, which has no document left to parse.
	docPath string
	doc     string
	// docWitness predicates over the decoded structural root. Required whenever
	// doc is non-empty.
	docWitness func(v any) error
	// wantCanonical, when non-empty, pins the canonical re-encoding of the
	// decoded root exactly. It is what makes the member-reorder row a real
	// claim rather than a restatement of determinism.
	wantCanonical string
}

// jsonParityTable is the declarative JSON matrix. Row order follows
// docs/rc/parity-classes-json.yaml so the two files diff side by side.
func jsonParityTable() []jsonParityRow {
	return []jsonParityRow{
		{
			id:          "json_add_file",
			kind:        kindChangeClass,
			description: "A new JSON document arrives in a new directory. The parse-boundary half parses its bytes three times and requires an identical canonical encoding and identical parse metadata each time; the docWitness requires the document's two members, so a parse that returned an empty tree fails. The graph half requires that NO node came from the new path (the parse-only abstention) while the control heading from the sibling Markdown document is present.",
			apply: func(f *fixture) {
				f.Write("vendor/config.json", "{\n  \"driver\": \"pg\",\n  \"pool\": 4\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					requireNoNodeFrom(g, "vendor/config.json"),
					requireNoNodeFrom(g, "api/schema.json"),
					g.requirePresent("api.ApiNotes"), // control: the tree really indexed
				)
			},
			docPath: "vendor/config.json",
			doc:     "{\n  \"driver\": \"pg\",\n  \"pool\": 4\n}\n",
			docWitness: func(v any) error {
				return jsonRequireMembers(v, "driver", "pool")
			},
			wantCanonical: `{"driver":"pg","pool":4}`,
		},
		{
			id:          "json_modify_file",
			kind:        kindChangeClass,
			description: "An indexed JSON document is rewritten in place with an added member. The docWitness requires the NEW member alongside both pre-existing members, so an apply that did nothing fails at the parse boundary. The graph half requires the abstention over the rewritten path and the control heading.",
			apply: func(f *fixture) {
				f.Write("api/schema.json", "{\n  \"name\": \"demo\",\n  \"version\": 1,\n  \"stable\": true\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					requireNoNodeFrom(g, "api/schema.json"),
					g.requirePresent("api.ApiNotes"),
				)
			},
			docPath: "api/schema.json",
			doc:     "{\n  \"name\": \"demo\",\n  \"version\": 1,\n  \"stable\": true\n}\n",
			docWitness: func(v any) error {
				return jsonRequireMembers(v, "name", "version", "stable")
			},
			wantCanonical: `{"name":"demo","stable":true,"version":1}`,
		},
		{
			id:          "json_delete_file",
			kind:        kindChangeClass,
			description: "The indexed JSON document is deleted. STATED RATHER THAN OVERCLAIMED: for a parse-only language a delete has NO observable graph consequence, so this row's witness is deliberately a control-only witness — it asserts that removing a file the graph never saw leaves the sibling Markdown document's symbol intact and the whole-tree byte parity undisturbed. There is no parse-boundary half, because there is no document left to parse. The row is carried because delete_file is a real Go class that must be dispositioned for this language, and `adapted` with this reason is the honest disposition.",
			apply: func(f *fixture) {
				f.Remove("api/schema.json")
			},
			witness: func(g *graphView) error {
				return all(
					requireNoNodeFrom(g, "api/schema.json"),
					g.requirePresent("api.ApiNotes"),
					requireFileNode(g, "api/notes.md"),
				)
			},
		},
		{
			id:          "json_nested_document",
			kind:        kindChangeClass,
			description: "A deeply nested document (object inside array inside object) replaces the flat one. The docWitness walks into the nesting and requires the leaf value, so a parser that flattened or truncated the structure fails; the three-times canonical encoding is what pins determinism over the nested shape, where a map-iteration-order defect would surface first.",
			apply: func(f *fixture) {
				f.Write("api/schema.json", "{\n  \"routes\": [\n    {\"path\": \"/a\", \"methods\": [\"GET\", \"POST\"]},\n    {\"path\": \"/b\", \"methods\": [\"GET\"]}\n  ],\n  \"name\": \"demo\"\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					requireNoNodeFrom(g, "api/schema.json"),
					g.requirePresent("api.ApiNotes"),
				)
			},
			docPath: "api/schema.json",
			doc:     "{\n  \"routes\": [\n    {\"path\": \"/a\", \"methods\": [\"GET\", \"POST\"]},\n    {\"path\": \"/b\", \"methods\": [\"GET\"]}\n  ],\n  \"name\": \"demo\"\n}\n",
			docWitness: func(v any) error {
				m, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("root is %T, want an object", v)
				}
				routes, ok := m["routes"].([]any)
				if !ok || len(routes) != 2 {
					return fmt.Errorf("routes is %#v, want a 2-element array", m["routes"])
				}
				first, ok := routes[0].(map[string]any)
				if !ok || first["path"] != "/a" {
					return fmt.Errorf("routes[0] is %#v, want an object with path /a", routes[0])
				}
				methods, ok := first["methods"].([]any)
				if !ok || len(methods) != 2 || methods[1] != "POST" {
					return fmt.Errorf("routes[0].methods is %#v, want [GET POST]", first["methods"])
				}
				return nil
			},
			wantCanonical: `{"name":"demo","routes":[{"methods":["GET","POST"],"path":"/a"},{"methods":["GET"],"path":"/b"}]}`,
		},
		{
			id:          "json_reorder_members",
			kind:        kindChangeClass,
			description: "The members of the object are permuted with no change to any value. RFC 8259 §4 states an object is an UNORDERED collection, so the permuted document must decode to a structurally identical value — and this row pins that by requiring the canonical encoding to equal the base document's, byte for byte. It is the one row whose claim is not merely `stable across passes` but `stable across a source permutation`, which is where a member-order-dependent parser would fail.",
			apply: func(f *fixture) {
				f.Write("api/schema.json", "{\n  \"version\": 1,\n  \"name\": \"demo\"\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					requireNoNodeFrom(g, "api/schema.json"),
					g.requirePresent("api.ApiNotes"),
				)
			},
			docPath: "api/schema.json",
			doc:     "{\n  \"version\": 1,\n  \"name\": \"demo\"\n}\n",
			docWitness: func(v any) error {
				return jsonRequireMembers(v, "name", "version")
			},
			// Byte-identical to the canonical encoding of jsonBaseTree()'s
			// api/schema.json, which declares the same two members in the
			// opposite order.
			wantCanonical: `{"name":"demo","version":1}`,
		},
		{
			id:          "json_reparse_identical_bytes",
			kind:        kindChangeClass,
			description: "The indexed JSON document is rewritten with BYTE-IDENTICAL content. The drift scanner sees an empty drift set and Reconcile short-circuits (engine/watch/service.go), so the incremental graph must equal the full graph over unchanged bytes; the parse-boundary half re-parses the same bytes and pins the same canonical encoding and the same content hash. Its witnesses are deliberately semantics-preserving — the row's force comes from the runner's two independent full passes and the three parse-boundary passes, which is where the determinism claim is actually asserted.",
			apply: func(f *fixture) {
				f.Write("api/schema.json", "{\n  \"name\": \"demo\",\n  \"version\": 1\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					requireNoNodeFrom(g, "api/schema.json"),
					g.requirePresent("api.ApiNotes"),
					requireFileNode(g, "api/notes.md"),
				)
			},
			docPath: "api/schema.json",
			doc:     "{\n  \"name\": \"demo\",\n  \"version\": 1\n}\n",
			docWitness: func(v any) error {
				return jsonRequireMembers(v, "name", "version")
			},
			wantCanonical: `{"name":"demo","version":1}`,
		},
	}
}

// jsonRequireMembers fails unless the decoded root is an object carrying every
// named member.
func jsonRequireMembers(v any, members ...string) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("root is %T, want an object", v)
	}
	for _, name := range members {
		if _, found := m[name]; !found {
			return fmt.Errorf("member %q absent; document has %d member(s)", name, len(m))
		}
	}
	if len(m) != len(members) {
		return fmt.Errorf("document has %d members, want exactly %d (%v)", len(m), len(members), members)
	}
	return nil
}

// jsonParseFingerprint parses src with the PRODUCTION JSON parser and returns a
// byte-level fingerprint of the result: the parse metadata plus the canonical
// re-encoding of the decoded structural root. encoding/json marshals map keys
// in sorted order, so the encoding is a canonical form of the AST and not a
// restatement of the input bytes.
func jsonParseFingerprint(t *testing.T, path, src string) (fingerprint, canonical string, root any) {
	t.Helper()
	res, err := parse.NewJSONParser().Parse(context.Background(), path, []byte(src))
	if err != nil {
		t.Fatalf("json parse %s: %v", path, err)
	}
	enc, err := json.Marshal(res.Root)
	if err != nil {
		t.Fatalf("canonical encode %s: %v", path, err)
	}
	return fmt.Sprintf("lang=%s hash=%s size=%d root=%s", res.Meta.Language, res.Meta.ContentHash, res.Meta.Size, enc),
		string(enc), res.Root
}

// jsonParseBoundaryPasses is the number of independent parses the
// parse-boundary half runs per row per axis. Three, not two: two passes cannot
// distinguish "deterministic" from "alternating".
const jsonParseBoundaryPasses = 3

// runJSONParityRow runs both halves of one JSON row on one (backend, profile)
// axis.
func runJSONParityRow(t *testing.T, b parityBackend, pr parityProfile, row jsonParityRow) {
	t.Helper()

	// Graph half — the abstention, the control, and the shared runner's two
	// independent full passes plus the full-vs-incremental comparison.
	runIntraFileParityRow(t, b, pr, jsonBaseTree(), changeClassRow{
		id:          row.id,
		kind:        row.kind,
		description: row.description,
		apply:       row.apply,
		witness:     row.witness,
	})

	// Parse-boundary half — where JSON's AST actually is.
	if row.doc == "" {
		return
	}
	if row.docWitness == nil {
		t.Fatalf("row %q declares a document but no docWitness", row.id)
	}
	want, canonical, root := jsonParseFingerprint(t, row.docPath, row.doc)
	if err := row.docWitness(root); err != nil {
		t.Errorf("[%s/%s] VACUOUS ROW: docWitness did not hold over the parsed document: %v", b.name+"/"+pr.name, row.id, err)
	}
	if row.wantCanonical != "" && canonical != row.wantCanonical {
		t.Errorf("[%s/%s] CANONICAL FORM: %s decoded to %s, want %s",
			b.name+"/"+pr.name, row.id, row.docPath, canonical, row.wantCanonical)
	}
	for pass := 2; pass <= jsonParseBoundaryPasses; pass++ {
		got, _, _ := jsonParseFingerprint(t, row.docPath, row.doc)
		if got != want {
			t.Errorf("[%s/%s] PARSE-DETERMINISM FAIL: pass %d of %s produced\n  %s\nwant\n  %s",
				b.name+"/"+pr.name, row.id, pass, row.docPath, got, want)
		}
	}
}

// TestJsonParityDeterminism_ByteStable is the SW-199 JSON gate. One subtest per
// (backend, profile, row), each running both halves: the parse-boundary
// determinism assertion over the document's bytes and the graph-level
// parse-only abstention with its control symbol.
func TestJsonParityDeterminism_ByteStable(t *testing.T) {
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range jsonParityTable() {
						row := row
						t.Run(row.id, func(t *testing.T) {
							t.Parallel()
							runJSONParityRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}
