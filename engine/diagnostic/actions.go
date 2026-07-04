package diagnostic

import "sort"

// actionGate returns a stage that attaches only safe suggested actions.
// safe_delete_symbol is attached only when all four conjuncts hold:
//  1. Confidence == Exact
//  2. Not public-API / framework entry point (reuses C2 classification:
//     not suppressed and internal name)
//  3. No live inbound references (proxy: analyzer already attached
//     safe_delete_symbol, meaning it saw no live refs)
//  4. Not suppressed (Suppression == "")
//
// Heuristic unresolved_reference findings receive no mutating action; instead
// they get an inspect_reference preview (and optionally add_suppression) so the
// agent can still investigate without risk.
func actionGate() filterStage {
	return func(diags []Diagnostic, t *tally) []Diagnostic {
		out := make([]Diagnostic, 0, len(diags))
		for _, d := range diags {
			d.Actions = actionSetFor(d)
			out = append(out, d)
		}
		return out
	}
}

// actionSetFor returns the appropriate action slice for a diagnostic after the
// safe-action gate has been applied.
func actionSetFor(d Diagnostic) []CodeAction {
	// Heuristic findings get only read-only / preview actions.
	if d.Confidence == ConfidenceHeuristic {
		return []CodeAction{
			{Kind: ActionInspectReference, TargetSymbol: d.TargetSymbol, Preview: true},
			{Kind: ActionAddSuppression, TargetSymbol: d.Symbol, Preview: true},
		}
	}

	// For exact findings, safe_delete_symbol is attached only when all conjuncts hold.
	if d.Code == "dead_symbol" && d.Confidence == ConfidenceExact &&
		d.Suppression == "" && !looksExported(nodeNameFromMessage(d.Message)) &&
		hasSafeDelete(d) {
		return []CodeAction{{Kind: ActionSafeDeleteSymbol, TargetSymbol: d.Symbol}}
	}

	// All other exact findings get a read-only inspect_reference action.
	return []CodeAction{{Kind: ActionInspectReference, TargetSymbol: d.TargetSymbol, Preview: true}}
}

// hasSafeDelete reports whether the diagnostic already carries a safe_delete_symbol
// action, which is the analyzer's signal that the symbol has no live refs.
func hasSafeDelete(d Diagnostic) bool {
	for _, a := range d.Actions {
		if a.Kind == ActionSafeDeleteSymbol {
			return true
		}
	}
	return false
}

// sortActions returns a deterministically sorted copy of actions.
func sortActions(actions []CodeAction) []CodeAction {
	out := make([]CodeAction, len(actions))
	copy(out, actions)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].TargetSymbol < out[j].TargetSymbol
	})
	return out
}
