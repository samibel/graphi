package typeresolve

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	corparse "github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/link"
)

// fixtureRun is one end-to-end run over an in-memory repo: the committed node
// set comes from the REAL core/parse extractor (exactly what ingest commits),
// the heuristic edge set from the REAL engine/link linker, and the confirmed
// edge set from Resolve. Comparing the two edge sets over the same fixture is
// the point of this file: the cases where the name heuristic is provably wrong
// and the type-checker is right.
type fixtureRun struct {
	res Result
	// confirmed renders Resolve's edges as "kind fromQN -> toQN".
	confirmed map[string]bool
	// heuristic renders the linker's edges the same way.
	heuristic map[string]bool
	labels    map[model.NodeId]string
}

func runFixture(t *testing.T, files map[string]string) fixtureRun {
	t.Helper()
	byteFiles := map[string][]byte{}
	names := make([]string, 0, len(files))
	for k, v := range files {
		byteFiles[k] = []byte(v)
		names = append(names, k)
	}
	sort.Strings(names)

	p := corparse.NewGoParser()
	committed := map[model.NodeId]struct{}{}
	labels := map[model.NodeId]string{}
	var nodes []model.Node
	var refs []link.FileRefs
	for _, name := range names {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		pr, err := p.Parse(context.Background(), name, byteFiles[name])
		if err != nil {
			// Mirrors ingest's full-index behavior: a file the extractor cannot
			// parse is a fail-closed skip and commits no nodes.
			continue
		}
		for _, n := range pr.Nodes {
			committed[n.ID()] = struct{}{}
			if n.Kind() == "file" {
				labels[n.ID()] = "file:" + n.SourcePath()
			} else {
				labels[n.ID()] = n.QualifiedName()
			}
			nodes = append(nodes, n)
		}
		dir := path.Dir(name)
		if dir == "." {
			dir = ""
		}
		refs = append(refs, link.FileRefs{
			SourcePath: name, Dir: dir, Language: "go",
			Pending: pr.PendingRefs, Imports: pr.Imports,
		})
	}

	heurEdges, _, err := link.New().Link("go", refs, link.BuildIndex(nodes))
	if err != nil {
		t.Fatalf("heuristic link: %v", err)
	}
	res, err := Resolve(byteFiles, committed)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	render := func(edges []model.Edge) map[string]bool {
		out := map[string]bool{}
		for _, e := range edges {
			out[e.Kind()+" "+labels[e.From()]+" -> "+labels[e.To()]] = true
		}
		return out
	}
	// Tier honesty: every edge this pass emits must be confirmed/1.0 — nothing
	// weaker, nothing else. Asserted on every fixture, not in a one-off test.
	for _, e := range res.Edges {
		if e.Tier() != model.TierConfirmed || e.Confidence() != 1.0 {
			t.Errorf("edge %s %s -> %s has tier %s/%v, want confirmed/1.0",
				e.Kind(), labels[e.From()], labels[e.To()], e.Tier(), e.Confidence())
		}
	}
	return fixtureRun{res: res, confirmed: render(res.Edges), heuristic: render(heurEdges), labels: labels}
}

func (fr fixtureRun) unit(t *testing.T, dir, name string) UnitResult {
	t.Helper()
	for _, u := range fr.res.Units {
		if u.Dir == dir && u.Name == name {
			return u
		}
	}
	t.Fatalf("no unit %s/%s in %v", dir, name, fr.res.Units)
	return UnitResult{}
}

// TestResolve_ShadowingBeatsHeuristic is evidence case 1: a package-level
// function shadowed by a local binding. The name heuristic resolves the
// shadowed call site to the package-level function (a WRONG derived edge);
// go/types binds it to the local and Resolve emits nothing for it — while the
// genuine call site is confirmed.
func TestResolve_ShadowingBeatsHeuristic(t *testing.T) {
	fr := runFixture(t, map[string]string{
		"decl.go": "package shadow\n\nfunc helper() {}\n",
		"use.go": `package shadow

func target() { helper() }

func shadowed() {
	helper := func() {}
	helper()
}
`,
	})

	wrong := "calls shadow.shadowed -> shadow.helper"
	if !fr.heuristic[wrong] {
		t.Fatalf("fixture no longer demonstrates the heuristic failure: linker edges lack %q\nheuristic: %v", wrong, fr.heuristic)
	}
	if fr.confirmed[wrong] {
		t.Errorf("Resolve emitted the shadowed (wrong) edge %q — go/types must bind the call to the local", wrong)
	}
	if !fr.confirmed["calls shadow.target -> shadow.helper"] {
		t.Errorf("genuine call site missing from confirmed edges: %v", fr.confirmed)
	}
}

// TestResolve_MethodDispatchByReceiverType is evidence case 2: two types with
// the same method name, both called. The linker's receiver-method heuristic
// keys on the bare method name and cannot tell the receivers apart, so it
// resolves both call sites to the SAME method node — at least one of them
// wrong. Only the type-checker knows each receiver's static type.
func TestResolve_MethodDispatchByReceiverType(t *testing.T) {
	fr := runFixture(t, map[string]string{
		"disp.go": `package disp

type A struct{}

func (A) Run() {}

type B struct{}

func (B) Run() {}

func use() {
	var a A
	var b B
	a.Run()
	b.Run()
}
`,
	})

	toA, toB := "calls disp.use -> disp.A.Run", "calls disp.use -> disp.B.Run"
	if !fr.confirmed[toA] || !fr.confirmed[toB] {
		t.Errorf("dispatch by static receiver type incomplete: want both %q and %q in %v", toA, toB, fr.confirmed)
	}
	if fr.heuristic[toA] && fr.heuristic[toB] {
		t.Fatalf("fixture no longer demonstrates the heuristic failure: the name heuristic resolved BOTH dispatches correctly\nheuristic: %v", fr.heuristic)
	}
}

// TestResolve_SameNameTwoPackages is evidence case 3: identically named
// functions in two intra-repo packages. The confirmed edge must follow the
// import, never the name.
func TestResolve_SameNameTwoPackages(t *testing.T) {
	fr := runFixture(t, map[string]string{
		"go.mod":   "module example.com/m\n\ngo 1.26\n",
		"u1/u1.go": "package u1\n\nfunc Do() {}\n",
		"u2/u2.go": "package u2\n\nfunc Do() {}\n",
		"main.go": `package main

import "example.com/m/u2"

func main() { u2.Do() }
`,
	})

	if !fr.confirmed["calls main.main -> u2.Do"] {
		t.Errorf("cross-package call not confirmed: %v", fr.confirmed)
	}
	if fr.confirmed["calls main.main -> u1.Do"] {
		t.Errorf("confirmed edge to u1.Do — the call goes through the u2 import")
	}
}

// TestResolve_ImplementsProven pins the implements derivation: method-set
// satisfaction proven by types.Implements for value receivers, pointer
// receivers, and interface-to-interface supersets — and nothing else (no
// alias edges, no unsatisfied types).
func TestResolve_ImplementsProven(t *testing.T) {
	fr := runFixture(t, map[string]string{
		"iface.go": `package iface

type Reader interface {
	Read(p []byte) (n int, err error)
}

type Closer interface {
	Read(p []byte) (n int, err error)
	Close() error
}

type W struct{}

func (W) Read(p []byte) (n int, err error) { return 0, nil }

type P struct{}

func (*P) Read(p []byte) (n int, err error) { return 0, nil }

type N struct{}

type RAlias = W
`,
	})

	var implements []string
	for s := range fr.confirmed {
		if strings.HasPrefix(s, "implements ") {
			implements = append(implements, s)
		}
	}
	sort.Strings(implements)
	want := []string{
		"implements iface.Closer -> iface.Reader", // interface superset
		"implements iface.P -> iface.Reader",      // pointer method set
		"implements iface.W -> iface.Reader",      // value method set
	}
	if fmt.Sprint(implements) != fmt.Sprint(want) {
		t.Errorf("implements edges = %v, want exactly %v", implements, want)
	}
}

// TestResolve_CallsVsReferences pins the kind classification: call position +
// function object = calls; function values, type mentions, and conversions
// are references.
func TestResolve_CallsVsReferences(t *testing.T) {
	fr := runFixture(t, map[string]string{
		"kinds.go": `package kinds

func helper() int { return 0 }

type T struct{}

type MyInt int

func consume(t T) {}

var fn = helper

var val = helper()

var conv = MyInt(3)

func caller() T {
	x := helper()
	_ = x
	var t T
	consume(t)
	return t
}
`,
	})

	for _, wantEdge := range []string{
		"references kinds.fn -> kinds.helper",  // function VALUE, not a call
		"calls kinds.val -> kinds.helper",      // initializer call
		"references kinds.conv -> kinds.MyInt", // conversion is not a call
		"calls kinds.caller -> kinds.helper",
		"calls kinds.caller -> kinds.consume",
		"references kinds.caller -> kinds.T",
		"references kinds.consume -> kinds.T", // parameter type
	} {
		if !fr.confirmed[wantEdge] {
			t.Errorf("missing %q\nconfirmed: %v", wantEdge, fr.confirmed)
		}
	}
	for _, wrongEdge := range []string{
		"calls kinds.fn -> kinds.helper",
		"calls kinds.conv -> kinds.MyInt",
	} {
		if fr.confirmed[wrongEdge] {
			t.Errorf("kind misclassified: %q must not exist", wrongEdge)
		}
	}
}

// TestResolve_TolerantStubsAndDegradation pins the fail-open contract: stub
// imports (stdlib here) cause swallowed type errors WITHOUT degrading the
// unit or losing its intra-repo edges, while a file that does not fully parse
// degrades exactly its own unit and nothing else.
func TestResolve_TolerantStubsAndDegradation(t *testing.T) {
	fr := runFixture(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.26\n",
		"main.go": `package main

import (
	"fmt"

	"example.com/m/util"
)

func main() { fmt.Println(util.Answer()) }
`,
		"util/util.go":     "package util\n\nfunc Answer() int { return 42 }\n",
		"broken/broken.go": "package broken\n\nfunc f() { if }\n",
	})

	root := fr.unit(t, ".", "main")
	if root.Degraded != "" {
		t.Errorf("root unit degraded (%q) — stub-import errors must be tolerated", root.Degraded)
	}
	if root.TypeErrors == 0 {
		t.Errorf("root unit reports zero type errors — the fmt stub must have produced some (tolerance untested otherwise)")
	}
	if !fr.confirmed["calls main.main -> util.Answer"] {
		t.Errorf("intra-repo edge lost despite stub tolerance: %v", fr.confirmed)
	}

	broken := fr.unit(t, "broken", "broken")
	if broken.Degraded != "file does not fully parse: broken/broken.go" {
		t.Errorf("broken unit Degraded = %q, want full-parse reason", broken.Degraded)
	}
	for s := range fr.confirmed {
		if strings.Contains(s, "broken.") {
			t.Errorf("edge involving the degraded unit leaked out: %q", s)
		}
	}
}

// TestResolve_NeverFabricate pins the committed-set discipline: an endpoint
// whose NodeId is not committed drops the intent (counted), and an empty
// committed set yields zero edges no matter what the type-checker proved.
func TestResolve_NeverFabricate(t *testing.T) {
	files := map[string]string{
		"decl.go": "package nf\n\nfunc helper() {}\n",
		"use.go":  "package nf\n\nfunc target() { helper() }\n",
	}
	byteFiles := map[string][]byte{}
	for k, v := range files {
		byteFiles[k] = []byte(v)
	}

	// Full committed set as baseline.
	full := runFixture(t, files)
	if !full.confirmed["calls nf.target -> nf.helper"] {
		t.Fatalf("baseline edge missing: %v", full.confirmed)
	}

	// Remove helper's node from the committed set: the intent must be dropped
	// and counted, not attached to a fabricated endpoint.
	committed := map[model.NodeId]struct{}{}
	p := corparse.NewGoParser()
	for _, name := range []string{"decl.go", "use.go"} {
		pr, err := p.Parse(context.Background(), name, byteFiles[name])
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		for _, n := range pr.Nodes {
			if n.QualifiedName() == "nf.helper" {
				continue
			}
			committed[n.ID()] = struct{}{}
		}
	}
	res, err := Resolve(byteFiles, committed)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Edges) != 0 {
		t.Errorf("edges emitted against an uncommitted endpoint: %d", len(res.Edges))
	}
	if res.DroppedIntents == 0 {
		t.Errorf("dropped intent not counted")
	}

	// Nil committed set: everything drops.
	res, err = Resolve(byteFiles, nil)
	if err != nil {
		t.Fatalf("Resolve(nil committed): %v", err)
	}
	if len(res.Edges) != 0 || res.DroppedIntents == 0 {
		t.Errorf("nil committed set: edges=%d dropped=%d, want 0 and >0", len(res.Edges), res.DroppedIntents)
	}
}

// TestResolve_Deterministic re-runs the richest fixture many times and demands
// a byte-identical full rendering (edge ids, evidence, reasons, units, drop
// counts) — the property the full-vs-incremental byte-parity design leans on.
func TestResolve_Deterministic(t *testing.T) {
	files := map[string]string{
		"go.mod":   "module example.com/m\n\ngo 1.26\n",
		"u1/u1.go": "package u1\n\nfunc Do() {}\n",
		"u2/u2.go": "package u2\n\nfunc Do() {}\n\ntype Reader interface {\n\tRead(p []byte) (int, error)\n}\n",
		"main.go": `package main

import (
	"fmt"

	"example.com/m/u2"
)

type W struct{}

func (W) Read(p []byte) (int, error) { return 0, nil }

func helper() int { return 0 }

var fn = helper

func main() {
	fmt.Println(helper())
	u2.Do()
	shadowed := func() {}
	shadowed()
}
`,
		"broken/broken.go": "package broken\n\nfunc f() { if }\n",
	}
	byteFiles := map[string][]byte{}
	for k, v := range files {
		byteFiles[k] = []byte(v)
	}
	p := corparse.NewGoParser()
	committed := map[model.NodeId]struct{}{}
	for name, content := range byteFiles {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		pr, err := p.Parse(context.Background(), name, content)
		if err != nil {
			continue // fail-closed skip, as in ingest's full index
		}
		for _, n := range pr.Nodes {
			committed[n.ID()] = struct{}{}
		}
	}

	renderAll := func(r Result) string {
		var b strings.Builder
		for _, e := range r.Edges {
			fmt.Fprintf(&b, "%s %s->%s %s %s %v %q\n", e.ID(), e.From(), e.To(), e.Kind(), e.Tier(), e.Evidence(), e.Reason())
		}
		for _, u := range r.Units {
			fmt.Fprintf(&b, "unit %s/%s degraded=%q typeErrs=%d\n", u.Dir, u.Name, u.Degraded, u.TypeErrors)
		}
		for _, s := range r.SkippedFiles {
			fmt.Fprintf(&b, "skip %s: %s\n", s.Path, s.Reason)
		}
		fmt.Fprintf(&b, "dropped=%d\n", r.DroppedIntents)
		return b.String()
	}

	var first string
	for i := 0; i < 20; i++ {
		res, err := Resolve(byteFiles, committed)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		got := renderAll(res)
		if first == "" {
			first = got
			if len(res.Edges) == 0 {
				t.Fatalf("determinism fixture produced no edges — it must exercise a real edge set")
			}
			continue
		}
		if got != first {
			t.Fatalf("iteration %d produced different output:\n--- first ---\n%s--- now ---\n%s", i, first, got)
		}
	}
}
