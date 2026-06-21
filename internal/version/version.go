// Package version carries the release version string stamped into the graphi
// binary at packaging time via -ldflags -X. Commit SHA and commit (build) date
// come from Go's VCS stamping (debug.ReadBuildInfo), which is deterministic for a
// given revision, so the binary remains reproducibly buildable. cmd/graphi blank-
// imports this package so it is linked and ldflags can set Version.
package version

// Version is the release version, stamped at build time via
// `-X github.com/samibel/graphi/internal/version.Version=...`. "dev" when unset.
var Version = "dev"
