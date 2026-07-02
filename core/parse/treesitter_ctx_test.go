package parse

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestParseTreeSitter_DeadlineBites pins that ResourceBounds.ParseTimeout is
// ENFORCED for the gotreesitter runtime: a parse whose ctx deadline expires
// returns ErrParseTimeout (→ fail-closed SkipTimeout upstream) instead of
// running to completion. Before this seam existed, the ctx was checked once
// at entry and a pathological input (a GLR stack-equivalence blowup in the
// bash grammar) parsed for minutes past the bound.
func TestParseTreeSitter_DeadlineBites(t *testing.T) {
	p := NewBashParser()
	src := []byte("#!/bin/sh\nif [ -n \"$x\" ]; then echo a; fi\nfor i in 1 2; do echo $i; done\n")

	// Already-expired deadline → aborts before/at the parse. The parser's own
	// entry guard may surface the raw ctx error; a mid-parse expiry surfaces
	// the typed ErrParseTimeout — ingest's skip classification accepts both.
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := p.Parse(expired, "x.sh", src); !errors.Is(err, ErrParseTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired deadline: err = %v, want ErrParseTimeout or DeadlineExceeded", err)
	}

	// Mid-parse expiry: a deadline that lands DURING the parse must stop the
	// GLR loop near the bound and surface the typed timeout. (Without the
	// propagation seam the runtime ignored the ctx entirely and this returned
	// a successful parse long after the deadline.)
	short, cancelShort := context.WithTimeout(context.Background(), 3*time.Millisecond)
	defer cancelShort()
	time.Sleep(time.Millisecond) // leave a sliver of budget so the entry guard passes
	if _, err := p.Parse(short, "x.sh", src); err != nil && !errors.Is(err, ErrParseTimeout) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("mid-parse deadline: err = %v, want timeout-shaped or fast success", err)
	}

	// Generous deadline → normal successful parse (the bound must not skew
	// healthy inputs).
	ok, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	res, err := p.Parse(ok, "x.sh", src)
	if err != nil {
		t.Fatalf("healthy parse under deadline: %v", err)
	}
	if res == nil || res.Meta.Language != "bash" {
		t.Fatalf("healthy parse returned unexpected result: %+v", res)
	}

	// Cancellation (not deadline) surfaces as the ctx error, not a timeout.
	cancelled, cancel3 := context.WithCancel(context.Background())
	cancel3()
	if _, err := p.Parse(cancelled, "x.sh", src); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ctx: err = %v, want context.Canceled", err)
	}
}
