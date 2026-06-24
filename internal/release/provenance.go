package release

// GrammarProvenance is the supply-chain provenance + license record for an
// embedded curated grammar source (SW-055 AC#5). Every grammar reachable from the
// default tier must be version-pinned, provenance-recorded, and license-verified
// against its ACTUAL license. The accompanying tests assert the pin matches go.mod,
// that `go mod verify` passes, and that the license read from the RESOLVED/pinned
// module is SPDX-permissive — so a future license-changing version bump fails CI.
type GrammarProvenance struct {
	// ModulePath is the Go module import path of the grammar source.
	ModulePath string
	// Version is the pinned semantic version (must match go.mod exactly).
	Version string
	// SourceURL is the upstream source location for human provenance.
	SourceURL string
	// LicenseSPDX is the ACTUAL SPDX license identifier of the pinned module,
	// verified against the LICENSE file in the resolved module cache. For the
	// default tier this is MIT (gotreesitter) — NOT the formerly-assumed Apache-2.0.
	LicenseSPDX string
	// LicenseFile is the basename of the license file shipped in the module.
	LicenseFile string
}

// DefaultTierGrammarProvenance is the single source of truth for the default
// tier's embedded grammar runtime provenance. The default tier's tree-sitter
// backend is the pure-Go gotreesitter runtime; its parse-table blobs are
// Go-embedded via the subset tags in DefaultGrammarSubsetTags (no grammar is
// fetched at build time). go-sitter-forest (the CGO bundle) is verified separately
// on the graphi-broad lane (SW-056) and is intentionally absent here.
var DefaultTierGrammarProvenance = GrammarProvenance{
	ModulePath:  "github.com/odvcencio/gotreesitter",
	Version:     "v0.20.2",
	SourceURL:   "https://github.com/odvcencio/gotreesitter",
	LicenseSPDX: "MIT",
	LicenseFile: "LICENSE",
}

// PermissiveSPDX is the set of SPDX license identifiers accepted as
// permissive/compatible for an embedded default-tier grammar. The license read
// from the pinned module's LICENSE file must be in this set or the supply-chain
// test fails (e.g. a future GPL relicense is rejected).
var PermissiveSPDX = map[string]struct{}{
	"MIT":          {},
	"Apache-2.0":   {},
	"BSD-2-Clause": {},
	"BSD-3-Clause": {},
	"ISC":          {},
}

// IsPermissive reports whether spdx is in the accepted permissive set.
func (p GrammarProvenance) IsPermissive() bool {
	_, ok := PermissiveSPDX[p.LicenseSPDX]
	return ok
}
