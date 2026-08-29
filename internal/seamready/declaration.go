package seamready

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// WHY gopkg.in/yaml.v3 IS USED HERE: the same standing internal/parity
// records. internal/coverage declines a general YAML dependency because THE
// SHIPPED BINARY stays lean and CGo-free; this package is linked by nothing
// that ships (cionly_test.go), so the import cannot fatten the artifact, and
// yaml.v3 is already a first-party dependency for exactly this job.

// Declaration mirrors docs/rc/seam-readiness.yaml: the observation threshold
// and, per operation, the named artifacts for the criteria the tool cannot
// observe itself.
type Declaration struct {
	Schema string `yaml:"schema"`
	// K is nil while owner decision 1 in stories/SW-238/preconditions.md is
	// not taken. It is a pointer so `k: null` and `k: 0` are different facts
	// (and 0 is rejected: a threshold of zero observations is the vacuous
	// coverage precondition (a) exists to refuse).
	K          *int                   `yaml:"k"`
	Operations []OperationDeclaration `yaml:"operations"`
}

// OperationDeclaration is one operation's artifacts, keyed by criterion id.
type OperationDeclaration struct {
	Operation string              `yaml:"operation"`
	Criteria  map[string]Artifact `yaml:"criteria"`
}

// Artifact is what a declaration may say about one criterion. The fields are
// a union across criteria; which ones a criterion reads is fixed in
// seamready.go, and an artifact carrying nothing reads UNKNOWN.
type Artifact struct {
	// ReleaseTags (c1): tags at which the operation shipped in shadow/active.
	ReleaseTags []string `yaml:"release_tags,omitempty"`
	// ExplainedMismatches (c2): recorded mismatches with a stated cause. Each
	// needs a positive count and a reason; the tool subtracts the counts.
	ExplainedMismatches []ExplainedMismatch `yaml:"explained_mismatches,omitempty"`
	// Tests (c3, c5): test functions that must exist in the tree.
	Tests []TestSymbol `yaml:"tests,omitempty"`
	// Workflow, RunID, SHA (c4, c6): the CI leg's last green run and the
	// commit it ran at.
	Workflow string `yaml:"workflow,omitempty"`
	RunID    string `yaml:"run_id,omitempty"`
	SHA      string `yaml:"sha,omitempty"`
}

// ExplainedMismatch is one recorded divergence with its cause.
type ExplainedMismatch struct {
	Kind   string `yaml:"kind"`
	Count  int    `yaml:"count"`
	Reason string `yaml:"reason"`
}

// TestSymbol names a top-level test function in a Go file, path relative to
// the repository root.
type TestSymbol struct {
	File   string `yaml:"file"`
	Symbol string `yaml:"symbol"`
}

// ParseDeclaration decodes and validates a declaration, failing closed: an
// unknown field anywhere, an unknown criterion id, a schema other than
// SchemaV1, a non-positive K, a duplicate or empty operation, an explained
// mismatch without a positive count and a reason, or a test symbol without both
// file and symbol is an error, never a row that quietly reads PASS.
func ParseDeclaration(raw []byte) (Declaration, error) {
	var d Declaration
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return Declaration{}, fmt.Errorf("seamready: parse declaration: %w", err)
	}
	if d.Schema != SchemaV1 {
		return Declaration{}, fmt.Errorf("seamready: declaration schema is %q, want %q", d.Schema, SchemaV1)
	}
	if d.K != nil && *d.K <= 0 {
		return Declaration{}, fmt.Errorf("seamready: k must be a positive observation count or null, got %d", *d.K)
	}
	seen := map[string]bool{}
	for i, o := range d.Operations {
		if o.Operation == "" {
			return Declaration{}, fmt.Errorf("seamready: operations[%d] has no operation id", i)
		}
		if seen[o.Operation] {
			return Declaration{}, fmt.Errorf("seamready: operation %q is declared twice", o.Operation)
		}
		seen[o.Operation] = true
		for id, art := range o.Criteria {
			if _, ok := criterionIndex[id]; !ok {
				return Declaration{}, fmt.Errorf("seamready: %s declares unknown criterion id %q (want c1..c6)", o.Operation, id)
			}
			for j, m := range art.ExplainedMismatches {
				if m.Count <= 0 {
					return Declaration{}, fmt.Errorf("seamready: %s %s explained_mismatches[%d] needs a positive count", o.Operation, id, j)
				}
				if m.Reason == "" {
					return Declaration{}, fmt.Errorf("seamready: %s %s explained_mismatches[%d] has no reason — an unexplained mismatch is a FAIL, not an omission", o.Operation, id, j)
				}
			}
			for j, ts := range art.Tests {
				if ts.File == "" || ts.Symbol == "" {
					return Declaration{}, fmt.Errorf("seamready: %s %s tests[%d] needs both file and symbol", o.Operation, id, j)
				}
			}
		}
	}
	return d, nil
}
