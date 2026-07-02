// Package model defines graphi's canonical, immutable Node and Edge value types
// — the spine of the code-intelligence graph — together with deterministic
// xxhash64 identity derivation, mandatory edge provenance, and byte-stable
// serialization.
//
// # Layering (CI-enforced)
//
// core/model is a PURE LEAF. It MUST NOT import core/parse, core/graphstore,
// engine/*, or surfaces/*. It depends only on the Go standard library plus the
// CGo-free hashing module github.com/cespare/xxhash/v2. ID derivation and
// serialization perform NO network, NO filesystem, and NO exec/shell operations
// (path normalization is pure string handling). This keeps the local-first gate
// (CGO_ENABLED=0, zero outbound network) intact.
//
// # Canonical identity form (versioned byte pre-image)
//
// A NodeId is the xxhash64 digest of a versioned canonical identity pre-image.
// The pre-image is a fixed, ordered tuple rendered with an unambiguous,
// length-prefixed UTF-8 encoding so that no field value can be confused with a
// field boundary. The exact byte layout is:
//
//		pre-image := domainTag '\x1f' u32be(identitySchemaVersion)
//		             field(kind) field(qualifiedName) field(normalizedPath)
//		field(s)  := u32be(len(utf8Bytes(s))) utf8Bytes(s)
//
//	  - domainTag is the ASCII literal "graphi/node-id" for Node identity and
//	    "graphi/edge-id" for Edge identity (a domain separator so a Node and an
//	    Edge built from coincidentally-equal strings never collide).
//	  - '\x1f' is the ASCII Unit Separator, used once after the domain tag.
//	  - u32be is a 4-byte big-endian (fixed-endianness) unsigned length/version,
//	    so identical inputs hash identically across architectures.
//	  - Strings are UTF-8 with NO normalization beyond what NormalizePath performs
//	    for the source path; content is preserved verbatim otherwise.
//
// For a Node the ordered identity tuple is (Kind, QualifiedName, normalized
// SourcePath). Line/column are NON-identity attributes and are NOT part of the
// pre-image: the same logical entity keeps its ID when it merely moves within a
// file. For an Edge the ordered identity tuple is (FromId, ToId, EdgeKind).
//
// IdentitySchemaVersion is bumped only when the identity layout changes; bumping
// it deliberately invalidates previously persisted IDs.
//
// # Edge provenance (mandatory, unrepresentable when incomplete)
//
// Every Edge carries provenance: a closed ConfidenceTier enum reconciled with
// architecture §7.3 — {heuristic, derived, confirmed} — a numeric Confidence in
// [0,1], a human-readable Reason, and a non-empty list of Evidence references.
// Edges are constructed only through NewEdge, which rejects a missing/unknown
// tier, an out-of-range confidence, an empty/whitespace-only reason, or empty
// evidence. There is no exported way to obtain an Edge without complete
// provenance, so an under-provenanced Edge is unrepresentable.
//
// # Determinism
//
// IDs are fixed-width 16-char lowercase hex (see FormatID). Serialization (see
// Marshal) emits a stable field ordering with Evidence canonically sorted, so
// the serialized bytes for a graph are identical regardless of node/edge
// insertion order or run, and round-trip losslessly (including ConfidenceTier
// and numeric Confidence). Golden vectors and shuffle/multi-run tests lock this
// in so any future drift fails CI.
//
// # Security posture
//
// xxhash64 IDs are NON-CRYPTOGRAPHIC content identifiers — they are NOT
// integrity or security tokens and must not be used as such. The 64-bit digest
// space has a known birthday-bound collision posture (~50% near 2^32 distinct
// identities); this is an accepted property of the identity scheme, not a bug.
// Edge Reason and Evidence are UNTRUSTED-ORIGIN data (they may derive from
// analyzed source or heuristics): downstream consumers must treat them as
// untrusted input and must not interpolate them into shells, SQL, or markup
// without escaping.
package model
