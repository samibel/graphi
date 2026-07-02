package review

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
)

// Gate verdicts — a stable, enumerated vocabulary so SW-043 can switch on them.
const (
	// VerdictPass: gating is disabled, or the worst region risk is at/below the
	// configured threshold. The merge is NOT blocked.
	VerdictPass = "PASS"
	// VerdictBlock: gating is enabled and the worst region risk EXCEEDS the
	// configured threshold. The merge is blocked; Reason links the evidence.
	VerdictBlock = "BLOCK"
)

// scoreScale is the fixed-point denominator the sibling risk scorer renders with
// (units of 1/scoreScale). The gate threshold is expressed in the SAME units so
// the comparison is integer (no float drift), mirroring SW-041's parseFixed.
const scoreScale = 1000

// GateConfig is the DOCUMENTED merge-gate model. It is hashed (with the render
// config) into the PublishResult ConfigHash so any change is auditable.
type GateConfig struct {
	// Enabled turns the gate on. When false, Evaluate always returns PASS — the
	// gate is OPT-IN (the story's "optional merge gate").
	Enabled bool `json:"enabled"`
	// BlockThreshold is the risk score, in fixed-point units (1/scoreScale, the
	// same scale RiskRecord.Score renders in), that the worst region must EXCEED
	// (strict, exclusive `>`) for the gate to BLOCK. A region scoring exactly AT
	// the threshold PASSES — the boundary is inclusive of pass.
	BlockThreshold int `json:"block_threshold"`
}

// DefaultGateConfig is gating DISABLED — the safe default for an "optional" gate.
// A threshold of 700 ("0.700") is provided so a caller that only flips Enabled
// gets a sensible high-risk cutoff (an impact+taint region trips it; a plain
// impact floor does not).
func DefaultGateConfig() GateConfig {
	return GateConfig{Enabled: false, BlockThreshold: 700}
}

// hash returns the deterministic fingerprint of the gate model.
func (c GateConfig) hash() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// GateEvidence is one piece of evidence linking a BLOCK decision to the specific
// offending region: its identity, its verbatim risk score, and the signal kinds
// detected on it (copied verbatim from the consumed reports — never re-derived).
type GateEvidence struct {
	Region    model.NodeId `json:"region"`            // EP-001 identity of the offending region
	RiskScore string       `json:"risk_score"`        // verbatim fixed-point risk score
	Signals   []string     `json:"signals,omitempty"` // SW-040 signal kinds on the region
}

// GateDecision is the merge-gate result: the verdict, a human-readable reason
// (evidence-linked on BLOCK), and the structured evidence. On PASS the Evidence
// slice is empty and Reason explains why (disabled / at-or-below threshold).
type GateDecision struct {
	Verdict   string         `json:"verdict"`   // PASS | BLOCK
	Reason    string         `json:"reason"`    // human-readable, evidence-linked on BLOCK
	Threshold string         `json:"threshold"` // rendered fixed-point threshold (for audit)
	Enabled   bool           `json:"enabled"`   // whether gating was enabled
	Evidence  []GateEvidence `json:"evidence"`  // offending regions (empty on PASS)
}

// Evaluate runs the optional merge gate over the consumed bundle. It NEVER calls
// the network and performs no I/O — pure evaluation over the consumed reports
// (AC3 / Security S4).
//
// Logic (AC2):
//   - gating disabled                     -> PASS (reason: disabled)
//   - worst region score <= threshold     -> PASS (boundary is inclusive of pass)
//   - worst region score >  threshold     -> BLOCK, Reason links every region
//     whose score exceeds the threshold (region + verbatim score + signal kinds)
func Evaluate(cfg GateConfig, bundle FindingsBundle) GateDecision {
	// Clamp a nonsensical negative threshold to 0 so the compared value and the
	// rendered audit string never disagree (renderFixed also clamps to "0.000").
	if cfg.BlockThreshold < 0 {
		cfg.BlockThreshold = 0
	}
	thresholdStr := renderFixed(cfg.BlockThreshold)

	if !cfg.Enabled {
		return GateDecision{
			Verdict:   VerdictPass,
			Reason:    "merge gate disabled",
			Threshold: thresholdStr,
			Enabled:   false,
			Evidence:  []GateEvidence{},
		}
	}

	// Index signal kinds by region so BLOCK evidence can name the offending
	// signals verbatim (consumed, never recomputed).
	sigByRegion := indexSignalKinds(bundle.Signals)

	// Collect every region whose risk score EXCEEDS the threshold (strict). The
	// gate blocks on the worst region; it lists all offenders as evidence.
	var offenders []GateEvidence
	worst := -1
	for _, rec := range bundle.Risk.Regions {
		if rec.Degraded || rec.Region == "" {
			continue
		}
		units, ok := parseFixed(rec.Score)
		if !ok {
			continue
		}
		if units > worst {
			worst = units
		}
		if units > cfg.BlockThreshold {
			offenders = append(offenders, GateEvidence{
				Region:    rec.Region,
				RiskScore: rec.Score,
				Signals:   sigByRegion[rec.Region],
			})
		}
	}

	if len(offenders) == 0 {
		reason := fmt.Sprintf("worst region risk at or below threshold %s", thresholdStr)
		if worst < 0 {
			reason = "no scored regions"
		}
		return GateDecision{
			Verdict:   VerdictPass,
			Reason:    reason,
			Threshold: thresholdStr,
			Enabled:   true,
			Evidence:  []GateEvidence{},
		}
	}

	// Deterministic evidence order: by region NodeId.
	sort.Slice(offenders, func(i, j int) bool { return offenders[i].Region < offenders[j].Region })

	var reason strings.Builder
	reason.WriteString(fmt.Sprintf("merge blocked: %d region(s) exceed the risk threshold %s", len(offenders), thresholdStr))
	return GateDecision{
		Verdict:   VerdictBlock,
		Reason:    reason.String(),
		Threshold: thresholdStr,
		Enabled:   true,
		Evidence:  offenders,
	}
}

// gateEvidenceLine renders one GateEvidence as a compact bullet for the body.
func gateEvidenceLine(ev GateEvidence) string {
	s := fmt.Sprintf("`%s` — risk %s", string(ev.Region), ev.RiskScore)
	if len(ev.Signals) > 0 {
		s += " (signals: " + strings.Join(ev.Signals, ", ") + ")"
	}
	return s
}

// indexSignalKinds builds a region -> sorted, deduplicated signal-kind list over
// the consumed SignalReport, skipping degraded records.
func indexSignalKinds(rep analysis.SignalReport) map[model.NodeId][]string {
	out := map[model.NodeId][]string{}
	for _, rec := range rep.Regions {
		if rec.Degraded || rec.Region == "" {
			continue
		}
		seen := map[string]struct{}{}
		var kinds []string
		for _, s := range rec.Signals {
			if _, ok := seen[s.Kind]; ok {
				continue
			}
			seen[s.Kind] = struct{}{}
			kinds = append(kinds, s.Kind)
		}
		sort.Strings(kinds)
		if len(kinds) > 0 {
			out[rec.Region] = kinds
		}
	}
	return out
}

// renderFixed renders integer fixed-point units (1/scoreScale) as a canonical
// 3-decimal string (e.g. 700 -> "0.700"). Byte-stable; mirrors analysis.renderFixed
// so the gate threshold prints in the same shape as the risk scores it compares.
func renderFixed(v int) string {
	if v < 0 {
		v = 0
	}
	whole := v / scoreScale
	frac := v % scoreScale
	return strconv.Itoa(whole) + "." + fmt.Sprintf("%03d", frac)
}

// parseFixed is the pure inverse of renderFixed: it parses a canonical
// fixed-point decimal string (e.g. "0.730") back to integer units (730).
// String-based (not float) so the threshold comparison is drift-free, mirroring
// analysis.parseFixed (SW-041). Returns ok=false for a malformed value.
func parseFixed(s string) (int, bool) {
	s = strings.TrimSpace(s)
	// Scores/thresholds are strictly non-negative. Reject any signed value up
	// front: strconv.Atoi("-0") returns 0, so "-0.123" would otherwise slip past
	// the per-part whole<0 checks and be accepted as +0.123.
	if s == "" || strings.HasPrefix(s, "-") {
		return 0, false
	}
	dot := strings.IndexByte(s, '.')
	if dot < 0 {
		whole, err := strconv.Atoi(s)
		if err != nil || whole < 0 {
			return 0, false
		}
		return whole * scoreScale, true
	}
	wholePart := s[:dot]
	fracPart := s[dot+1:]
	if wholePart == "" || len(fracPart) != 3 {
		return 0, false
	}
	whole, err := strconv.Atoi(wholePart)
	if err != nil || whole < 0 {
		return 0, false
	}
	frac, err := strconv.Atoi(fracPart)
	if err != nil || frac < 0 || frac >= scoreScale {
		return 0, false
	}
	return whole*scoreScale + frac, true
}
