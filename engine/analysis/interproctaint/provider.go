package interproctaint

import "github.com/samibel/graphi/engine/analysis/taint"

// SolvedProvider is the real taint.SummaryProvider that replaces the conservative
// NoOpSummaryProvider once the interprocedural fixpoint is solved. It answers a
// callee's taint transfer at a call site from the solved per-procedure summaries:
// labels the procedure sanitizes are removed, everything else passes through.
//
// It is keyed by procedure qualified name (the key the taint analyzer probes via
// node.QualifiedName()). When two distinct procedures share a qualified name with
// conflicting sanitizer effects, the provider conservatively treats the name as a
// pass-through (no kill) so it can never introduce a false-negative "no flow".
type SolvedProvider struct {
	known   map[string]struct{}
	killAll map[string]bool
	kills   map[string][]string
}

var _ taint.SummaryProvider = (*SolvedProvider)(nil)

// NewSolvedProvider builds a provider from a solved (or loaded) Solution.
func NewSolvedProvider(sol Solution) *SolvedProvider {
	p := &SolvedProvider{
		known:   make(map[string]struct{}),
		killAll: make(map[string]bool),
		kills:   make(map[string][]string),
	}
	// Track name conflicts so colliding names degrade to pass-through (sound).
	conflict := make(map[string]struct{})
	type effect struct {
		killAll bool
		kills   string // canonical join, for conflict detection
	}
	seen := make(map[string]effect)
	for _, s := range sol.Summaries {
		name := s.ProcName
		if name == "" {
			continue
		}
		p.known[name] = struct{}{}
		eff := effect{killAll: s.KillAll, kills: joinLabels(s.KillLabels)}
		if prev, ok := seen[name]; ok {
			if prev != eff {
				conflict[name] = struct{}{}
			}
			continue
		}
		seen[name] = eff
		if s.KillAll {
			p.killAll[name] = true
		}
		if len(s.KillLabels) > 0 {
			p.kills[name] = append([]string(nil), s.KillLabels...)
		}
	}
	for name := range conflict {
		// Degrade to pass-through: keep it known, drop the kill effect.
		delete(p.killAll, name)
		delete(p.kills, name)
	}
	return p
}

// HasSummary reports whether a solved summary exists for the procedure name.
func (p *SolvedProvider) HasSummary(procID string) bool {
	if p == nil {
		return false
	}
	_, ok := p.known[procID]
	return ok
}

// TransferLabels applies the procedure's solved transfer to the input labels:
// a universal sanitizer drops everything; a label-specific sanitizer removes its
// labels; otherwise the labels pass through unchanged.
func (p *SolvedProvider) TransferLabels(procID string, input taint.LabelSet) taint.LabelSet {
	if p == nil {
		return input
	}
	if p.killAll[procID] {
		return nil
	}
	if kills, ok := p.kills[procID]; ok && len(kills) > 0 {
		return input.Remove(kills)
	}
	return input
}
