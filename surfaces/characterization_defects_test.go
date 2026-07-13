package surfaces_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
)

// buildApplier wires the edit/refactor applier over the pinned fixture exactly as
// cmd/graphi's makeEditorClient does (ingest → applier with a parser-consistency
// checker), so the characterization exercises the REAL refactor planner.
func buildApplier(t *testing.T) (*edit.Applier, string) {
	t.Helper()
	ctx := context.Background()
	fixture := charFixtureDir(t)

	store := graphstore.NewMemStore()
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), t.TempDir())
	if err != nil {
		t.Fatalf("ingest.New: %v", err)
	}
	t.Cleanup(func() { _ = ing.Close() })
	if err := ing.IngestAll(ctx, fixture); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, e := ingest.New(fs, ingest.NewNotebookParser(parse.NewDefaultRegistry()), "")
		if e != nil {
			return nil, nil, nil, e
		}
		return fs, fi, func() { _ = fi.Close(); _ = fs.Close() }, nil
	})
	applier, err := edit.NewApplier(store, ing, fixture, checker)
	if err != nil {
		t.Fatalf("NewApplier: %v", err)
	}

	// Resolve the fixture symbol we drive the refactor against.
	helloID := findFuncID(t, store, "Hello")
	return applier, helloID
}

func normalizeOps(ops []edit.EditOp) []string {
	out := make([]string, len(ops))
	for i, o := range ops {
		out[i] = fmt.Sprintf("%s [%d,%d)=%q", o.FilePath, o.ByteSpan.Start, o.ByteSpan.End, string(o.Replacement))
	}
	return out
}

func opsEqual(a, b []edit.EditOp) bool {
	na, nb := normalizeOps(a), normalizeOps(b)
	if len(na) != len(nb) {
		return false
	}
	for i := range na {
		if na[i] != nb[i] {
			return false
		}
	}
	return true
}

// TestCharacterization_ExtractMove_NameRewrite_ExpectedRed is the SW-110 AC6
// explicitly-documented EXPECTED-RED characterization of a KNOWN DEFECT:
// `extract` and `move` are NOT implemented as their own operations — planRefactor
// funnels rename, signature_change, extract AND move through the SAME
// planNameRewrite pass (engine/edit/refactor.go), so an `extract`/`move` preview
// blindly rewrites every occurrence of OldName→NewName across the blast radius,
// exactly as a `rename` would. That is wrong: extracting a region should mint a
// NEW symbol and moving one should relocate it — neither should rename call sites.
//
// This test PINS the defective state: it asserts an extract preview and a move
// preview produce edit plans BYTE-IDENTICAL to a rename preview (a global name
// rewrite). It does NOT fix the defect (SW-112 / SAFE-01 owns that) and it does
// NOT make the suite red — it is "expected-red" in the documented sense: it
// captures the red/defective behavior as the current, reviewed expectation and
// FAILS LOUDLY the moment extract/move stop mirroring rename (i.e. when the fix
// lands), forcing the fix owner to update this baseline.
func TestCharacterization_ExtractMove_NameRewrite_ExpectedRed(t *testing.T) {
	applier, helloID := buildApplier(t)
	ctx := context.Background()

	preview := func(kind edit.RefactorKind) []edit.EditOp {
		res, err := applier.ApplyRefactor(ctx, edit.RefactorOp{
			Kind:         kind,
			TargetSymbol: helloID,
			OldName:      "Hello",
			NewName:      "Greeting",
			DryRun:       true,
		})
		if err != nil {
			t.Fatalf("%s preview: %v", kind, err)
		}
		return res.PlannedOps
	}

	rename := preview(edit.RefactorRename)
	if len(rename) == 0 {
		t.Fatalf("rename preview produced no planned edits; the fixture must have rewritable occurrences of Hello")
	}
	// Sanity: the rename really is a NAME rewrite (every op writes the new spelling).
	for _, op := range rename {
		if string(op.Replacement) != "Greeting" {
			t.Fatalf("expected rename to write %q, got %q at %s", "Greeting", string(op.Replacement), op.FilePath)
		}
	}

	extract := preview(edit.RefactorExtract)
	move := preview(edit.RefactorMove)

	if !opsEqual(extract, rename) {
		t.Errorf("KNOWN-DEFECT CHARACTERIZATION FLIPPED: `extract` no longer mirrors `rename`'s name rewrite. "+
			"The extract/move name-rewrite defect appears FIXED — update this baseline and close it against SW-112 (SAFE-01).\n"+
			" rename =%v\n extract=%v", normalizeOps(rename), normalizeOps(extract))
	}
	if !opsEqual(move, rename) {
		t.Errorf("KNOWN-DEFECT CHARACTERIZATION FLIPPED: `move` no longer mirrors `rename`'s name rewrite. "+
			"The extract/move name-rewrite defect appears FIXED — update this baseline and close it against SW-112 (SAFE-01).\n"+
			" rename=%v\n move  =%v", normalizeOps(rename), normalizeOps(move))
	}

	if !t.Failed() {
		t.Logf("characterized defect: extract & move both blindly rewrite %d occurrences Hello→Greeting, identical to rename (SW-112 fail-closes this)", len(rename))
	}
}
