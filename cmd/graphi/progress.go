package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/samibel/graphi/engine/ingest"
)

// spinnerFrames is the braille glyph cycle (the same set bubbles' spinner
// uses); advanced once per draw.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// redrawMinGap caps in-place redraws at ~30 fps so a fast parse loop doesn't
// spend its time writing escape sequences.
const redrawMinGap = 33 * time.Millisecond

// isTerminal reports whether f is an interactive character device (same
// pattern as isStdinTTY in clientoffer.go). TERM=dumb forces non-TTY so
// escape-less terminals never see control bytes.
func isTerminal(f *os.File) bool {
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ingestProgress renders ingest.ProgressEvent to a terminal as a single
// in-place status line (spinner + phase + percentage), or degrades to sparse
// plain log lines when w is not a TTY. Handle is the func passed to
// Ingester.WithProgress; Finish must be called exactly once around the caller
// side of IngestAll (idempotent, so calling it on the notRepo path too is
// harmless).
type ingestProgress struct {
	w   io.Writer
	tty bool
	now func() time.Time // injectable clock for tests

	mu            sync.Mutex
	last          ingest.ProgressEvent
	seen          bool // at least one event handled (gates the final summary)
	sawDrift      bool // warm start: a drift scan preceded any ingest phases
	frame         int
	lastDraw      time.Time
	start         time.Time
	parseStart    time.Time // first parse event; anchors the ETA rate
	lastMilestone int       // non-TTY: last 25%-bucket already logged
	stop          chan struct{}
	finished      bool
}

func newIngestProgress(w io.Writer, tty bool) *ingestProgress {
	p := &ingestProgress{w: w, tty: tty, now: time.Now, stop: make(chan struct{})}
	p.start = p.now()
	if tty {
		// Ticker keeps the spinner animating through long silent stretches
		// (linkFiles, the go/types pass, a slow single-file parse).
		go func() {
			t := time.NewTicker(100 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-p.stop:
					return
				case <-t.C:
					p.mu.Lock()
					if p.seen && !p.finished {
						p.draw()
					}
					p.mu.Unlock()
				}
			}
		}()
	}
	return p
}

// Handle consumes one progress event. It is called synchronously from the
// ingesting goroutine, so it only updates state and (throttled) redraws.
func (p *ingestProgress) Handle(ev ingest.ProgressEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	phaseChanged := !p.seen || ev.Phase != p.last.Phase
	if ev.Phase == ingest.PhaseDrift {
		p.sawDrift = true
	}
	if phaseChanged && ev.Phase == ingest.PhaseParse {
		p.parseStart = p.now() // anchor for the ETA rate
	}
	p.last = ev
	p.seen = true
	if ev.Phase == ingest.PhaseDone {
		// Terminal state: Finish prints the durable summary, nothing to draw.
		return
	}
	if p.tty {
		if phaseChanged || p.now().Sub(p.lastDraw) >= redrawMinGap {
			p.draw()
		}
		return
	}
	if phaseChanged {
		fmt.Fprintln(p.w, p.plainPhaseLine(ev))
		p.lastMilestone = 0
	}
	if ev.Phase == ingest.PhaseParse && ev.Total > 0 && ev.Done > 0 {
		if bucket := ev.Done * 4 / ev.Total; bucket > p.lastMilestone {
			p.lastMilestone = bucket
			fmt.Fprintf(p.w, "graphi: indexing… %d%% (%d/%d)\n", bucket*25, ev.Done, ev.Total)
		}
	}
}

// Finish tears the progress line down. On success it prints a durable summary
// (only if any event was ever handled); on error it just clears the line so
// the caller's error print lands clean. Idempotent.
func (p *ingestProgress) Finish(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	close(p.stop)
	if !p.seen {
		return
	}
	if p.tty {
		fmt.Fprint(p.w, "\r\x1b[2K")
	}
	if err == nil {
		fmt.Fprint(p.w, p.summaryLine())
	}
}

// summaryLine picks the durable success line by how the run ended: a warm
// start that never left the drift scan found nothing to do; a warm start
// that re-ingested a delta "updated" it; a cold run "indexed" the repo.
func (p *ingestProgress) summaryLine() string {
	switch {
	case p.last.Phase == ingest.PhaseDrift:
		return fmt.Sprintf("graphi: index up to date (%d files, checked in %s)\n", p.last.Done, p.elapsed())
	case p.sawDrift:
		return fmt.Sprintf("graphi: updated %s in %s\n", filesWord(p.last.Total), p.elapsed())
	default:
		return fmt.Sprintf("graphi: indexed %s in %s\n", filesWord(p.last.Total), p.elapsed())
	}
}

// filesWord renders "N file(s)" with correct pluralization.
func filesWord(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

// draw rewrites the status line in place. Callers hold p.mu and have already
// applied the redraw throttle.
func (p *ingestProgress) draw() {
	p.frame = (p.frame + 1) % len(spinnerFrames)
	p.lastDraw = p.now()
	fmt.Fprintf(p.w, "\r\x1b[2K%c %s", spinnerFrames[p.frame], p.statusLine(p.last))
}

// statusLine renders the phase-specific TTY status text.
func (p *ingestProgress) statusLine(ev ingest.ProgressEvent) string {
	switch ev.Phase {
	case ingest.PhaseDrift:
		if ev.Done > 0 {
			return fmt.Sprintf("graphi: checking for changes… %d files", ev.Done)
		}
		return "graphi: checking for changes…"
	case ingest.PhaseWalk:
		if ev.Done > 0 {
			return fmt.Sprintf("graphi: scanning repo… %d files", ev.Done)
		}
		return "graphi: scanning repo…"
	case ingest.PhaseParse:
		if ev.Total > 0 {
			line := fmt.Sprintf("graphi: indexing %d/%d files (%d%%)", ev.Done, ev.Total, ev.Done*100/ev.Total)
			if eta := p.eta(ev); eta != "" {
				line += " " + eta
			}
			if ev.Path != "" {
				line += " — " + truncatePathLeft(ev.Path, 44)
			}
			return line
		}
		return "graphi: indexing…"
	case ingest.PhaseLink:
		return "graphi: linking cross-file references…"
	case ingest.PhaseResolve:
		return "graphi: resolving types…"
	default:
		return "graphi: finishing up…"
	}
}

// plainPhaseLine renders the non-TTY phase-entry log line.
func (p *ingestProgress) plainPhaseLine(ev ingest.ProgressEvent) string {
	switch ev.Phase {
	case ingest.PhaseDrift:
		return "graphi: checking for changes…"
	case ingest.PhaseWalk:
		return "graphi: scanning repo…"
	case ingest.PhaseParse:
		return fmt.Sprintf("graphi: indexing %s…", filesWord(ev.Total))
	case ingest.PhaseLink:
		return "graphi: linking cross-file references…"
	case ingest.PhaseResolve:
		return "graphi: resolving types…"
	default:
		return "graphi: finishing up…"
	}
}

// etaWarmupDone / etaWarmupElapsed gate the ETA display: the average rate is
// meaningless over the first handful of files or the first moments (a single
// slow README would predict hours), so the estimate only appears once enough
// signal exists — exactly the runs long enough that the user is asking
// "how much longer?".
const (
	etaWarmupDone    = 8
	etaWarmupElapsed = 1 * time.Second
)

// eta renders "~1m40s left" from the average parse rate since the phase
// started, or "" while the estimate would still be noise. Callers hold p.mu.
func (p *ingestProgress) eta(ev ingest.ProgressEvent) string {
	elapsed := p.now().Sub(p.parseStart)
	if ev.Done < etaWarmupDone || ev.Done >= ev.Total || elapsed < etaWarmupElapsed {
		return ""
	}
	remaining := time.Duration(float64(elapsed) / float64(ev.Done) * float64(ev.Total-ev.Done))
	return fmt.Sprintf("~%s left", remaining.Truncate(time.Second))
}

// truncatePathLeft shortens a repo-relative path from the LEFT (the tail is
// the informative part) to at most max runes, prefixing an ellipsis.
func truncatePathLeft(path string, max int) string {
	r := []rune(path)
	if len(r) <= max {
		return path
	}
	return "…" + string(r[len(r)-max+1:])
}

// elapsed formats the wall time since construction with one decimal.
func (p *ingestProgress) elapsed() string {
	return p.now().Sub(p.start).Truncate(100 * time.Millisecond).String()
}
