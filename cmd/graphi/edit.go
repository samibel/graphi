package main

import (
	"context"
	"fmt"
	"os"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/edit"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
)

// runRefactor launches the SW-038 edit/refactor command surface (refactor-preview
// / refactor / undo). It builds an in-process client with a fully-wired edit
// applier + change recorder over the repo at -root, ingested into the store at
// -db with its ingest-meta sidecar under -meta. The CLI surface holds no engine
// logic — it parses flags and calls the shared client.
//
//	graphi <verb> -root <repo> [-db path] [-meta dir] <verb-flags>
func runRefactor(args []string, verb string) int {
	root, dbPath, metaDir, rest := extractEditFlags(args)
	if root == "" {
		fmt.Fprintln(os.Stderr, "graphi: -root <repo> is required for edit/refactor commands")
		return 1
	}
	c, cleanup, err := makeEditorClient(root, dbPath, metaDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v: %v\n", verb, err)
		return 1
	}
	defer cleanup()

	switch verb {
	case "refactor-preview":
		err = cli.RunRefactorPreview(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "refactor":
		err = cli.RunRefactor(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "undo":
		err = cli.RunUndo(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "inline":
		err = cli.RunInline(context.Background(), c, rest, os.Stdout, os.Stderr)
	case "safe-delete":
		err = cli.RunSafeDelete(context.Background(), c, rest, os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %s: %v\n", verb, err)
		return 1
	}
	return 0
}

// extractEditFlags pulls -root, -db, -meta off the front of args (the rest are
// the verb-specific flags).
func extractEditFlags(args []string) (root, dbPath, metaDir string, rest []string) {
	rest = args
	take := func(name string) (string, bool) {
		if len(rest) >= 2 && rest[0] == name {
			v := rest[1]
			rest = rest[2:]
			return v, true
		}
		if len(rest[0]) > len(name)+1 && rest[0][:len(name)+1] == name+"=" {
			v := rest[0][len(name)+1:]
			rest = rest[1:]
			return v, true
		}
		return "", false
	}
	for len(rest) > 0 {
		if v, ok := take("-root"); ok {
			root = v
			continue
		}
		if v, ok := take("-db"); ok {
			dbPath = v
			continue
		}
		if v, ok := take("-meta"); ok {
			metaDir = v
			continue
		}
		break
	}
	return root, dbPath, metaDir, rest
}

// makeEditorClient builds an in-process client with the edit/refactor command
// surface wired (SW-038): it opens the store, constructs an Ingester over the
// default parser registry + meta sidecar, performs an initial full ingest of the
// repo, builds the Applier with a consistency checker and a ChangeRecorder, and
// attaches them via Direct.WithEditor. The returned cleanup releases all handles.
func makeEditorClient(root, dbPath, metaDir string) (client.Client, func(), error) {
	ctx := context.Background()
	store, err := openStore(dbPath)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open store: %w", err)
	}
	reg := parse.NewDefaultRegistry()
	ing, err := ingest.New(store, ingest.NewNotebookParser(reg), metaDir)
	if err != nil {
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("ingest: %w", err)
	}
	if err := ing.IngestAll(ctx, root); err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("initial ingest: %w", err)
	}
	checker := edit.NewParserConsistencyChecker(func() (graphstore.Graphstore, *ingest.Ingester, func(), error) {
		fs := graphstore.NewMemStore()
		fi, ierr := ingest.New(fs, ingest.NewNotebookParser(parse.NewDefaultRegistry()), "")
		if ierr != nil {
			return nil, nil, nil, ierr
		}
		return fs, fi, func() { _ = fi.Close(); _ = fs.Close() }, nil
	})
	applier, err := edit.NewApplier(store, ing, root, checker)
	if err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("applier: %w", err)
	}
	recorder, err := edit.NewChangeRecorder(ctx, ing, metaDir)
	if err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, func() {}, fmt.Errorf("change recorder: %w", err)
	}
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithEditor(applier, recorder)
	cleanup := func() { _ = ing.Close(); _ = store.Close() }
	return c, cleanup, nil
}
