// Package canary implements graphi's hermetic egress-denied canary and
// zero-telemetry CI gate (SW-008).
//
// The package provides two independent mechanisms behind one acceptance
// contract:
//
//  1. Runtime egress canary — exercises the graphi surface stack and asserts
//     that ZERO non-loopback network dials are attempted. The assertion is made
//     at dial-attempt time via an injected interceptor (not libpcap), so even a
//     blocked attempt is caught. On Linux it can additionally run inside a
//     loopback-only network namespace; when isolation is unavailable the canary
//     HARD-FAILS rather than silently passing.
//
//  2. Static zero-telemetry gate — scans the default CGo-free build graph for
//     telemetry/analytics SDK imports and non-allowlisted outbound dial
//     constructors, failing CI with the offending symbol + import path.
//
// Both mechanisms build with CGO_ENABLED=0 and produce machine-readable JSON
// artifacts. They live OUTSIDE the surfaces/engine production code (this is a
// CI/test concern, not a runtime surface concern).
//
// Layering: internal/canary imports only the standard library, the graphi
// query operation vocabulary (for surface-union derivation), and go/packages
// for the static gate. It must not import surfaces/* or engine/* production
// runtime code.
package canary

// SurfaceUnion is the canonical set of graphi commands/tools the canary must
// exercise. It is derived programmatically (see NewSurfaceUnion) so the canary
// cannot silently miss a new tool as graphi grows — fulfilling the "drive every
// tool/command at least once" acceptance criterion without a hand-maintained
// list.
type SurfaceUnion struct {
	// CLICommands are the graphi subcommands (cmd/graphi/main.go dispatch).
	CLICommands []string `json:"cli_commands"`
	// QueryOperations are the structural query operations (engine/query.Operations).
	QueryOperations []string `json:"query_operations"`
	// SearchTool is the search capability name advertised over MCP/CLI.
	SearchTool string `json:"search_tool"`
}

// CoveredTools returns the flattened list of covered tool/command identifiers,
// stable-sorted, for the machine-readable canary artifact.
func (su SurfaceUnion) CoveredTools() []string {
	out := make([]string, 0, len(su.CLICommands)+len(su.QueryOperations)+1)
	out = append(out, su.CLICommands...)
	for _, op := range su.QueryOperations {
		out = append(out, "query:"+op)
	}
	if su.SearchTool != "" {
		out = append(out, "search")
	}
	return sortedUnique(out)
}
