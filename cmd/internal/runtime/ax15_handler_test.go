// SW-255 (AX-15): the composition root hands the module set's handler to the
// surface client, and the surface client hands it to the executor — through the
// existing Composition.Client() wiring, not through a global.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/deadcode"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/surfaces/client"
)

// TestAX15_TheCompositionClientCarriesTheModuleHandler is AC-5 and AC-6 at the
// composition root: the built-in set lists engine.deadcode, the catalog is
// still 56 operations, the client the runtime hands out is an
// OperationHandlerProvider for exactly dead_code, and the handler it carries
// answers byte-for-byte what the legacy method answers over the same session.
func TestAX15_TheCompositionClientCarriesTheModuleHandler(t *testing.T) {
	withCompositionMode(t, CompositionBuilder)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := charRepository(t)

	rt, err := OpenSession(context.Background(), Options{Cwd: repo})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	defer rt.Close()

	comp := rt.Composition()
	if comp == nil {
		t.Fatal("no composition")
	}
	contributions := comp.Contributions()
	if got := contributions.Operations().Len(); got != 56 {
		t.Fatalf("catalog holds %d operations, want 56 unchanged", got)
	}
	found := false
	for _, m := range comp.Modules() {
		if m.ID == module.IDDeadCode {
			found = true
		}
	}
	if !found {
		t.Fatalf("Modules() does not list %s: %v", module.IDDeadCode, contributions.ModuleIDs())
	}
	if got := contributions.Handled(); len(got) != 1 || got[0] != deadcode.Operation {
		t.Fatalf("Handled() = %v, want exactly [%s]", got, deadcode.Operation)
	}

	provider, ok := rt.Client.(client.OperationHandlerProvider)
	if !ok {
		t.Fatal("the runtime's client does not carry the module handlers")
	}
	if got := provider.HandledOperations(); len(got) != 1 || got[0] != deadcode.Operation {
		t.Fatalf("client.HandledOperations() = %v, want [%s]", got, deadcode.Operation)
	}
	handler, ok := provider.OperationHandler(deadcode.Operation)
	if !ok {
		t.Fatalf("the client has no handler for %s", deadcode.Operation)
	}

	// Two different code paths over one session: the legacy typed method and
	// the module handler the composition bound to its ports.
	want, err := rt.Client.DeadCode(context.Background(), client.DeadCodeParams{MaxItems: 5})
	if err != nil {
		t.Fatalf("legacy DeadCode: %v", err)
	}
	got, err := handler(context.Background(), json.RawMessage(`{"max_items":5}`))
	if err != nil {
		t.Fatalf("module handler: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the module handler and the legacy method disagree over one session\n  handler: %s\n  legacy:  %s", got, want)
	}
	if len(want) == 0 {
		t.Fatal("the comparison produced no bytes and proves nothing")
	}

	// The attach path composes the same handler over the same store.
	attached, err := Attach(rt.DBPath, "", rt.MetaDir)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer attached.Close()
	if got := attached.Composition().Contributions().Handled(); len(got) != 1 || got[0] != deadcode.Operation {
		t.Fatalf("attach composition Handled() = %v", got)
	}
}
