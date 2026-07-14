package surfaces_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
)

// buildApplier wires the edit/refactor applier over the pinned fixture exactly as
// cmd/graphi's makeEditorClient does (ingest → applier with a parser-consistency
// checker), so the gate exercises the REAL refactor planner.
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

// treeDigest hashes every regular file under root (relative path + content), so
// a single mutated byte anywhere in the fixture tree changes the digest.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	h := sha256.New()
	for _, p := range files {
		rel, err := filepath.Rel(root, p)
		if err != nil {
			t.Fatalf("rel %s: %v", p, err)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestSAFE01_ExtractMove_FailClosed is the SW-112 (SAFE-01) gate that replaced
// the SW-110 AC6 expected-red characterization
// (TestCharacterization_ExtractMove_NameRewrite_ExpectedRed): historically
// planRefactor funneled extract AND move through the SAME planNameRewrite pass
// as rename, so both blindly rewrote every OldName→NewName occurrence — wrong
// semantics presented as success.
//
// Since SW-112 the contract is FAIL CLOSED: extract and move are rejected with
// the typed edit.ErrNotImplemented BEFORE any graph read or source write, for
// previews and commits alike, across the real surface wiring. This test proves
// the rejection AND that nothing in the pinned fixture tree was mutated, while
// rename keeps planning a real name rewrite (the planner itself still works).
func TestSAFE01_ExtractMove_FailClosed(t *testing.T) {
	applier, helloID := buildApplier(t)
	ctx := context.Background()
	fixture := charFixtureDir(t)
	digestBefore := treeDigest(t, fixture)

	// Sanity guard: the implemented rename path still plans a real name rewrite
	// (preview only — the fixture is checked-in corpus and must never be edited).
	renameRes, err := applier.ApplyRefactor(ctx, edit.RefactorOp{
		Kind:         edit.RefactorRename,
		TargetSymbol: helloID,
		OldName:      "Hello",
		NewName:      "Greeting",
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("rename preview: %v", err)
	}
	if len(renameRes.PlannedOps) == 0 {
		t.Fatalf("rename preview produced no planned edits; the fixture must have rewritable occurrences of Hello")
	}

	for _, kind := range []edit.RefactorKind{edit.RefactorExtract, edit.RefactorMove} {
		for _, dryRun := range []bool{true, false} {
			mode := map[bool]string{true: "preview", false: "commit"}[dryRun]
			res, err := applier.ApplyRefactor(ctx, edit.RefactorOp{
				Kind:            kind,
				TargetSymbol:    helloID,
				OldName:         "Hello",
				NewName:         "Greeting",
				DestinationFile: "moved.go",
				DryRun:          dryRun,
			})
			if !errors.Is(err, edit.ErrNotImplemented) {
				t.Errorf("%s %s: want edit.ErrNotImplemented, got err=%v", kind, mode, err)
			}
			if len(res.PlannedOps) != 0 || len(res.ImpactFiles) != 0 {
				t.Errorf("%s %s: rejected op leaked planned work: ops=%d impact=%d", kind, mode, len(res.PlannedOps), len(res.ImpactFiles))
			}
		}
	}

	if got := treeDigest(t, fixture); got != digestBefore {
		t.Fatalf("fixture tree mutated by rejected extract/move — the fail-closed contract is broken")
	}
}
