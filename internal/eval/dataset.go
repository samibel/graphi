package eval

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/samibel/graphi/engine/query"
)

// Capabilities returns the canonical capability set, derived mechanically from
// the MCP tool registry (query.Operations) plus the CLI/MCP search command. This
// is the single source the coverage matrix checks against — it cannot drift from
// the declared surface.
func Capabilities() []string {
	caps := append([]string(nil), query.Operations...)
	caps = append(caps, "search")
	sort.Strings(caps)
	return caps
}

// Case is one labeled eval entry: a query, the winnowed graphi context, and the
// whole-file-read baseline the claim is measured against.
type Case struct {
	ID              string `json:"id"`
	Capability      string `json:"capability"`
	Query           string `json:"query"`
	GraphiContext   string `json:"graphi_context"`
	BaselineContext string `json:"baseline_context"`
}

// Dataset is the frozen, version-stamped labeled eval set.
type Dataset struct {
	Version     string `json:"version"`
	Description string `json:"description"`
	Cases       []Case `json:"cases"`
}

// LoadDataset loads the embedded frozen dataset.
func LoadDataset() (*Dataset, error) {
	var ds Dataset
	if err := json.Unmarshal(embeddedCases, &ds); err != nil {
		return nil, fmt.Errorf("eval: parse embedded dataset: %w", err)
	}
	if ds.Version == "" {
		return nil, fmt.Errorf("eval: dataset missing version stamp")
	}
	if err := ds.Validate(); err != nil {
		return nil, err
	}
	return &ds, nil
}

// Validate checks the dataset is well-formed: non-empty, every case has id +
// capability + both contexts, and every capability is a declared capability.
func (d *Dataset) Validate() error {
	if len(d.Cases) == 0 {
		return fmt.Errorf("eval: dataset has no cases")
	}
	declared := map[string]bool{}
	for _, c := range Capabilities() {
		declared[c] = true
	}
	seen := map[string]bool{}
	for i, c := range d.Cases {
		if c.ID == "" {
			return fmt.Errorf("eval: case %d missing id", i)
		}
		if seen[c.ID] {
			return fmt.Errorf("eval: duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Capability == "" {
			return fmt.Errorf("eval: case %q missing capability", c.ID)
		}
		if !declared[c.Capability] {
			return fmt.Errorf("eval: case %q uses undeclared capability %q", c.ID, c.Capability)
		}
		if c.GraphiContext == "" || c.BaselineContext == "" {
			return fmt.Errorf("eval: case %q missing context", c.ID)
		}
	}
	return nil
}
