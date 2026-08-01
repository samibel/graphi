package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// harnessTestFile / harnessTestName are the citation docs/rc/parity-classes.yaml
// must carry for every class this table proves. The drift guard's VERDICT
// direction asserts the YAML agrees with these, and that the cited line really
// holds that func declaration — so a refactor that moves the driver cannot leave
// the matrix pointing at a stale line.
const (
	harnessTestFile   = "engine/conformance/conformance_test.go"
	harnessTestName   = "TestFullVsIncremental_ByteParity"
	harnessSourceFile = "conformance_test.go"
)

// ---------------------------------------------------------------------------
// The fixture: a change-set-recording view of one row's t.TempDir() tree
// ---------------------------------------------------------------------------

// fixture is what a table row's `apply` mutates. It wraps the row's private
// t.TempDir() tree, delegates every mutation to the retained writeFile /
// removeFile primitives, and RECORDS the exact path set the change touched.
//
// The recording is not bookkeeping for its own sake: AC-6's idempotency
// assertion has to RE-APPLY the identical change set, and ingest's drift
// detection is content-hash based (engine/ingest/ingest.go:383 Drift), so
// rewriting identical bytes produces an empty drift set and Reconcile
// short-circuits at engine/watch/service.go:276. Re-application therefore has to
// name its paths explicitly, which means the row has to have recorded them.
type fixture struct {
	t       *testing.T
	Root    string
	written map[string]struct{}
	removed map[string]struct{}
}

func newFixture(t *testing.T, root string) *fixture {
	return &fixture{t: t, Root: root, written: map[string]struct{}{}, removed: map[string]struct{}{}}
}

// Write creates or replaces rel with content.
func (f *fixture) Write(rel, content string) {
	f.t.Helper()
	writeFile(f.t, f.Root, rel, content)
	f.written[rel] = struct{}{}
	delete(f.removed, rel)
}

// Remove deletes rel.
func (f *fixture) Remove(rel string) {
	f.t.Helper()
	removeFile(f.t, f.Root, rel)
	f.removed[rel] = struct{}{}
	delete(f.written, rel)
}

// Move relocates rel to dst, rewriting the content on the way — the primitive
// the move_symbol and rename_package rows are built from. It is deliberately
// remove+write rather than os.Rename, because that is what an editor or a `git
// mv` looks like to the drift scanner: one path disappears and another appears
// inside a single change set.
func (f *fixture) Move(rel, dst, content string) {
	f.t.Helper()
	f.Remove(rel)
	f.Write(dst, content)
}

// parseSet is the sorted list of paths that exist on disk after the change, and
// is what the parallel parse pool is handed.
func (f *fixture) parseSet() []string { return sortedKeys(f.written) }

// changeSet is the sorted union of written and removed paths — the argument
// shape ApplyChangedParsed expects (engine/watch/service.go:284-287 builds the
// same union).
func (f *fixture) changeSet() []string {
	all := append(sortedKeys(f.written), sortedKeys(f.removed)...)
	sort.Strings(all)
	return all
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The graph view a witness predicates over
// ---------------------------------------------------------------------------

// graphView is the resulting graph, read out of the store once so a witness can
// interrogate it without N store round-trips. `file` nodes are kept out of the
// qualified-name index (a file node's QN is its path and would collide with
// nothing useful), mirroring engine/ingest/typeresolve_test.go:35 edgeBetween.
type graphView struct {
	nodes []model.Node
	edges []model.Edge
	byQN  map[string]model.Node
	byID  map[model.NodeId]model.Node
}

func newGraphView(ctx context.Context, st graphstore.Graphstore) (*graphView, error) {
	nodes, err := st.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("read nodes: %w", err)
	}
	edges, err := st.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, fmt.Errorf("read edges: %w", err)
	}
	g := &graphView{
		nodes: nodes,
		edges: edges,
		byQN:  make(map[string]model.Node, len(nodes)),
		byID:  make(map[model.NodeId]model.Node, len(nodes)),
	}
	for _, n := range nodes {
		g.byID[n.ID()] = n
		if n.Kind() != "file" {
			g.byQN[n.QualifiedName()] = n
		}
	}
	return g, nil
}

func (g *graphView) node(qn string) (model.Node, bool) {
	n, ok := g.byQN[qn]
	return n, ok
}

// requirePresent fails unless a non-file node with this qualified name exists.
func (g *graphView) requirePresent(qns ...string) error {
	for _, qn := range qns {
		if _, ok := g.node(qn); !ok {
			return fmt.Errorf("node %q absent; graph has %s", qn, g.qnList())
		}
	}
	return nil
}

// requireAbsent fails if a node with this qualified name still exists.
func (g *graphView) requireAbsent(qns ...string) error {
	for _, qn := range qns {
		if n, ok := g.node(qn); ok {
			return fmt.Errorf("node %q still present (%s at %s:%d); graph has %s",
				qn, n.Kind(), n.SourcePath(), n.Line(), g.qnList())
		}
	}
	return nil
}

// edge finds fromQN --kind--> toQN, matching on qualified names so a witness
// never has to know a NodeId.
func (g *graphView) edge(fromQN, kind, toQN string) (model.Edge, bool) {
	from, okF := g.node(fromQN)
	to, okT := g.node(toQN)
	if !okF || !okT {
		return model.Edge{}, false
	}
	for _, e := range g.edges {
		if e.From() == from.ID() && e.To() == to.ID() && e.Kind() == kind {
			return e, true
		}
	}
	return model.Edge{}, false
}

func (g *graphView) requireEdge(fromQN, kind, toQN string) error {
	if _, ok := g.edge(fromQN, kind, toQN); !ok {
		return fmt.Errorf("edge %s --%s--> %s absent; graph has %s", fromQN, kind, toQN, g.edgeList())
	}
	return nil
}

func (g *graphView) requireNoEdge(fromQN, kind, toQN string) error {
	if e, ok := g.edge(fromQN, kind, toQN); ok {
		return fmt.Errorf("edge %s --%s--> %s still present (tier=%s reason=%q)",
			fromQN, kind, toQN, e.Tier(), e.Reason())
	}
	return nil
}

// requireEdgeAtTier is how a witness pins WHICH mechanism minted an edge. The
// two `implements` producers are distinguishable only by tier: the syntactic
// embed extractor (core/parse/extract_go.go) mints below confirmed, while
// go/types method-set satisfaction (engine/typeresolve/check.go:498) mints at
// model.TierConfirmed. add_implementation / remove_implementation exist to cover
// the SECOND mechanism, so their witnesses must not accept the first.
func (g *graphView) requireEdgeAtTier(fromQN, kind, toQN string, tier model.ConfidenceTier) error {
	e, ok := g.edge(fromQN, kind, toQN)
	if !ok {
		return fmt.Errorf("edge %s --%s--> %s absent; graph has %s", fromQN, kind, toQN, g.edgeList())
	}
	if e.Tier() != tier {
		return fmt.Errorf("edge %s --%s--> %s has tier %q, want %q (wrong mechanism minted it)",
			fromQN, kind, toQN, e.Tier(), tier)
	}
	return nil
}

// requireSourcePath pins a node's source_path, which is what makes a same-package
// move observable at all: a Go symbol's qualified name is package-qualified, so
// moving a declaration between files in one package preserves the NodeId and
// changes only source_path and line — both carried by nodeWire
// (core/model/serialize.go:82).
func (g *graphView) requireSourcePath(qn, want string) error {
	n, ok := g.node(qn)
	if !ok {
		return fmt.Errorf("node %q absent; graph has %s", qn, g.qnList())
	}
	if got := filepath.ToSlash(n.SourcePath()); got != want {
		return fmt.Errorf("node %q source_path = %q, want %q", qn, got, want)
	}
	return nil
}

// requireLine pins a node's declaration line.
func (g *graphView) requireLine(qn string, want int) error {
	n, ok := g.node(qn)
	if !ok {
		return fmt.Errorf("node %q absent; graph has %s", qn, g.qnList())
	}
	if n.Line() != want {
		return fmt.Errorf("node %q line = %d, want %d", qn, n.Line(), want)
	}
	return nil
}

// requireExternal / requireNoExternal assert on interned external nodes, which
// the linker mints as kind "external" with an empty source path
// (engine/link/link.go:265).
func (g *graphView) requireExternal(qn string) error {
	n, ok := g.node(qn)
	if !ok {
		return fmt.Errorf("external node %q absent; graph has %s", qn, g.qnList())
	}
	if n.Kind() != "external" {
		return fmt.Errorf("node %q has kind %q, want \"external\"", qn, n.Kind())
	}
	return nil
}

func (g *graphView) requireNoExternal(qn string) error {
	if n, ok := g.node(qn); ok && n.Kind() == "external" {
		return fmt.Errorf("external node %q was not swept", qn)
	}
	return nil
}

// all runs every predicate and returns the first failure, so a witness reads as
// a list of claims rather than a ladder of ifs.
func all(checks ...error) error {
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	return nil
}

func (g *graphView) qnList() string {
	qns := make([]string, 0, len(g.byQN))
	for qn, n := range g.byQN {
		qns = append(qns, fmt.Sprintf("%s(%s)", qn, n.Kind()))
	}
	sort.Strings(qns)
	return fmt.Sprint(qns)
}

func (g *graphView) edgeList() string {
	out := make([]string, 0, len(g.edges))
	for _, e := range g.edges {
		out = append(out, fmt.Sprintf("%s--%s->%s[%s]",
			g.byID[e.From()].QualifiedName(), e.Kind(), g.byID[e.To()].QualifiedName(), e.Tier()))
	}
	sort.Strings(out)
	return fmt.Sprint(out)
}

// ---------------------------------------------------------------------------
// The declared table
// ---------------------------------------------------------------------------

// changeClassRow is one row of the FR-7 change-class matrix, expressed as data.
// It replaces conformance_test.go's former hardcoded `steps []func()` literal,
// which carried no class labels and could not be joined to anything.
//
// Fields, and why each is required:
//
//	id           the frozen wire identifier from docs/rc/parity-classes.yaml. It
//	             is the drift guard's join key, so it is not a label — never
//	             rename it, change `description` instead.
//	kind         change_class | crash_condition, mirrored from the YAML so the
//	             KIND direction can catch a harness that counts a crash
//	             condition among its change classes.
//	description  what the row proves, in prose, INCLUDING what it does not prove.
//	deferredTo   non-empty means the row is a declared placeholder owned by
//	             another story. Such a row SKIPS; it never reports green.
//	seed         files added to baseTree() before the change, when the class
//	             needs shapes the base tree does not carry.
//	apply        the change itself, over a recording fixture.
//	witness      the NON-VACUITY predicate (AC-3). It must fail if `apply` did
//	             nothing. `apply` and `witness` are a pair: a row whose apply
//	             could be commented out with the witness still passing proves
//	             parity over an empty change and is worthless.
//	knownDefect  non-empty names a TRACKED PRODUCT DEFECT that makes this class
//	             genuinely non-parity today. It is not a waiver: see the long
//	             comment on runKnownDefectRow. It must equal the class's
//	             `known_defect` in docs/rc/parity-classes.yaml, and while it is
//	             set the drift guard makes it MECHANICALLY IMPOSSIBLE for that
//	             class to read verdict: "PROVEN".
//	pin          required exactly when knownDefect is set: the row-level pin of
//	             the CURRENT DIVERGENT — that is, WRONG — behaviour. It states
//	             precisely how the incremental graph differs from the full graph
//	             today, so the defect is published as executable data and any
//	             change to it, including a FIX, turns the row red.
//
// witness returns error rather than bool so a failure names what it expected;
// a bool witness reports only that something is wrong.
type changeClassRow struct {
	id          string
	kind        string
	description string
	deferredTo  string
	seed        map[string]string
	apply       func(f *fixture)
	witness     func(g *graphView) error
	knownDefect string
	pin         func(inc, full *graphView) error
}

// baseTree is the fixture every row starts from: a real Go module with a
// cross-package call (shop.Checkout -> tax.Rate) through an intra-module import,
// so the heuristic linker resolves it and the go/types pass can confirm it. A
// go.mod is not optional — engine/ingest/typeresolve.go:83 opens the module root,
// and without it the confirmed tier never appears and half the rows would prove
// nothing.
func baseTree() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.26\n",
		"shop/cart.go": `package shop

import "example.com/m/tax"

func Checkout() int { return price() + tax.Rate() }
`,
		"shop/price.go": `package shop

func price() int { return 10 }
`,
		"tax/tax.go": `package tax

func Rate() int { return 7 }
`,
	}
}

// changeClassTable is the declarative FR-7 change-class matrix. Every row with
// harness_row: "required" in docs/rc/parity-classes.yaml MUST appear here or
// TestParityMatrix_DriftGuard/MISSING fails; every id here MUST be declared
// there or PHANTOM fails.
//
// Row order follows the YAML's row order so the two files diff side by side.
func changeClassTable() []changeClassRow {
	return []changeClassRow{
		{
			id:          "add_file",
			kind:        kindChangeClass,
			description: "A new file arrives in a new package. The pure add path: new file node, new symbol nodes, no rewrite of anything already indexed.",
			apply: func(f *fixture) {
				f.Write("util/util.go", "package util\n\nfunc Helper() int { return 1 }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("util.Helper"),
					g.requirePresent("shop.Checkout"), // control: the base tree really was indexed
				)
			},
		},
		{
			id:          "modify_file",
			kind:        kindChangeClass,
			description: "An indexed file is rewritten in place: one body changes and one declaration is added, so the file's node set grows while its existing nodes keep their identity.",
			apply: func(f *fixture) {
				f.Write("shop/price.go", "package shop\n\nfunc price() int { return 11 }\n\nfunc Extra() int { return price() }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.Extra"),
					g.requirePresent("shop.price"), // identity preserved across the rewrite
				)
			},
		},
		{
			id:   "delete_file",
			kind: kindChangeClass,
			description: "A file declaring a symbol that ANOTHER package calls through an intra-module import is deleted, so the per-file stale-node purge, the re-link pass and the " +
				"external-interning path all run over it. THIS CLASS DOES NOT HOLD TODAY: it carries the tracked defect PARITY-001, so the row PINS the current wrong behaviour instead of " +
				"asserting parity. Read the PARITY-001 block on `apply` before touching anything here.",
			knownDefect: "PARITY-001",
			apply: func(f *fixture) {
				// ==========================================================
				// PARITY-001 — TRACKED PRODUCT DEFECT, NOT A HARNESS BUG.
				// Filed: projects/graphi/backlog.md. NOT FIXED HERE.
				// Scheduled: v0.7.2 batch item 3, behind SW-151's F4 residual
				// and SW-153's freshness diagnosis. Measure-first stands — do
				// NOT fix this early, it moves the candidate.
				// ==========================================================
				//
				// PRECONDITION (corrected in review round 1 — the first
				// statement of it was TOO NARROW and understated the blast
				// radius). Deleting a file that declares a symbol which another
				// package calls through an intra-module import makes
				// incremental diverge from full, permanently.
				//
				// It is NOT limited to deleting the sole file of a package. A
				// probe in which the package SURVIVES via a second file
				// diverges identically (6 nodes/5 edges vs 7/6), as does one in
				// which the importer is EXPLICITLY named in the change set.
				// What matters is only that the deleted file declared a
				// cross-package callee. Controls that do NOT diverge: deleting
				// a file nothing references, and deleting a SAME-package
				// callee's file (an unresolved same-package call mints no
				// external node, so full has nothing extra either).
				//
				// OBSERVED, for the fixture this row uses:
				//
				//   full parse        5 nodes / 4 edges
				//   incremental apply 4 nodes / 3 edges
				//
				// Full mints an interned external node `example.com/m/tax.Rate`
				// plus a heuristic edge `shop.Checkout --calls-->
				// example.com/m/tax.Rate` (reason: "external calls (unresolved
				// import example.com/m/tax)"). One incremental apply mints
				// neither.
				//
				// CAUSE — PHASE ORDERING, NOT THE DEPENDENT CASCADE. (This too
				// was corrected in review round 1. The first statement blamed
				// the dependent cascade / dependentsOf; that is REFUTED, because
				// putting the importer explicitly in the change set — so it
				// certainly IS re-parsed and re-linked — diverges identically.
				// Whoever fixes this must not be sent to dependentsOf, where
				// there is nothing to fix.)
				//
				// engine/ingest/ingest.go:709 calls linkFiles BEFORE the
				// deleted-path purge at :721-736. linkFiles builds its symbol
				// index by streaming the LIVE store
				// (engine/ingest/linkfiles.go:64-71,
				// graphstore.ForEachNode over i.store), and at that moment the
				// purge has not run, so `tax.Rate` is STILL A NODE. The linker
				// therefore resolves the call intra-module and never classifies
				// it as an unresolved external. Only afterwards does
				// removeFileTx delete `tax.Rate`, cascading its edges away —
				// leaving the graph one node and one edge short of full.
				//
				// That ordering also explains, exactly, why the SECOND apply
				// converges: by then `tax.Rate` is already gone, so linkFiles
				// builds its index without it, resolution fails, and the
				// external node is minted. The second apply does what the first
				// should have.
				//
				// PERMANENT in the shipped path, not eventually-consistent:
				// once the delete is applied, DriftSet returns empty and every
				// further Reconcile is a no-op (verified over six reconciles,
				// snapshot unmoved), so `graphi sync` stays diverged from
				// `graphi rebuild` until a full rebuild. Backend-independent
				// (byte-identical failure on MemStore and SQLite) and not a
				// watcher artifact (reproduces with plain serial
				// ing.IngestChanged, and independently through the shipped
				// DriftDetail -> IngestChanged path graphi sync drives).
				f.Remove("tax/tax.go")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireAbsent("tax.Rate"),
					g.requirePresent("shop.Checkout"), // control: only the deleted file's nodes went
				)
			},
			// pin: the CURRENT WRONG BEHAVIOUR, stated exactly. Not an
			// expectation — a published defect. If graphi is fixed, this fails
			// and tells the fixer to restore the parity assertion.
			pin: func(inc, full *graphView) error {
				return requireOnlyMissing(inc, full,
					[]string{"example.com/m/tax.Rate"},
					[][3]string{{"shop.Checkout", "calls", "example.com/m/tax.Rate"}},
				)
			},
		},
		{
			id:          "rename_symbol",
			kind:        kindChangeClass,
			description: "A cross-package callee is renamed and its importer updated in the SAME change set: the old symbol's identity dies, a new one is minted, and every edge into it must be re-pointed.",
			apply: func(f *fixture) {
				f.Write("tax/tax.go", "package tax\n\nfunc Ratio() int { return 7 }\n")
				f.Write("shop/cart.go", "package shop\n\nimport \"example.com/m/tax\"\n\nfunc Checkout() int { return price() + tax.Ratio() }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("tax.Ratio"),
					g.requireAbsent("tax.Rate"),
					g.requireEdge("shop.Checkout", "calls", "tax.Ratio"),
				)
			},
		},
		{
			id:   "move_symbol",
			kind: kindChangeClass,
			description: "A declaration is RELOCATED, in both directions FR-7 distinguishes. Same-package file->file: shop.price moves price.go -> moved.go, which preserves the " +
				"package-qualified NodeId while changing source_path and line, so two files claim one NodeId inside a single change set and the source file's stale-node purge runs " +
				"against a node the destination file now owns. Cross-package: tax.Rate becomes rate.Rate in a new directory, which does mint a new identity and must cascade the importer.",
			apply: func(f *fixture) {
				// Same-package move: identity-preserving, path-changing.
				f.Move("shop/price.go", "shop/moved.go", "package shop\n\nfunc price() int { return 10 }\n")
				// Cross-package move: identity-changing, importer-cascading.
				f.Move("tax/tax.go", "rate/rate.go", "package rate\n\nfunc Rate() int { return 7 }\n")
				f.Write("shop/cart.go", "package shop\n\nimport \"example.com/m/rate\"\n\nfunc Checkout() int { return price() + rate.Rate() }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireSourcePath("shop.price", "shop/moved.go"),
					g.requirePresent("rate.Rate"),
					g.requireAbsent("tax.Rate"),
					g.requireEdge("shop.Checkout", "calls", "shop.price"),
					g.requireEdge("shop.Checkout", "calls", "rate.Rate"),
				)
			},
		},
		{
			id:   "rename_package",
			kind: kindChangeClass,
			description: "The directory AND the package clause change together, and the importer's import path with them. This is the row engine/typeresolve/pkggraph.go GroupPackages " +
				"makes risky: it keys units by (directory, package clause), so a rename re-keys every unit in the directory at once and every importer must be re-resolved through engine/link/link.go Linker.Link.",
			seed: map[string]string{
				"tax/helper.go": "package tax\n\nfunc Helper() int { return 1 }\n",
			},
			apply: func(f *fixture) {
				f.Move("tax/tax.go", "levy/tax.go", "package levy\n\nfunc Rate() int { return 7 }\n")
				f.Move("tax/helper.go", "levy/helper.go", "package levy\n\nfunc Helper() int { return 1 }\n")
				f.Write("shop/cart.go", "package shop\n\nimport \"example.com/m/levy\"\n\nfunc Checkout() int { return price() + levy.Rate() }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("levy.Rate", "levy.Helper"),
					g.requireAbsent("tax.Rate", "tax.Helper"),
					g.requireEdge("shop.Checkout", "calls", "levy.Rate"),
				)
			},
		},
		{
			id:          "add_call",
			kind:        kindChangeClass,
			description: "A new call site appears between two already-indexed symbols, so a new calls edge is minted with full provenance and no node identity moves.",
			apply: func(f *fixture) {
				f.Write("shop/cart.go", "package shop\n\nimport \"example.com/m/tax\"\n\nfunc Checkout() int { return price() + tax.Rate() }\n\nfunc Also() int { return price() }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requireEdge("shop.Also", "calls", "shop.price"),
					g.requireEdge("shop.Checkout", "calls", "tax.Rate"),
				)
			},
		},
		{
			id:   "remove_call",
			kind: kindChangeClass,
			description: "The hard form of removal: the caller drops a call but KEEPS its identity, so DeleteNode never cascades the edge away and only the stale-edge sweep can converge it. " +
				"Doubles as the FR-7 'no stale linker edges' proof at table level.",
			apply: func(f *fixture) {
				f.Write("shop/cart.go", "package shop\n\nfunc Checkout() int { return price() }\n")
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.Checkout"), // identity kept
					g.requireNoEdge("shop.Checkout", "calls", "tax.Rate"),
					g.requireEdge("shop.Checkout", "calls", "shop.price"),
				)
			},
		},
		{
			id:   "change_interface",
			kind: kindChangeClass,
			description: "An interface changes in BOTH directions inside one change set: a method is ADDED to Reader and an embed is REMOVED from Collector. It closes the residual " +
				"SW-156 recorded against engine/ingest/hierarchy_e2e_test.go:139, whose body only ever ADDS an embed even though its doc comment says 'adds/removes'.",
			seed: map[string]string{
				"shop/types.go": `package shop

type Reader interface {
	Read() int
}

type Extra interface {
	Extra() int
}

type Collector interface {
	Reader
	Extra
	Collect() error
}
`,
			},
			apply: func(f *fixture) {
				f.Write("shop/types.go", `package shop

type Reader interface {
	Read() int
	Peek() int
}

type Extra interface {
	Extra() int
}

type Collector interface {
	Reader
	Collect() error
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.Reader", "shop.Extra", "shop.Collector"),
					g.requireEdge("shop.Collector", "implements", "shop.Reader"),
					// The removed embed's edge must be gone in BOTH mechanisms:
					// the syntactic embed is no longer written, and Collector's
					// method set no longer supersets Extra.
					g.requireNoEdge("shop.Collector", "implements", "shop.Extra"),
				)
			},
		},
		{
			id:   "add_implementation",
			kind: kindChangeClass,
			description: "A concrete type STARTS satisfying an interface by method set, with no embed involved — the go/types mechanism (engine/typeresolve/check.go) rather than the " +
				"syntactic embed extractor. SW-156 marked this class PARTIAL because only the embedding mechanism was byte-proven; the witness pins tier=confirmed so this row cannot be satisfied by the embedding path.",
			seed: map[string]string{
				"shop/sink.go": `package shop

type Sink interface {
	Put(v int) error
}

type Buf struct {
	n int
}
`,
			},
			apply: func(f *fixture) {
				f.Write("shop/sink.go", `package shop

type Sink interface {
	Put(v int) error
}

type Buf struct {
	n int
}

func (b *Buf) Put(v int) error {
	b.n = v
	return nil
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.Sink", "shop.Buf"),
					g.requireEdgeAtTier("shop.Buf", "implements", "shop.Sink", model.TierConfirmed),
				)
			},
		},
		{
			id:   "remove_implementation",
			kind: kindChangeClass,
			description: "The direction that can leave a stale edge, covered for BOTH mechanisms in one change set: the method that made Buf satisfy Sink is deleted (go/types " +
				"method-set edge must vanish) and the struct embed of Base is deleted (syntactic inherits edge must vanish). Nothing in the tree covered either before this row.",
			seed: map[string]string{
				"shop/sink.go": `package shop

type Sink interface {
	Put(v int) error
}

type Base struct {
	id int
}

type Buf struct {
	Base
	n int
}

func (b *Buf) Put(v int) error {
	b.n = v
	return nil
}
`,
			},
			apply: func(f *fixture) {
				f.Write("shop/sink.go", `package shop

type Sink interface {
	Put(v int) error
}

type Base struct {
	id int
}

type Buf struct {
	n int
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("shop.Sink", "shop.Base", "shop.Buf"), // controls: nothing was deleted wholesale
					g.requireNoEdge("shop.Buf", "implements", "shop.Sink"),
					g.requireNoEdge("shop.Buf", "inherits", "shop.Base"),
				)
			},
		},
		{
			id:   "branch_switch",
			kind: kindChangeClass,
			description: "DECLARED PLACEHOLDER, NOT PROVEN HERE. A branch switch needs a real git repository and a real working-tree swap; this harness is hermetic and creates no " +
				"repository (t.TempDir() only, no clone, no network). cmd/graphi/sync_test.go:33 asserts only the announcement line, not a graph delta. Owned by SW-158.",
			deferredTo: "SW-158",
		},
		{
			id:   "change_build_tag",
			kind: kindChangeClass,
			description: "DEGENERATE BY CONSTRUCTION, AND THE ROW SAYS SO. This row proves PARITY over the change and proves NOTHING about build-tag semantics. No build-constraint " +
				"evaluation exists anywhere in graphi: engine/typeresolve/pkggraph.go GroupPackages groups by (directory, package clause) from parser.ImportsOnly bytes and never inspects " +
				"//go:build, and engine/typeresolve/doc.go:24 forbids the go/packages dependency constraint evaluation would come from. To graphi a build-tag edit is a comment-line " +
				"content change, so the ONLY thing it moves is the line numbers of the declarations below it — which is exactly what the witness asserts. Parity over it holds trivially; " +
				"the class stays in the matrix because FR-7 lists it (spec decision 5) and dropping a listed class to make the matrix look stronger is the substitution this programme forbids.",
			seed: map[string]string{
				"tagged/tagged.go": "package tagged\n\nfunc Tagged() int { return 1 }\n",
			},
			apply: func(f *fixture) {
				f.Write("tagged/tagged.go", "//go:build !ignore\n\npackage tagged\n\nfunc Tagged() int { return 1 }\n")
			},
			witness: func(g *graphView) error {
				// Baseline: `func Tagged` is on line 3. After the two-line
				// constraint preamble it is on line 5. graphi read the constraint
				// as ordinary leading comment content and shifted the
				// declaration; it did not evaluate it.
				return all(
					g.requirePresent("tagged.Tagged"),
					g.requireLine("tagged.Tagged", 5),
				)
			},
		},
		{
			id:   "replace_generated_file",
			kind: kindChangeClass,
			description: "A file carrying the `// Code generated ... DO NOT EDIT.` marker is replaced wholesale by a regenerated body with a different symbol set. Within the parse/ingest " +
				"path the class has no special-casing at all — the generated-marker detector at surfaces/client/direct.go:596 feeds engine/diagnostic/suppress.go's dead-code triage and is " +
				"reached by neither ingest nor model.Graph — so this is a high-symbol-count churn stress on the ordinary modify path rather than a degenerate row. The real-source instance (grpc-go, 49 marked files) belongs to SW-144.",
			seed: map[string]string{
				"gen/api.pb.go": `// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
//	protoc-gen-go v1.0.0

package gen

type Request struct {
	Field int
}

func GetA() int { return 1 }

func GetB() int { return 2 }

func GetC() int { return 3 }
`,
			},
			apply: func(f *fixture) {
				f.Write("gen/api.pb.go", `// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
//	protoc-gen-go v2.0.0

package gen

type Request struct {
	Field  int
	Added  string
}

type Response struct {
	Code int
}

func GetB() int { return 20 }

func GetC() int { return 30 }

func GetD() int { return 40 }

func GetE() int { return 50 }
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requirePresent("gen.GetD", "gen.GetE", "gen.Response"),
					g.requireAbsent("gen.GetA"),
					g.requirePresent("gen.GetB", "gen.GetC", "gen.Request"), // survivors keep identity
				)
			},
		},
		{
			id:   "change_external_import",
			kind: kindChangeClass,
			description: "An external import is SWAPPED for a different external package and a second one is ADDED in the same change set, so one interned external node is orphaned and " +
				"swept while two are minted. It closes the residual SW-156 recorded against engine/ingest/link_external_lifecycle_e2e_test.go:29, whose three subtests only ever remove a referencer and never add or swap an import path.",
			seed: map[string]string{
				"ext/ext.go": `package ext

import "os"

func Use() {
	_, _ = os.ReadFile("/a")
}
`,
			},
			apply: func(f *fixture) {
				f.Write("ext/ext.go", `package ext

import (
	"strconv"
	"strings"
)

func Use() {
	_, _ = strconv.Atoi("1")
	_ = strings.ToUpper("x")
}
`)
			},
			witness: func(g *graphView) error {
				return all(
					g.requireExternal("strconv.Atoi"),
					g.requireExternal("strings.ToUpper"),
					g.requireNoExternal("os.ReadFile"),
				)
			},
		},
		{
			id:   "interrupted_full_pass",
			kind: kindCrashCondition,
			description: "DECLARED PLACEHOLDER, NOT PROVEN HERE, AND NOT A CHANGE CLASS. Killing a full pass needs a real process boundary. The in-process form is settled work with an " +
				"ADR behind it (engine/ingest/faultmatrix_test.go:160, docs/adr/0004-ingest-recovery-disposition.md K1-K3); the real-process complement that ADR reserves at :92-94 is owned by SW-158.",
			deferredTo: "SW-158",
		},
		{
			id:   "restart_and_recovery",
			kind: kindCrashCondition,
			description: "DECLARED PLACEHOLDER, NOT PROVEN HERE, AND NOT A CHANGE CLASS. Restart-and-recover needs a real process boundary. In-process K6-K8 are dispositioned in " +
				"docs/adr/0004-ingest-recovery-disposition.md:34-41; the real-process complement is owned by SW-158.",
			deferredTo: "SW-158",
		},
	}
}

// ---------------------------------------------------------------------------
// The row runner
// ---------------------------------------------------------------------------

// parityBackend pairs a human name with a graphstore.Factory, following
// core/graphstore/contract_test.go:17's shape so the IDENTICAL row body runs
// against every backend.
type parityBackend struct {
	name    string
	factory graphstore.Factory
}

// parityBackends is the two-backend set AC-7 requires. Every full-vs-incremental
// parity proof in the tree before this story was MemStore-only while the shipped
// store is SQLite, and that gap is the reason this is parameterized rather than
// hardcoded. The comparison unit stays the PORTABLE snapshot envelope
// (core/graphstore/snapshot.go), not raw backend bytes, so the two backends'
// results are directly comparable — engine/ingest/faultmatrix_test.go:231 already
// relies on that store-independence.
func parityBackends() []parityBackend {
	return []parityBackend{
		{name: "mem", factory: graphstore.MemFactory},
		{name: "sqlite", factory: graphstore.SQLiteFactory},
	}
}

// newBackendStore builds a fresh empty store for a backend and registers its
// close.
func newBackendStore(t *testing.T, b parityBackend) graphstore.Graphstore {
	t.Helper()
	st, err := b.factory(t.TempDir())
	if err != nil {
		t.Fatalf("[%s] factory: %v", b.name, err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// writeTree materializes a file map under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		writeFile(t, root, rel, files[rel])
	}
}

// runChangeClassRow is the per-row gate. For one class on one backend it:
//
//  1. seeds baseTree()+row.seed into a private t.TempDir();
//  2. builds the graph INCREMENTALLY through the watcher-driven parallel parse
//     path, in two reconciles — one for the seed, one for row.apply — so the
//     change is applied as a real incremental delta rather than a fresh index;
//  3. builds a second graph by a single FULL parse of the same final on-disk
//     state;
//  4. asserts the two portable snapshots are BYTE-IDENTICAL (AC-8: snapshot
//     bytes are the assertion, never a spot query);
//  5. runs the row's non-vacuity witness against the incremental graph (AC-3);
//  6. re-applies the identical change set and asserts the snapshot is unmoved
//     (AC-6 idempotency).
//
// Each row gets its own tree and its own stores, deliberately. A single shared
// sequence would let one class's failure poison every later class's comparison,
// and this table's whole job is to attribute a parity failure to a named class.
func runChangeClassRow(t *testing.T, b parityBackend, row changeClassRow) {
	t.Helper()
	if row.deferredTo != "" {
		// A declared placeholder SKIPS. It must never report green: a green row
		// for a class nobody exercised is exactly the substituted evidence this
		// programme exists to remove.
		t.Skipf("class %q is a declared placeholder owned by %s and is NOT proven here: %s",
			row.id, row.deferredTo, row.description)
	}
	if row.apply == nil || row.witness == nil {
		t.Fatalf("class %q has no apply/witness and is not marked deferred", row.id)
	}
	if (row.knownDefect == "") != (row.pin == nil) {
		t.Fatalf("class %q must declare knownDefect and pin together (knownDefect=%q, pin set=%v): a "+
			"tracked defect with no pin publishes nothing, and a pin with no defect id is untraceable",
			row.id, row.knownDefect, row.pin != nil)
	}

	ctx := context.Background()
	root := t.TempDir()
	f := newFixture(t, root)

	seed := baseTree()
	for rel, content := range row.seed {
		seed[rel] = content
	}

	// (1)+(2) Incremental: seed, reconcile, apply, reconcile.
	incStore := newBackendStore(t, b)
	inc := buildIncrementalParallel(t, root, incStore, []func(){
		func() { writeTree(t, root, seed) },
		func() { row.apply(f) },
	})

	// (3) Full parse of the FINAL on-disk state.
	fullStore := newBackendStore(t, b)
	fullIng := newIngester(t, fullStore)
	if err := fullIng.IngestAll(ctx, root); err != nil {
		t.Fatalf("[%s/%s] full IngestAll: %v", b.name, row.id, err)
	}

	incSnap := snapshot(t, incStore)
	fullSnap := snapshot(t, fullStore)

	// (5) Non-vacuity, FIRST and unconditionally — it is required of every row,
	// including a knownDefect row. Asserted against the INCREMENTAL graph, the
	// path under test. A row whose witness fails proved nothing about anything:
	// the change did not reach the graph. That is a HARNESS defect, not a product
	// defect, and it is checked before the parity verdict so a vacuous row cannot
	// hide behind either a green parity assertion or a green pin.
	g, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("[%s/%s] read incremental graph: %v", b.name, row.id, err)
	}
	if err := row.witness(g); err != nil {
		t.Errorf("[%s/%s] VACUOUS ROW: the witness for this class did not hold, so `apply` did not "+
			"produce the graph shape the row claims to exercise: %v", b.name, row.id, err)
	}

	if row.knownDefect != "" {
		runKnownDefectRow(t, b, row, inc, root, f, incStore, fullStore, incSnap, fullSnap)
		return
	}

	// (4) The assertion. Snapshot bytes, nothing weaker.
	if string(incSnap) != string(fullSnap) {
		t.Errorf("[%s/%s] PARITY FAIL: incremental != full snapshot bytes.\n"+
			"class: %s\nchange set: %v\n%s",
			b.name, row.id, row.description, f.changeSet(),
			snapshotDiff(t, "incremental", incSnap, "full", fullSnap))
	}

	// (6) Idempotency.
	assertIdempotentReapplication(t, b, row, inc, root, f, incSnap)
}

// runKnownDefectRow is what a class does INSTEAD of asserting parity, once the
// owner has accepted that the class genuinely does not hold and has scheduled the
// correction. Read this before touching it, because the difference between this
// and a skip is the entire point.
//
// A SKIP would delete the evidence. A `-skip` flag on the driver would delete it
// AND silently license promoting the class's verdict back to PROVEN. This does
// the opposite of both: it keeps the row executing on every PR, and it PINS THE
// WRONG BEHAVIOUR AS DATA. Three assertions, each of which fails loudly if the
// world moves:
//
//  1. incremental and full MUST STILL DIFFER. If they converge, the defect is
//     fixed (or masked) and this row is now lying — it fails, and whoever fixed
//     it is told to restore the parity assertion and clear known_defect.
//  2. the difference is EXACTLY what row.pin says. Not "some difference" — the
//     named missing nodes and edges and nothing else. If the defect changes
//     shape or grows, the pin fails.
//  3. re-applying the identical change set converges to the FULL bytes. That is
//     the same defect seen from the idempotency side, and pinning it records the
//     most diagnostic fact about the bug: the second apply does what the first
//     should have.
//
// The pin is labelled WRONG everywhere it appears. It is never phrased as an
// expectation, and the verdict in docs/rc/parity-classes.yaml may not read PROVEN
// while known_defect is set — TestParityMatrix_DriftGuard/VERDICT enforces that
// MECHANICALLY, independent of whether this suite is green, which is precisely
// the trap a green pin would otherwise open.
func runKnownDefectRow(t *testing.T, b parityBackend, row changeClassRow, inc *incBuild,
	root string, f *fixture, incStore, fullStore graphstore.Graphstore, incSnap, fullSnap []byte) {
	t.Helper()
	ctx := context.Background()

	// (1) The divergence must still exist.
	if string(incSnap) == string(fullSnap) {
		t.Errorf("[%s/%s] PINNED DEFECT %s NO LONGER REPRODUCES: incremental and full snapshot bytes "+
			"now AGREE. This is good news and a required action, not a pass: restore this row's parity "+
			"assertion, delete its knownDefect and pin, clear `known_defect` in "+
			"docs/rc/parity-classes.yaml, and re-verdict the class.",
			b.name, row.id, row.knownDefect)
		return
	}

	// (2) …and it must be EXACTLY the shape this row publishes.
	incView, err := newGraphView(ctx, incStore)
	if err != nil {
		t.Fatalf("[%s/%s] read incremental graph: %v", b.name, row.id, err)
	}
	fullView, err := newGraphView(ctx, fullStore)
	if err != nil {
		t.Fatalf("[%s/%s] read full graph: %v", b.name, row.id, err)
	}
	if err := row.pin(incView, fullView); err != nil {
		t.Errorf("[%s/%s] PINNED DEFECT %s CHANGED SHAPE: the divergence is no longer the one this row "+
			"publishes, so the recorded reproduction is stale: %v\n%s",
			b.name, row.id, row.knownDefect, err,
			snapshotDiff(t, "incremental", incSnap, "full", fullSnap))
	}

	// (3) The idempotency face of the same defect: the SECOND apply does what the
	// first should have, so re-application both MOVES the graph and lands on the
	// full-parse bytes.
	parsed, err := inc.svc.Pool().ParseBatch(ctx, inc.ing, root, f.parseSet())
	if err != nil {
		t.Fatalf("[%s/%s] pinned re-parse: %v", b.name, row.id, err)
	}
	if err := inc.ing.ApplyChangedParsed(ctx, root, f.changeSet(), parsed); err != nil {
		t.Fatalf("[%s/%s] pinned re-apply: %v", b.name, row.id, err)
	}
	reapplied := snapshot(t, inc.store)
	if string(reapplied) == string(incSnap) {
		t.Errorf("[%s/%s] PINNED DEFECT %s CHANGED SHAPE: re-applying the identical change set no "+
			"longer moves the graph. The recorded reproduction said the second apply converges; it "+
			"no longer does.", b.name, row.id, row.knownDefect)
	}
	if string(reapplied) != string(fullSnap) {
		t.Errorf("[%s/%s] PINNED DEFECT %s CHANGED SHAPE: the second apply no longer converges to the "+
			"full-parse bytes.\n%s", b.name, row.id, row.knownDefect,
			snapshotDiff(t, "after re-apply", reapplied, "full", fullSnap))
	}

	t.Logf("PINNED KNOWN DEFECT %s on class %q [%s]: incremental %d nodes / %d edges vs full %d / %d. "+
		"This row asserts the CURRENT WRONG BEHAVIOUR so the defect stays published and cannot be "+
		"quietly fixed, quietly worsened, or quietly promoted. It is NOT a parity proof.",
		row.knownDefect, row.id, b.name,
		len(incView.nodes), len(incView.edges), len(fullView.nodes), len(fullView.edges))
}

// requireOnlyMissing is the pin helper: it asserts the full graph carries exactly
// these extra qualified names and these extra edges relative to the incremental
// graph, and that the two agree on everything else. "Exactly" is what makes the
// pin fail when a defect is fixed OR when it grows.
func requireOnlyMissing(inc, full *graphView, missingNodes []string, missingEdges [][3]string) error {
	incQN := map[string]bool{}
	for qn := range inc.byQN {
		incQN[qn] = true
	}
	want := map[string]bool{}
	for _, qn := range missingNodes {
		want[qn] = true
		if incQN[qn] {
			return fmt.Errorf("node %q is PRESENT in the incremental graph; the pin says it is missing", qn)
		}
		if _, ok := full.node(qn); !ok {
			return fmt.Errorf("node %q is absent from the FULL graph too; the pin describes a divergence that does not exist", qn)
		}
	}
	var unexpected []string
	for qn := range full.byQN {
		if !incQN[qn] && !want[qn] {
			unexpected = append(unexpected, qn)
		}
	}
	for qn := range incQN {
		if _, ok := full.node(qn); !ok {
			unexpected = append(unexpected, "(only in incremental) "+qn)
		}
	}
	sort.Strings(unexpected)
	if len(unexpected) > 0 {
		return fmt.Errorf("the divergence is wider than the pin: also %v", unexpected)
	}
	for _, e := range missingEdges {
		if _, ok := inc.edge(e[0], e[1], e[2]); ok {
			return fmt.Errorf("edge %s --%s--> %s is PRESENT in the incremental graph; the pin says it is missing", e[0], e[1], e[2])
		}
		if _, ok := full.edge(e[0], e[1], e[2]); !ok {
			return fmt.Errorf("edge %s --%s--> %s is absent from the FULL graph too; the pin describes a divergence that does not exist", e[0], e[1], e[2])
		}
	}
	if got, want := len(full.edges)-len(inc.edges), len(missingEdges); got != want {
		return fmt.Errorf("full has %d more edges than incremental; the pin names %d", got, want)
	}
	if got, want := len(full.nodes)-len(inc.nodes), len(missingNodes); got != want {
		return fmt.Errorf("full has %d more nodes than incremental; the pin names %d", got, want)
	}
	return nil
}

// assertIdempotentReapplication is the FR-7 idempotency gate
// ("Wiederholte Ausführung ist idempotent", PRD FR-7 :833). It applies the
// IDENTICAL change set a second time through the same watcher parallel-parse
// path and requires the snapshot bytes to be unmoved.
//
// READ THIS BEFORE COLLAPSING IT INTO TestRepeatRun_Determinism — they prove
// different things and the difference is the whole reason this exists:
//
//	TestRepeatRun_Determinism repeats DISPATCH. It runs an operation N times
//	against ONE already-built graph and checks the envelope bytes do not move.
//	It never touches the ingest path, so it cannot see a double-apply that
//	duplicates an edge, resurrects a purged node, or fails to be a no-op.
//
//	This assertion repeats APPLICATION. The same files, the same paths, the same
//	parse results go through Pool.ParseBatch -> ApplyChangedParsed a second time,
//	and the graph must be unmoved. That is what FR-7 asks for and what nothing in
//	the tree asserted: engine/link/link_test.go:158 TestLink_Idempotent is a
//	linker unit over a hand-built intent set, not a re-application through ingest.
//
// The re-application deliberately does NOT go through watch.Service.Reconcile.
// Reconcile derives its work from DriftSet, and drift is content-hash based
// (engine/ingest/ingest.go:383), so re-writing identical bytes yields an empty
// drift set and Reconcile returns at engine/watch/service.go:276 having done
// nothing — the assertion would pass vacuously. Naming the recorded change set
// explicitly is what makes the second apply real.
func assertIdempotentReapplication(t *testing.T, b parityBackend, row changeClassRow,
	inc *incBuild, root string, f *fixture, before []byte) {
	t.Helper()
	ctx := context.Background()

	parsed, err := inc.svc.Pool().ParseBatch(ctx, inc.ing, root, f.parseSet())
	if err != nil {
		t.Fatalf("[%s/%s] idempotency re-parse: %v", b.name, row.id, err)
	}
	if err := inc.ing.ApplyChangedParsed(ctx, root, f.changeSet(), parsed); err != nil {
		t.Fatalf("[%s/%s] idempotency re-apply: %v", b.name, row.id, err)
	}
	after := snapshot(t, inc.store)
	if string(before) != string(after) {
		t.Errorf("[%s/%s] IDEMPOTENCY FAIL: re-applying the identical change set %v moved the graph.\n%s",
			b.name, row.id, f.changeSet(),
			snapshotDiff(t, "before re-apply", before, "after re-apply", after))
	}
}

// snapshotEnvelope is the minimum of core/graphstore/snapshot.go's wire shape
// this file needs to render a readable difference. It is used ONLY for
// diagnostics.
type snapshotEnvelope struct {
	Graph struct {
		Nodes []struct {
			ID            string `json:"id"`
			Kind          string `json:"kind"`
			QualifiedName string `json:"qualified_name"`
			SourcePath    string `json:"source_path"`
			Line          int    `json:"line"`
		} `json:"nodes"`
		Edges []struct {
			ID       string   `json:"id"`
			From     string   `json:"from"`
			To       string   `json:"to"`
			Kind     string   `json:"kind"`
			Tier     string   `json:"confidence_tier"`
			Reason   string   `json:"reason"`
			Evidence []string `json:"evidence"`
		} `json:"edges"`
	} `json:"graph"`
}

// snapshotDiff renders the node/edge set difference between two snapshot
// envelopes, labelled with the two sides' names.
//
// It is DIAGNOSTICS ONLY. The assertion is byte equality at the call site, and no
// verdict is ever derived from this rendering — the same rule the spec states for
// graphi compare / BranchDiffReport: it EXPLAINS a mismatch and never DECIDES
// one. If this renders "no set-level difference" while the bytes differ, that is
// itself a finding (an ordering or a field this view does not carry).
func snapshotDiff(t *testing.T, aName string, a []byte, bName string, b []byte) string {
	t.Helper()
	var ae, be snapshotEnvelope
	if err := json.Unmarshal(a, &ae); err != nil {
		return fmt.Sprintf("undiffable (%s: %v); sizes %d vs %d bytes", aName, err, len(a), len(b))
	}
	if err := json.Unmarshal(b, &be); err != nil {
		return fmt.Sprintf("undiffable (%s: %v); sizes %d vs %d bytes", bName, err, len(a), len(b))
	}

	nodeKey := func(e snapshotEnvelope) map[string]string {
		m := map[string]string{}
		for _, n := range e.Graph.Nodes {
			m[n.ID] = fmt.Sprintf("%s %s %s:%d", n.Kind, n.QualifiedName, n.SourcePath, n.Line)
		}
		return m
	}
	an, bn := nodeKey(ae), nodeKey(be)
	qn := func(e snapshotEnvelope, id string) string {
		for _, n := range e.Graph.Nodes {
			if n.ID == id {
				return n.QualifiedName
			}
		}
		return id
	}
	edgeKey := func(e snapshotEnvelope) map[string]string {
		m := map[string]string{}
		for _, ed := range e.Graph.Edges {
			m[ed.ID] = fmt.Sprintf("%s --%s[%s]--> %s reason=%q evidence=%v",
				qn(e, ed.From), ed.Kind, ed.Tier, qn(e, ed.To), ed.Reason, ed.Evidence)
		}
		return m
	}
	aeg, beg := edgeKey(ae), edgeKey(be)

	var out []string
	out = append(out, fmt.Sprintf("%s: %d nodes / %d edges / %d bytes | %s: %d nodes / %d edges / %d bytes",
		aName, len(an), len(aeg), len(a), bName, len(bn), len(beg), len(b)))
	report := func(label string, only map[string]string, other map[string]string) {
		var lines []string
		for id, desc := range only {
			if _, ok := other[id]; !ok {
				lines = append(lines, "    "+desc)
			}
		}
		sort.Strings(lines)
		if len(lines) > 0 {
			out = append(out, fmt.Sprintf("  %s (%d):", label, len(lines)))
			out = append(out, lines...)
		}
	}
	report(fmt.Sprintf("nodes only in %s", aName), an, bn)
	report(fmt.Sprintf("nodes only in %s", bName), bn, an)
	report(fmt.Sprintf("edges only in %s", aName), aeg, beg)
	report(fmt.Sprintf("edges only in %s", bName), beg, aeg)
	if len(out) == 1 {
		out = append(out, "  no set-level difference — the divergence is ordering or a field this "+
			"diagnostic view does not carry (node meta, evidence ordering). That is itself a finding.")
	}
	return strings.Join(out, "\n")
}

func splitLines(b []byte) []string {
	var out []string
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, string(b[start:i]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}

// TestChangeClassTable_WitnessesAreNonVacuous is AC-3's non-vacuity proof,
// MECHANIZED for every row instead of demonstrated once by hand.
//
// For each implemented class it builds the graph from the row's seed tree with
// `apply` DELIBERATELY NOT RUN, and requires the row's witness to FAIL. That is
// exactly the "comment out the apply, observe the witness fail" check, executed
// for all fourteen rows on every PR — so a row cannot rot into vacuity later, and
// a NEW row cannot be added with a witness that holds before its own change.
//
// A row that passes here has a witness that does not observe its own change: the
// row would then be asserting full-vs-incremental parity over a graph the change
// never touched, which is the seventeen-times-nothing failure mode this table
// exists to prevent.
//
// It uses a single FULL parse of the seed tree rather than the watcher path,
// because vacuity is a property of the (apply, witness) pair over the graph and
// is independent of which path built it — and one backend suffices for the same
// reason. The parity assertion itself, which is path- and backend-sensitive,
// runs on both backends in TestFullVsIncremental_ByteParity.
func TestChangeClassTable_WitnessesAreNonVacuous(t *testing.T) {
	ctx := context.Background()
	for _, row := range changeClassTable() {
		row := row
		if row.deferredTo != "" {
			continue // no apply, no witness, nothing to be vacuous about
		}
		t.Run(row.id, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			seed := baseTree()
			for rel, content := range row.seed {
				seed[rel] = content
			}
			writeTree(t, root, seed)

			store := graphstore.NewMemStore()
			t.Cleanup(func() { _ = store.Close() })
			ing := newIngester(t, store)
			if err := ing.IngestAll(ctx, root); err != nil {
				t.Fatalf("seed IngestAll: %v", err)
			}
			g, err := newGraphView(ctx, store)
			if err != nil {
				t.Fatalf("read seed graph: %v", err)
			}
			if err := row.witness(g); err == nil {
				t.Errorf("VACUOUS WITNESS: class %q's witness HOLDS on the seed graph, before `apply` "+
					"has run. The row would assert parity over a change its witness cannot observe. "+
					"Tighten the witness so it fails without the change.", row.id)
			} else {
				t.Logf("red without the change, as required: %v", err)
			}
		})
	}
}

// TestParityMatrix_HarnessCitationIsCurrent backs the VERDICT direction's
// citation half: docs/rc/parity-classes.yaml points every class this table
// proves at harnessTestFile / harnessTestName, and the line it cites must really
// hold that func declaration. Without this, a refactor that moves the driver
// leaves seventeen rows citing a line that means nothing.
func TestParityMatrix_HarnessCitationIsCurrent(t *testing.T) {
	src, err := os.ReadFile(harnessSourceFile)
	if err != nil {
		t.Fatalf("read %s: %v", harnessSourceFile, err)
	}
	lines := splitLines(src)
	want := "func " + harnessTestName + "(t *testing.T) {"

	rows := loadParityClasses(t)
	for _, r := range rows {
		if r.TestFile != harnessTestFile {
			continue
		}
		n := 0
		if _, err := fmt.Sscanf(r.TestLine, "%d", &n); err != nil || n < 1 || n > len(lines) {
			t.Errorf("row %q cites test_line %q, which is not a line of %s (%d lines)",
				r.ID, r.TestLine, harnessTestFile, len(lines))
			continue
		}
		if lines[n-1] != want {
			t.Errorf("row %q cites %s:%s but that line reads %q, want %q",
				r.ID, r.TestFile, r.TestLine, lines[n-1], want)
		}
	}
}
