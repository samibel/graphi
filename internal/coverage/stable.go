package coverage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/surfaces/mcp"
)

// CanonicalStableOps returns the frozen 12 stable operation ids (SCOPE-01 /
// SW-111). The single source is surfaces/mcp.StableOperations, so the coverage
// matrix, the MCP tool descriptions, and CLI help all agree on the stable set.
// The result is a fresh slice the caller may mutate.
func CanonicalStableOps() []string {
	return append([]string(nil), mcp.StableOperations...)
}

// StableTierReport is the SCOPE-01 stable-tier invariant outcome: the set of
// matrix rows tagged `tier: stable` must equal EXACTLY the frozen stable
// operations — no 13th entry, none missing, no duplicate.
type StableTierReport struct {
	// Extra lists rows tagged tier=stable whose id is NOT a canonical stable op.
	Extra []Capability
	// Missing lists canonical stable ops with no tier=stable row in the matrix.
	Missing []string
	// Count is the number of tier=stable rows found; Want is the canonical count.
	Count int
	Want  int
}

// Pass reports whether the matrix's stable tier equals exactly the frozen set.
// Count==Want (with Extra/Missing empty) also catches a duplicate-id stable row.
func (r StableTierReport) Pass() bool {
	return len(r.Extra) == 0 && len(r.Missing) == 0 && r.Count == r.Want
}

// CheckStableTier verifies the SCOPE-01 stable-tier invariant: the ids of all
// rows tagged `tier: stable` must equal exactly the canonical 12 operations
// (surfaces/mcp.StableOperations), with exactly 12 such rows.
func CheckStableTier(matrix []Capability) StableTierReport {
	want := map[string]bool{}
	for _, op := range mcp.StableOperations {
		want[op] = true
	}
	rep := StableTierReport{Want: len(want)}
	seen := map[string]bool{}
	for _, c := range matrix {
		if c.Tier != TierStable {
			continue
		}
		rep.Count++
		if want[c.ID] {
			seen[c.ID] = true
		} else {
			rep.Extra = append(rep.Extra, c)
		}
	}
	for op := range want {
		if !seen[op] {
			rep.Missing = append(rep.Missing, op)
		}
	}
	sortCapabilities(rep.Extra)
	sort.Strings(rep.Missing)
	return rep
}

// Format renders a deterministic, human-readable report suitable for CI logs.
func (r StableTierReport) Format() string {
	if r.Pass() {
		return fmt.Sprintf("stable-tier check PASS — exactly the %d frozen stable operations are tagged `tier: stable`.\n", r.Want)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "stable-tier check FAILED — the `tier: stable` set must equal EXACTLY the %d frozen operations (SCOPE-01), but found %d stable rows:\n", r.Want, r.Count)
	if len(r.Extra) > 0 {
		b.WriteString("\n  rows tagged stable but NOT one of the frozen 12 (retier to labs/disabled, or remove):\n")
		lines := make([]string, 0, len(r.Extra))
		for _, c := range r.Extra {
			lines = append(lines, fmt.Sprintf("    - %s %q", c.Category, c.ID))
		}
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, "\n"))
		b.WriteByte('\n')
	}
	if len(r.Missing) > 0 {
		b.WriteString("\n  frozen stable operations MISSING a `tier: stable` row (add or retier one):\n")
		for _, op := range r.Missing {
			fmt.Fprintf(&b, "    - %q\n", op)
		}
	}
	b.WriteString("\nThe canonical set is surfaces/mcp.StableOperations.\n")
	return b.String()
}

// MCPDefaultProfileReport verifies the matrix representation of the runtime
// profile boundary. The global StableTierReport alone is insufficient: moving
// the stable "impact" label from its MCP row to the analyzer row preserves the
// same 12 ids but falsely documents impact as Labs on the default MCP surface.
type MCPDefaultProfileReport struct {
	Extra   []Capability
	Missing []string
	Count   int
	Want    int
}

// Pass reports whether the matrix's stable MCP rows are exactly the default
// server catalog (the 12 product operations minus lifecycle-only index).
func (r MCPDefaultProfileReport) Pass() bool {
	return len(r.Extra) == 0 && len(r.Missing) == 0 && r.Count == r.Want
}

// CheckMCPDefaultProfile cross-checks tier=stable MCP rows against the same
// StableMCPToolNames source used by tools/list and dispatch.
func CheckMCPDefaultProfile(matrix []Capability) MCPDefaultProfileReport {
	wantNames := mcp.StableMCPToolNames()
	want := make(map[string]bool, len(wantNames))
	for _, name := range wantNames {
		want[name] = true
	}
	rep := MCPDefaultProfileReport{Want: len(want)}
	seen := make(map[string]bool, len(want))
	for _, c := range matrix {
		if c.Category != CategoryMCPTool || c.Tier != TierStable {
			continue
		}
		rep.Count++
		if want[c.ID] {
			seen[c.ID] = true
		} else {
			rep.Extra = append(rep.Extra, c)
		}
	}
	for name := range want {
		if !seen[name] {
			rep.Missing = append(rep.Missing, name)
		}
	}
	sortCapabilities(rep.Extra)
	sort.Strings(rep.Missing)
	return rep
}

// Format renders a deterministic CI report for the MCP profile invariant.
func (r MCPDefaultProfileReport) Format() string {
	if r.Pass() {
		return fmt.Sprintf("mcp-default-profile check PASS — exactly %d Stable MCP tools are default; Labs remains opt-in.\n", r.Want)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mcp-default-profile check FAILED — tier=stable MCP rows must equal exactly the %d default tools, found %d:\n", r.Want, r.Count)
	if len(r.Extra) > 0 {
		b.WriteString("\n  non-default tools incorrectly tagged stable:\n")
		for _, c := range r.Extra {
			fmt.Fprintf(&b, "    - %q\n", c.ID)
		}
	}
	if len(r.Missing) > 0 {
		b.WriteString("\n  default MCP tools not tagged stable:\n")
		for _, name := range r.Missing {
			fmt.Fprintf(&b, "    - %q\n", name)
		}
	}
	b.WriteString("\nThe canonical default is surfaces/mcp.StableMCPToolNames().\n")
	return b.String()
}
