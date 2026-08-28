package mcp

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/client"
)

// TestSW248_DefaultProfileReachesExactlyTheElevenStableTools is AC-1's floor and
// AC-6's guard in one assertion. The projection is only worth anything if it
// agrees with the profile that actually ships, and the eleven-tool default is
// frozen contract — so if this test ever needs updating, either the projection
// has drifted or the freeze has been broken, and both want a human.
func TestSW248_DefaultProfileReachesExactlyTheElevenStableTools(t *testing.T) {
	got := DefaultProfile().Tools()
	want := StableMCPToolNames()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the default profile advertises\n  %v\nwant the eleven Stable tools\n  %v", got, want)
	}
	if len(got) != 11 {
		t.Fatalf("the default MCP profile advertises %d tool(s), want 11", len(got))
	}
}

// TestSW248_TheMigratedOperationsAreReachableOnlyThroughLabs is the defect,
// pinned. Every operation on the executor seam is Labs, the default profile
// advertises none of them, and that is the fact the divergence readout now
// discloses instead of leaving a reader to infer "not yet".
//
// This test asserts the CURRENT truth rather than a desired one. If a future
// story legitimately makes one of them reachable by default, this test fails
// and that story updates it — deliberately, in its own diff, which is precisely
// what did not happen when the gap opened.
func TestSW248_TheMigratedOperationsAreReachableOnlyThroughLabs(t *testing.T) {
	migrated := client.MigratedOperations()
	if len(migrated) == 0 {
		t.Fatal("no operation is on the executor seam; this test has nothing to pin")
	}
	for _, r := range SeamReachability(migrated) {
		if r.InDefault {
			t.Errorf("%s is reachable through the default MCP profile; the Stable-12 freeze "+
				"and the eleven-tool default profile are contract, and this story did not move a tier", r.Operation)
		}
		if len(r.ReachedBy) == 0 {
			t.Errorf("%s is on the seam and NO shipped profile reaches it — it dual-runs and can "+
				"never be observed", r.Operation)
			continue
		}
		if !reflect.DeepEqual(r.ReachedBy, []string{ProfileLabsID}) {
			t.Errorf("%s is reached by %v, want only %s", r.Operation, r.ReachedBy, ProfileLabsID)
		}
	}
}

// TestSW248_ReachabilityIsDerivedFromTheLiveProfileBuilders guards against the
// projection quietly becoming a second hand-maintained list. Both profile sets
// must be exactly what the descriptor builders produce — the same functions
// toolDescriptors() selects between for a real session.
func TestSW248_ReachabilityIsDerivedFromTheLiveProfileBuilders(t *testing.T) {
	for _, tc := range []struct {
		id      string
		builder func() []map[string]any
	}{
		{ProfileDefaultID, stableToolDescriptors},
		{ProfileLabsID, maximalToolDescriptors},
	} {
		var profile SurfaceProfile
		for _, p := range ShippedProfiles() {
			if p.ID == tc.id {
				profile = p
			}
		}
		want := make([]string, 0)
		for name := range toolNameSet(tc.builder()) {
			want = append(want, name)
		}
		sort.Strings(want)
		if got := profile.Tools(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s advertises %d tool(s) in the projection and %d in the live builder",
				tc.id, len(got), len(want))
		}
	}
}

// TestSW248_TheLabsProfileIsASupersetOfTheDefault is the structural property the
// two-profile model rests on: a client that opts in never loses a tool. If it
// ever failed, "reachable only through the opt-in profile" would stop being a
// complete answer and the readout's advice would be wrong for some operation.
func TestSW248_TheLabsProfileIsASupersetOfTheDefault(t *testing.T) {
	var def, labs SurfaceProfile
	for _, p := range ShippedProfiles() {
		switch p.ID {
		case ProfileDefaultID:
			def = p
		case ProfileLabsID:
			labs = p
		}
	}
	for _, name := range def.Tools() {
		if !labs.Reaches(name) {
			t.Errorf("%s is in the default profile but not in the maximal one", name)
		}
	}
}

// TestSW248_EveryShippedProfileNamesAnInvocation keeps the readout actionable.
// A profile the report can only name by internal id tells an operator that
// something is unreachable without telling them what to run.
func TestSW248_EveryShippedProfileNamesAnInvocation(t *testing.T) {
	defaults := 0
	for _, p := range ShippedProfiles() {
		if p.ID == "" || p.Invocation == "" {
			t.Errorf("profile %+v is missing an id or an invocation", p)
		}
		if !strings.HasPrefix(p.Invocation, "graphi ") {
			t.Errorf("profile %s's invocation %q is not a command an operator can run", p.ID, p.Invocation)
		}
		if p.Default {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("%d shipped profile(s) are marked default, want exactly 1 — the readout reasons "+
			"about one reference profile", defaults)
	}
}

// TestSW248_ReachabilityAnswersForAnUnknownOperation pins the honest answer for
// an id no profile knows: reached by nothing, rather than an absent row. An
// absent row reads as "fine", which is the laundering the divergence document
// already refuses elsewhere.
func TestSW248_ReachabilityAnswersForAnUnknownOperation(t *testing.T) {
	got := SeamReachability([]string{"no_such_operation"})
	if len(got) != 1 {
		t.Fatalf("SeamReachability dropped an unknown operation instead of answering for it: %v", got)
	}
	if got[0].InDefault || len(got[0].ReachedBy) != 0 {
		t.Fatalf("an unknown operation reports reachable: %+v", got[0])
	}
}
