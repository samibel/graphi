package bench

import (
	"fmt"
	"os"
	"sort"
)

// Comparator is the comparison a metric must satisfy against its budget. All
// benchmark metrics use CmpLE: the measured value must not exceed the budget.
type Comparator string

const CmpLE Comparator = "<="

// MetricBudget is a single pinned metric definition in bench-budget.yml.
type MetricBudget struct {
	Baseline int64      // pinned reference value (delta = measured - baseline)
	Budget   int64      // fail threshold (measured must not exceed for CmpLE)
	Unit     string     // "ms" or "bytes"
	Op       Comparator // comparator, defaults to CmpLE
}

// Manifest is the parsed, validated bench-budget.yml.
type Manifest struct {
	Version         int
	BaselineVersion string
	FixtureDigest   string
	Metrics         map[string]MetricBudget
}

// LoadManifest reads and schema-validates the bench-budget manifest at path.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bench: read manifest %s: %w", path, err)
	}
	root, err := parseTinyYAML(data)
	if err != nil {
		return nil, err
	}
	m := &Manifest{Metrics: map[string]MetricBudget{}}
	if v, ok := root["version"]; ok {
		iv, ok := v.(int64)
		if !ok {
			return nil, fmt.Errorf("bench: 'version' must be an integer, got %T", v)
		}
		m.Version = int(iv)
	}
	if v, ok := root["baseline_version"]; ok {
		m.BaselineVersion, _ = v.(string)
	}
	if m.BaselineVersion == "" {
		return nil, fmt.Errorf("bench: manifest missing 'baseline_version' (required for version-stamped baselines)")
	}
	if v, ok := root["fixture_digest"]; ok {
		m.FixtureDigest, _ = v.(string)
	}
	metricsNode, ok := root["metrics"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("bench: manifest missing 'metrics' mapping")
	}
	for name, raw := range metricsNode {
		mbNode, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("bench: metric %q must be a mapping", name)
		}
		mb, err := parseMetricBudget(mbNode)
		if err != nil {
			return nil, fmt.Errorf("bench: metric %q: %w", name, err)
		}
		m.Metrics[name] = mb
	}
	return m, nil
}

func parseMetricBudget(node map[string]any) (MetricBudget, error) {
	getInt := func(k string) (int64, bool, error) {
		v, ok := node[k]
		if !ok {
			return 0, false, nil
		}
		i, ok := v.(int64)
		if !ok {
			return 0, true, fmt.Errorf("'%s' must be an integer, got %T", k, v)
		}
		return i, true, nil
	}
	mb := MetricBudget{Op: CmpLE}
	b, ok, err := getInt("baseline")
	if err != nil {
		return mb, err
	}
	if !ok {
		return mb, fmt.Errorf("missing 'baseline'")
	}
	mb.Baseline = b
	bud, ok, err := getInt("budget")
	if err != nil {
		return mb, err
	}
	if !ok {
		return mb, fmt.Errorf("missing 'budget'")
	}
	mb.Budget = bud
	if u, ok := node["unit"]; ok {
		mb.Unit, _ = u.(string)
	}
	if op, ok := node["op"]; ok {
		mb.Op = Comparator(op.(string))
	}
	if mb.Op == "" {
		mb.Op = CmpLE
	}
	if mb.Op != CmpLE {
		return mb, fmt.Errorf("unsupported comparator %q (only '<=' is supported)", mb.Op)
	}
	return mb, nil
}

// MetricResult is the per-metric gate outcome.
type MetricResult struct {
	Name     string     `json:"name"`
	Measured float64    `json:"measured"`
	Baseline float64    `json:"baseline"`
	Budget   float64    `json:"budget"`
	Delta    float64    `json:"delta"` // measured - baseline
	Unit     string     `json:"unit"`
	Op       Comparator `json:"op"`
	Pass     bool       `json:"pass"`
}

// GateReport is the full budget-gate outcome.
type GateReport struct {
	Pass    bool           `json:"pass"`
	Results []MetricResult `json:"results"`
	Failed  []string       `json:"failed,omitempty"`
}

// Gate compares measured metrics against the manifest budgets. A metric fails
// when it exceeds its budget (CmpLE); the report names every failing metric and
// its delta versus the pinned baseline. Metrics present in the measurement but
// absent from the manifest are ignored; metrics in the manifest but absent from
// the measurement cause a gate failure (unmeasured budgeted metric).
func Gate(measured map[string]float64, man *Manifest) GateReport {
	rep := GateReport{Pass: true}
	// Deterministic order: sort metric names.
	names := make([]string, 0, len(man.Metrics))
	for n := range man.Metrics {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		mb := man.Metrics[name]
		val, ok := measured[name]
		if !ok {
			rep.Pass = false
			rep.Failed = append(rep.Failed, name)
			rep.Results = append(rep.Results, MetricResult{
				Name: name, Baseline: float64(mb.Baseline),
				Budget: float64(mb.Budget), Unit: mb.Unit, Op: mb.Op,
				Pass: false,
			})
			continue
		}
		pass := val <= float64(mb.Budget)
		if !pass {
			rep.Pass = false
			rep.Failed = append(rep.Failed, name)
		}
		rep.Results = append(rep.Results, MetricResult{
			Name: name, Measured: val, Baseline: float64(mb.Baseline),
			Budget: float64(mb.Budget), Delta: val - float64(mb.Baseline),
			Unit: mb.Unit, Op: mb.Op, Pass: pass,
		})
	}
	return rep
}

// FormatFailure renders a human-readable, metric-naming failure message for a
// failing gate (AC: over-budget regression names the metric and its delta).
func (r GateReport) FormatFailure() string {
	if r.Pass {
		return ""
	}
	var b []byte
	for _, res := range r.Results {
		if res.Pass {
			continue
		}
		if res.Measured == 0 && res.Delta == 0 {
			// unmeasured budgeted metric
			b = append(b, []byte(fmt.Sprintf("  - %s: UNMEASURED (budgeted but not reported)\n", res.Name))...)
			continue
		}
		b = append(b, []byte(fmt.Sprintf(
			"  - %s: measured %.2f %s > budget %.2f (delta vs baseline %+.2f)\n",
			res.Name, res.Measured, res.Unit, res.Budget, res.Delta,
		))...)
	}
	return fmt.Sprintf("benchmark gate FAILED — over-budget metrics:\n%s", b)
}
