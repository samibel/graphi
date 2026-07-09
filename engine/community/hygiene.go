package community

// artifactKinds are node kinds that are LINKER/INGEST ARTIFACTS, not navigable
// source symbols: interned `external` targets (stdlib / 3rd-party, empty source
// path), interned `package` namespace nodes, and `file` nodes. They must never
// appear as community members — a community is the structural grouping of
// SYMBOLS, and these nodes are not symbols a reader navigates to. Excluding them
// mirrors the kind-based hygiene the structural query and search services already
// apply (query.kindExternal/kindPackage, search.kindExternal), and keeps the
// generated wiki pages — which render straight from community membership — free
// of `[external]`/`[package]`/`[file]` noise (WP-14 follow-up E).
var artifactKinds = map[string]struct{}{
	"external": {},
	"package":  {},
	"file":     {},
}

// isArtifactKind reports whether a node kind is a non-symbol graph artifact that
// must be excluded from community membership.
func isArtifactKind(kind string) bool {
	_, ok := artifactKinds[kind]
	return ok
}
