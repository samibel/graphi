package model

import (
	"encoding/binary"
	"fmt"
	"path"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// IdentitySchemaVersion versions the canonical identity byte layout used to
// derive NodeId and EdgeId. Bump ONLY when the layout changes; bumping it
// deliberately invalidates previously persisted IDs.
const IdentitySchemaVersion uint32 = 1

// idHexWidth is the fixed width of a rendered ID: a 64-bit digest as lowercase
// hex is always 16 characters.
const idHexWidth = 16

// Domain-separation tags woven into the identity pre-image so a Node and an Edge
// (or any two identity domains) built from coincidentally-equal strings never
// produce the same digest.
const (
	nodeIDDomainTag = "graphi/node-id"
	edgeIDDomainTag = "graphi/edge-id"
)

// unitSeparator (ASCII US, 0x1f) is written once after the domain tag.
const unitSeparator byte = 0x1f

// writeField appends a length-prefixed (u32 big-endian) UTF-8 field to b. The
// length prefix makes the encoding unambiguous: no field value can be mistaken
// for a field boundary, so distinct tuples can never share a pre-image.
func writeField(b []byte, s string) []byte {
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(s)))
	b = append(b, lenBuf[:]...)
	b = append(b, s...)
	return b
}

// identityPreimage builds the versioned canonical identity byte pre-image for a
// domain tag and an ordered tuple of fields. See the package doc for the exact
// byte layout. The result is deterministic and architecture-independent.
func identityPreimage(domainTag string, fields ...string) []byte {
	// Rough capacity hint: tag + sep + version + per-field(4 + len).
	size := len(domainTag) + 1 + 4
	for _, f := range fields {
		size += 4 + len(f)
	}
	b := make([]byte, 0, size)
	b = append(b, domainTag...)
	b = append(b, unitSeparator)
	var verBuf [4]byte
	binary.BigEndian.PutUint32(verBuf[:], IdentitySchemaVersion)
	b = append(b, verBuf[:]...)
	for _, f := range fields {
		b = writeField(b, f)
	}
	return b
}

// FormatID renders a 64-bit digest as a fixed-width 16-char lowercase hex
// string with fixed (leading-zero-padded) width. NodeId and EdgeId share this
// single helper so all IDs are stable across architectures and uniform in shape.
func FormatID(digest uint64) string {
	return fmt.Sprintf("%016x", digest)
}

// deriveID hashes a canonical identity pre-image with xxhash64 and renders the
// fixed-width hex ID.
func deriveID(domainTag string, fields ...string) string {
	digest := xxhash.Sum64(identityPreimage(domainTag, fields...))
	return FormatID(digest)
}

// NormalizePath converts an identity source path to repo-relative POSIX form
// using PURE STRING HANDLING (no filesystem I/O). It:
//
//   - converts backslashes to forward slashes (Windows → POSIX),
//   - strips a Windows drive prefix (e.g. "C:") and any leading slashes so the
//     result is repo-relative (no host-absolute prefix leaks into IDs),
//   - lexically cleans the path, collapsing "." and resolving/removing ".."
//     traversal without ever touching the filesystem,
//   - guarantees the result never escapes the repo root: any leading ".."
//     segments left after cleaning are dropped.
//
// The same logical entity therefore yields the same ID across machines and
// working directories.
func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	// Windows → POSIX separators.
	p = strings.ReplaceAll(p, "\\", "/")
	// Strip a drive letter prefix like "C:" or "c:".
	if len(p) >= 2 && p[1] == ':' &&
		((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		p = p[2:]
	}
	// Make repo-relative: drop leading slashes.
	p = strings.TrimLeft(p, "/")
	// Lexical clean (no I/O): collapses ".", resolves "..".
	cleaned := path.Clean("/" + p) // anchor at "/" so ".." can't escape
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}
