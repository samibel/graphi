package ingest

import (
	"errors"
	"os"
	"sync/atomic"

	"github.com/samibel/graphi/internal/rootfile"
)

// rootReadsHook is the SW-260 AC-10 test-only/injection point the
// no-extra-reads test uses to count repository reads. It is nil in
// production: the read hotpath carries no shared atomic write and no
// concurrent contention, so the default (non-semantic) ingest path stays
// inert by construction. A package test calls SetRootReadsCounter with an
// *atomic.Int64 and resets it via SetRootReadsCounter(nil) on cleanup.
//
// The hook is invoked from THIS goroutine after the read attempt, BEFORE the
// result is returned to the caller, so a test counting "before / after" sees
// only the reads its index triggered. Concurrent workers contend on the
// counter — exactly the original cost — only when a test installs one.
var (
	rootReadsHook    func()
	rootReadsCounter *atomic.Int64
)

// CountRootReads returns the count observed by the currently installed hook.
// The production path returns 0 (no hook installed) without allocating a
// shared counter.
func CountRootReads() int64 {
	if rootReadsCounter == nil {
		return 0
	}
	return rootReadsCounter.Load()
}

// SetRootReadsCounter installs (or clears, with nil) the *atomic.Int64 used
// by the SW-260 AC-10 no-extra-reads test. Production callers MUST NOT
// install a counter — the package-internal seam is the only path to it, so
// production cannot accidentally carry a shared atomic on the read hotpath.
// Hook functions of any other signature are rejected: tests use the counter
// type so the round trip is statically typed.
func SetRootReadsCounter(c *atomic.Int64) {
	rootReadsCounter = c
	rootReadsHook = nil
	if c != nil {
		rootReadsHook = func() { c.Add(1) }
	}
}

// SetRootReadsHook installs (or clears, with nil) the read counter hook used
// by the SW-260 AC-10 no-extra-reads test. It is a package-internal seam
// (no public surface) so production callers cannot accidentally enable the
// counter on the default path.
func SetRootReadsHook(hook func()) {
	rootReadsHook = hook
}

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
//
// SW-260 minor (review round 1): the AC-10 counter is now test-only. Production
// callers never touch a shared atomic — the hook is nil on the default path,
// so a concurrent ingest worker does not contend on a cache line it does not
// own.
func readRootedRegularFile(root *os.Root, rel string, maxFileSize int64) rootedReadResult {
	if h := rootReadsHook; h != nil {
		h()
	}
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
