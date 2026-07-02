package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/engine/ingest"
)

// fakeClock advances a fixed instant on demand so throttle behavior is
// deterministic (no sleeps).
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

// newTestProgress builds a renderer without the ticker goroutine (tty is set
// after construction) so tests control every draw.
func newTestProgress(w *bytes.Buffer, tty bool) (*ingestProgress, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1700000000, 0)}
	p := newIngestProgress(w, false)
	p.tty = tty
	p.now = clk.now
	p.start = clk.now()
	return p, clk
}

func TestIngestProgress_TTYRendering(t *testing.T) {
	var buf bytes.Buffer
	p, clk := newTestProgress(&buf, true)

	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseWalk})
	clk.advance(time.Second)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseWalk, Done: 128})
	clk.advance(time.Second)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Total: 1200})
	clk.advance(time.Second)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 342, Total: 1200})
	clk.advance(time.Second)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseLink, Done: 1200, Total: 1200})
	clk.advance(time.Second)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseResolve, Done: 1200, Total: 1200})
	clk.advance(400 * time.Millisecond)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseDone, Done: 1200, Total: 1200})
	p.Finish(nil)

	out := buf.String()
	for _, want := range []string{
		"\r", "\x1b[2K",
		"scanning repo… 128 files",
		"indexing 342/1200 files (28%)",
		"linking cross-file references…",
		"resolving types…",
		"graphi: indexed 1200 files in 5.4s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("TTY output missing %q:\n%q", want, out)
		}
	}
	// The summary must land on a cleared line: the last erase sequence
	// precedes the summary text.
	if lastClear := strings.LastIndex(out, "\x1b[2K"); lastClear > strings.Index(out, "graphi: indexed") {
		t.Fatalf("summary not printed after the final line clear:\n%q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("TTY output must end with a newline after Finish:\n%q", out)
	}
}

func TestIngestProgress_TTYRedrawThrottle(t *testing.T) {
	var buf bytes.Buffer
	p, clk := newTestProgress(&buf, true)

	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 1, Total: 100})
	before := buf.Len()
	clk.advance(time.Millisecond)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 2, Total: 100})
	if buf.Len() != before {
		t.Fatal("same-phase event 1ms apart must not redraw")
	}
	clk.advance(redrawMinGap)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 3, Total: 100})
	if buf.Len() == before {
		t.Fatal("event past the redraw gap must redraw")
	}
}

func TestIngestProgress_NonTTY(t *testing.T) {
	var buf bytes.Buffer
	p, _ := newTestProgress(&buf, false)

	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseWalk})
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Total: 1200})
	for d := 1; d <= 1200; d++ {
		p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: d, Total: 1200})
	}
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseLink, Done: 1200, Total: 1200})
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseResolve, Done: 1200, Total: 1200})
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseDone, Done: 1200, Total: 1200})
	p.Finish(nil)

	out := buf.String()
	if strings.ContainsAny(out, "\x1b\r") {
		t.Fatalf("non-TTY output contains control bytes:\n%q", out)
	}
	want := []string{
		"graphi: scanning repo…",
		"graphi: indexing 1200 files…",
		"graphi: indexing… 25% (300/1200)",
		"graphi: indexing… 50% (600/1200)",
		"graphi: indexing… 75% (900/1200)",
		"graphi: indexing… 100% (1200/1200)",
		"graphi: linking cross-file references…",
		"graphi: resolving types…",
		"graphi: indexed 1200 files in 0s",
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("non-TTY output = %d lines, want %d:\n%q", len(lines), len(want), out)
	}
	for k, w := range want {
		if lines[k] != w {
			t.Fatalf("line %d = %q, want %q", k, lines[k], w)
		}
	}
}

func TestIngestProgress_FinishOnError(t *testing.T) {
	var tbuf bytes.Buffer
	p, _ := newTestProgress(&tbuf, true)
	p.Handle(ingest.ProgressEvent{Phase: ingest.PhaseParse, Done: 3, Total: 10})
	p.Finish(context.DeadlineExceeded)
	if got := tbuf.String(); !strings.HasSuffix(got, "\r\x1b[2K") {
		t.Fatalf("TTY Finish(err) must end with a bare line clear:\n%q", got)
	}

	var pbuf bytes.Buffer
	q, _ := newTestProgress(&pbuf, false)
	q.Handle(ingest.ProgressEvent{Phase: ingest.PhaseWalk})
	before := pbuf.Len()
	q.Finish(context.DeadlineExceeded)
	if pbuf.Len() != before {
		t.Fatalf("non-TTY Finish(err) must print nothing, got:\n%q", pbuf.String()[before:])
	}
}

func TestIngestProgress_FinishIdempotentAndSilentWithoutEvents(t *testing.T) {
	var buf bytes.Buffer
	p, _ := newTestProgress(&buf, true)
	// notRepo path: Finish before any event must stay silent.
	p.Finish(nil)
	p.Finish(nil)
	if buf.Len() != 0 {
		t.Fatalf("Finish without events must print nothing, got %q", buf.String())
	}
}
