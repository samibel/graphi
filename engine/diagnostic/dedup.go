package diagnostic

import (
	"sort"

	"github.com/samibel/graphi/core/model"
)

// dedupKey is the exact tuple that identifies duplicate diagnostics.
// Components: code, file, line, target symbol, reason, suppression category.
// Suppression is included so that a C2-aggregated representative and its
// suppressed members are not re-collapsed by C3 dedup.
type dedupKey struct {
	Code         string
	File         string
	Line         int
	TargetSymbol model.NodeId
	Reason       string
	Suppression  SuppressionCategory
}

// dedupStage returns a stage that collapses diagnostics sharing the exact dedup
// key. The surviving representative is the canonical-comparator-smallest member
// of the group, and its Actions slice is the deterministic union of all group
// members' actions. OccurrenceCount is set to the group size. Findings that
// were already aggregated by C2 keep their C2 aggregate count unchanged.
func dedupStage() filterStage {
	return func(diags []Diagnostic, t *tally) []Diagnostic {
		groups := map[dedupKey][]Diagnostic{}
		for _, d := range diags {
			key := dedupKeyOf(d)
			groups[key] = append(groups[key], d)
		}

		out := make([]Diagnostic, 0, len(groups))
		for _, group := range groups {
			if len(group) == 1 {
				out = append(out, group[0])
				continue
			}
			// Deterministic representative: canonical-comparator-smallest.
			sortDiagnostics(group)
			rep := group[0]
			rep.Actions = unionActions(group)
			// Only count what this stage actually collapsed. If a C2-aggregated
			// finding (OccurrenceCount > 1 from C2) enters as a single unit, do
			// not re-count its members here.
			if rep.OccurrenceCount == 1 {
				rep.OccurrenceCount = len(group)
			}
			t.DedupCollapsed += len(group) - 1
			out = append(out, rep)
		}
		return out
	}
}

// dedupKeyOf builds the exact key for a diagnostic. Reason is proxied by the
// diagnostic code until SW-123 formalizes reason codes.
func dedupKeyOf(d Diagnostic) dedupKey {
	return dedupKey{
		Code:         d.Code,
		File:         d.File,
		Line:         d.Line,
		TargetSymbol: d.TargetSymbol,
		Reason:       string(d.Reason),
		Suppression:  d.Suppression,
	}
}

// unionActions returns the deterministic sorted union of all actions across the
// group, preserving a safe_delete_symbol if any member carries it.
func unionActions(group []Diagnostic) []CodeAction {
	seen := map[string]CodeAction{}
	for _, d := range group {
		for _, a := range d.Actions {
			key := string(a.Kind) + ":" + string(a.TargetSymbol)
			seen[key] = a
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]CodeAction, 0, len(seen))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}
