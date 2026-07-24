//go:build graphi_broad

package parse

import (
	"context"
	"testing"
)

// Compile-time: the graphi-broad root handle implements the release seam, so
// ReleaseRoot reaches ts_tree_delete for it. Without this, dropping the Go
// reference leaks the C tree permanently (the bare runtime registers no
// finalizer on trees).
var _ closableRoot = (*forestAST)(nil)

// TestReleaseRoot_ClosesBroadTree parses a Zig fixture through the CGO forest
// backend and proves the release path: the parse retains the owning Tree on
// the root handle, ReleaseRoot closes it and drops the reference, and a
// second release (or a direct double Close) is an idempotent no-op — the
// bare Tree.Close is once-guarded, so this cannot double-free.
func TestReleaseRoot_ClosesBroadTree(t *testing.T) {
	const src = `fn add(a: i32, b: i32) i32 {
    return a + b;
}
`
	p := NewZigParser()
	res, err := p.Parse(context.Background(), "demo/main.zig", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ast, ok := res.Root.(*forestAST)
	if !ok || ast == nil {
		t.Fatalf("Root = %T, want *forestAST", res.Root)
	}
	if ast.tree == nil {
		t.Fatal("the parse must retain the owning Tree on the root handle — without it Close can never free the C tree")
	}
	if len(res.Nodes) == 0 {
		t.Fatal("fixture must extract at least one node before release")
	}

	ReleaseRoot(res)
	if res.Root != nil {
		t.Fatal("ReleaseRoot must drop the broad Root")
	}
	ReleaseRoot(res) // idempotent on the result
	ast.Close()      // and idempotent on the handle itself (Tree.Close is once-guarded)
}
