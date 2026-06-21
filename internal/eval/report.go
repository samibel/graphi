package eval

// EvalName is the distinct named CI check emitted by this harness.
const EvalName = "token-parity-eval"

// CaseResult is the per-case measurement.
type CaseResult struct {
	ID             string  `json:"id"`
	Capability     string  `json:"capability"`
	GraphiTokens   int     `json:"graphi_tokens"`
	BaselineTokens int     `json:"baseline_tokens"`
	Ratio          float64 `json:"ratio"` // baseline/graphi (higher = more compression)
}

// Report is the version-stamped, machine-readable eval report.
type Report struct {
	Name            string            `json:"name"`
	Pass            bool              `json:"pass"` // overall gate (coverage + drift +, in claim mode, claim)
	MethodVersion   string            `json:"method_version"`
	DatasetVersion  string            `json:"dataset_version"`
	AggregateRatio  float64           `json:"aggregate_ratio"` // sum(baseline)/sum(graphi)
	ClaimThreshold  float64           `json:"claim_threshold"`
	ClaimSupported  bool              `json:"claim_supported"` // aggregate >= threshold
	ClaimHeldBack   bool              `json:"claim_held_back"`
	ClaimMode       bool              `json:"claim_mode"`
	Cases           []CaseResult      `json:"cases"`
	Coverage        map[string][]string `json:"coverage"`   // capability -> case ids
	Uncovered       []string          `json:"uncovered,omitempty"`
	CoverageDrift   *DriftResult      `json:"coverage_drift,omitempty"`
	Violations      []string          `json:"violations,omitempty"`
}

// DefaultClaimThreshold is the default "~50×" threshold gating the public claim.
const DefaultClaimThreshold = 50.0

// Run measures the dataset and produces the version-stamped report. It computes
// per-case and aggregate ratios, builds the coverage matrix, checks for uncovered
// capabilities, and (when claimMode) gates the claim against the threshold.
func Run(ds *Dataset, claimMode bool, claimThreshold float64) (Report, error) {
	if err := AssertBaselineVersion(FixtureBaselineVersion); err != nil {
		return Report{}, err
	}
	if claimThreshold <= 0 {
		claimThreshold = DefaultClaimThreshold
	}
	rep := Report{
		Name:           EvalName,
		MethodVersion:  BaselineMethodVersion,
		DatasetVersion: ds.Version,
		ClaimThreshold: claimThreshold,
		ClaimMode:      claimMode,
		Pass:           true,
		Coverage:       map[string][]string{},
	}

	var sumGraphi, sumBaseline int
	cases := make([]CaseResult, 0, len(ds.Cases))
	for _, c := range ds.Cases {
		g := CountTokens(c.GraphiContext)
		b := CountTokens(c.BaselineContext)
		ratio := 0.0
		if g > 0 {
			ratio = float64(b) / float64(g)
		}
		cases = append(cases, CaseResult{ID: c.ID, Capability: c.Capability, GraphiTokens: g, BaselineTokens: b, Ratio: ratio})
		sumGraphi += g
		sumBaseline += b
		rep.Coverage[c.Capability] = append(rep.Coverage[c.Capability], c.ID)
	}
	rep.Cases = cases
	if sumGraphi > 0 {
		rep.AggregateRatio = float64(sumBaseline) / float64(sumGraphi)
	}
	rep.ClaimSupported = rep.AggregateRatio >= claimThreshold
	rep.ClaimHeldBack = !rep.ClaimSupported

	// Coverage enforcement: every declared capability must have >=1 case.
	for _, cap := range Capabilities() {
		if len(rep.Coverage[cap]) == 0 {
			rep.Uncovered = append(rep.Uncovered, cap)
		}
	}
	if len(rep.Uncovered) > 0 {
		rep.Pass = false
		rep.Violations = append(rep.Violations, "coverage: capabilities with zero eval cases: "+joinComma(rep.Uncovered))
	}

	// Coverage drift gate vs committed baseline.
	baseline, err := LoadCoverageBaseline()
	if err != nil {
		return Report{}, err
	}
	current := coveredCapabilities(ds)
	drift := checkDriftPointer(current, baseline.Capabilities)
	rep.CoverageDrift = &drift
	if drift.Regressed {
		rep.Pass = false
		rep.Violations = append(rep.Violations, "coverage drift: lost capabilities: "+joinComma(drift.Lost))
	}

	// Claim gate (only enforced in claim-validation mode).
	if claimMode && rep.ClaimHeldBack {
		rep.Pass = false
		rep.Violations = append(rep.Violations, "claim held back: aggregate ratio below threshold (resolving OQ4 with evidence)")
	}

	return rep, nil
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
