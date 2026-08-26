package parity

import "strings"

// ---------------------------------------------------------------------------
// W5.n slice (b) — HCL.
//
// SYMBOL SHAPE per docs/rc/parity-classes-hcl.yaml: a top-level block's TYPE
// and LABELS carry the identity (`resource "aws_s3_bucket" "logs"`), so the
// rename-shaped class renames a LABEL rather than a body attribute.
// ---------------------------------------------------------------------------

func parseDetHCL() ParseDetLanguage {
	sh := ParseDetShape{
		Ext: ".tf",
		NewFile: func(seed string) []byte {
			return []byte("variable \"" + seed + "\" {\n  type    = string\n  default = \"x\"\n}\n")
		},
		Append: func(src []byte, seed string) ([]byte, error) {
			add := "\nvariable \"" + seed + "_added\" {\n  type    = string\n  default = \"y\"\n}\n"
			return append(append([]byte(nil), src...), add...), nil
		},
		Rename:  hclRename,
		Reorder: hclReorder,
	}
	return ParseDetLanguage{
		Name:           "hcl",
		Exts:           []string{".tf", ".hcl", ".tfvars"},
		MinSymbolKinds: []string{"type", "variable", "function"},
		Specs: parseDetStandardSpecs("hcl", "hcl_rename_block_label", "hcl_reorder_blocks", sh,
			"The LAST label of a top-level block is renamed in place. The block's type and labels carry "+
				"the symbol identity, so the rename is a delete-plus-add inside one file.",
			"The top-level blocks of one file are permuted with no textual change to any block body."),
	}
}

// hclRename renames the LAST quoted label of the LAST top-level block.
//
// It refuses a block with no quoted label (errNoTarget) rather than inventing
// one: a `terraform { }` block has no label, and adding one would change the
// block's meaning instead of its name.
func hclRename(src []byte, seed string) ([]byte, error) {
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
		head := b[:open]
		last := strings.LastIndex(head, "\"")
		if last <= 0 {
			continue
		}
		first := strings.LastIndex(head[:last], "\"")
		if first < 0 {
			continue
		}
		blocks[i] = head[:first+1] + seed + "_renamed" + head[last:] + b[open:]
		return []byte(strings.Join(blocks, "")), nil
	}
	return nil, errNoTarget
}

// hclReorder rotates the top-level blocks. HCL is order-independent by
// specification, so unlike CSS there is no preamble that must stay first.
func hclReorder(src []byte) ([]byte, error) {
	blocks, err := splitTopLevelBraceBlocks(src)
	if err != nil {
		return nil, err
	}
	var lead string
	var real []string
	for _, b := range blocks {
		if strings.TrimSpace(b) == "" || !strings.Contains(b, "{") {
			if len(real) == 0 {
				lead += b
				continue
			}
			real[len(real)-1] += b
			continue
		}
		real = append(real, b)
	}
	rotated := rotateSections(real)
	if rotated == nil {
		return nil, errNoTarget
	}
	return []byte(lead + strings.Join(rotated, "")), nil
}
