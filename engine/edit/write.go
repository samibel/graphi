package edit

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeFileAtomic writes data to path via a temp file in the SAME directory
// followed by an atomic rename, fsyncing the temp file before the rename so a
// crash can never leave partially-written source under the final name. It
// mirrors core/graphstore/snapshot.go:writeFileAtomic — the source-mutation
// security invariant (atomic temp + fsync + rename) is identical, so we reuse the
// exact same shape rather than hand-rolling a weaker write. Same-directory temp
// keeps the rename on one filesystem so it is truly atomic.
//
// The file's existing permissions are preserved when it already exists; new
// files are created 0o600. This matters because an edit must not silently widen
// or narrow the mode of a user's source file.
func writeFileAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)

	perm := os.FileMode(0o600)
	if fi, statErr := os.Stat(path); statErr == nil {
		perm = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".graphi-edit-*.tmp")
	if err != nil {
		return fmt.Errorf("edit: create temp source file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, werr := tmp.Write(data); werr != nil {
		_ = tmp.Close()
		return fmt.Errorf("edit: write temp source file: %w", werr)
	}
	if serr := tmp.Sync(); serr != nil {
		_ = tmp.Close()
		return fmt.Errorf("edit: sync temp source file: %w", serr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return fmt.Errorf("edit: close temp source file: %w", cerr)
	}
	if cerr := os.Chmod(tmpName, perm); cerr != nil {
		return fmt.Errorf("edit: chmod temp source file: %w", cerr)
	}
	if rerr := os.Rename(tmpName, path); rerr != nil {
		return fmt.Errorf("edit: rename source file into place: %w", rerr)
	}
	return nil
}
