package coverage

// CAP-01 (SW-117): the machine-readable capability manifest. It is GENERATED
// from the checked matrix (docs/coverage-matrix.yaml, itself drift-checked
// against the live registries and the frozen stable set), so downstream
// consumers — the REL-01 release profile join, external tooling, docs — read
// ONE artifact instead of re-parsing the YAML or the rendered Markdown. The
// encoding is deterministic (stable field order, matrix row order, trailing
// newline), so two generations of the same matrix are byte-identical and the
// cmd/coverage -check freshness gate can compare bytes.

import (
	"encoding/json"
	"fmt"
)

// MatrixJSONPath is the repo-relative path of the generated manifest.
const MatrixJSONPath = "docs/capability-manifest.json"

// ManifestSchemaVersion versions the generated JSON shape (not the capability
// content). Bump it when fields are added/renamed so consumers can detect the
// contract change.
const ManifestSchemaVersion = 1

// manifest is the serialized shape. Capabilities keep the matrix's canonical
// row order; StableOperations restates the frozen SCOPE-01 set so a consumer
// need not re-derive it by filtering tiers.
type manifest struct {
	SchemaVersion    int                  `json:"schema_version"`
	GeneratedBy      string               `json:"generated_by"`
	StableOperations []string             `json:"stable_operations"`
	Capabilities     []manifestCapability `json:"capabilities"`
}

type manifestCapability struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Status   string `json:"status"`
	Tier     string `json:"tier"`
	Epic     string `json:"epic,omitempty"`
	Note     string `json:"note,omitempty"`
}

// RenderJSON encodes the manifest for the given matrix rows deterministically.
func RenderJSON(caps []Capability) ([]byte, error) {
	m := manifest{
		SchemaVersion:    ManifestSchemaVersion,
		GeneratedBy:      "cmd/coverage -generate (do not edit; source: " + MatrixYAMLPath + ")",
		StableOperations: CanonicalStableOps(),
		Capabilities:     make([]manifestCapability, 0, len(caps)),
	}
	for _, c := range caps {
		epic := c.Epic
		if epic == "-" {
			epic = ""
		}
		m.Capabilities = append(m.Capabilities, manifestCapability{
			ID:       c.ID,
			Category: c.Category,
			Status:   c.Status,
			Tier:     c.Tier,
			Epic:     epic,
			Note:     c.Note,
		})
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("coverage: encode manifest: %w", err)
	}
	return append(b, '\n'), nil
}
