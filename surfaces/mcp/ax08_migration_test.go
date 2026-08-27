package mcp

import (
	"sort"
	"testing"

	"github.com/samibel/graphi/surfaces/client"
)

// SW-228 (AX-08): the MCP surface's half of the migration is a TABLE, and a
// table can fall out of step with the set it serves in two directions. Both are
// failures here rather than at request time:
//
//   - a tool in migratedTools that surfaces/client would refuse to dispatch
//     turns every call to it into a typed rejection — a working tool replaced by
//     an error message;
//   - a migrated operation with no entry in migratedTools is an operation whose
//     dispatch arm was deleted and never replaced — a 404 where a tool used to
//     be, if the arm is gone, or a silent second dispatch path if it is not.
//
// The check is by NAME because the MCP tool name and the catalog operation id
// are the same string by construction (SW-225's projection depends on it), and
// this test is one of the places that stays true.
func TestAX08_MCPMigratedToolsMatchTheMigratedSet(t *testing.T) {
	migrated := client.MigratedOperations()
	if len(migrated) == 0 {
		t.Fatal("client.MigratedOperations() is empty; this test is not checking anything")
	}

	inTable := make([]string, 0, len(migratedTools))
	for name := range migratedTools {
		inTable = append(inTable, name)
		if !client.IsMigratedOperation(name) {
			t.Errorf("the MCP table routes %q through the executor seam, but surfaces/client "+
				"does not migrate it — every call would be rejected by name", name)
		}
	}
	sort.Strings(inTable)

	table := map[string]bool{}
	for _, name := range inTable {
		table[name] = true
	}
	for _, operation := range migrated {
		if !table[operation] {
			t.Errorf("%q dispatches through the executor seam but has no MCP table entry; if its "+
				"arm was removed the tool no longer works, and if it was not, the surface has two "+
				"dispatch paths for one operation", operation)
		}
	}
}

// TestAX08_MigratedToolsAreAdvertisedAndLabs pins two properties the removed
// arms used to carry implicitly.
//
// Advertisement: every migrated tool must still be a tool this server knows
// about. A table entry for a name that no descriptor advertises would be dead
// code that toolsCall can never reach, and a reader would have no way to tell
// that from a live route.
//
// Tier: every migrated operation is Labs, so none of them may appear in the
// Stable profile. The generic branch runs BEFORE the tier-specific arms, so a
// Stable operation appearing in this table would route a frozen operation
// through the executor — the exact thing AX-12 owns and this story does not.
func TestAX08_MigratedToolsAreAdvertisedAndLabs(t *testing.T) {
	advertised := map[string]bool{}
	for _, name := range ToolNames() {
		advertised[name] = true
	}
	for name := range migratedTools {
		if !advertised[name] {
			t.Errorf("%q is in the migrated dispatch table but is not an advertised tool", name)
		}
		if IsStableMCPTool(name) {
			t.Errorf("%q is a Stable tool and must not be migrated: Stable migration is AX-12's "+
				"decision and is gated on release evidence this story does not have", name)
		}
	}
}
