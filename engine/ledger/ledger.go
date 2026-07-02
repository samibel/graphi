package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Ledger is a durable savings ledger backed by an append-only JSONL journal.
// Use Open to create or reopen a ledger; Record to append a priced entry;
// SessionTotal/Cumulative to read the rollups; Close to release the file.
//
// A Ledger is safe for use by a single writer (the daemon). It performs no
// network I/O.
type Ledger struct {
	path          string
	f             *os.File
	entries       []Entry // valid, in-order entries (Seq strictly increasing)
	lastSeq       int64
	session       string // fresh per Open
	cumulative    MicroUSD
	sessionSum    MicroUSD
	sessionCapped bool // true if any contribution in THIS session was capped
}

// ErrClosed is returned by any operation after Close.
var ErrClosed = errors.New("ledger: closed")

// Open creates or reopens the ledger journal at path. It reads the existing
// journal, truncates any torn final line to recover to the last fully-committed
// consistent state, recomputes the cumulative total from the surviving entries,
// assigns a fresh SessionID for this session, and positions the file for
// appending. The path must be a local file (no network).
func Open(path string) (*Ledger, error) {
	if isRemoteSource(path) {
		return nil, fmt.Errorf("ledger: non-local source rejected (%q) — journal must be a local file", path)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("ledger: open %s: %w", path, err)
	}
	l := &Ledger{path: path, f: f}
	if err := l.reload(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return l, nil
}

// reload reads the journal, truncates a torn tail, rebuilds in-memory state, and
// recomputes the cumulative total from the surviving entries. It is the single
// reload path used by Open.
func (l *Ledger) reload() error {
	if _, err := l.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("ledger: seek: %w", err)
	}
	data, err := io.ReadAll(l.f)
	if err != nil {
		return fmt.Errorf("ledger: read journal: %w", err)
	}

	// Split into lines, preserving the byte offset where each line starts so we
	// can truncate the torn tail precisely.
	type lineInfo struct {
		offset int64
		text   string
	}
	var lines []lineInfo
	start := int64(0)
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			text := string(data[start:i]) // excludes the trailing '\n'
			lines = append(lines, lineInfo{offset: start, text: text})
			start = int64(i + 1)
		}
	}

	// Parse lines in order; stop at the first invalid/non-monotonic line — that
	// marks the torn tail. validThrough is the byte offset of the end of the last
	// valid line's newline (i.e. the length to keep).
	var (
		valid        []Entry
		lastSeq      int64    = 0
		validThrough int64    = 0
		sessions              = map[string]struct{}{}
		cumulative   MicroUSD = 0
	)
	for _, ln := range lines {
		t := strings.TrimSpace(ln.text)
		if t == "" {
			// An interior blank line in an append-only journal is unexpected;
			// treat a trailing blank (from a final '\n') as benign only if it is
			// the last line. A blank line WITH content after it would have been
			// caught by the next iteration's parse. Here, an empty trailing line
			// just advances validThrough and continues.
			validThrough = ln.offset + int64(len(ln.text)) + 1 // include its newline
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(t), &e); err != nil {
			break // torn line — stop
		}
		if e.Seq <= 0 || e.Seq <= lastSeq {
			break // not strictly monotonic — torn/duplicate tail
		}
		// Valid entry.
		valid = append(valid, e)
		lastSeq = e.Seq
		sessions[e.SessionID] = struct{}{}
		if e.Priced {
			cumulative += e.MicroUSD
		}
		validThrough = ln.offset + int64(len(ln.text)) + 1 // include this line's newline
	}

	// If a torn tail was present, truncate the file to the last valid boundary.
	if fi, err := l.f.Stat(); err == nil && validThrough < fi.Size() {
		if err := l.f.Truncate(validThrough); err != nil {
			return fmt.Errorf("ledger: truncate torn tail: %w", err)
		}
	}

	l.entries = valid
	l.lastSeq = lastSeq
	l.cumulative = cumulative
	l.sessionSum = 0        // fresh session starts at 0
	l.sessionCapped = false // fresh session: no caps yet
	l.session = nextSessionID(sessions)
	return nil
}

// nextSessionID derives a deterministic fresh session id from the set of session
// ids already in the journal. It parses numeric suffixes of the form "sess-N";
// the new session is "sess-(maxN+1)". If no prior sessions exist or none parse,
// it starts at "sess-1". This avoids wall-clock/random while keeping session ids
// distinct and ordered.
func nextSessionID(sessions map[string]struct{}) string {
	maxN := 0
	for s := range sessions {
		if n, err := strconv.Atoi(strings.TrimPrefix(s, "sess-")); err == nil && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("sess-%d", maxN+1)
}

// Record appends one priced entry to the journal durably. It assigns the next
// monotonic Seq, writes one JSON line, calls Sync() (fsync) to commit, and only
// then updates the in-memory session+cumulative totals. A crash before Sync
// leaves the line absent or torn; the next Open recovers by truncating the tail.
//
// Unpriced credits (Priced=false) are recorded for auditability but contribute 0
// to totals.
func (l *Ledger) Record(c Credit) (Entry, error) {
	return l.RecordCapped(c, false)
}

// RecordCapped appends one entry to the journal durably, tagging it with whether
// the anti-gaming cap (SW-020) clamped this contribution. The capApplied flag is
// persisted so the readout can transparently indicate "a cap was applied". See
// Record for the commit semantics (Seq assignment, append, fsync).
func (l *Ledger) RecordCapped(c Credit, capApplied bool) (Entry, error) {
	if l.f == nil {
		return Entry{}, ErrClosed
	}
	e := Entry{
		Seq:        l.lastSeq + 1,
		SessionID:  l.session,
		CallID:     c.CallID,
		Model:      c.Model,
		MicroUSD:   c.MicroUSD,
		Priced:     c.Priced,
		CapApplied: capApplied,
	}
	line, err := json.Marshal(e)
	if err != nil {
		return Entry{}, fmt.Errorf("ledger: marshal entry: %w", err)
	}
	// O_APPEND positions the write at end-of-file atomically; write line + newline.
	if _, err := l.f.Write(append(line, '\n')); err != nil {
		return Entry{}, fmt.Errorf("ledger: write entry: %w", err)
	}
	if err := l.f.Sync(); err != nil { // fsync — durable commit
		return Entry{}, fmt.Errorf("ledger: fsync entry: %w", err)
	}
	l.entries = append(l.entries, e)
	l.lastSeq = e.Seq
	if e.Priced {
		l.cumulative += e.MicroUSD
		l.sessionSum += e.MicroUSD
	}
	if capApplied {
		l.sessionCapped = true
	}
	return e, nil
}

// SessionTotal returns the per-session USD total (sum of this session's priced
// entries). A fresh session after restart starts at 0.
func (l *Ledger) SessionTotal() MicroUSD {
	if l.f == nil {
		return 0
	}
	return l.sessionSum
}

// Cumulative returns the cumulative USD total across ALL sessions (sum of every
// priced entry ever committed). It survives daemon restarts unchanged.
func (l *Ledger) Cumulative() MicroUSD {
	if l.f == nil {
		return 0
	}
	return l.cumulative
}

// RecomputeFromEntries recomputes the cumulative and per-session totals directly
// from the in-memory entry list. It is used by tests to prove the cached totals
// match a fresh recompute (no drift), and to prove the rollup identity
// (cumulative == sum of per-session totals == sum of entries).
func (l *Ledger) RecomputeFromEntries() (cumulative MicroUSD, bySession map[string]MicroUSD) {
	bySession = make(map[string]MicroUSD)
	for _, e := range l.entries {
		if e.Priced {
			cumulative += e.MicroUSD
			bySession[e.SessionID] += e.MicroUSD
		}
	}
	return cumulative, bySession
}

// Entries returns a defensive copy of the valid entries, ordered by Seq.
func (l *Ledger) Entries() []Entry {
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Close releases the journal file. After Close, further operations return
// ErrClosed.
func (l *Ledger) Close() error {
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// isRemoteSource reports whether path looks like a remote URL. The journal must
// be a local file.
func isRemoteSource(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

// SessionIDs returns the sorted distinct session ids present in the journal,
// for observability/testing.
func (l *Ledger) SessionIDs() []string {
	seen := map[string]struct{}{}
	for _, e := range l.entries {
		seen[e.SessionID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
