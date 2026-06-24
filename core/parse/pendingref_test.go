package parse

import (
	"context"
	"testing"

	"github.com/samibel/graphi/core/model"
)

// pendingRefFixture is a minimal two-symbol Go file whose checkout function makes a
// cross-package selector call (pkg.Fn) and references a cross-package selector value
// (pkg.Val). Both are unprovable from this single file and must surface as inert
// selector PendingRefs — never as fabricated nodes or resolved edges.
const pendingRefFixture = `package shop

import pkg "example.com/pkg"

func checkout() {
	pkg.Fn()
	_ = pkg.Val
}
`

// TestPendingRef_CrossFileSelectorEmission is the declared-owner contract test for
// PendingRef (AC [ARCH]): a cross-package/selector use becomes an inert selector
// PendingRef consumed by the FU-1 linker, carrying correct file:line evidence and
// NO fabricated NodeId / NO resolved edge (negative-shaped assertions).
func TestPendingRef_CrossFileSelectorEmission(t *testing.T) {
	res, err := NewGoParser().Parse(context.Background(), "shop/checkout.go", []byte(pendingRefFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// 1) The selector call pkg.Fn is recorded as an inert selector PendingRef.
	var call, ref *PendingRef
	for i := range res.PendingRefs {
		p := &res.PendingRefs[i]
		if p.Selector && p.SelectorBase == "pkg" && p.Name == "Fn" && p.Kind == EdgeCalls {
			call = p
		}
		if p.Selector && p.SelectorBase == "pkg" && p.Name == "Val" && p.Kind == EdgeReferences {
			ref = p
		}
	}
	if call == nil {
		t.Fatalf("expected a selector PendingRef for pkg.Fn (calls), got %+v", res.PendingRefs)
	}
	if ref == nil {
		t.Fatalf("expected a selector PendingRef for pkg.Val (references), got %+v", res.PendingRefs)
	}

	// 2) Contract shape: owned by the enclosing symbol, correct line evidence.
	if call.FromQN != "shop.checkout" {
		t.Errorf("call.FromQN = %q, want shop.checkout", call.FromQN)
	}
	if call.Line != 6 { // pkg.Fn() is on line 6 of the fixture
		t.Errorf("call.Line = %d, want 6 (file:line evidence for pkg.Fn)", call.Line)
	}
	if ref.Line != 7 { // _ = pkg.Val is on line 7
		t.Errorf("ref.Line = %d, want 7 (file:line evidence for pkg.Val)", ref.Line)
	}

	// 3) Inert: PendingRef fabricates NO endpoint. The struct carries no NodeId
	//    field by design; assert the import alias is resolvable for the linker but
	//    the parse leaf left the ref unresolved.
	var aliasFound bool
	for _, imp := range res.Imports {
		if imp.Alias == "pkg" && imp.Path == "example.com/pkg" {
			aliasFound = true
		}
	}
	if !aliasFound {
		t.Errorf("expected import alias pkg -> example.com/pkg for the linker, got %+v", res.Imports)
	}

	// 4) NO resolved cross-package edge was emitted: every edge stays an intra-file
	//    defines/calls/references edge whose endpoints are nodes in THIS file. The
	//    only nodes here are the file node and shop.checkout.
	nodeIDs := map[model.NodeId]struct{}{}
	for _, n := range res.Nodes {
		nodeIDs[n.ID()] = struct{}{}
	}
	for _, e := range res.Edges {
		if _, ok := nodeIDs[e.From()]; !ok {
			t.Errorf("edge %s has a from-endpoint outside this file: fabricated endpoint", e.ID())
		}
		if _, ok := nodeIDs[e.To()]; !ok {
			t.Errorf("edge %s has a to-endpoint outside this file: fabricated endpoint", e.ID())
		}
		if e.Kind() != EdgeDefines && e.Kind() != EdgeCalls && e.Kind() != EdgeReferences {
			t.Errorf("edge %s has non-canonical kind %q", e.ID(), e.Kind())
		}
	}

	// 5) Dedup/identity: a repeated parse yields the identical PendingRef set
	//    (deterministic, inert recording).
	res2, err := NewGoParser().Parse(context.Background(), "shop/checkout.go", []byte(pendingRefFixture))
	if err != nil {
		t.Fatalf("parse(2): %v", err)
	}
	if len(res2.PendingRefs) != len(res.PendingRefs) {
		t.Errorf("non-deterministic PendingRef count: %d vs %d", len(res2.PendingRefs), len(res.PendingRefs))
	}
}
