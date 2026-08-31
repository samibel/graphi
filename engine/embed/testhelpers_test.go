package embed_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"os"

	_ "modernc.org/sqlite"
)

// test-helper functions used by the conformance suite. Kept in a separate
// file so the conformance test body focuses on contract assertions.

// openSQLiteForTest opens a pure-Go SQLite database at path with the
// standard pragmas (WAL, busy_timeout) the production code uses.
func openSQLiteForTest(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
}

// readBytes reads a file's bytes. Used by the AC-8 migration test to
// sha256 the real graph.db file after the migration.
func readBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// sha256Hex returns the lowercase hex sha256 of b.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
