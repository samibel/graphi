package coverage

import (
	"fmt"
	"sort"
	"strings"
)

// Repo-relative paths to the checked-in matrix source and rendered table.
const (
	MatrixYAMLPath = "docs/coverage-matrix.yaml"
	MatrixMDPath   = "docs/coverage-matrix.md"
)

// statusBadge maps a status to its legend glyph.
func statusBadge(status string) string {
	switch status {
	case StatusShipped:
		return "✅ shipped"
	case StatusPartial:
		return "🟡 partial"
	case StatusPlanned:
		return "⏳ planned"
	default:
		return status
	}
}

// tierBadge maps a SCOPE-01 stability tier to its legend glyph.
func tierBadge(tier string) string {
	switch tier {
	case TierStable:
		return "🟢 stable"
	case TierLabs:
		return "🧪 labs"
	case TierDisabled:
		return "⛔ disabled"
	default:
		return tier
	}
}

// categoryOrder fixes the section order in the rendered table; categories not
// listed are appended alphabetically after these.
var categoryOrder = []string{CategoryParser, CategoryAnalyzer, CategoryMCPTool, CategorySurface, CategoryCLI}

// categoryTitle is the human heading for a category section.
func categoryTitle(cat string) string {
	switch cat {
	case CategoryParser:
		return "Parsers"
	case CategoryAnalyzer:
		return "Analyzers"
	case CategoryMCPTool:
		return "MCP tools"
	case CategorySurface:
		return "Surfaces"
	case CategoryCLI:
		return "CLI subcommands"
	default:
		return strings.Title(cat) //nolint:staticcheck // simple ASCII heading
	}
}

// RenderMarkdown renders the human-readable coverage matrix table from the
// capability rows. It is a PURE function of caps (no live data), so the
// checked-in docs/coverage-matrix.md is exactly RenderMarkdown(LoadMatrix(yaml));
// a freshness test asserts that equality so the .md can never drift from the
// .yaml. The drift guard separately keeps the .yaml honest against live code.
func RenderMarkdown(caps []Capability) string {
	sorted := make([]Capability, len(caps))
	copy(sorted, caps)
	sortCapabilities(sorted)

	byCat := map[string][]Capability{}
	for _, c := range sorted {
		byCat[c.Category] = append(byCat[c.Category], c)
	}

	// Determine section order: fixed categories first, then any extras alpha.
	seen := map[string]bool{}
	order := make([]string, 0, len(byCat))
	for _, c := range categoryOrder {
		if len(byCat[c]) > 0 {
			order = append(order, c)
			seen[c] = true
		}
	}
	var extra []string
	for c := range byCat {
		if !seen[c] {
			extra = append(extra, c)
		}
	}
	sort.Strings(extra)
	order = append(order, extra...)

	var b strings.Builder
	b.WriteString("# graphi — capability coverage matrix\n\n")
	b.WriteString("<!-- GENERATED FILE — do not edit by hand.\n")
	b.WriteString("     Source of truth: docs/coverage-matrix.yaml\n")
	b.WriteString("     Regenerate:      go run ./cmd/coverage -generate\n")
	b.WriteString("     CI-enforced:     internal/coverage drift guard fails the build if this\n")
	b.WriteString("                      matrix omits, adds, or mislabels a live capability\n")
	b.WriteString("                      (registered parsers/analyzers, the maximal MCP registry, present surfaces). -->\n\n")
	b.WriteString("Every row below is checked against the live registries by the `coverage-matrix`\n")
	b.WriteString("CI gate (`internal/coverage`). A docs-only change that contradicts the code — a\n")
	b.WriteString("missing capability, a phantom \"shipped\" entry, or a live capability marked\n")
	b.WriteString("\"planned\" — breaks the build. **Status legend:** ✅ shipped · 🟡 partial · ⏳ planned.\n")
	b.WriteString("**Stability tier (SCOPE-01):** 🟢 stable · 🧪 labs · ⛔ disabled. The `stable` set is\n")
	b.WriteString("FROZEN to exactly the 12 operations below; the guard fails the build if a 13th\n")
	b.WriteString("row is tagged stable or one is dropped.\n\n")
	b.WriteString("> **`tier` is not GA.** The `tier` column answers exactly one machine question —\n")
	b.WriteString("> *is this row one of the 12 frozen operations?* Parser and surface rows are\n")
	b.WriteString("> structurally ineligible for `stable`, so `go`, `cli`, and `mcp` read `labs`\n")
	b.WriteString("> despite being the entire GA scope. GA is a prose tier defined in\n")
	b.WriteString("> [`stability-tiers.md`](stability-tiers.md).\n\n")
	b.WriteString(renderStableCallout(sorted))
	b.WriteString(renderMCPProfileCallout(sorted))
	b.WriteString(fmt.Sprintf("Total capabilities: **%d**. See [`architecture-plan.md`](architecture-plan.md) for the design context.\n", len(sorted)))

	for _, cat := range order {
		rows := byCat[cat]
		fmt.Fprintf(&b, "\n## %s (%d)\n\n", categoryTitle(cat), len(rows))
		b.WriteString("| id | tier | status | epic | note |\n")
		b.WriteString("|---|---|---|---|---|\n")
		for _, c := range rows {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
				c.ID, tierBadge(c.Tier), statusBadge(c.Status), mdCell(c.Epic), mdCell(c.Note))
		}
	}
	return b.String()
}

// renderMCPProfileCallout makes the distinction ToolNames intentionally
// carries explicit: the matrix lists the maximal registry, while only its
// stable MCP rows form the maximal default profile. Concrete bindings then
// remove operations their transport cannot execute; Labs is an explicit CLI
// opt-in and remains capability-gated at runtime.
func renderMCPProfileCallout(sorted []Capability) string {
	var total, stable, labs, disabled int
	for _, c := range sorted {
		if c.Category != CategoryMCPTool {
			continue
		}
		total++
		switch c.Tier {
		case TierStable:
			stable++
		case TierLabs:
			labs++
		case TierDisabled:
			disabled++
		}
	}
	return fmt.Sprintf("**MCP profiles:** the default in-process `graphi mcp` binding advertises exactly **%d Stable tools**. Every binding then removes operations its concrete transport cannot execute; the current daemon binding exposes seven and honestly omits its four unwired agent-tool RPCs. `graphi mcp -labs` explicitly opts into the capability-gated Labs catalog; this matrix records its maximal **%d-tool** union (%d Labs, %d disabled), not a promise that every optional service or transport is wired. `index` is Stable lifecycle, not an MCP tool.\n\n", stable, total, labs, disabled)
}

// renderStableCallout renders the frozen 12-op stable set (SCOPE-01) as a visible
// bullet so the taxonomy is legible in the generated doc, not just in the tier
// column. It lists every row tagged `tier: stable`, sorted by (category, id).
func renderStableCallout(sorted []Capability) string {
	var stable []Capability
	for _, c := range sorted {
		if c.Tier == TierStable {
			stable = append(stable, c)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**The %d stable operations (frozen):** ", len(stable))
	ids := make([]string, 0, len(stable))
	for _, c := range stable {
		ids = append(ids, "`"+c.ID+"`")
	}
	b.WriteString(strings.Join(ids, ", "))
	b.WriteString(".\n\n")
	return b.String()
}

// mdCell renders an empty value as an em dash and escapes pipes so a note cannot
// break the table.
func mdCell(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}
