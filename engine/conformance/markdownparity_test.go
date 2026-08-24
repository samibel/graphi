package conformance_test

// SW-199 (W5.m, intra/parse residual, markdown slice d): the Markdown
// parse-determinism and intra-file parity gate, bound to
// docs/rc/parity-classes-markdown.yaml by the drift guard in
// markdownparity_matrix_test.go.
//
// LEVEL, AND WHAT THAT MEANS FOR THE WITNESS SHAPE. Markdown is
// `intra-file-only`. The rows prove PARSE DETERMINISM — the same input bytes
// produce the same AST and its serialization is byte-stable — plus intra-file
// full-vs-incremental parity on both stores and both profile axes. Every
// cross-file Go class is dispositioned `not_applicable` with a language-spec
// reason in the YAML's `go_class_applicability:` block.
//
// THE LANGUAGE-SPEC POINT, because it is the load-bearing one. Markdown's own
// spec (CommonMark, sections "Links" and "Link reference definitions") DOES
// define a cross-file construct: the inline link `[text](./target.md)` names
// another document. graphi's Markdown extractor mints heading symbols only and
// does not resolve link destinations, so the abstention here is graphi's, not
// Markdown's, and the YAML says so in exactly those words. Markdown is also
// the family where the symbol half is honest and the cross-file half is not —
// headings are real symbols, links are not extracted — which is what
// distinguishes its disposition from css's.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures. NOT the real-repository matrix (G4), NOT a correctness proof of the
// Markdown grammar — correctness evidence lives in
// core/parse/parser_markdown_test.go.

import (
	"testing"
)

// markdownBaseTree is the Markdown fixture every row starts from: two
// documents in one directory. Markdown symbol QNs are `<dir>.<heading text>`,
// so a heading's whole text — spaces included — is part of the identifier.
func markdownBaseTree() map[string]string {
	return map[string]string{
		"book/guide.md": `# Alpha

text

## Beta

more
`,
		"book/intro.md": `# Preface

hello
`,
	}
}

// markdownParityTable is the declarative Markdown matrix. Row order follows
// docs/rc/parity-classes-markdown.yaml so the two files diff side by side.
func markdownParityTable() []changeClassRow {
	return []changeClassRow{
		{
			id:          "markdown_add_file",
			kind:        kindChangeClass,
			description: "A new document arrives in a new directory: the pure add path. The witness requires the new heading symbol and the base-tree control symbol, so a row that indexed nothing cannot pass.",
			apply: func(f *fixture) {
				f.Write("notes/changelog.md", "# Release Notes\n\nfirst entry\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("notes.Release Notes"),
					requireFileNode(g, "notes/changelog.md"),
					g.requirePresent("book.Alpha"),
				)
			},
		},
		{
			id:          "markdown_modify_file",
			kind:        kindChangeClass,
			description: "An indexed document is rewritten in place: a section is appended while the existing headings keep identity. The witness requires the new heading AND both pre-existing headings.",
			apply: func(f *fixture) {
				f.Write("book/guide.md", `# Alpha

text

## Beta

more

## Gamma

extra
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("book.Gamma"),
					g.requirePresent("book.Alpha"),
					g.requirePresent("book.Beta"),
				)
			},
		},
		{
			id:          "markdown_delete_file",
			kind:        kindChangeClass,
			description: "An indexed document is deleted, so the per-file stale-node purge runs over it. The witness requires the deleted document's heading AND its file node gone, and the sibling document's headings still present — a stale node surviving a delete is the failure this row exists to catch.",
			apply: func(f *fixture) {
				f.Remove("book/intro.md")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("book.Preface"),
					requireNoFileNode(g, "book/intro.md"),
					g.requirePresent("book.Alpha", "book.Beta"),
				)
			},
		},
		{
			id:          "markdown_rename_heading",
			kind:        kindChangeClass,
			description: "A heading is renamed in place (# Alpha -> # Overview). The symbol's qualified name is the heading text, so the rename is a delete-plus-add inside one file. The witness requires the new name present AND the old name absent.",
			apply: func(f *fixture) {
				f.Write("book/guide.md", `# Overview

text

## Beta

more
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("book.Overview"),
					g.requireAbsent("book.Alpha"),
					g.requirePresent("book.Beta"),
				)
			},
		},
		{
			id:          "markdown_reorder_sections",
			kind:        kindChangeClass,
			description: "The two sections of one document are swapped with no textual change to either heading. The symbol SET is unchanged and only source order and line numbers move, which is the sharpest test of the canonical-ordering discipline: a snapshot whose row order followed parse order rather than a canonical key would diverge here between the incremental and full passes.",
			apply: func(f *fixture) {
				f.Write("book/guide.md", `## Beta

more

# Alpha

text
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("book.Alpha", "book.Beta"),
					g.requireLine("book.Beta", 1),
					g.requireLine("book.Alpha", 5),
				)
			},
		},
		{
			id:          "markdown_reparse_identical_bytes",
			kind:        kindChangeClass,
			description: "An indexed document is rewritten with BYTE-IDENTICAL content. This is the direct same-bytes-same-AST row: the drift scanner sees an empty drift set and Reconcile short-circuits (engine/watch/service.go), so the incremental graph must equal the full graph over unchanged bytes. Its witness pins the surviving symbol set rather than a new symbol, because the change is deliberately semantics-preserving — the row's force comes from the runner's two independent full passes, which is where the determinism claim is actually asserted.",
			apply: func(f *fixture) {
				f.Write("book/guide.md", `# Alpha

text

## Beta

more
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("book.Alpha", "book.Beta"),
					g.requirePresent("book.Preface"),
					requireFileNode(g, "book/guide.md"),
				)
			},
		},
	}
}

// TestMarkdownParityDeterminism_ByteStable is the SW-199 Markdown gate. One
// subtest per (backend, profile, row): two independent full passes over the
// identical bytes serialize identically (parse determinism), the incremental
// watcher-driven graph equals the full graph (intra-file parity), and the row's
// non-vacuity witness holds.
func TestMarkdownParityDeterminism_ByteStable(t *testing.T) {
	base := markdownBaseTree()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range markdownParityTable() {
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
