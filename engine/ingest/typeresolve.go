package ingest

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/typeresolve"
)

// EnvNoTyperesolve is the kill switch for the go/types confirmed-tier pass:
// set GRAPHI_NO_TYPERESOLVE=1 to skip it entirely (heuristic edges are then
// the final word, exactly the pre-v0.2.0 behavior). Any non-empty value other
// than "0" disables the pass.
const EnvNoTyperesolve = "GRAPHI_NO_TYPERESOLVE"

func typeresolveDisabled() bool {
	v := os.Getenv(EnvNoTyperesolve)
	return v != "" && v != "0"
}

// typeresolveKind reports whether kind is one of the edge kinds the
// typeresolve pass emits (and therefore reconciles). Deliberately narrower
// than the linker's sweep set: imports/inherits/overrides are never confirmed
// by the type-check pass and must not be touched by its reconciliation.
func typeresolveKind(kind string) bool {
	return kind == "calls" || kind == "references" || kind == "implements"
}

// typeresolvePass is the third ingest phase (after parse-commit and linkFiles,
// at BOTH the full and the incremental site): the whole-repo go/types pass
// that turns name-heuristic knowledge into confirmed-tier knowledge where the
// type-checker can prove it.
//
// Design (parity by construction): the pass always recomputes over the ENTIRE
// walked snapshot, so its output is a pure function of the final source state
// and the committed node set — full-vs-incremental byte parity needs no
// per-file bookkeeping. Memoization can be layered underneath later without
// changing observable behavior.
//
// Reconciliation contract with the store:
//   - Fresh confirmed edges are upserted. A confirmed edge shares its
//     (from,to,kind) EdgeId with the heuristic/derived edge for the same
//     logical relation, so PutEdge REPLACES the weaker tier: confirmed wins.
//   - A stored confirmed edge that the pass no longer emits is stale ONLY if
//     its from-node's package unit was successfully checked this pass (the
//     pass is authoritative for checked units). Those are deleted — the
//     heuristic layer for any reprocessed file was already re-put by
//     linkFiles BEFORE this pass, and an upserted heuristic edge carries the
//     heuristic tier again, so it is invisible to this sweep.
//   - A unit that DEGRADED (parse failure, import cycle, checker panic) is
//     skipped by the sweep: degradation never deletes knowledge. Its symbols
//     keep whatever the store holds — heuristic edges from linkFiles, or
//     prior confirmed edges when the unit's files were not reprocessed.
//
// Returns the ids of the edges it put, so the incremental site can funnel
// them into the edit-provenance side-channel like the linker's edges.
func (i *Ingester) typeresolvePass(ctx context.Context, w graphstore.Writer, root string, units []fileUnit) ([]string, error) {
	if typeresolveDisabled() || i.profile == profile.Fast {
		return nil, nil
	}
	hasGo := false
	for _, u := range units {
		if strings.HasSuffix(u.relPath, ".go") && !strings.HasSuffix(u.relPath, "_test.go") {
			hasGo = true
			break
		}
	}
	if !hasGo {
		return nil, nil // non-Go repo: no units to check, skip the store scans
	}
	// Re-read only what the resolver consumes: Go sources (including _test.go,
	// whose PATHS steer GroupPackages' skip bookkeeping) and go.mod. Units
	// carry no bytes, and the old whole-unit-list map held every file of the
	// repo — assets included — resident for the entire pass.
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: typeresolve open root: %w", err)
	}
	defer rootHandle.Close()
	files := make(map[string][]byte)
	for _, u := range units {
		if u.relPath != "go.mod" && !strings.HasSuffix(u.relPath, ".go") {
			continue
		}
		read := readRootedRegularFile(rootHandle, u.relPath, i.bounds.MaxFileSize)
		if read.reason != "" {
			// A file the walk just saw failed to re-read (vanished or grew
			// mid-pass). Missing INPUT must not shrink the fresh edge set
			// while units still check "non-degraded": most destructively, a
			// missing go.mod blanks the module path, every unit still checks
			// clean against stub imports, and the stale-confirmed sweep below
			// would then delete EVERY cross-package confirmed edge. Skip the
			// whole pass instead — degradation never deletes knowledge; the
			// next pass re-runs it over a stable tree.
			return nil, nil
		}
		files[u.relPath] = read.src
	}

	// Stream the committed node set straight from the durable layer into the
	// two derived maps — no whole-graph slice, no cache mirror.
	committed := make(map[model.NodeId]struct{})
	dirOf := make(map[model.NodeId]string)
	if err := graphstore.ForEachNode(ctx, i.store, func(n model.Node) error {
		committed[n.ID()] = struct{}{}
		dirOf[n.ID()] = path.Dir(n.SourcePath())
		return nil
	}); err != nil {
		return nil, fmt.Errorf("ingest: typeresolve read nodes: %w", err)
	}

	res, err := typeresolve.Resolve(files, committed)
	if err != nil {
		return nil, fmt.Errorf("ingest: typeresolve: %w", err)
	}

	checkedDirs := make(map[string]struct{}, len(res.Units))
	for _, u := range res.Units {
		if u.Degraded == "" {
			checkedDirs[u.Dir] = struct{}{}
		}
	}
	fresh := make(map[model.EdgeId]struct{}, len(res.Edges))
	for _, e := range res.Edges {
		fresh[e.ID()] = struct{}{}
	}

	// Sweep stale confirmed edges of checked units (see the contract above).
	// Stream the edge scan and collect only the STALE ids; deletes run after
	// the cursor closes (collect-then-delete, matching linkFiles), in the same
	// EdgeId-ascending order the old slice iteration used.
	var stale []model.EdgeId
	if err := graphstore.ForEachEdge(ctx, i.store, func(e model.Edge) error {
		if e.Tier() != model.TierConfirmed || !typeresolveKind(e.Kind()) {
			return nil
		}
		if _, current := fresh[e.ID()]; current {
			return nil
		}
		if _, checked := checkedDirs[dirOf[e.From()]]; !checked {
			return nil // degraded or unknown unit: degradation never deletes knowledge
		}
		stale = append(stale, e.ID())
		return nil
	}); err != nil {
		return nil, fmt.Errorf("ingest: typeresolve read edges: %w", err)
	}
	for _, id := range stale {
		if err := w.DeleteEdge(ctx, id); err != nil {
			return nil, fmt.Errorf("ingest: typeresolve delete stale confirmed edge %s: %w", id, err)
		}
	}

	ids := make([]string, 0, len(res.Edges))
	for _, e := range res.Edges {
		if err := w.PutEdge(ctx, e); err != nil {
			return nil, fmt.Errorf("ingest: typeresolve put edge %s: %w", e.ID(), err)
		}
		ids = append(ids, string(e.ID()))
	}
	return ids, nil
}

// touchesGoResolution reports whether any (re)processed path can change the
// go/types resolution result: a Go source file (test files cannot — the
// typeresolve grouping skips them in v1 — but a rename between _test and
// non-test arrives as the non-test path anyway, so plain .go matching stays
// correct and cheap) or the root go.mod (the module path steers intra-repo
// import resolution).
func touchesGoResolution(paths map[string]struct{}) bool {
	for p := range paths {
		if p == "go.mod" {
			return true
		}
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			return true
		}
	}
	return false
}
