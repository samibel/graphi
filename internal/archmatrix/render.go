package archmatrix

import (
	"fmt"
	"sort"
	"strings"
)

// implBadge maps an implementation status to its legend glyph.
func implBadge(status string) string {
	switch status {
	case ImplFull:
		return "✅ full"
	case ImplUnavailable:
		return "⛔ unavailable"
	case ImplTypedSkip:
		return "🟡 typed-skip"
	default:
		return status
	}
}

// RenderMarkdown renders the matrix deterministically. Rows are grouped by
// bounded context in migration order, so the document reads as the plan it is.
// The "surfaces today" column comes from usage, derived from the source by
// ScanSurfaceUsage, so the rendered file goes stale — and CI says so — as soon as
// a handler starts or stops calling a method.
func RenderMarkdown(m Matrix, usage Usage, refs SentinelRefs) string {
	var b strings.Builder

	b.WriteString("# graphi — P2 architecture migration matrix\n\n")
	b.WriteString("<!-- GENERATED FILE — do not edit by hand.\n")
	b.WriteString("     Source of truth: " + MatrixYAMLPath + "\n")
	b.WriteString("     Regenerate:      go run ./cmd/archmatrix -generate\n")
	b.WriteString("     CI-enforced:     internal/archmatrix drift guard fails the build if a live\n")
	b.WriteString("                      surfaces/client.Client method or error sentinel is missing\n")
	b.WriteString("                      from this matrix, or if the matrix names one that is gone. -->\n\n")

	b.WriteString("This is the ARCH-P0 inventory for the P2 modularization: every method of the\n")
	b.WriteString("broad `surfaces/client.Client` contract, the bounded context that will own it,\n")
	b.WriteString("the application service it moves to, and the phase that moves it.\n\n")
	b.WriteString("The method set is derived from the live interface by reflection and the sentinel\n")
	b.WriteString("set from the package source, so this file cannot drift from the code: adding a\n")
	b.WriteString("method to the legacy client without deciding its owning context fails CI.\n\n")
	b.WriteString("**Implementation legend:** ✅ full — executes the real operation · ")
	b.WriteString("⛔ unavailable — refuses with a typed sentinel, doing no work · ")
	b.WriteString("🟡 typed-skip — returns a typed graceful-skip payload with no error.\n\n")
	b.WriteString("> `unavailable` counts are the compatibility debt the PRD forbids growing: no new\n")
	b.WriteString("> capability may be added by appending a method here and stubbing it out in the\n")
	b.WriteString("> remote clients.\n\n")

	// Summary: methods per context, and the stub debt per implementation.
	b.WriteString("## Summary\n\n")
	fmt.Fprintf(&b, "%d methods across %d bounded contexts.\n\n", len(m.Methods), len(contextOrder))
	b.WriteString("| Context | Target service | Phase | Methods | `unavailable` on HTTP | `unavailable` on daemon |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, ctx := range contextOrder {
		rows := m.ByContext(ctx)
		plan := contextPlans[ctx]
		httpStubs, daemonStubs := 0, 0
		for _, row := range rows {
			if row.HTTP == ImplUnavailable {
				httpStubs++
			}
			if row.Daemon == ImplUnavailable {
				daemonStubs++
			}
		}
		fmt.Fprintf(&b, "| %s (%s) | `%s` | %d | %d | %d | %d |\n",
			plan.Title, ctx, plan.Service, plan.Phase, len(rows), httpStubs, daemonStubs)
	}
	b.WriteString("\n")

	// Per-context method tables.
	for _, ctx := range contextOrder {
		rows := m.ByContext(ctx)
		if len(rows) == 0 {
			continue
		}
		plan := contextPlans[ctx]
		fmt.Fprintf(&b, "## %s — `%s` (phase %d)\n\n", plan.Title, plan.Service, plan.Phase)
		b.WriteString("| Method | Direct | HTTP | Daemon | Surfaces today | Refusal sentinels | Note |\n")
		b.WriteString("|---|---|---|---|---|---|---|\n")
		for _, row := range rows {
			name := "`" + row.Name + "`"
			if row.Pilot {
				name += " ⭐"
			}
			note := row.Note
			if row.Decision != "" {
				note = strings.TrimSpace(note + " **Open decision: " + row.Decision + "**")
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
				name, implBadge(row.Direct), implBadge(row.HTTP), implBadge(row.Daemon),
				usage.For(row.Name), refs.For(row.Name), note)
		}
		b.WriteString("\n")
	}

	// Pilots.
	var pilots []string
	for _, row := range m.Methods {
		if row.Pilot {
			pilots = append(pilots, "`"+row.Name+"`")
		}
	}
	sort.Strings(pilots)
	if len(pilots) > 0 {
		b.WriteString("## ⭐ Phase 3 differential pilots\n\n")
		b.WriteString("These use cases are migrated first, behind the compatibility facade, to prove\n")
		b.WriteString("the legacy and application paths agree before the bulk migration starts: ")
		b.WriteString(strings.Join(pilots, ", "))
		b.WriteString(".\n\n")
	}

	// Open decisions.
	var decisions []Method
	for _, row := range m.Methods {
		if row.Decision != "" {
			decisions = append(decisions, row)
		}
	}
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].Name < decisions[j].Name })
	b.WriteString("## Open decisions\n\n")
	if len(decisions) == 0 {
		b.WriteString("None — every method's owning context is settled.\n\n")
	} else {
		b.WriteString("These placements are the plan of record but need maintainer sign-off before the\n")
		b.WriteString("phase that acts on them begins.\n\n")
		b.WriteString("| Method | Proposed context | Question |\n|---|---|---|\n")
		for _, row := range decisions {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", row.Name, row.Context, row.Decision)
		}
		b.WriteString("\n")
	}

	// Sentinel inventory.
	b.WriteString("## Error sentinel inventory\n\n")
	b.WriteString("Every exported `Err…` in `surfaces/client`. The refactor must preserve these\n")
	b.WriteString("identities: an unwired capability stays fail-closed, and no unavailable path may\n")
	b.WriteString("quietly become a success.\n\n")
	b.WriteString("| Sentinel | Kind | Note |\n|---|---|---|\n")
	for _, row := range m.Sentinels {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", row.Name, row.Kind, row.Note)
	}
	b.WriteString("\n")

	byKind := map[string]int{}
	for _, row := range m.Sentinels {
		byKind[row.Kind]++
	}
	kinds := make([]string, 0, len(byKind))
	for kind := range byKind {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	parts := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		parts = append(parts, fmt.Sprintf("%s: %d", kind, byKind[kind]))
	}
	fmt.Fprintf(&b, "%d sentinels total (%s).\n", len(m.Sentinels), strings.Join(parts, " · "))

	return b.String()
}
