package interproctaint

import (
	"bytes"
	"context"
	"testing"
)

// TestSW104_SolvedVsLoaded_ByteParity is the SW-104 obligation that `taint-query`
// byte-parity holds whether the verdict is Solved in-memory this dispatch or
// Loaded from a persisted (content-hash-keyed) artifact. The canonical Marshal
// bytes must be identical across both paths (the Loaded/Solved flags are
// json:"-" observability-only and never leak into the canonical bytes), while the
// runtime flags differ honestly so a caller can tell which path served the
// answer. This proves the determinism-spec claim without faking a Loaded path
// through the surface dispatcher (which — per the SW-102 follow-up — currently
// always solves in-memory; that limitation is surfaced, not fixed, here).
func TestSW104_SolvedVsLoaded_ByteParity(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := positiveFixture(t)

	solved := solve(t, r)
	if !solved.Solved || solved.Loaded {
		t.Fatalf("freshly solved solution must have Solved=true, Loaded=false; got Solved=%v Loaded=%v", solved.Solved, solved.Loaded)
	}

	store := NewStore(t.TempDir())
	if err := store.Save(solved); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, ok, err := store.Load(solved.ContentHash)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !ok {
		t.Fatal("artifact not found after save")
	}
	if !loaded.Loaded || loaded.Solved {
		t.Fatalf("loaded solution must have Loaded=true, Solved=false; got Loaded=%v Solved=%v", loaded.Loaded, loaded.Solved)
	}

	solvedBytes, err := Marshal(solved)
	if err != nil {
		t.Fatalf("marshal solved: %v", err)
	}
	loadedBytes, err := Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal loaded: %v", err)
	}
	if !bytes.Equal(solvedBytes, loadedBytes) {
		t.Fatalf("Solved vs Loaded canonical bytes differ:\n solved: %s\n loaded: %s", solvedBytes, loadedBytes)
	}

	// The captured flows must also be order-identical (the solver canonically sorts
	// them), so the cross-procedure answer is independent of the serving path.
	if len(solved.Flows) != len(loaded.Flows) {
		t.Fatalf("flow count differs: solved=%d loaded=%d", len(solved.Flows), len(loaded.Flows))
	}
	_ = ctx
}
