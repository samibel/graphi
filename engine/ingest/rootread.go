package ingest

import (
	"errors"
	"os"

	"github.com/samibel/graphi/internal/rootfile"
)

// rootedReadResult is the fail-closed result of reading one repository file.
// A non-empty reason means src is deliberately discarded. size is populated
// only for SkipOversize and is the exact descriptor size when known, otherwise
// the minimum number of bytes observed by the bounded read.
type rootedReadResult struct {
	src    []byte
	reason SkipReason
	size   int64
}

// readRootedRegularFile opens rel through root, validates the opened descriptor
// against root-confined Lstat results, and reads from that descriptor. Root.Open
// prevents intermediate symlinks from escaping root; rejecting a final symlink
// both before and after Open closes the final-component replacement window.
func readRootedRegularFile(root *os.Root, rel string, maxFileSize int64) rootedReadResult {
	src, err := rootfile.ReadFrom(root, rel, maxFileSize)
	if err == nil {
		return rootedReadResult{src: src}
	}
	var tooLarge *rootfile.TooLargeError
	if errors.As(err, &tooLarge) {
		return rootedReadResult{reason: SkipOversize, size: tooLarge.Size}
	}
	return rootedReadResult{reason: SkipUnreadable}
}
