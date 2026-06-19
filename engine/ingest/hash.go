package ingest

import (
	"encoding/hex"
	"hash/fnv"
)

// hashBytes returns a deterministic hex digest of src using FNV-1a 64-bit.
func hashBytes(src []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(src)
	return hex.EncodeToString(h.Sum(nil))
}
