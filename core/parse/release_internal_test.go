package parse

import "testing"

type countingCloser struct{ closed int }

func (c *countingCloser) Close() { c.closed++ }

func TestReleaseRoot_NilSafeClosesAndDrops(t *testing.T) {
	ReleaseRoot(nil) // nil result: no panic

	// A plain (non-closable) root is simply dropped — the default-tier case.
	res := &ParseResult{Root: struct{}{}}
	ReleaseRoot(res)
	if res.Root != nil {
		t.Fatal("ReleaseRoot must drop a non-closable Root")
	}
	ReleaseRoot(res) // second release: no-op

	// A closable root (the graphi-broad tree handle shape) is closed exactly
	// once — the second ReleaseRoot sees Root == nil and never re-closes.
	c := &countingCloser{}
	res = &ParseResult{Root: c}
	ReleaseRoot(res)
	ReleaseRoot(res)
	if c.closed != 1 {
		t.Fatalf("closable Root closed %d times, want exactly 1", c.closed)
	}
	if res.Root != nil {
		t.Fatal("ReleaseRoot must drop a closable Root after closing it")
	}
}
