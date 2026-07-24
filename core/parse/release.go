package parse

// closableRoot is the optional release seam a backend AST handle may
// implement. The default tier never does — go/ast and the pure-Go
// gotreesitter trees are ordinary GC-managed values — but the opt-in
// graphi-broad CGO backend's tree handle owns C heap that only an explicit
// ts_tree_delete returns (the bare runtime registers no finalizer on trees),
// so dropping the Go reference alone would leak the C tree permanently.
type closableRoot interface{ Close() }

// ReleaseRoot drops res.Root, closing the backend handle first when the
// backend supports explicit release. It is idempotent and nil-safe, and is
// the ONLY way ingest code should discard a Root: a plain `res.Root = nil`
// frees pure-Go backends but silently leaks the graphi-broad C tree.
func ReleaseRoot(res *ParseResult) {
	if res == nil || res.Root == nil {
		return
	}
	if c, ok := res.Root.(closableRoot); ok {
		c.Close()
	}
	res.Root = nil
}
