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
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/engine/typeresolve"
)

// EnvNoTyperesolve is the kill switch for the semantic confirmed-tier pass:
// set GRAPHI_NO_TYPERESOLVE=1 to skip it entirely (heuristic edges are then
// the final word, exactly the pre-v0.2.0 behavior). Any non-empty value other
// than "0" disables the pass.
const EnvNoTyperesolve = "GRAPHI_NO_TYPERESOLVE"

func typeresolveDisabled() bool {
	v := os.Getenv(EnvNoTyperesolve)
	return v != "" && v != "0"
}

// envNoTyperesolveLang is the per-language kill switch (ADR 0007):
// GRAPHI_NO_TYPERESOLVE_<LANG> (language id uppercased) disables one
// registrant while the global switch keeps its whole-pass meaning. Same value
// semantics as the global switch.
func envNoTyperesolveLang(lang string) string {
	return EnvNoTyperesolve + "_" + strings.ToUpper(lang)
}

func semanticResolverDisabled(lang string) bool {
	v := os.Getenv(envNoTyperesolveLang(lang))
	return v != "" && v != "0"
}

// typeresolveKind reports whether kind is one of the edge kinds the
// typeresolve pass emits (and therefore reconciles). Deliberately narrower
// than the linker's sweep set: imports/inherits/overrides are never confirmed
// by the semantic pass and must not be touched by its reconciliation.
func typeresolveKind(kind string) bool {
	return kind == "calls" || kind == "references" || kind == "implements"
}

// typeresolvePass is the third ingest phase (after parse-commit and linkFiles,
// at BOTH the full and the incremental site): the whole-repo semantic pass
// that turns name-heuristic knowledge into confirmed-tier knowledge where a
// registered resolver can prove it. Dispatch is registry-driven (ADR 0007,
// WP-J0): each resolver in i.semantic runs the identical
// gate → read → resolve → reconcile body over its own language's files. Today
// the registry holds exactly the go/types resolver, so behavior is
// byte-identical to the pre-seam Go-only pass.
//
// Design (parity by construction): each resolver always recomputes over the
// ENTIRE walked snapshot, so its output is a pure function of the final source
// state and the committed node set — full-vs-incremental byte parity needs no
// per-file bookkeeping. Memoization can be layered underneath later without
// changing observable behavior.
//
// Returns the ids of the edges it put, so the incremental site can funnel
// them into the edit-provenance side-channel like the linker's edges. The
// result is nil exactly when NO resolver completed a run (disabled, fast
// profile, no subject files, or a skipped re-read) — the distinction
// TestTyperesolvePass_SkipsOnFailedReread pins.
func (i *Ingester) typeresolvePass(ctx context.Context, w graphstore.Writer, root string, units []fileUnit) ([]string, error) {
	if typeresolveDisabled() || i.profile == profile.Fast {
		return nil, nil
	}
	var ids []string
	for _, res := range i.semantic.Resolvers() {
		if semanticResolverDisabled(res.Language()) {
			continue
		}
		resIDs, err := i.semanticResolve(ctx, w, root, units, res)
		if err != nil {
			return nil, err
		}
		if resIDs == nil {
			continue // this resolver skipped: no subjects, or a failed re-read
		}
		if ids == nil {
			ids = []string{}
		}
		ids = append(ids, resIDs...)
	}
	return ids, nil
}

// semanticResolve runs one resolver's whole-repo pass: the exact body the
// Go-only pass had before the registry seam, parameterized by the resolver's
// path predicates and Resolve call.
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
//   - THE SWEEP UNIT IS (DIRECTORY, LANGUAGE), not the directory (ADR 0008
//     ruling D9). An edge is a candidate only when its from-node's source file
//     is in THIS resolver's Owns set, so a directory holding two languages is
//     two sweep units and no registrant can reach the sibling's edges. Before
//     D9 the JVM registrants dodged that by marking a mixed directory degraded
//     — an exemption nothing counted, which kept superseded confirmed edges
//     alive across every incremental sync.
func (i *Ingester) semanticResolve(ctx context.Context, w graphstore.Writer, root string, units []fileUnit, res typeresolve.Resolver) ([]string, error) {
	hasSubject := false
	for _, u := range units {
		if res.Subject(u.relPath) {
			hasSubject = true
			break
		}
	}
	if !hasSubject {
		return nil, nil // no subject files for this resolver: skip the store scans
	}
	// Re-read only what the resolver consumes (its Input predicate). Units
	// carry no bytes, and a whole-unit-list map would hold every file of the
	// repo — assets included — resident for the entire pass.
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("ingest: typeresolve open root: %w", err)
	}
	defer rootHandle.Close()
	files := make(map[string][]byte)
	for _, u := range units {
		if !res.Input(u.relPath) {
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
			// next pass re-runs it over a stable tree. The flag additionally
			// blocks this pass's wholesale package-evidence replace (see
			// semanticEvidenceReady) for the same reason.
			i.semanticReadFailure = true
			return nil, nil
		}
		files[u.relPath] = read.src
	}

	// Stream the committed node set straight from the durable layer into the
	// two derived maps — no whole-graph slice, no cache mirror.
	committed := make(map[model.NodeId]struct{})
	dirOf := make(map[model.NodeId]string)
	// sweepDirOf is dirOf RESTRICTED to the nodes this resolver owns — the
	// (directory, language) sweep key of ADR 0008 D9. A node missing from it is
	// another language's and is not this pass's to delete, whatever its
	// directory. It is a second map rather than a language field on the node
	// because model.Node carries no language: a node's language is a property
	// of its source path, and Owns is the registrant's own statement of which
	// paths are its.
	sweepDirOf := make(map[model.NodeId]string)
	if err := graphstore.ForEachNode(ctx, i.store, func(n model.Node) error {
		committed[n.ID()] = struct{}{}
		dir := path.Dir(n.SourcePath())
		dirOf[n.ID()] = dir
		if res.Owns(n.SourcePath()) {
			sweepDirOf[n.ID()] = dir
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("ingest: typeresolve read nodes: %w", err)
	}

	result, err := res.Resolve(files, committed)
	if err != nil {
		return nil, fmt.Errorf("ingest: typeresolve: %w", err)
	}
	// Retain the compact trust summary at the ONE point the full Result exists;
	// the Result itself stays transient. Every early return above records no
	// run — a skipped resolver claims no facts. The per-package evidence rows
	// (P1 WP1.2, PRD §14.3/§22) are folded here too; presence in semanticRuns
	// tells the evidence writer these rows are a complete whole-repo recompute
	// (safe to replace the persisted set) rather than a skipped pass's zero
	// value. Facts are keyed PER LANGUAGE and folded back into the single-slot
	// snapshot/persistence shapes by trust_fold.go, whose single-language case
	// is the identity — the pre-seam bytes exactly.
	i.recordSemanticRun(res.Language(), semanticRun{
		facts:    trust.NewTypeResolutionFacts(result),
		evidence: packageEvidenceFromResult(res.Language(), result, dirOf),
		skips:    result.NamedSkips,
	})

	checkedDirs := make(map[string]struct{}, len(result.Units))
	for _, u := range result.Units {
		if u.Degraded == "" {
			checkedDirs[u.Dir] = struct{}{}
		}
	}
	fresh := make(map[model.EdgeId]struct{}, len(result.Edges))
	for _, e := range result.Edges {
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
		dir, owned := sweepDirOf[e.From()]
		if !owned {
			return nil // another language's edge (ADR 0008 D9): not this pass's to delete
		}
		if _, checked := checkedDirs[dir]; !checked {
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

	ids := make([]string, 0, len(result.Edges))
	for _, e := range result.Edges {
		if err := w.PutEdge(ctx, e); err != nil {
			return nil, fmt.Errorf("ingest: typeresolve put edge %s: %w", e.ID(), err)
		}
		ids = append(ids, string(e.ID()))
	}
	return ids, nil
}

// semanticTriggers reports whether any (re)processed path can change any
// registered resolver's resolution result — the registry-driven form of the
// old touchesGoResolution gate. Deliberately evaluated WITHOUT consulting the
// kill switches: the pass itself is the single place that honors them, so a
// disabled pass costs one no-op call rather than duplicating switch logic
// here. The per-language reasoning (why go.mod triggers, why _test.go does
// not) lives on each resolver's Triggers predicate.
func (i *Ingester) semanticTriggers(paths map[string]struct{}) bool {
	for p := range paths {
		for _, r := range i.semantic.Resolvers() {
			if r.Triggers(p) {
				return true
			}
		}
	}
	return false
}
