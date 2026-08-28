package mcp

import "sort"

// SW-248 — the profile-reachability projection.
//
// # The gap this closes
//
// SW-228 (AX-08) moved ten operations onto the executor seam and SW-244 made
// `shadow` the compiled-in position, so a released binary reports "10 shadow"
// and a divergence record that says `UNKNOWN: no dual-run observation has been
// recorded`. Both statements are true. Together they mislead, because all ten
// migrated operations are Labs and the DEFAULT MCP profile advertises the
// eleven Stable tools — so a client bound to the shipped default cannot reach
// one of them, and the record will still read UNKNOWN however long the release
// line runs. "0 observations" read as *not yet* when the truth was *not
// possible in this profile*.
//
// Nothing in the tree could answer the question, because nothing modelled it:
// the seam knows which operations are MIGRATED (surfaces/client) and this
// package knows which tools a PROFILE advertises, and the two facts had never
// been joined. This file is that join, and it is deliberately the only place
// that performs it.
//
// # Why the sets are derived and never listed
//
// Both profile sets come from the LIVE descriptor builders — the same two
// functions toolDescriptors() selects between for a real session — so a tool
// added to, or removed from, either profile moves this projection in the same
// commit. A hand-kept list here would be a second source of truth for exactly
// the property the projection exists to check, and would go stale in precisely
// the case that matters: somebody changing what a profile advertises.
//
// The DEFAULT profile's membership is `stableToolDescriptors()`, whose
// dispatch-side twin is the `!s.labs && !IsStableMCPTool(p.Name)` refusal in
// toolcalls.go — a tools/call for anything outside it is rejected, so
// "advertised in this profile" and "callable in this profile" are the same
// question.
//
// # What reachability does NOT claim
//
// It is the PROFILE-STATIC answer: whether a client bound to that profile can
// name the tool at all. A concrete session narrows further through
// CapabilityReporter (filterSupportedToolDescriptors) when a backing service is
// unwired, so `Reaches` is an upper bound for one session. That is the correct
// direction for this story's question — an operation this projection calls
// unreachable is unreachable for certain, which is the claim the divergence
// record and `graphi doctor` are asked to make.
//
// It is also MCP-only, and the scope is stated rather than implied. The HTTP
// surface (`graphi serve`) does dispatch through the same seam, but runHTTP
// installs no DivergenceRecorder (see cmd/graphi/serve.go and
// docs/executor-seam-rollback.md §5), so its dual-runs are never persisted and
// cannot make an empty record fill. The CLI verbs call the legacy client
// methods directly and never reach the seam at all. The MCP profiles are
// therefore the whole of the persisted record's reachability.

// Profile ids. They are stable strings because `graphi doctor --json` and the
// divergence document both carry them.
const (
	// ProfileDefaultID is the profile a stock install gets: `graphi setup`
	// registers `graphi mcp` with no flags, so this is what Claude Code and
	// every other configured client binds.
	ProfileDefaultID = "mcp-default"
	// ProfileLabsID is the opt-in maximal profile.
	ProfileLabsID = "mcp-labs"
)

// SurfaceProfile is one shipped MCP binding, reduced to the two things a
// reachability question needs: how an operator selects it, and which tools it
// advertises.
type SurfaceProfile struct {
	// ID is the stable machine identifier.
	ID string
	// Invocation is the command that binds this profile, so a readout can tell
	// an operator what to run rather than naming an internal id at them.
	Invocation string
	// Default reports whether this is the profile a stock install binds.
	Default bool

	tools map[string]bool
}

// Reaches reports whether a client bound to this profile can call the named
// tool. Operation ids and tool names are the same string for every migrated
// operation; an operation that is not an advertised tool name in any profile is
// reachable nowhere, which is the finding SeamReachability reports.
func (p SurfaceProfile) Reaches(name string) bool { return p.tools[name] }

// Tools returns this profile's advertised tool names, sorted, freshly
// allocated.
func (p SurfaceProfile) Tools() []string {
	out := make([]string, 0, len(p.tools))
	for name := range p.tools {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ShippedProfiles returns every MCP profile a released binary can be started
// in, most restrictive first. The default profile is always first: a readout
// that has to pick ONE reference profile must pick the one a stock install
// binds, and putting it first makes that the obvious choice rather than a
// lookup.
func ShippedProfiles() []SurfaceProfile {
	return []SurfaceProfile{
		{
			ID:         ProfileDefaultID,
			Invocation: "graphi mcp",
			Default:    true,
			tools:      toolNameSet(stableToolDescriptors()),
		},
		{
			ID:         ProfileLabsID,
			Invocation: "graphi mcp -labs",
			tools:      toolNameSet(maximalToolDescriptors()),
		},
	}
}

// DefaultProfile returns the profile a stock install binds. It panics if no
// shipped profile is marked default, because a build in which nothing is the
// default is a build whose readouts have no reference to speak about — failing
// loudly at the first call beats every caller inventing a fallback.
func DefaultProfile() SurfaceProfile {
	for _, p := range ShippedProfiles() {
		if p.Default {
			return p
		}
	}
	panic("mcp: no shipped MCP profile is marked default")
}

// OperationReach is one operation's reachability across the shipped profiles.
type OperationReach struct {
	// Operation is the operation id, which is also its MCP tool name.
	Operation string
	// ReachedBy holds the ids of the shipped profiles that advertise it, in
	// ShippedProfiles order. Empty means no shipped profile reaches it.
	ReachedBy []string
	// Invocations holds the corresponding commands, in the same order, so a
	// readout can print `graphi mcp -labs` instead of `mcp-labs`.
	Invocations []string
	// InDefault reports whether the default profile reaches it — the single
	// fact the SW-248 defect turned on.
	InDefault bool
}

// SeamReachability answers the reachability question for a set of operation
// ids, in the order given. It is the one function the composition root, the
// divergence readout and the CI gate all go through, so the three cannot
// disagree about what "reachable" means.
func SeamReachability(operations []string) []OperationReach {
	profiles := ShippedProfiles()
	out := make([]OperationReach, 0, len(operations))
	for _, op := range operations {
		reach := OperationReach{Operation: op}
		for _, p := range profiles {
			if !p.Reaches(op) {
				continue
			}
			reach.ReachedBy = append(reach.ReachedBy, p.ID)
			reach.Invocations = append(reach.Invocations, p.Invocation)
			if p.Default {
				reach.InDefault = true
			}
		}
		out = append(out, reach)
	}
	return out
}

// toolNameSet reduces a descriptor catalog to its advertised names.
func toolNameSet(descriptors []map[string]any) map[string]bool {
	set := make(map[string]bool, len(descriptors))
	for _, d := range descriptors {
		if name, ok := d["name"].(string); ok && name != "" {
			set[name] = true
		}
	}
	return set
}
