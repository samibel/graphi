package exthost

import "errors"

// The spike's typed sentinels.
//
// Two rules govern this list. First, a failure that has a SW-222 registry
// meaning uses the SW-222 sentinel rather than a parallel one: an undeclared
// port is registry.ErrMissingDependency, because "you asked for something you
// did not declare" is that vocabulary's exact sense (see host.go). Second, every
// sentinel below names a condition the host detects about a FOREIGN process, for
// which core/registry has no word.
//
// Each is wrapped with an actionable detail at the raise site — the extension
// id, the two versions that disagreed, the two hashes that disagreed, the limit
// and the observed value. A tier-C failure is diagnosed by a user who did not
// write the extension, so "mismatch" alone is not a message.
var (
	// ErrNotActivated: Start was called without explicit activation. There is no
	// default-on path and no discovery, so this is the state the spike is in
	// unless a caller deliberately left it (ADR 0013 I4, N2).
	ErrNotActivated = errors.New("exthost: extension not activated")

	// ErrDescriptor: the descriptor is missing, unreadable, oversized, not a
	// regular file, or does not validate.
	ErrDescriptor = errors.New("exthost: invalid extension descriptor")

	// ErrArtifactMismatch: the executable's bytes do not hash to the SHA-256 the
	// descriptor pins. Raised BEFORE the process is spawned.
	ErrArtifactMismatch = errors.New("exthost: extension artifact sha256 mismatch")

	// ErrUnsafeBinary: the host refuses to execute this path as an extension —
	// an empty path, a path that is not a bare filename beside the descriptor,
	// a Go test binary (*.test), or os.Args[0] itself. See host.go for why the
	// last two are fatal rather than merely odd.
	ErrUnsafeBinary = errors.New("exthost: refusing to execute this binary as an extension")

	// ErrProtocolMismatch: the extension does not speak this frame protocol.
	ErrProtocolMismatch = errors.New("exthost: protocol version mismatch")

	// ErrAPIVersionUnsupported: the host's API version falls outside the closed
	// range the extension declared it was written for.
	ErrAPIVersionUnsupported = errors.New("exthost: host api version outside the extension's declared range")

	// ErrIdentityMismatch: the handshake's id/version disagree with the
	// descriptor's. The bytes were verified, so this means the descriptor
	// describes a different extension than the one that was pinned.
	ErrIdentityMismatch = errors.New("exthost: extension identity mismatch")

	// ErrProtocolViolation: a well-formed frame arrived in a place the protocol
	// does not allow it, or a required field was absent.
	ErrProtocolViolation = errors.New("exthost: extension violated the protocol")

	// ErrResponseTooLarge: a frame declared, or produced, more bytes than the
	// descriptor's max_response_bytes. The declared length is checked before the
	// body is read, so an extension cannot make the host allocate what it
	// refuses to accept.
	ErrResponseTooLarge = errors.New("exthost: extension response exceeds the maximum response size")

	// ErrTimeout: the extension exceeded its wall-clock limit, or the caller
	// cancelled. The process is killed and the host stays healthy.
	ErrTimeout = errors.New("exthost: extension exceeded its time limit")

	// ErrCrashed: the extension process ended before answering.
	ErrCrashed = errors.New("exthost: extension process exited before answering")

	// ErrExtension: the extension reported a failure of its own. Distinct from
	// every sentinel above: the extension is working, and says no.
	ErrExtension = errors.New("exthost: extension reported an error")

	// ErrConfidenceLaundering: the extension claimed a confidence tier ADR 0013
	// D5 closes to extensions. The result is REJECTED rather than downgraded —
	// silently repairing it would teach an author that the ceiling is advisory.
	ErrConfidenceLaundering = errors.New("exthost: extension may not mint this confidence tier")

	// ErrClosed: the extension has been shut down.
	ErrClosed = errors.New("exthost: extension is closed")
)
