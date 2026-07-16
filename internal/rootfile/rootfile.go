// Package rootfile reads regular files without allowing path resolution to
// escape a caller-owned directory tree.
package rootfile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	// ErrNotRegular means the final path component was a symlink or the opened
	// descriptor was not a regular file.
	ErrNotRegular = errors.New("rootfile: path is not a regular file")
	// ErrChanged means the path no longer names the descriptor validated by the
	// first root-confined Lstat.
	ErrChanged = errors.New("rootfile: path changed while opening")
)

// TooLargeError reports a hard read-bound violation without carrying file
// content. Size is exact when descriptor metadata exposed it and otherwise is
// the minimum number of bytes observed by the bounded read.
type TooLargeError struct {
	Limit int64
	Size  int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("rootfile: file exceeds %d-byte limit (observed %d bytes)", e.Limit, e.Size)
}

// Read opens rootPath as an os.Root and reads name through it. A missing final
// file retains an os.IsNotExist-compatible error; all other failures are
// fail-closed. limit <= 0 disables the byte limit.
func Read(rootPath, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("rootfile: open root: %w", err)
	}
	defer root.Close()
	return ReadFrom(root, name, limit)
}

// ReadFrom opens name through root, validates the opened descriptor against
// root-confined Lstat results, and reads from that descriptor. os.Root prevents
// intermediate symlinks from escaping root. Final symlinks are rejected both
// before and after Open, including a replacement that resolves to the same
// inode as the initial regular file.
func ReadFrom(root *os.Root, name string, limit int64) ([]byte, error) {
	return readFromWithHooks(root, name, limit, nil)
}

// readHooks make the security-sensitive replacement/growth windows
// deterministic in package tests. Production callers always pass nil.
type readHooks struct {
	afterLstat    func()
	afterOpenStat func()
}

func readFromWithHooks(root *os.Root, name string, limit int64, hooks *readHooks) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		// Preserve the direct PathError only for the initial lookup, where
		// not-exist genuinely means an optional file is absent. A disappearance
		// during any later validation step remains a fail-closed changed-path
		// error and is intentionally wrapped below.
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("rootfile: lstat %q: %w", name, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("rootfile: lstat %q: %w", name, ErrNotRegular)
	}
	if hooks != nil && hooks.afterLstat != nil {
		hooks.afterLstat()
	}

	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("rootfile: open %q: %w", name, err)
	}
	defer f.Close()

	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("rootfile: stat opened %q: %w", name, err)
	}
	if !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("rootfile: stat opened %q: %w", name, ErrNotRegular)
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("rootfile: compare opened %q: %w", name, ErrChanged)
	}

	// Re-check the final path after Open. If a regular file was replaced by a
	// symlink that resolves to the same inode, SameFile(before, opened) alone
	// would not expose the replacement. A change after this check is harmless:
	// bytes come from the already-open validated descriptor, not a new path
	// resolution.
	current, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("rootfile: re-lstat %q: %w", name, err)
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
		return nil, fmt.Errorf("rootfile: re-lstat %q: %w", name, ErrNotRegular)
	}
	if !os.SameFile(current, opened) {
		return nil, fmt.Errorf("rootfile: compare current %q: %w", name, ErrChanged)
	}

	if limit > 0 && opened.Size() > limit {
		return nil, &TooLargeError{Limit: limit, Size: opened.Size()}
	}
	if hooks != nil && hooks.afterOpenStat != nil {
		hooks.afterOpenStat()
	}

	var reader io.Reader = f
	// MaxInt64 needs no +1 sentinel: FileInfo.Size cannot represent a larger
	// regular file. Every smaller positive limit retains at most one sentinel
	// byte beyond the allowed payload.
	const maxInt64 = int64(^uint64(0) >> 1)
	if limit > 0 && limit < maxInt64 {
		reader = io.LimitReader(f, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("rootfile: read %q: %w", name, err)
	}
	if limit > 0 && int64(len(data)) > limit {
		observed := int64(len(data))
		if end, statErr := f.Stat(); statErr == nil && end.Size() > observed {
			observed = end.Size()
		}
		return nil, &TooLargeError{Limit: limit, Size: observed}
	}
	return data, nil
}
