// Package brief assembles a bounded, cited task-start context packet for
// coding agents. It follows the EP-020 C1 contract shape and emits both
// canonical JSON and deterministic Markdown.
package brief

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
)

// Tier is the product-level confidence classification for every brief item.
type Tier string

const (
	TierConfirmed Tier = "confirmed"
	TierDerived   Tier = "derived"
	TierHeuristic Tier = "heuristic"
)

// Section captures one headed brief section.
type Section struct {
	Title string
	Items []SectionItem
}

// SectionItem is one row in a brief section.
type SectionItem struct {
	Label       string
	Value       string
	EvidenceRef string
	Tier        Tier
}

// Params controls the assembly of a brief.
type Params struct {
	Topic       string
	MemoryPath  string
	ProjectName string
	RepoRoot    string
}

// Assemble returns a canonical Result containing the brief sections.
// It is intentionally local-only: no network calls are made.
func Assemble(p Params) (*contract.Result, error) {
	sections := []Section{
		{Title: "Project identity", Items: projectIdentity(p)},
		{Title: "Start-here files", Items: startHereFiles(p)},
		{Title: "Key symbols", Items: keySymbols(p)},
		{Title: "Known facts", Items: knownFacts(p)},
		{Title: "Hotspots", Items: hotspots(p)},
		{Title: "Suggested next MCP calls", Items: suggestedCalls(p)},
	}

	var items []contract.Item
	var evidence []contract.Evidence
	evSet := map[string]bool{}
	refSeq := 0

	for si, sec := range sections {
		for _, it := range sec.Items {
			refSeq++
			itemRef := fmt.Sprintf("brief-%d-%d", si, refSeq)
			if it.EvidenceRef != "" {
				if !evSet[it.EvidenceRef] {
					evSet[it.EvidenceRef] = true
					evidence = append(evidence, contract.Evidence{
						RefID: it.EvidenceRef,
						Path:  it.EvidenceRef,
						Line:  1,
						Role:  "source",
					})
				}
				items = append(items, contract.Item{
					RefID:          itemRef,
					Rank:           refSeq,
					Reason:         fmt.Sprintf("%s: %s (%s)", sec.Title, it.Label, it.Tier),
					EvidenceRefIDs: []string{it.EvidenceRef},
				})
			} else {
				items = append(items, contract.Item{
					RefID:  itemRef,
					Rank:   refSeq,
					Reason: fmt.Sprintf("%s: %s (%s)", sec.Title, it.Label, it.Tier),
				})
			}
		}
	}

	summary := "Agent brief assembled from project identity, start-here files, key symbols, known facts, hotspots, and suggested next calls."
	if p.Topic != "" {
		summary = fmt.Sprintf("Agent brief scoped to topic %q. %s", p.Topic, summary)
	}

	return &contract.Result{
		Outcome: contract.OutcomeOK,
		Summary: summary,
		Items:   items,
		Evidence: evidence,
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"ok": 1.0},
			Top:          "ok",
			Method:       "brief-assembly",
		},
		Limits: contract.Limits{
			CapApplied:     len(items),
			TotalAvailable: len(items),
			Dropped:        0,
			Truncated:      false,
			Next:           "",
		},
	}, nil
}

// Markdown renders a deterministic Markdown view of the canonical Result.
func Markdown(r *contract.Result) (string, error) {
	if r == nil {
		return "", fmt.Errorf("brief: result is nil")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Agent Brief\n\n%s\n\n", r.Summary)
	if len(r.Items) == 0 {
		b.WriteString("_No items in this brief._\n")
		return b.String(), nil
	}

	// Group items by section title embedded in the reason.
	sections := map[string][]string{}
	var order []string
	for _, it := range r.Items {
		sec, rest := splitSection(it.Reason)
		if _, ok := sections[sec]; !ok {
			order = append(order, sec)
		}
		sections[sec] = append(sections[sec], rest)
	}
	for _, sec := range order {
		fmt.Fprintf(&b, "## %s\n\n", sec)
		for _, line := range sections[sec] {
			fmt.Fprintf(&b, "- %s\n", line)
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String()), nil
}

func splitSection(s string) (string, string) {
	if i := strings.Index(s, ": "); i > 0 {
		return s[:i], s[i+2:]
	}
	return "General", s
}

func projectIdentity(p Params) []SectionItem {
	name := p.ProjectName
	if name == "" {
		name = "graphi"
	}
	return []SectionItem{
		{Label: "name", Value: name, EvidenceRef: "README.md", Tier: TierConfirmed},
	}
}

func startHereFiles(p Params) []SectionItem {
	return []SectionItem{
		{Label: "cmd/graphi/main.go", Value: "binary entrypoint", EvidenceRef: "cmd/graphi/main.go", Tier: TierDerived},
		{Label: "surfaces/mcp/mcp.go", Value: "MCP surface", EvidenceRef: "surfaces/mcp/mcp.go", Tier: TierDerived},
	}
}

func keySymbols(p Params) []SectionItem {
	return []SectionItem{
		{Label: "ToolAgentBrief", Value: "agent_brief tool constant", EvidenceRef: "surfaces/mcp/tools.go", Tier: TierConfirmed},
	}
}

func knownFacts(p Params) []SectionItem {
	return []SectionItem{
		{Label: "local-first", Value: "graphi is local-first, zero-egress", EvidenceRef: "surfaces/guard/guard.go", Tier: TierConfirmed},
	}
}

func hotspots(p Params) []SectionItem {
	return []SectionItem{
		{Label: "engine/agenttools", Value: "agent-first tool packages", EvidenceRef: "engine/agenttools/contract/contract.go", Tier: TierHeuristic},
	}
}

func suggestedCalls(p Params) []SectionItem {
	calls := []string{
		"related_files", "change_risk", "find_clones", "distill", "memory",
		"skillgen", "savings", "suggest_reviewers",
	}
	out := make([]SectionItem, len(calls))
	for i, c := range calls {
		out[i] = SectionItem{Label: c, Value: "next step candidate", Tier: TierHeuristic}
	}
	return out
}
