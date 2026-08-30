package ingest

import (
	"errors"
	"os"

	"github.com/samibel/graphi/internal/rootfile"
)

// rootReadsHook is the SW-260 AC-10 test-only/injection point the
// no-extra-reads test uses to count repository reads. It is nil in
// production: the read hotpath carries no shared atomic write and no
// concurrent contention, so the default (non-semantic) ingest path stays
// inert by construction. The hook is invoked from THIS goroutine after the
// read attempt, BEFORE the result is returned to the caller, so a test
// counting "before / after" sees only the reads its index triggered.
//
// The hook is an unexported package-internal seam: the test that drives it
// lives in this package (engine/ingest), so production callers cannot
// install one — only same-package tests can. This avoids widening the
// package's exported API solely for test instrumentation and keeps a single
// mutable global instead of two.
var rootReadsHook func()

// installRootReadsHook installs (or clears, with nil) the read-counter hook
// used by the SW-260 AC-10 no-extra-reads test. It is unexported because the
// test lives in this package; production callers MUST NOT install a hook —
// the package-internal seam is the only path to it, so production cannot
// accidentally carry a shared atomic on the read hotpath. Install with a
// closure that increments an *atomic.Int64 in the test; install with nil to
// restore the production state.
func installRootReadsHook(hook func()) { rootReadsHook = hook }

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
//
// SW-260 review round 2: the hook is collapsed to ONE unexported package-level
// function variable. The previous revision exported three symbols
// (CountRootReads, SetRootReadsCounter, SetRootReadsHook) solely for
// same-package test instrumentation; two of them were redundant (one was
// unused, and the third duplicated the work of installing a counter through
// an *atomic.Int64). The test lives in this package, so an unexported hook
// is sufficient — production callers cannot accidentally enable the counter
// because they cannot reach the symbol that installs it.
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
