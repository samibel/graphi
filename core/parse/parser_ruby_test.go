package parse

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// rubyGoldenFixture is the committed, FROZEN Ruby fixture (SW-054). Ruby has no
// local-var top-level kind at this tier, so `variable` is ABSENT BY DESIGN.
const rubyGoldenFixture = `require "logger"

TAX = 7

class Store
  def checkout
    price(3)
  end
end

def price(c)
  TAX
end

def run
  price(3)
  obj.log(1)
  run()
end
`

func parseRubyFixture(t *testing.T) ([]model.Node, []model.Edge) {
	t.Helper()
	res := parseRubyFixtureResult(t)
	return res.Nodes, res.Edges
}

func parseRubyFixtureResult(t *testing.T) *ParseResult {
	t.Helper()
	res, err := NewRubyParser().Parse(context.Background(), "shop/cart.rb", []byte(rubyGoldenFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return res
}

// TestExtractRuby_Nodes asserts the EXACT closed node set + kinds; variable absent.
func TestExtractRuby_Nodes(t *testing.T) {
	nodes, _ := parseRubyFixture(t)

	want := map[string]model.NodeKind{
		"shop/cart.rb":  goKindFile,
		"shop.TAX":      goKindConstant,
		"shop.Store":    goKindType,
		"shop.checkout": goKindMethod,
		"shop.price":    goKindFunction,
		"shop.run":      goKindFunction,
	}
	for qn, kind := range want {
		n, ok := nodeByQN(nodes, qn)
		if !ok {
			t.Errorf("missing node %q", qn)
			continue
		}
		if n.Kind() != kind {
			t.Errorf("node %q kind = %q, want %q", qn, n.Kind(), kind)
		}
	}
	if len(nodes) != len(want) {
		t.Errorf("node count = %d, want %d (%v)", len(nodes), len(want), want)
	}

	emitted := map[model.NodeKind]struct{}{}
	for _, n := range nodes {
		emitted[n.Kind()] = struct{}{}
	}
	for _, k := range []model.NodeKind{"file", "function", "method", "type", "constant"} {
		if _, ok := emitted[k]; !ok {
			t.Errorf("expected kind literal %q to be present", k)
		}
	}
	if _, ok := emitted["variable"]; ok {
		t.Errorf("ruby must not emit variable (absent by design)")
	}
	for bad := range emitted {
		switch string(bad) {
		case "file", "function", "method", "type", "constant":
		default:
			t.Errorf("unexpected node kind literal %q (closed vocabulary violated)", bad)
		}
	}
}

// TestExtractRuby_Edges asserts intra-file defines/calls edges with use-site provenance.
func TestExtractRuby_Edges(t *testing.T) {
	nodes, edges := parseRubyFixture(t)

	id := func(qn string) model.NodeId {
		n, ok := nodeByQN(nodes, qn)
		if !ok {
			t.Fatalf("node %q not found", qn)
		}
		return n.ID()
	}
	has := func(from, to model.NodeId, kind string) (model.Edge, bool) {
		for _, e := range edges {
			if e.From() == from && e.To() == to && e.Kind() == kind {
				return e, true
			}
		}
		return model.Edge{}, false
	}

	file := id("shop/cart.rb")
	for _, qn := range []string{"shop.TAX", "shop.Store", "shop.checkout", "shop.price", "shop.run"} {
		if _, ok := has(file, id(qn), goEdgeDefines); !ok {
			t.Errorf("missing defines edge file -> %q", qn)
		}
	}
	if _, ok := has(id("shop.checkout"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge checkout -> price")
	}
	if _, ok := has(id("shop.run"), id("shop.price"), goEdgeCalls); !ok {
		t.Error("missing calls edge run -> price")
	}
	callEdge, ok := has(id("shop.run"), id("shop.run"), goEdgeCalls)
	if !ok {
		t.Fatal("missing recursive calls edge run -> run")
	}
	// Use-site: run -> run recursive call on line 18 (1-based).
	if got := callEdge.Evidence()[0]; got != "shop/cart.rb:18" {
		t.Errorf("run->run call evidence = %q, want %q (use-site file:line pin)", got, "shop/cart.rb:18")
	}

	for _, e := range edges {
		if e.Kind() == goEdgeCalls && e.To() == file {
			t.Errorf("unexpected call edge into file node: %v", e)
		}
		if !e.Tier().Valid() || e.Reason() == "" || len(e.Evidence()) == 0 {
			t.Errorf("edge %s lacks provenance", e.ID())
		}
		for _, ev := range e.Evidence() {
			if !strings.HasPrefix(ev, "shop/cart.rb:") {
				t.Errorf("edge %s evidence %q is not file:line", e.ID(), ev)
			}
		}
	}
}

// TestExtractRuby_PendingRefs asserts the selector use obj.log becomes a PendingRef.
func TestExtractRuby_PendingRefs(t *testing.T) {
	res := parseRubyFixtureResult(t)
	var foundSelector bool
	for _, p := range res.PendingRefs {
		if p.FromQN == "shop.run" && p.Selector && p.SelectorBase == "obj" && p.Name == "log" && p.Kind == goEdgeCalls {
			foundSelector = true
		}
		if p.FromQN == "" || p.Name == "" {
			t.Errorf("pending ref with empty FromQN/Name: %+v", p)
		}
	}
	if !foundSelector {
		t.Errorf("expected a selector PendingRef for obj.log, got %+v", res.PendingRefs)
	}
}

// TestExtractRuby_Imports asserts require is recorded + surfaced in References.
func TestExtractRuby_Imports(t *testing.T) {
	res := parseRubyFixtureResult(t)
	var found bool
	for _, imp := range res.Imports {
		if imp.Path == "logger" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected require logger, got %+v", res.Imports)
	}
	var inRefs bool
	for _, r := range res.References {
		if r == "logger" {
			inRefs = true
		}
	}
	if !inRefs {
		t.Errorf("expected logger in References, got %+v", res.References)
	}
}

// TestExtractRuby_Deterministic asserts repeated + concurrent (-race) determinism.
func TestExtractRuby_Deterministic(t *testing.T) {
	n1, e1 := parseRubyFixture(t)
	n2, e2 := parseRubyFixture(t)
	if len(n1) != len(n2) || len(e1) != len(e2) {
		t.Fatalf("non-deterministic counts")
	}
	for i := range n1 {
		if n1[i].ID() != n2[i].ID() {
			t.Errorf("node %d id drift", i)
		}
	}
	for i := range e1 {
		if e1[i].ID() != e2[i].ID() {
			t.Errorf("edge %d id drift", i)
		}
	}
	want := idStream(n1, e1)
	const workers = 32
	var wg sync.WaitGroup
	results := make([]string, workers)
	parser := NewRubyParser()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := parser.Parse(context.Background(), "shop/cart.rb", []byte(rubyGoldenFixture))
			if err != nil {
				t.Errorf("worker %d parse: %v", idx, err)
				return
			}
			results[idx] = idStream(res.Nodes, res.Edges)
		}(w)
	}
	wg.Wait()
	for i, got := range results {
		if got != want {
			t.Errorf("worker %d produced a divergent id stream", i)
		}
	}
}

// rubyBareCallFixture exercises the SW-194b.5 emission: Ruby's dominant call
// spelling is receiver-less AND parenless, which tree-sitter-ruby represents as
// a bare `identifier` node — the SAME node it uses for a local-variable read and
// for a parameter read. The fixture puts all three shapes in one file so the
// emitter is pinned on the distinction, not just on the happy case.
const rubyBareCallFixture = `def checkout
  helper
end

def local_only
  memo = 1
  memo
end

def with_param(arg, opt = 1, *rest, &blk)
  arg
  opt
  rest
  blk
end

def blocky
  [1].each { |it| it }
end

def rescued
  begin
    guarded
  rescue => err
    err
  end
end
`

// TestExtractRuby_BareParenlessCallIsAPendingRef is the SW-194b.5 AC-1 pin for
// Ruby. `helper` in `def checkout; helper; end` is a cross-file call site with
// no receiver and no argument list; before SW-194b.5 the Ruby extractor scanned
// only `call` CST nodes, so this spelling produced NO PendingRef and the linker
// had nothing to resolve through the require's ambient directory — the graph
// could not answer "who calls helper?" across files. The emission site is
// core/parse/parser_ruby.go's rubyScanUses.
func TestExtractRuby_BareParenlessCallIsAPendingRef(t *testing.T) {
	res, err := NewRubyParser().Parse(context.Background(), "app/main.rb", []byte(rubyBareCallFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]bool{}
	for _, p := range res.PendingRefs {
		if p.Selector {
			continue
		}
		got[p.FromQN+"."+p.Name] = true
	}

	// The bare, receiver-less, parenless call sites MUST be recorded.
	for _, want := range []string{"app.checkout.helper", "app.rescued.guarded"} {
		if !got[want] {
			t.Errorf("bare parenless call %q produced no PendingRef; got %+v", want, res.PendingRefs)
		}
	}

	// Locally-bound names are NOT call sites and must NOT be recorded: a local
	// assignment target, every parameter spelling, a block parameter and a
	// rescue exception variable. Emitting these is the false-positive failure
	// mode the emitter's scope table exists to prevent.
	for _, never := range []string{
		"app.local_only.memo",
		"app.with_param.arg", "app.with_param.opt", "app.with_param.rest", "app.with_param.blk",
		"app.blocky.it",
		"app.rescued.err",
	} {
		if got[never] {
			t.Errorf("locally-bound name %q was recorded as a call PendingRef; got %+v", never, res.PendingRefs)
		}
	}

	// The method's own name is a declaration, not a use.
	for _, never := range []string{"app.checkout.checkout", "app.blocky.blocky"} {
		if got[never] {
			t.Errorf("method name %q was recorded as a call PendingRef", never)
		}
	}
}

// rubyBinderNegative is one snippet plus the EXACT set of bare PendingRef names
// the extractor is allowed to record for it. Asserting the exact set, not just
// the absence of one name, is deliberate: a subset assertion cannot see a
// phantom the author did not predict.
type rubyBinderNegative struct {
	name string
	src  string
	want []string // bare (non-selector) PendingRef names, sorted
}

// TestExtractRuby_BindersAreNotCallSites is the SW-194b.5 round-2 pin for the
// FALSE-POSITIVE half of Ruby's bare-identifier emission, at the parser
// boundary. Round 1 emitted a PendingRef for every identifier its binder
// enumeration did not recognise; the SW-194b.5 review demonstrated ten binder
// forms it missed, each of which became an INVENTED `calls` edge as soon as the
// bound name collided with a real definition. Rows 1-10 below are exactly those
// forms. The rest were found sweeping around them in the same spirit.
//
// A missing edge is the gap this story set out to close; an invented edge is a
// lie in the graph and is strictly worse, so every row here wants the EMPTY set
// unless the snippet contains a real call. The positive rows at the end keep the
// table honest: they prove the emitter is still live, so an empty `want` means
// "correctly silent", never "the emitter is dead".
//
// The end-to-end consequence — that none of these mints a graph edge at either
// the heuristic or the derived tier — is pinned separately in
// engine/conformance/rubyphantomedges_test.go, driven through the real ingester.
func TestExtractRuby_BindersAreNotCallSites(t *testing.T) {
	cases := []rubyBinderNegative{
		// --- the ten forms the SW-194b.5 review demonstrated ---
		{name: "in_array_pattern", src: "def checkout(x)\n  case x\n  in [a, b]\n    a\n    b\n  end\nend\n"},
		{name: "in_hash_pattern", src: "def checkout(x)\n  case x\n  in {k: c}\n    c\n  end\nend\n"},
		{name: "in_as_pattern", src: "def checkout(x)\n  case x\n  in Integer => n\n    n\n  end\nend\n"},
		{name: "in_array_pattern_splat", src: "def checkout(x)\n  case x\n  in [h, *tl]\n    h\n    tl\n  end\nend\n"},
		{name: "in_hash_pattern_shorthand", src: "def checkout(x)\n  case x\n  in {name:}\n    name\n  end\nend\n"},
		{name: "splat_multiple_assignment", src: "def checkout(arr)\n  first, *rest = arr\n  first\n  rest\nend\n"},
		{name: "nested_destructuring", src: "def checkout(arr)\n  (a, b), c = arr\n  a\n  b\n  c\nend\n"},
		{name: "rightward_pattern_assignment", src: "def checkout(payload)\n  payload => {name: label}\n  label\nend\n"},
		{name: "numbered_block_parameter", src: "def checkout(xs)\n  xs.map { _1 + _2 }\nend\n"},
		{name: "it_block_parameter", src: "def checkout(xs)\n  xs.each { it }\nend\n"},

		// --- found sweeping around the review's list ---
		{name: "in_find_pattern", src: "def checkout(x)\n  case x\n  in [*, q, *]\n    q\n  end\nend\n"},
		{name: "in_bare_binder", src: "def checkout(x)\n  case x\n  in y\n    y\n  end\nend\n"},
		{name: "in_alternative_pattern", src: "def checkout(x)\n  case x\n  in A | B\n    1\n  end\nend\n"},
		{name: "in_nested_pattern", src: "def checkout(x)\n  case x\n  in {a: {b: c}, d: [e, f]}\n    c\n    e\n    f\n  end\nend\n"},
		{name: "in_pin_pattern", src: "def checkout(x, y)\n  case x\n  in ^y\n    y\n  end\nend\n"},
		{name: "in_test_pattern", src: "def checkout(payload)\n  if payload in {q: w}\n    w\n  end\nend\n"},
		{name: "in_guard_reads_binder", src: "def checkout(x)\n  case x\n  in [p] if p > 1\n    p\n  end\nend\n"},
		{name: "destructured_block_parameter", src: "def checkout(xs)\n  xs.each { |(b, c)| b; c }\nend\n"},
		{name: "block_local_variable", src: "def checkout(xs)\n  xs.each { |q; loc| q; loc }\nend\n"},
		{name: "for_loop_destructuring", src: "def checkout(arr)\n  for (i, j) in arr\n    i\n    j\n  end\nend\n"},
		{name: "keyword_rest_parameter", src: "def checkout(**kw, &blk)\n  kw\n  blk\nend\n"},
		{name: "alias_statement", src: "def checkout\n  alias old_name new_name\nend\n"},
		{name: "undef_statement", src: "def checkout\n  undef gone\nend\n"},
		{name: "element_reference_target", src: "def checkout(arr)\n  arr[0] = 1\n  arr\nend\n"},
		{name: "attribute_target", src: "def checkout(obj)\n  obj.attr = 1\n  obj\nend\n"},
		{name: "chained_assignment", src: "def checkout\n  a = b = 1\n  a\n  b\nend\n"},
		{name: "nested_def_is_not_the_outer_method", src: "def checkout\n  def inner\n    q\n  end\nend\n"},

		// --- positives: the emitter is LIVE, so the empty sets above mean
		// "correctly silent", not "nothing is measured" ---
		{name: "POSITIVE_bare_statement", src: "def checkout\n  helper\nend\n", want: []string{"helper"}},
		{name: "POSITIVE_assignment_rhs", src: "def checkout\n  x = helper\n  x\nend\n", want: []string{"helper"}},
		{name: "POSITIVE_parameter_default", src: "def checkout(b = helper)\n  b\nend\n", want: []string{"helper"}},
		{name: "POSITIVE_pattern_scrutinee", src: "def checkout\n  case helper\n  in [z]\n    z\n  end\nend\n", want: []string{"helper"}},
		{name: "POSITIVE_pattern_guard", src: "def checkout(x)\n  case x\n  in [p] if helper\n    p\n  end\nend\n", want: []string{"helper"}},
		{name: "POSITIVE_endless_method", src: "def checkout(v) = helper\n", want: []string{"helper"}},
		{name: "POSITIVE_rescue_body", src: "def checkout\n  begin\n    guarded\n  rescue => err\n    err\n  end\nend\n", want: []string{"guarded"}},
		{name: "POSITIVE_block_body", src: "def checkout(xs)\n  xs.each { |q| helper }\nend\n", want: []string{"helper"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NewRubyParser().Parse(context.Background(), "app/main.rb", []byte(tc.src))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var got []string
			for _, p := range res.PendingRefs {
				if p.Selector {
					continue
				}
				got = append(got, p.Name)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("bare PendingRef set mismatch for\n%s\n got: %v\nwant: %v\n"+
					"an UNEXPECTED name here is an invented call site: the identifier is a "+
					"binding, a declaration or a name reference, not a call",
					tc.src, got, want)
			}
		})
	}
}
