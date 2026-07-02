package typeresolve

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/model"
	corparse "github.com/samibel/graphi/core/parse"
)

// crossFixtures is ONE multi-file package exercising every declaration shape
// whose naming the extractor and this package must agree on: plain functions,
// init, blank func, value/pointer/generic receivers, struct/interface embeds,
// aliases, grouped consts with iota, multi-name var specs — plus everything
// that must map to NO node: locals, params, fields, interface methods, blank
// values, function literals.
var crossFixtures = map[string]string{
	"a.go": `package fix

func PlainFunc() int { return varA }

func init() {}

func _() {}

type Base struct{ n int }

type Widget struct {
	Base
	label string
}

func (w Widget) Value() int      { return w.Base.n }
func (w *Widget) SetLabel(s string) { w.label = s }
`,
	"b.go": `package fix

type Stack[T any] struct{ items []T }

func (s *Stack[T]) Push(v T) { s.items = append(s.items, v) }
func (s Stack[T]) Len() int  { return len(s.items) }

type Reader interface {
	Read(p []byte) (int, error)
}

type Closer interface {
	Reader
	Close() error
}

type Alias = Widget

type _ int

const (
	KA = iota
	KB
	_
)

var varA, varB = 1, 2

var _ = varB
`,
	"c.go": `package fix

func locals() int {
	type localT int
	const localC = 1
	var localV localT = localT(localC)
	f := func() int { return int(localV) }
	return f()
}
`,
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// extractorNodes runs the REAL core/parse Go parser over the fixtures and
// returns NodeId -> "kind qn @file" for every non-file node it emits.
func extractorNodes(t *testing.T) map[model.NodeId]string {
	t.Helper()
	p := corparse.NewGoParser()
	out := map[model.NodeId]string{}
	for _, name := range sortedNames(crossFixtures) {
		res, err := p.Parse(context.Background(), name, []byte(crossFixtures[name]))
		if err != nil {
			t.Fatalf("extractor parse %s: %v", name, err)
		}
		for _, n := range res.Nodes {
			if n.Kind() == "file" {
				continue
			}
			out[n.ID()] = fmt.Sprintf("%s %s @%s", n.Kind(), n.QualifiedName(), n.SourcePath())
		}
	}
	return out
}

// typeCheckFixtures type-checks the same fixtures with stdlib go/types and
// returns the FileSet plus the Defs map.
func typeCheckFixtures(t *testing.T) (*token.FileSet, map[*ast.Ident]types.Object) {
	t.Helper()
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range sortedNames(crossFixtures) {
		f, err := parser.ParseFile(fset, name, crossFixtures[name], parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("types parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	conf := types.Config{
		// The fixture package is self-contained; any import would be a fixture
		// bug, surfaced through the collected error below.
		Error: func(err error) { t.Errorf("type-check error (fixture must be clean): %v", err) },
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	if _, err := conf.Check("fix", fset, files, info); err != nil {
		t.Fatalf("type-check: %v", err)
	}
	return fset, info.Defs
}

// TestGoldenCross_ExtractorVsReconstruction is the load-bearing test of the
// typeresolve phase: the NodeIds this package reconstructs from types.Objects
// must be EXACTLY the NodeIds the real extractor emitted for the same source —
// full identity (kind + qualified name + source path), not just name shape.
//
// Two directions, asserted separately because they fail differently:
//   - reconstruction-only (fabrication) must be EMPTY: an id the extractor
//     never created would let a confirmed edge point at a phantom node.
//   - extractor-only (skipped) must be exactly the documented blank-type
//     asymmetry: nodes a confirmed edge can never need (see qn.go).
func TestGoldenCross_ExtractorVsReconstruction(t *testing.T) {
	want := extractorNodes(t)
	fset, defs := typeCheckFixtures(t)

	got := map[model.NodeId]string{}
	for ident, obj := range defs {
		if obj == nil {
			continue
		}
		id, ok := NodeIDFor(obj, fset)
		if !ok {
			continue
		}
		kind, qn, _ := ObjectNode(obj)
		if prev, dup := got[id]; dup && prev != fmt.Sprintf("%s %s @%s", kind, qn, fset.Position(obj.Pos()).Filename) {
			t.Errorf("two objects reconstruct to the same NodeId with different identities: %s vs %s %s", prev, kind, qn)
		}
		got[id] = fmt.Sprintf("%s %s @%s", kind, qn, fset.Position(obj.Pos()).Filename)
		_ = ident
	}

	// Direction 1 — never fabricate: every reconstructed id must exist in the
	// extractor's output.
	for id, desc := range got {
		if _, exists := want[id]; !exists {
			t.Errorf("reconstruction FABRICATED a node the extractor never emitted: %s (id %s)", desc, id)
		}
	}

	// Direction 2 — completeness: every extractor node must be reconstructed,
	// except the documented blank-type asymmetry.
	var skippedBlankTypes []string
	for id, desc := range want {
		if _, covered := got[id]; covered {
			continue
		}
		if strings.HasPrefix(desc, "type ") && strings.Contains(desc, "._ ") {
			skippedBlankTypes = append(skippedBlankTypes, desc)
			continue
		}
		t.Errorf("extractor node NOT reconstructed (confirmed edges to it would be dropped): %s (id %s)", desc, id)
	}
	if len(skippedBlankTypes) != 1 {
		t.Errorf("expected exactly the one documented blank-type asymmetry, got %d: %v", len(skippedBlankTypes), skippedBlankTypes)
	}

	// Sanity floor: the fixture must keep exercising a substantial shape set.
	if len(want) < 15 {
		t.Errorf("fixture shrank to %d extractor nodes — keep the declaration-shape coverage", len(want))
	}
}

// TestObjectNode_NoNodeShapes pins ok=false for every object class the
// extractor creates no node for. Fabricating any of these would attach
// confirmed edges to phantom endpoints.
func TestObjectNode_NoNodeShapes(t *testing.T) {
	fset, defs := typeCheckFixtures(t)
	_ = fset

	wantNoNode := map[string]string{
		"Read":   "interface method (no top-level FuncDecl)",
		"Close":  "interface method (no top-level FuncDecl)",
		"label":  "struct field",
		"n":      "struct field",
		"items":  "struct field",
		"localT": "function-local type",
		"localC": "function-local const",
		"localV": "function-local var",
		"p":      "parameter",
		"s":      "parameter/receiver",
		"v":      "parameter",
		"w":      "receiver",
		"f":      "local func-literal binding",
		"T":      "type parameter",
	}
	for ident, obj := range defs {
		if obj == nil {
			continue
		}
		reason, mustSkip := wantNoNode[ident.Name]
		if !mustSkip {
			continue
		}
		if kind, qn, ok := ObjectNode(obj); ok {
			t.Errorf("%s (%s) mapped to a node %s %s — must be ok=false", ident.Name, reason, kind, qn)
		}
	}
}

// TestObjectNode_QNShapes pins the exact qualified-name strings for the tricky
// shapes (receiver stripping, generics, init/blank funcs) independently of the
// extractor, so a failure here localizes to the reconstruction rules.
func TestObjectNode_QNShapes(t *testing.T) {
	_, defs := typeCheckFixtures(t)

	want := map[string][2]string{ // ident -> {kind, qn}; only unambiguous names
		"PlainFunc": {KindFunction, "fix.PlainFunc"},
		"init":      {KindFunction, "fix.init"},
		"Value":     {KindMethod, "fix.Widget.Value"},
		"SetLabel":  {KindMethod, "fix.Widget.SetLabel"}, // pointer receiver stripped
		"Push":      {KindMethod, "fix.Stack.Push"},      // generic + pointer receiver
		"Len":       {KindMethod, "fix.Stack.Len"},       // generic value receiver
		"Alias":     {KindType, "fix.Alias"},
		"Widget":    {KindType, "fix.Widget"},
		"KA":        {KindConstant, "fix.KA"},
		"varB":      {KindVariable, "fix.varB"},
	}
	seen := map[string]bool{}
	for ident, obj := range defs {
		if obj == nil {
			continue
		}
		exp, care := want[ident.Name]
		if !care {
			continue
		}
		kind, qn, ok := ObjectNode(obj)
		if !ok {
			t.Errorf("%s: ok=false, want %s %s", ident.Name, exp[0], exp[1])
			continue
		}
		if kind != exp[0] || qn != exp[1] {
			t.Errorf("%s: got %s %s, want %s %s", ident.Name, kind, qn, exp[0], exp[1])
		}
		seen[ident.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("fixture no longer declares %s — restore the shape coverage", name)
		}
	}
}
