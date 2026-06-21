package meter

import (
	"fmt"
	"os"
	"strings"
)

// LocalFileReader is the production FileReader. It reads whole-file content from
// local disk ONLY and rejects remote sources so the local-first contract is
// enforced at the only I/O boundary in the baseline computation. It performs no
// network I/O of any kind.
type LocalFileReader struct{}

// NewLocalFileReader returns a disk-backed FileReader.
func NewLocalFileReader() LocalFileReader { return LocalFileReader{} }

// ReadFile reads the entire file at path from local disk. Remote sources are
// rejected with an explicit error (local-first contract).
func (LocalFileReader) ReadFile(path string) ([]byte, error) {
	if isRemoteSource(path) {
		return nil, fmt.Errorf("meter: remote source rejected (%q) — FileReader is local-disk only", path)
	}
	return os.ReadFile(path) //nolint:gosec // local-first engine reading graph-tracked artifact paths
}

// isRemoteSource reports whether path looks like a remote URL. The baseline may
// only be computed from local files; this guard keeps the local-first contract
// honest.
func isRemoteSource(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}
