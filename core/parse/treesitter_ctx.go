package parse

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	gts "github.com/odvcencio/gotreesitter"
)

// parseTreeSitter runs one bounded tree-sitter parse. It is the single seam
// through which every gotreesitter-backed parser executes, and it is what
// makes ResourceBounds.ParseTimeout actually bite for this runtime: the ctx
// deadline is propagated into the parser (whose GLR loop checks it between
// iterations) and ctx cancellation flips the runtime's cancellation flag, so
// a pathological input — e.g. a shell script that drives the bash grammar's
// stack-equivalence checks quadratic — stops near the bound instead of
// running minutes past it. Deadline expiry surfaces as ErrParseTimeout so the
// callers' fail-closed skip semantics engage; a plain cancellation surfaces
// as the ctx error.
func parseTreeSitter(ctx context.Context, lang *gts.Language, src []byte) (*gts.Tree, error) {
	parser := gts.NewParser(lang)
	if dl, ok := ctx.Deadline(); ok {
		remain := time.Until(dl)
		if remain <= 0 {
			return nil, ErrParseTimeout
		}
		parser.SetTimeoutMicros(uint64(remain / time.Microsecond))
	}
	if done := ctx.Done(); done != nil {
		var cancelled uint32
		parser.SetCancellationFlag(&cancelled)
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-done:
				atomic.StoreUint32(&cancelled, 1)
			case <-stop:
			}
		}()
	}
	tree, err := parser.Parse(src)
	// The runtime returns a truncated tree (not an error) when it stops on the
	// timeout/cancellation, so the ctx state is authoritative for the outcome.
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return nil, ErrParseTimeout
		}
		return nil, ctxErr
	}
	return tree, err
}
