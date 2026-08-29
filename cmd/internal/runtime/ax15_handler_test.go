// SW-255 (AX-15): the composition root hands the module set's handler to the
// surface client, and the surface client hands it to the executor — through the
// existing Composition.Client() wiring, not through a global.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/samibel/graphi/engine/agenttools/deadcode"
	"github.com/samibel/graphi/engine/module"
	"github.com/samibel/graphi/engine/query/compound"
	"github.com/samibel/graphi/surfaces/client"
)

// TestAX15_TheCompositionClientCarriesTheModuleHandler is AC-5 and AC-6 at the
// composition root: the catalog is still 56 operations, the client carries
// both engine-side handlers, and each answers byte-for-byte what the legacy
// method answers over the same session.
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
	found := map[string]bool{}
	for _, m := range comp.Modules() {
		found[m.ID] = true
	}
	for _, id := range []string{module.IDCompound, module.IDDeadCode} {
		if !found[id] {
			t.Fatalf("Modules() does not list %s: %v", id, contributions.ModuleIDs())
		}
	}
	wantHandled := []string{compound.Operation, deadcode.Operation}
	if got := contributions.Handled(); !reflect.DeepEqual(got, wantHandled) {
		t.Fatalf("Handled() = %v, want %v", got, wantHandled)
	}

	provider, ok := rt.Client.(client.OperationHandlerProvider)
	if !ok {
		t.Fatal("the runtime's client does not carry the module handlers")
	}
	if got := provider.HandledOperations(); !reflect.DeepEqual(got, wantHandled) {
		t.Fatalf("client.HandledOperations() = %v, want %v", got, wantHandled)
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

	compoundHandler, ok := provider.OperationHandler(compound.Operation)
	if !ok {
		t.Fatalf("the client has no handler for %s", compound.Operation)
	}
	const queryText = "SEED no.such.symbol\nHOP out calls\n"
	wantCompound, err := rt.Client.Compound(context.Background(), queryText)
	if err != nil {
		t.Fatalf("legacy Compound: %v", err)
	}
	gotCompound, err := compoundHandler(context.Background(), json.RawMessage(`{"query":"SEED no.such.symbol\nHOP out calls\n"}`))
	if err != nil {
		t.Fatalf("compound module handler: %v", err)
	}
	if !bytes.Equal(gotCompound, wantCompound) {
		t.Fatalf("compound handler and legacy method disagree\n  handler: %s\n  legacy:  %s", gotCompound, wantCompound)
	}

	// The attach path composes the same handler over the same store.
	attached, err := Attach(rt.DBPath, "", rt.MetaDir)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer attached.Close()
	if got := attached.Composition().Contributions().Handled(); !reflect.DeepEqual(got, wantHandled) {
		t.Fatalf("attach composition Handled() = %v", got)
	}
}
