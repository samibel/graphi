package conformance_test

// SW-199 (W5.m, intra/parse residual, yaml slice f): the YAML
// parse-determinism and intra-file parity gate, bound to
// docs/rc/parity-classes-yaml.yaml by the drift guard in
// yamlparity_matrix_test.go.
//
// LEVEL, AND WHAT THAT MEANS FOR THE WITNESS SHAPE. YAML is `intra-file-only`.
// The rows prove PARSE DETERMINISM — the same input bytes produce the same AST
// and its serialization is byte-stable — plus intra-file full-vs-incremental
// parity on both stores and both profile axes. Every cross-file Go class is
// dispositioned `not_applicable` with a language-spec reason in the YAML's
// `go_class_applicability:` block.
//
// THE LANGUAGE-SPEC POINT, because it is the load-bearing one and because YAML
// is the family where it cuts the other way. YAML 1.2's specification defines a
// TAG mechanism — node properties carrying a tag, and the tag-resolution rules
// that hand an unrecognised tag to the application ("Tags", and the schema
// chapter's treatment of unresolved tags). That mechanism is the language-level
// hook a `!include ./other.yaml` node rides on, and it is what puts YAML on the
// "yes" side of the §5.5 test: unlike TOML and HCL, whose specs give the
// language NO way at all to name another document, YAML's own spec provides the
// construct through which a cross-document reference is expressed. Two limits
// are stated here rather than overclaimed: YAML does NOT standardise `!include`
// itself (the application resolves it), and YAML's anchors and aliases are
// explicitly stream-local and are NOT a cross-file reference. What follows is
// that graphi's YAML extractor mints top-level mapping keys and resolves no
// tag, so the abstention recorded for this family is GRAPHI's, not YAML's —
// and saying it the other way round, by citing graphi's parser comment as if it
// were the language's answer, is the LANGHONEST-001 circular-abstention defect
// this family is the most exposed to of the six.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures. NOT the real-repository matrix (G4), NOT a correctness proof of the
// YAML grammar — correctness evidence lives in core/parse/parser_yaml_test.go.

import (
	"testing"
)

// yamlBaseTree is the YAML fixture every row starts from: two documents in one
// directory. YAML symbol QNs are `<dir>.<top-level mapping key>`; nested keys
// are deliberately NOT minted, so `server.port` never appears.
func yamlBaseTree() map[string]string {
	return map[string]string{
		"deploy/app.yaml": `name: demo
server:
  port: 8080
replicas: 3
`,
		"deploy/svc.yaml": `kind: Service
ports:
  - 80
`,
	}
}

// yamlParityTable is the declarative YAML matrix. Row order follows
// docs/rc/parity-classes-yaml.yaml so the two files diff side by side.
func yamlParityTable() []changeClassRow {
	return []changeClassRow{
		{
			id:          "yaml_add_file",
			kind:        kindChangeClass,
			description: "A new YAML document arrives in a new directory: the pure add path. The witness requires the new top-level keys and the base-tree control symbol, so a row that indexed nothing cannot pass.",
			apply: func(f *fixture) {
				f.Write("charts/values.yaml", "image: nginx\ntag: latest\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("charts.image", "charts.tag"),
					requireFileNode(g, "charts/values.yaml"),
					g.requirePresent("deploy.name"),
				)
			},
		},
		{
			id:          "yaml_modify_file",
			kind:        kindChangeClass,
			description: "An indexed YAML document is rewritten in place: a top-level key is appended while the existing keys keep identity. The witness requires the new key AND all three pre-existing keys.",
			apply: func(f *fixture) {
				f.Write("deploy/app.yaml", `name: demo
server:
  port: 8080
replicas: 3
env: prod
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("deploy.env"),
					g.requirePresent("deploy.name", "deploy.server", "deploy.replicas"),
				)
			},
		},
		{
			id:          "yaml_delete_file",
			kind:        kindChangeClass,
			description: "An indexed YAML document is deleted, so the per-file stale-node purge runs over it. The witness requires the deleted document's keys AND its file node gone, and the sibling document's keys still present — a stale node surviving a delete is the failure this row exists to catch.",
			apply: func(f *fixture) {
				f.Remove("deploy/svc.yaml")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("deploy.kind", "deploy.ports"),
					requireNoFileNode(g, "deploy/svc.yaml"),
					g.requirePresent("deploy.name", "deploy.server", "deploy.replicas"),
				)
			},
		},
		{
			id:          "yaml_rename_key",
			kind:        kindChangeClass,
			description: "A top-level mapping key is renamed in place (name -> appName). The symbol's qualified name is the key, so the rename is a delete-plus-add inside one document. The witness requires the new key present AND the old key absent.",
			apply: func(f *fixture) {
				f.Write("deploy/app.yaml", `appName: demo
server:
  port: 8080
replicas: 3
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("deploy.appName"),
					g.requireAbsent("deploy.name"),
					g.requirePresent("deploy.server", "deploy.replicas"),
				)
			},
		},
		{
			id:          "yaml_reorder_keys",
			kind:        kindChangeClass,
			description: "The top-level keys of one document are permuted with no textual change to any value. The symbol SET is unchanged and only source order and line numbers move, which is the sharpest test of the canonical-ordering discipline: a snapshot whose row order followed parse order rather than a canonical key would diverge here between the incremental and full passes.",
			apply: func(f *fixture) {
				f.Write("deploy/app.yaml", `replicas: 3
server:
  port: 8080
name: demo
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("deploy.name", "deploy.server", "deploy.replicas"),
					g.requireLine("deploy.replicas", 1),
					g.requireLine("deploy.name", 4),
				)
			},
		},
		{
			id:          "yaml_reparse_identical_bytes",
			kind:        kindChangeClass,
			description: "An indexed YAML document is rewritten with BYTE-IDENTICAL content. This is the direct same-bytes-same-AST row: the drift scanner sees an empty drift set and Reconcile short-circuits (engine/watch/service.go), so the incremental graph must equal the full graph over unchanged bytes. Its witness pins the surviving symbol set rather than a new symbol, because the change is deliberately semantics-preserving — the row's force comes from the runner's two independent full passes, which is where the determinism claim is actually asserted.",
			apply: func(f *fixture) {
				f.Write("deploy/app.yaml", `name: demo
server:
  port: 8080
replicas: 3
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("deploy.name", "deploy.server", "deploy.replicas"),
					g.requirePresent("deploy.kind", "deploy.ports"),
					requireFileNode(g, "deploy/app.yaml"),
				)
			},
		},
	}
}

// TestYamlParityDeterminism_ByteStable is the SW-199 YAML gate. One subtest per
// (backend, profile, row): two independent full passes over the identical bytes
// serialize identically (parse determinism), the incremental watcher-driven
// graph equals the full graph (intra-file parity), and the row's non-vacuity
// witness holds.
func TestYamlParityDeterminism_ByteStable(t *testing.T) {
	base := yamlBaseTree()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range yamlParityTable() {
						row := row
						t.Run(row.id, func(t *testing.T) {
							t.Parallel()
							runIntraFileParityRow(t, b, pr, base, row)
						})
					}
				})
			}
		})
	}
}
