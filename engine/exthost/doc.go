// Package exthost is the SW-231 (AX-11) process-extension SPIKE.
//
// # This package is disposable, and nothing in graphi depends on it
//
// It exists to answer one question with measurements instead of opinion: is an
// ADR 0013 **trust tier C** extension — a read-only analyzer running as a
// separate local process behind a versioned stdio protocol — worth building?
// The deliverable of SW-231 is the go/no-go record in
// docs/decisions/2026-08-process-extension-go-no-go.md, in either direction.
// "No, graphi stays on rule packs (tier A) and static modules (tier B)" is a
// planned, valid outcome (ADR 0013 D2).
//
// Because of that, this package is wired to NOTHING. No cmd, no surface and no
// engine package imports it; the shipped `graphi` binary's import closure does
// not contain it, which is pinned by TestSpike_NotInTheShippedImportClosure in
// isolation_test.go. Removing the spike is `rm -r engine/exthost
// extensions/example-analyzer` plus the decision document — there is no
// default-path dependency to unwind (AC-6).
//
// # A subprocess is trusted local code, NOT a sandbox
//
// This is ADR 0013 D3, and it is repeated here rather than cited because it is
// the sentence a reader of this package most needs. A normal subprocess runs
// with the user's OS rights. The descriptor's port list makes the extension's
// access to GRAPHI's data transparent and bounded AT THE HOST API; it does not
// and cannot prevent the extension process from reading any file the user can
// read, writing any file the user can write, or opening any network connection
// the user can open. surfaces/guard constrains graphi's own dialers and
// listeners; it has no authority over a different process's syscalls, and the
// egress canary measures the graphi process, not this one.
//
// Therefore: a tier-C extension is trusted local code — the same category as a
// shell script the user chose to run. Not "sandboxed", not "isolated", not
// "restricted". Activating one takes the user outside the scope of graphi's
// zero-egress promise, which is why activation here is explicit, per-extension,
// default-off and Labs-only (ADR 0013 I4, T7).
//
// What the host DOES enforce, and what it is worth:
//
//   - the artifact's SHA-256 is verified against the descriptor BEFORE the
//     process is spawned — so an altered binary is never executed, not merely
//     never trusted;
//   - the protocol version and the host API version are negotiated in a
//     handshake that must complete before any operation request is written;
//   - a hard wall-clock limit, a hard maximum response size checked against the
//     DECLARED frame length before the body is read, and cancellation;
//   - every port the extension reaches for is recorded and, if undeclared,
//     refused with the SW-222 registry.ErrMissingDependency sentinel — the
//     extension never receives a database path, so ADR 0013 N4 ("no extension
//     access to SQLite files") holds by absence, not by policy;
//   - `confirmed` is closed to extensions (ADR 0013 D5): a result claiming it is
//     REJECTED, not downgraded, so the caller never sees laundered confidence;
//   - every result carries the extension's id, version and artifact hash.
//
// None of that is a security boundary against a hostile author. All of it is
// containment against a BUGGY one, plus transparency about what was run.
//
// # Activation is explicit, and discovery does not exist
//
// Start refuses unless Config.Activated is true AND Config.DescriptorPath names
// a regular file the caller chose. There is deliberately no Discover, Scan or
// LoadAll: the package contains no directory listing, no glob and no filesystem
// walk, which TestSpike_NoDirectoryDiscovery pins by scanning this package's own
// source. That is ADR 0013 N2 made structural rather than promised.
//
// # Protocol
//
// See protocol.go. Frames are `graphi-ext/1 <byte-length>\n<json>` over the
// child's stdin/stdout. gRPC was evaluated as the comparison the plan asks for
// and is costed in the decision document; it is not implemented, because
// adopting google.golang.org/grpc + protobuf into the module to measure it would
// itself breach the binary-size budget this spike has to respect.
package exthost
