package conformance_test

// SW-199 (W5.m, intra/parse residual, hcl slice b): the HCL parse-determinism
// and intra-file parity gate, bound to docs/rc/parity-classes-hcl.yaml by the
// drift guard in hclparity_matrix_test.go.
//
// LEVEL, AND WHAT THAT MEANS FOR THE WITNESS SHAPE. HCL is `intra-file-only`.
// The rows prove PARSE DETERMINISM — the same input bytes produce the same AST
// and its serialization is byte-stable — plus intra-file full-vs-incremental
// parity on both stores and both profile axes. Every cross-file Go class is
// dispositioned `not_applicable` with a language-spec reason in the YAML's
// `go_class_applicability:` block. The shared runner
// (intrafile_shared_test.go) states the three assertions it makes.
//
// THE LANGUAGE-SPEC POINT, because it is the load-bearing one. The HCL
// specification (hashicorp/hcl, the HCL Native Syntax and Structure specs)
// defines blocks, attributes and expressions — and NO cross-file construct at
// all. Terraform's `module { source = ... }` is a SCHEMA a host application
// layers on top of HCL, not an HCL construct: HCL hands the host an
// unevaluated body and the host decides what a label means. So HCL's own
// answer to "does the language define a cross-file reference?" is no, and that
// is the spec-level abstention this family records. Citing graphi's HCL parser
// comment instead would be the LANGHONEST-001 circular-abstention defect.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures. NOT the real-repository matrix (G4), NOT a correctness proof of the
// HCL grammar — correctness evidence lives in core/parse/parser_hcl_test.go.

import (
	"testing"
)

// hclBaseTree is the HCL fixture every row starts from: two Terraform-syntax
// files in one directory. HCL symbol QNs are `<dir>.<block-type>.<labels…>` for
// blocks and `<dir>.<name>` for top-level attributes.
func hclBaseTree() map[string]string {
	return map[string]string{
		"infra/main.tf": `variable "region" {
  default = "eu"
}

resource "aws_s3_bucket" "data" {
  bucket = "logs"
}
`,
		"infra/network.tf": `variable "cidr" {
  default = "10.0.0.0/16"
}

owner = "platform"
`,
	}
}

// hclParityTable is the declarative HCL matrix. Row order follows
// docs/rc/parity-classes-hcl.yaml so the two files diff side by side.
func hclParityTable() []changeClassRow {
	return []changeClassRow{
		{
			id:          "hcl_add_file",
			kind:        kindChangeClass,
			description: "A new HCL file arrives in a new directory: the pure add path. The witness requires the new block symbol and the base-tree control symbol, so a row that indexed nothing cannot pass.",
			apply: func(f *fixture) {
				f.Write("edge/dns.tf", "resource \"aws_route53_zone\" \"public\" {\n  name = \"example.com\"\n}\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("edge.resource.aws_route53_zone.public"),
					requireFileNode(g, "edge/dns.tf"),
					g.requirePresent("infra.variable.region"),
				)
			},
		},
		{
			id:          "hcl_modify_file",
			kind:        kindChangeClass,
			description: "An indexed HCL file is rewritten in place: a block is appended while the existing block and resource keep identity. The witness requires the new block AND both pre-existing symbols.",
			apply: func(f *fixture) {
				f.Write("infra/main.tf", `variable "region" {
  default = "eu"
}

resource "aws_s3_bucket" "data" {
  bucket = "logs"
}

output "bucket_id" {
  value = "x"
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("infra.output.bucket_id"),
					g.requirePresent("infra.variable.region"),
					g.requirePresent("infra.resource.aws_s3_bucket.data"),
				)
			},
		},
		{
			id:          "hcl_delete_file",
			kind:        kindChangeClass,
			description: "An indexed HCL file is deleted, so the per-file stale-node purge runs over it. The witness requires the deleted file's block and attribute symbols AND its file node gone, and the sibling file's symbols still present — a stale node surviving a delete is the failure this row exists to catch.",
			apply: func(f *fixture) {
				f.Remove("infra/network.tf")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("infra.variable.cidr", "infra.owner"),
					requireNoFileNode(g, "infra/network.tf"),
					g.requirePresent("infra.variable.region", "infra.resource.aws_s3_bucket.data"),
				)
			},
		},
		{
			id:          "hcl_rename_block_label",
			kind:        kindChangeClass,
			description: "A block label is renamed in place (variable \"region\" -> variable \"primary_region\"). The symbol's qualified name is derived from the block type plus its labels, so the rename is a delete-plus-add inside one file. The witness requires the new name present AND the old name absent.",
			apply: func(f *fixture) {
				f.Write("infra/main.tf", `variable "primary_region" {
  default = "eu"
}

resource "aws_s3_bucket" "data" {
  bucket = "logs"
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("infra.variable.primary_region"),
					g.requireAbsent("infra.variable.region"),
					g.requirePresent("infra.resource.aws_s3_bucket.data"),
				)
			},
		},
		{
			id:          "hcl_reorder_blocks",
			kind:        kindChangeClass,
			description: "The top-level blocks of one file are permuted with no textual change to any block body. The symbol SET is unchanged and only source order and line numbers move, which is the sharpest test of the canonical-ordering discipline: a snapshot whose row order followed parse order rather than a canonical key would diverge here between the incremental and full passes.",
			apply: func(f *fixture) {
				f.Write("infra/main.tf", `resource "aws_s3_bucket" "data" {
  bucket = "logs"
}

variable "region" {
  default = "eu"
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("infra.variable.region", "infra.resource.aws_s3_bucket.data"),
					g.requireLine("infra.resource.aws_s3_bucket.data", 1),
					g.requireLine("infra.variable.region", 5),
				)
			},
		},
		{
			id:          "hcl_reparse_identical_bytes",
			kind:        kindChangeClass,
			description: "An indexed HCL file is rewritten with BYTE-IDENTICAL content. This is the direct same-bytes-same-AST row: the drift scanner sees an empty drift set and Reconcile short-circuits (engine/watch/service.go), so the incremental graph must equal the full graph over unchanged bytes. Its witness pins the surviving symbol set rather than a new symbol, because the change is deliberately semantics-preserving — the row's force comes from the runner's two independent full passes, which is where the determinism claim is actually asserted.",
			apply: func(f *fixture) {
				f.Write("infra/main.tf", `variable "region" {
  default = "eu"
}

resource "aws_s3_bucket" "data" {
  bucket = "logs"
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("infra.variable.region", "infra.resource.aws_s3_bucket.data"),
					g.requirePresent("infra.variable.cidr", "infra.owner"),
					requireFileNode(g, "infra/main.tf"),
				)
			},
		},
	}
}

// TestHclParityDeterminism_ByteStable is the SW-199 HCL gate. One subtest per
// (backend, profile, row): two independent full passes over the identical bytes
// serialize identically (parse determinism), the incremental watcher-driven
// graph equals the full graph (intra-file parity), and the row's non-vacuity
// witness holds.
func TestHclParityDeterminism_ByteStable(t *testing.T) {
	base := hclBaseTree()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range hclParityTable() {
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
