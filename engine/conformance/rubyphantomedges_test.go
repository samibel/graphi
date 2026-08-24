package conformance_test

// SW-194b.5 (rebuild round 1) — the PHANTOM-EDGE gate for Ruby's bare-identifier
// call emission, asserted END TO END through the real ingester rather than at the
// parser boundary.
//
// WHY THIS FILE EXISTS. SW-194b.5 round 1 taught the Ruby extractor to record a
// receiver-less, parenless `identifier` as a call site, because that is Ruby's
// dominant call spelling and the graph could not otherwise answer "who calls
// helper?" across files. The same CST node is also how Ruby spells a local READ,
// and round 1's guard enumerated the binder shapes it knew about. Everything it
// did not know about — pattern matching, destructuring, implicit block
// parameters — became an INVENTED `calls` edge as soon as the bound name
// happened to match a real definition: `in {status: status}` minted
// app.checkout --calls--> lib.status at the heuristic tier, and
// `first, *rest = arr` minted app.checkout --calls--> app.rest at the derived
// tier, for calls that appear nowhere in the source.
//
// A missing edge is the gap the story set out to close; an invented edge is a
// LIE IN THE SHIPPED GRAPH and is strictly worse. This table pins the absence,
// at the tier the phantom actually appeared at, for every binder form the
// SW-194b.5 review demonstrated plus the ones found sweeping around them.
//
// NON-VACUITY IS BUILT IN, not asserted elsewhere: the `want: true` control rows
// drive the identical harness over the identical fixture shape with a REAL call
// and require the edge to be PRESENT at the named tier. If the emission ever
// stopped working altogether, the negatives would still pass and the controls
// would fail — so a green run means "the mechanism is live AND it does not fire
// on binders", never "nothing was measured".

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// rubyPhantomLib defines, in a SEPARATE directory, every bare name the binder
// bodies below bind. Collision with a real definition is the whole mechanism:
// a phantom PendingRef is inert until the name happens to exist.
const rubyPhantomLib = `def status
  1
end

def a
  1
end

def b
  1
end

def c
  1
end

def n
  1
end

def h
  1
end

def tl
  1
end

def name
  1
end

def rest
  1
end

def label
  1
end

def it
  1
end
`

type rubyPhantomCase struct {
	name string
	// body is spliced into `def checkout(payload, arr, xs) … end` in app/main.rb.
	body string
	// callee is the bare name whose edge is asserted present or absent.
	callee string
	// want is true for a CONTROL row (the edge MUST exist), false for a phantom
	// row (the edge must NOT exist).
	want bool
}

func rubyCrossFilePhantomCases() []rubyPhantomCase {
	return []rubyPhantomCase{
		{name: "control_real_bare_call", body: "  status\n", callee: "status", want: true},
		{name: "hash_pattern_binder", body: "  case payload\n  in {status: status}\n    status\n  end\n", callee: "status"},
		{name: "array_pattern_binder", body: "  case payload\n  in [a, b]\n    a\n    b\n  end\n", callee: "a"},
		{name: "array_pattern_binder_second", body: "  case payload\n  in [a, b]\n    a\n    b\n  end\n", callee: "b"},
		{name: "hash_pattern_value_binder", body: "  case payload\n  in {k: c}\n    c\n  end\n", callee: "c"},
		{name: "as_pattern_binder", body: "  case payload\n  in Integer => n\n    n\n  end\n", callee: "n"},
		{name: "array_pattern_splat_head", body: "  case payload\n  in [h, *tl]\n    h\n    tl\n  end\n", callee: "h"},
		{name: "array_pattern_splat_rest", body: "  case payload\n  in [h, *tl]\n    h\n    tl\n  end\n", callee: "tl"},
		{name: "hash_pattern_shorthand_key", body: "  case payload\n  in {name:}\n    name\n  end\n", callee: "name"},
		{name: "find_pattern_binder", body: "  case payload\n  in [*, c, *]\n    c\n  end\n", callee: "c"},
		{name: "bare_in_pattern_binder", body: "  case payload\n  in n\n    n\n  end\n", callee: "n"},
		{name: "pin_pattern_read", body: "  status = 1\n  case payload\n  in ^status\n    status\n  end\n", callee: "status"},
		{name: "splat_multiple_assignment", body: "  a, *rest = arr\n  rest\n", callee: "rest"},
		{name: "nested_destructuring_assignment", body: "  (a, b), c = arr\n  a\n  b\n  c\n", callee: "a"},
		{name: "nested_destructuring_assignment_tail", body: "  (a, b), c = arr\n  a\n  b\n  c\n", callee: "c"},
		{name: "rightward_pattern_assignment", body: "  payload => {name: label}\n  label\n", callee: "label"},
		{name: "in_test_pattern", body: "  if payload in {c: c}\n    c\n  end\n", callee: "c"},
		{name: "it_block_parameter", body: "  xs.each { it }\n", callee: "it"},
		{name: "destructured_block_parameter", body: "  xs.each { |(a, b)| a; b }\n", callee: "a"},
		{name: "block_local_variable", body: "  xs.each { |q; c| q; c }\n", callee: "c"},
		{name: "for_loop_destructuring", body: "  for (a, b) in arr\n    a\n    b\n  end\n", callee: "b"},
		// Found sweeping around the review's list: `alias`/`undef` name a method,
		// they do not call it, and neither name is a binding — so the binding
		// table alone cannot suppress them. These two rows are the witness that
		// the call-position allowlist (GATE 1) is load-bearing on its own.
		{name: "alias_old_name", body: "  alias status name\n", callee: "status"},
		{name: "alias_new_name", body: "  alias status name\n", callee: "name"},
		{name: "undef_name", body: "  undef status\n", callee: "status"},
	}
}

// TestRubyBinderFormsMintNoCrossFilePhantomCallEdge drives the CROSS-FILE,
// heuristic-tier path: `require_relative` opens the callee's directory as an
// ambient lookup, so a bare PendingRef whose name matches a definition there
// becomes app.checkout --calls--> lib.<name> at model.TierHeuristic.
func TestRubyBinderFormsMintNoCrossFilePhantomCallEdge(t *testing.T) {
	t.Parallel()
	for _, tc := range rubyCrossFilePhantomCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{
				"lib/util.rb": rubyPhantomLib,
				"app/main.rb": "require_relative \"../lib/util\"\n\ndef checkout(payload, arr, xs)\n" + tc.body + "end\n",
			}
			g := rubyPhantomGraph(t, files)
			assertRubyPhantom(t, g, "app.checkout", "lib."+tc.callee, model.TierHeuristic, tc.want)
		})
	}
}

// TestRubyBinderFormsMintNoIntraFilePhantomCallEdge drives the INTRA-FILE path,
// which needs no `require` at all and lands at model.TierDerived — the worse of
// the two, because every Ruby file that defines a method whose name is also an
// idiomatic binder name was exposed.
func TestRubyBinderFormsMintNoIntraFilePhantomCallEdge(t *testing.T) {
	t.Parallel()
	for _, tc := range rubyCrossFilePhantomCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{
				"app/main.rb": rubyPhantomLib + "\ndef checkout(payload, arr, xs)\n" + tc.body + "end\n",
			}
			g := rubyPhantomGraph(t, files)
			assertRubyPhantom(t, g, "app.checkout", "app."+tc.callee, model.TierDerived, tc.want)
		})
	}
}

// assertRubyPhantomCalleeDefined is the ANTI-VACUITY guard for this table. A
// negative row proves nothing unless the callee it names actually exists in the
// graph: a phantom PendingRef is inert until the name collides with a real
// definition, so a row naming an undefined callee could never have gone red and
// would be exactly the kind of green-that-measured-nothing this repo treats as
// its #1 defect class. `_1` is therefore NOT a row here — `def _1` is not legal
// Ruby, so no collision can be constructed — and is pinned at the parser
// boundary instead (core/parse/parser_ruby_test.go).
func assertRubyPhantomCalleeDefined(t *testing.T, g *graphView, defQN string) {
	t.Helper()
	if err := g.requirePresent(defQN); err != nil {
		t.Fatalf("VACUOUS ROW: %s is not defined in the graph, so the absence this row asserts "+
			"could never have been violated: %v", defQN, err)
	}
}

// rubyPhantomGraph ingests a file map with a single FULL pass and returns the
// resulting graph view.
func rubyPhantomGraph(t *testing.T, files map[string]string) *graphView {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	writeTree(t, root, files)
	store := newBackendStore(t, parityBackends()[0])
	if err := newIngester(t, store, "").IngestAll(ctx, root); err != nil {
		t.Fatalf("IngestAll: %v", err)
	}
	g, err := newGraphView(ctx, store)
	if err != nil {
		t.Fatalf("read graph: %v", err)
	}
	return g
}

func assertRubyPhantom(t *testing.T, g *graphView, from, to string, tier model.ConfidenceTier, want bool) {
	t.Helper()
	assertRubyPhantomCalleeDefined(t, g, to)
	e, ok := g.edge(from, "calls", to)
	switch {
	case want && !ok:
		t.Fatalf("CONTROL FAILED: %s --calls--> %s absent, so this table proves nothing about "+
			"absence either — the emission mechanism is not live. graph has %s", from, to, g.edgeList())
	case want && e.Tier() != tier:
		t.Fatalf("CONTROL FAILED: %s --calls--> %s has tier %q, want %q", from, to, e.Tier(), tier)
	case !want && ok:
		t.Fatalf("PHANTOM EDGE: %s --calls--> %s (tier %q) was minted for a call that does not "+
			"appear in the source; the identifier is a BINDING, not a call site. graph has %s",
			from, to, e.Tier(), g.edgeList())
	}
}
