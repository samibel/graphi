package extpack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// HashBytes returns the lowercase hex SHA-256 of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// VerifyHash checks data against an expected lowercase hex SHA-256.
//
// The error names WHAT was being verified and both hashes, because the two
// realistic causes — the user typed the wrong `--sha256`, or the file is not the
// file they think it is — are told apart by exactly that comparison. It is a
// fail-closed check with no "warn and continue" path: an unverified pack is
// never installed and never loaded.
func VerifyHash(what string, data []byte, want string) error {
	if err := ValidateHex(want); err != nil {
		return fmt.Errorf("extpack: %s: %w", what, err)
	}
	got := HashBytes(data)
	if got != want {
		return fmt.Errorf("extpack: %s sha256 mismatch: expected %s, the bytes hash to %s", what, want, got)
	}
	return nil
}
