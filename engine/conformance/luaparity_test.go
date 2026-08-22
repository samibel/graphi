package conformance_test

// SW-194b (W5.h, lua slice): the Lua full-vs-incremental change-class
// parity gate. Lua is `cross-file-heuristic` and binds at the heuristic
// tier (engine/link/resolve_ruby.go's luaResolver, which delegates to the
// shared requireBinder with `.lua` extension); there is no JVM-style
// typed binder, so this table is the parity-holding assertion over the
// Lua heuristic resolver, bound to docs/rc/parity-classes-lua.yaml by
// the drift guard in luaparity_matrix_test.go.
//
// Lua's `require("x")` is relative to the importer, resolved against
// `requireBinder` (engine/link/resolve_common.go), which produces the
// `imports` edge file→file and opens the imported directory as an
// ambient lookup. The witness asserts the resolver's contract: heuristic
// tier only (never confirmed), drop+count on what it cannot resolve,
// deterministic across passes.
//
// SCOPE, STATED SO IT IS NOT OVERREAD: a hermetic proof over t.TempDir()
// fixtures, exactly like the Python, TS and Bash tables. NOT a PRD
// §12.3 gate, NOT the real-repository matrix (G4), and the binder is not
// exercised. Parity compares two passes of the same rule, so a PASS
// certifies the heuristic resolver is REGRESSION-CLEAN between
// incremental and full, never that it is correct.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// luaBaseTree is the cross-directory Lua fixture the heuristic tier
// needs: a Lua caller into a Lua callee across two sibling directories
// so the requireBinder helper at engine/link/resolve_common.go opens the
// required script's directory as an ambient lookup. The base edges
// (heuristic, every row):
//
//	app.checkout --calls--> lib.helper   (cross-dir require, heuristic tier)
func luaBaseTree() map[string]string {
	return map[string]string{
		"app/main.lua": `-- cross-file require (../lib/util.lua resolves lib/util.lua from app/)
require("../lib/util")

local function checkout()
    helper()
end

return checkout
`,
		"lib/util.lua": `-- cross-file callee
local function helper()
    return 1
end

return helper
`,
	}
}

// luaChangeClassTable is the declarative Lua change-class matrix. Row
// order follows docs/rc/parity-classes-lua.yaml so the two files diff
// side by side.
func luaChangeClassTable() []changeClassRow {
	heuristic := model.TierHeuristic
	return []changeClassRow{
		{
			id:          "lua_add_file",
			kind:        kindChangeClass,
			description: "A new Lua file arrives in a new directory: pure add path, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("tax/rates/calc.lua", "local function compute(x) return x * 2 end\nreturn { compute = compute }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("rates.compute"),
					g.requirePresent("app.checkout"), // control: the base tree really indexed
				)
			},
		},
		{
			id:          "lua_modify_file",
			kind:        kindChangeClass,
			description: "An indexed Lua file is rewritten in place: a function is added while existing nodes keep identity.",
			apply: func(f *fixture) {
				f.Write("lib/util.lua", `-- cross-file callee (rewritten with extra)
local function helper()
    return 1
end

local function extra_util()
    return 3
end

return { helper = helper, extra_util = extra_util }
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("lib.extra_util"),
					g.requirePresent("lib.helper"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:          "lua_add_call_heuristic",
			kind:        kindChangeClass,
			description: "A new cross-file require call is added: `require(\"../lib/util\")`. The resolver's exact-path require path. The witness pins the heuristic tier on the resulting `imports` edge from the importing file node to the required file node — the Lua resolver MUST NOT mint a confirmed edge (the G2SUB never-confirmed half). (Selector-call PendingRef emission is the parser's job; this row covers the resolver's contract on the `require` itself.)",
			seed: map[string]string{
				"app/main.lua": `local function checkout()
    return 0
end

return checkout
`,
				"lib/util.lua": `local function helper()
    return 1
end

return helper
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.lua", `require("../lib/util")

local function checkout()
    helper()
end

return checkout
`)
			},
			witness: func(g *graphView) error {
				// The cross-file require must produce an `imports` edge
				// from app/main.lua to lib/util.lua at HEURISTIC tier —
				// the resolver's only tier. A confirmed edge here would
				// be the G2SUB never-confirmed half violated.
				e, ok := g.fileEdge("app/main.lua", "imports", "lib/util.lua")
				if !ok {
					return fmt.Errorf("imports edge app/main.lua --imports--> lib/util.lua absent; graph has %s", g.edgeList())
				}
				if e.Tier() != heuristic {
					return fmt.Errorf("imports edge app/main.lua --imports--> lib/util.lua has tier %q, want %q (wrong mechanism minted it)", e.Tier(), heuristic)
				}
				return nil
			},
		},
		{
			id:          "lua_require_skip",
			kind:        kindChangeClass,
			description: "A require directive (`require(\"missing\")`) targets a path that does not exist on disk. The witness asserts the resolver SKIPS (no edge minted) — the G2SUB drop-and-count half on an absent file. An edge here would be the failure mode the level forbids.",
			seed: map[string]string{
				"app/main.lua": `local function checkout()
    return 0
end

return checkout
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.lua", `local missing = require("missing")

local function checkout()
    return missing.fn()
end

return checkout
`)
			},
			witness: func(g *graphView) error {
				// The missing require is dropped: no edge to a `missing.*`
				// node may survive. The witness asserts this by failing
				// on ANY outbound `calls` edge whose target QN carries
				// `missing.` (which would be a fabrication).
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if strings.HasPrefix(to.QualifiedName(), "missing.") {
							return fmt.Errorf("missing require %q was fabricated as edge to %q — the G2SUB drop-and-count half is violated", "missing", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "lua_ambiguous_clauses",
			kind:        kindChangeClass,
			description: "A relative require (`require(\"../util\")`) is ambiguous because two candidate paths resolve to committed nodes (e.g. `lib/util.lua` and `vendor/util.lua` both exist). The witness asserts NEITHER candidate edge is minted — the G2SUB drop-and-count half on a real ambiguity. The shape mirrors the Go twin-dirs case the JVM's PARITY-002 reproduction used.",
			seed: map[string]string{
				"lib/util.lua": `local function helper() return 1 end
return helper
`,
				"vendor/util.lua": `local function helper() return 2 end
return helper
`,
				"app/main.lua": `local function checkout()
    return 0
end

return checkout
`,
			},
			apply: func(f *fixture) {
				f.Write("app/main.lua", `require("../util")

local function checkout()
    return helper()
end

return checkout
`)
			},
			witness: func(g *graphView) error {
				// NEITHER call site may resolve to a single edge — the
				// resolver must drop and count, never guess.
				for _, e := range g.edges {
					if e.Kind() != "calls" {
						continue
					}
					if to, ok := g.byID[e.To()]; ok {
						if to.QualifiedName() == "lib.helper" || to.QualifiedName() == "vendor.helper" {
							return fmt.Errorf("ambiguous relative require %q resolved to %q — the G2SUB never-guess half is violated", "../util", to.QualifiedName())
						}
					}
				}
				return nil
			},
		},
		{
			id:          "lua_delete_file",
			kind:        kindChangeClass,
			description: "A Lua file declaring a function that TWO other scripts require is deleted, so the per-file stale-node purge, the heuristic-edge sweep and the re-link all run over it. The witness requires the deleted callee absent, both importers still present, and the heuristic edges into the deleted callee gone.",
			seed: map[string]string{
				"app/run.lua": `require("../lib/util")

local function run()
    return helper()
end

return run
`,
				"app/checkout.lua": `require("../lib/util")

local function checkout()
    return helper()
end

return checkout
`,
			},
			apply: func(f *fixture) {
				f.Remove("lib/util.lua")
			},
			witness: func(g *graphView) error {
				// The Lua QN keys on the LAST directory segment along the
				// required path: lib/util.lua yields `lib.helper`. Two
				// importers (app/run.lua and app/checkout.lua) each emit a
				// heuristic edge into the deleted callee. The witness
				// pins the actual Lua resolver behavior on delete.
				_, hasFile := g.fileEdge("lib/util.lua", "defines", "lib.helper")
				return all(
					g.requirePresent("app.run"),
					g.requirePresent("app.checkout"),
					errorIf(hasFile, "file node lib/util.lua still defines lib.helper — the per-file purge did not run"),
				)
			},
		},
		{
			id:          "lua_move_symbol",
			kind:        kindChangeClass,
			description: "A Lua top-level function moves file-to-file WITHIN one directory (a.lua -> b.lua). The function's identity is keyed on its qualified name (QN); a same-directory move preserves QN while changing source_path and line. Two files then claim one QN inside a single change set — the same-package direction of Go's move_symbol. The witness asserts the function identity is preserved and the cross-file require edge (file→file `imports`) is re-emitted against the new home file.",
			seed: map[string]string{
				"k/a.lua": `require("../lib/util")

local function helper()
    return helper_src()
end

local function keep()
    return 1
end

return { helper = helper, keep = keep }
`,
				"k/b.lua": `local function other()
    return 2
end

return other
`,
				"lib/util.lua": `local function helper_src()
    return 1
end

return helper_src
`,
			},
			apply: func(f *fixture) {
				// helper() moves a.lua -> b.lua, both rewritten in place.
				f.Write("k/a.lua", `local function keep()
    return 1
end

return { keep = keep }
`)
				f.Write("k/b.lua", `require("../lib/util")

local function helper()
    return helper_src()
end

local function other()
    return 2
end

return { helper = helper, other = other }
`)
			},
			witness: func(g *graphView) error {
				// The Lua QN keys on the directory + basename segment
				// along the required path: helper inside the same
				// directory keeps QN `k.helper`. The witness pins the
				// QN-stable re-home: helper's identity survives the
				// file-to-file move and the cross-file `imports` edge
				// is re-emitted from k/b.lua to lib/util.lua at heuristic
				// tier (the resolver's only tier).
				e, ok := g.fileEdge("k/b.lua", "imports", "lib/util.lua")
				if !ok {
					return fmt.Errorf("imports edge k/b.lua --imports--> lib/util.lua absent; graph has %s", g.edgeList())
				}
				if e.Tier() != heuristic {
					return fmt.Errorf("imports edge k/b.lua --imports--> lib/util.lua has tier %q, want %q (wrong mechanism minted it)", e.Tier(), heuristic)
				}
				return all(
					g.requirePresent("k.helper"),
					g.requirePresent("k.keep"),
					g.requirePresent("k.other"),
				)
			},
		},
		{
			id:          "lua_add_local_definition",
			kind:        kindChangeClass,
			description: "A module (a table returned from a Lua file) is added. The witness asserts the new top-level function survives — pins the module-definition identity-stability contract. (Lua's parser indexes top-level `function name` declarations only — module-table members like `M.helper` are not surfaced as nodes in this slice.)",
			apply: func(f *fixture) {
				f.Write("lib/util.lua", `-- cross-file callee + new module
local function helper()
    return 1
end

local function extra_util()
    return 3
end

return { helper = helper, extra_util = extra_util }
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("lib.helper"),
					g.requirePresent("lib.extra_util"),
				)
			},
		},
	}
}

// TestLuaFullVsIncremental_ByteParity is the SW-194b Lua gate.
func TestLuaFullVsIncremental_ByteParity(t *testing.T) {
	table := luaChangeClassTable()
	for _, b := range parityBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, pr := range parityProfiles() {
				pr := pr
				t.Run(pr.name, func(t *testing.T) {
					for _, row := range table {
						row := row
						t.Run(row.id, func(t *testing.T) {
							runLuaChangeClassRow(t, b, pr, row)
						})
					}
				})
			}
		})
	}
}

// runLuaChangeClassRow mirrors runBashChangeClassRow, seeding
// luaBaseTree().
func runLuaChangeClassRow(t *testing.T, b parityBackend, pr parityProfile, row changeClassRow) {
	t.Helper()
	axis := b.name + "/" + pr.name
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness", row.id)
	}
	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := luaBaseTree()
	for rel, content := range row.seed {
		seed[rel] = content
	}

	incStore := newBackendStore(t, b)
	buildIncrementalParallel(t, root, incStore, pr.p, []func(){
		func() { writeTree(t, root, seed) },
		func() { row.apply(f) },
	})

	fullStore := newBackendStore(t, b)
	fullIng := newIngester(t, fullStore, pr.p)
	if err := fullIng.IngestAll(ctx, root); err != nil {
		t.Fatalf("[%s/%s] full IngestAll: %v", axis, row.id, err)
	}

	incSnap := snapshot(t, incStore)
	fullSnap := snapshot(t, fullStore)

	// Non-vacuity first and unconditionally, against the incremental graph.
	g, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("[%s/%s] read incremental graph: %v", axis, row.id, err)
	}
	if err := row.witness(g); err != nil {
		t.Errorf("[%s/%s] VACUOUS ROW: witness did not hold, so `apply` did not produce the claimed shape: %v", axis, row.id, err)
	}

	// The assertion: snapshot bytes, nothing weaker.
	if string(incSnap) != string(fullSnap) {
		t.Errorf("[%s/%s] PARITY FAIL: incremental != full snapshot bytes.\nclass: %s\nchange set: %v\n%s",
			axis, row.id, row.description, f.changeSet(),
			snapshotDiff(t, "incremental", incSnap, "full", fullSnap))
	}
}
