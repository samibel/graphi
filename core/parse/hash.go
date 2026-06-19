package parse

import (
	"encoding/hex"
	"hash/fnv"
)

// contentHash returns a deterministic hex digest of src.
//
// graphi's provenance canon specifies xxhash64 for node/edge IDs. To keep
// core/parse a pure stdlib leaf for SW-001 (no external module, CGO_ENABLED=0,
// zero outbound fetch), we use the standard library's FNV-1a 64-bit hash here.
// It satisfies the determinism contract this story tests (same input -> same
// digest). Swapping in the canonical xxhash64 implementation later is a localized
// change behind this single helper and does not alter the Parser contract.
func contentHash(src []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(src)
	return hex.EncodeToString(h.Sum(nil))
}
