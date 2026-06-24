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

// statusBadge maps a status to its legend glyph, matching epics/index.md.
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

// categoryOrder fixes the section order in the rendered table; categories not
// listed are appended alphabetically after these.
var categoryOrder = []string{CategoryParser, CategoryAnalyzer, CategoryMCPTool, CategorySurface}

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
	b.WriteString("                      (registered parsers/analyzers, advertised MCP tools, present surfaces). -->\n\n")
	b.WriteString("Every row below is checked against the live registries by the `coverage-matrix`\n")
	b.WriteString("CI gate (`internal/coverage`). A docs-only change that contradicts the code — a\n")
	b.WriteString("missing capability, a phantom \"shipped\" entry, or a live capability marked\n")
	b.WriteString("\"planned\" — breaks the build. **Legend:** ✅ shipped · 🟡 partial · ⏳ planned.\n\n")
	b.WriteString(fmt.Sprintf("Total capabilities: **%d**. See [`architecture-plan.md`](architecture-plan.md) for the design context.\n", len(sorted)))

	for _, cat := range order {
		rows := byCat[cat]
		fmt.Fprintf(&b, "\n## %s (%d)\n\n", categoryTitle(cat), len(rows))
		b.WriteString("| id | status | epic | note |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, c := range rows {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n",
				c.ID, statusBadge(c.Status), mdCell(c.Epic), mdCell(c.Note))
		}
	}
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
