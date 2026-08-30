package ingest

import (
	"errors"
	"os"
	"sync/atomic"

	"github.com/samibel/graphi/internal/rootfile"
)

// rootReads counts every repository file read the ingest pipeline performs
// (walk hashing, parse, module map, typeresolve re-reads). It is the
// instrument behind the SW-260 AC-10 no-extra-reads test: the parser's span
// sidecar is computed from the AST already in memory, so the default index
// must read exactly as many files as it did before spans existed. Read only by
// package tests; one atomic add per read is noise next to the read itself.
var rootReads atomic.Int64

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
	rootReads.Add(1)
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
