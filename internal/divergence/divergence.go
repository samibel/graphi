// Package divergence persists and reads the executor-seam divergence record —
// the durable half of what surfaces/client's shadow comparison observes (story
// SW-232 / AX-12a).
//
// # Why this package exists
//
// Before AX-12a the only record of a legacy-vs-executor divergence was
// surfaces/client's process-global counter (CanaryMismatches). Two facts made
// it unusable as evidence: it never reached disk, so a restart erased it, and
// the processes that dispatch through the seam are long-running servers
// (`graphi mcp`, `graphi serve`) while every local diagnostic that could show
// it (`graphi doctor`, `graphi status`) is a different, short-lived process. A
// doctor check reading that counter would print a green zero for a server that
// had diverged on every call. The SW-238 precondition assessment called that
// unclosable in principle, which is what this package closes.
//
// # Shape of the record
//
// One SEGMENT file per writing process, under <state>/executor-divergence/.
// A process only ever writes its own segment, so there is no cross-process
// read-modify-write race to lose counts in: the reader globs the directory and
// sums. Each segment holds, per operation, the observation count, the mismatch
// count, first-seen and last-seen, and a bounded rendering of the most recent
// divergence.
//
// # Zero egress
//
// Writing is local file I/O and nothing else: no logger, no socket, no dialer.
// The zero-egress posture is a hard invariant and observability does not get to
// be its exception (privacy_test.go in this package pins that).
//
// # Honesty
//
// Read + Assess never report "no divergence" for an operation nothing was ever
// observed on. An unobserved operation is UNKNOWN, an unreadable segment is
// counted and disclosed, and the overall verdict degrades to PARTIAL as long as
// any migrated operation has no observation at all. Absence of evidence must
// not read as evidence of parity (SW-232 AC-3).
//
// Layering: divergence lives under internal/, outside the
// cmd→surfaces→engine→core graph, and imports nothing from graphi. The writer
// is installed into surfaces/client by the composition root, so the surface
// package keeps no dependency on this one.
package divergence

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Schema is the wire identifier of the persisted segment and of the rendered
// document. A future shape change gets a new version rather than a silent
// reinterpretation of these bytes.
//
// SW-245 added `dispatches`, `skipped`, `skip_reasons` and `coverage` WITHOUT
// bumping this, which is the deliberate call and the rule for the next addition:
// the version changes when an existing key changes meaning, not when a key is
// added. The addition is compatible in both directions — an older reader ignores
// keys it does not know, and a newer reader reading a record written before
// SW-245 sees no `skipped`, which is the truth about that record (nothing was
// deferred, so nothing was skipped) and makes `dispatches` fall out of the
// observation count. A field whose ABSENCE could not be read as its zero value
// would have to bump the version instead. The operator-facing note is in
// docs/executor-seam-rollback.md §5, "Record format".
const Schema = "executor-divergence-v1"

// dirName is the sub-directory of the graphi state directory that holds the
// segments.
const dirName = "executor-divergence"

// MaxValueLength bounds each repository-influenced string kept in the record.
// A divergence rendering is a pointer to an investigation, not the evidence
// itself, and unbounded values in a file every diagnostic prints is how a
// bounded record becomes an unbounded one.
const MaxValueLength = 256

// maxSegments caps how many segment files the directory retains. Older
// segments are pruned oldest-first by modification time so an operator who
// leaves shadow on for a month does not accumulate one file per restart
// forever. The count is generous on purpose: pruning loses counts, so it
// happens rarely.
//
// The exact bound, stated plainly because the honest version is not the
// comfortable one: prune has no writer-liveness concept beyond protecting the
// pruning process's OWN segment. It sorts by modification time, and a segment
// belonging to another process that is still RUNNING but has simply been quiet
// (no observation since its last flush) is as old, by mtime, as one belonging
// to a process that exited months ago. Once the directory holds maxSegments
// other files, that live-but-quiet writer's segment is eligible for deletion
// and the counts it already wrote are gone from every future Read — and the
// live writer will not rewrite them, because its in-memory record is a running
// total that it re-serialises only when it next observes something.
//
// It is not silent. Every prune is counted into the pruning process's own
// segment (Store.pruned), carried forward from any victim that had a count of
// its own, and surfaced by the read path as a lower-bound disclosure exactly
// like an unreadable segment. It also takes 64+ distinct writer segments to
// reach at all, which a single-or-few-server install does not produce.
const maxSegments = 64

// defaultFlushInterval bounds how often a store touches the disk while
// observations are arriving. A mismatch and the first observation always flush
// immediately; the steady state coalesces, so shadow mode pays at most one
// small write per interval per process rather than one per call.
const defaultFlushInterval = 2 * time.Second

// Mismatch is a bounded rendering of one divergence.
type Mismatch struct {
	Kind     string    `json:"kind"`
	Legacy   string    `json:"legacy"`
	Executor string    `json:"executor"`
	Seen     time.Time `json:"seen"`
}

// OperationRecord is one operation's counters inside a segment, and the same
// shape after merging segments.
//
// Skipped and SkipReasons are SW-245's coverage disclosure. Since the dual run
// moved off the caller's critical path the seam can reach a bounded queue that
// is full, or a shutdown that ran out of drain budget, and NOT compare a call it
// otherwise would have. Those calls are counted here so Observations can never
// be read as "every dispatch was compared" when it was not — see the
// DivergenceRecorder.RecordSkipped contract in surfaces/client/canary.go.
type OperationRecord struct {
	Operation    string `json:"operation"`
	Observations int    `json:"observations"`
	Mismatches   int    `json:"mismatches"`
	// Skipped is how many dispatches reached the seam and were NOT compared.
	Skipped int `json:"skipped,omitempty"`
	// SkipReasons breaks Skipped down by cause, because "dropped under load"
	// and "abandoned at shutdown" are different findings for an operator.
	SkipReasons  map[string]int `json:"skip_reasons,omitempty"`
	FirstSeen    *time.Time     `json:"first_seen,omitempty"`
	LastSeen     *time.Time     `json:"last_seen,omitempty"`
	LastMismatch *Mismatch      `json:"last_mismatch,omitempty"`
}

// segment is the on-disk file one process owns.
type segment struct {
	Schema string `json:"schema"`
	PID    int    `json:"pid"`
	// Pruned is how many OTHER segments this writer has deleted to hold the
	// directory under maxSegments, including any count carried forward from a
	// segment that had pruned some itself. It is what makes retention loss
	// disclosable rather than invisible: the reader sums it and reports the
	// totals as a lower bound (see maxSegments).
	Pruned     int               `json:"pruned_segments,omitempty"`
	Operations []OperationRecord `json:"operations"`
}

// Dir returns the directory holding the segments for a graphi state directory.
func Dir(stateDir string) string { return filepath.Join(stateDir, dirName) }

// Store is one process's writer. It is safe for concurrent use; the seam calls
// it from whatever goroutine served the request.
type Store struct {
	dir  string
	path string

	mu        sync.Mutex
	ops       map[string]*OperationRecord
	pruned    int
	dirty     bool
	flushed   bool
	lastFlush time.Time
	lastErr   error

	// now and flushEvery are fields rather than constants so tests can drive
	// the clock and the coalescing window.
	now        func() time.Time
	flushEvery time.Duration
}

// NewStore prepares a writer for one process under stateDir. It performs no
// I/O: the directory is created by the first flush, so a process that never
// observes a divergence leaves no trace on disk at all.
func NewStore(stateDir string) (*Store, error) {
	if stateDir == "" {
		return nil, fmt.Errorf("divergence: empty state directory")
	}
	name, err := segmentName()
	if err != nil {
		return nil, err
	}
	dir := Dir(stateDir)
	return &Store{
		dir:        dir,
		path:       filepath.Join(dir, name),
		ops:        map[string]*OperationRecord{},
		now:        time.Now,
		flushEvery: defaultFlushInterval,
	}, nil
}

// segmentName is <pid>-<nonce>.json. The nonce is what makes it unique across
// restarts: a bare pid is reused by the operating system, and a reused pid
// would silently overwrite an earlier process's record.
func segmentName() (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("divergence: segment nonce: %w", err)
	}
	return fmt.Sprintf("%d-%s.json", os.Getpid(), hex.EncodeToString(nonce[:])), nil
}

// RecordDivergence records one dual-run observation. mismatch says whether the
// two paths disagreed; kind/legacy/executor describe the disagreement and are
// ignored when they did not.
//
// It is the method surfaces/client's DivergenceRecorder interface names, so the
// surface package can be handed this store without importing it.
func (s *Store) RecordDivergence(operation string, mismatch bool, kind, legacy, executor string) {
	if operation == "" {
		return
	}
	s.mu.Lock()
	at := s.now().UTC()
	rec, ok := s.ops[operation]
	if !ok {
		rec = &OperationRecord{Operation: operation}
		s.ops[operation] = rec
	}
	rec.Observations++
	if rec.FirstSeen == nil {
		first := at
		rec.FirstSeen = &first
	}
	last := at
	rec.LastSeen = &last
	if mismatch {
		rec.Mismatches++
		rec.LastMismatch = &Mismatch{
			Kind:     bound(kind),
			Legacy:   bound(legacy),
			Executor: bound(executor),
			Seen:     at,
		}
	}
	s.dirty = true
	// Flush immediately for the two events an operator must not lose to a
	// kill: the first observation (which is what makes the record exist at
	// all) and every mismatch (which is the finding). Everything else
	// coalesces into the interval.
	due := mismatch || !s.flushed || at.Sub(s.lastFlush) >= s.flushEvery
	s.mu.Unlock()
	if due {
		_ = s.Flush()
	}
}

// RecordSkipped records count dispatches that reached the seam and were NOT
// compared, for the stated reason (SW-245 AC-4).
//
// A skip is a COVERAGE finding, not a divergence, so it does not touch the
// mismatch counters and cannot make a record read DIVERGED. It does flush
// eagerly the first time it happens, for the reason a mismatch does: a coverage
// gap an operator never gets to read is the same as one that was never
// disclosed. Repeats coalesce into the ordinary interval, because a queue that
// is dropping is dropping fast and one write per drop would turn a load problem
// into a disk problem.
func (s *Store) RecordSkipped(operation string, count int, reason string) {
	if operation == "" || count <= 0 {
		return
	}
	if reason == "" {
		reason = "unspecified"
	}
	s.mu.Lock()
	at := s.now().UTC()
	rec, ok := s.ops[operation]
	if !ok {
		rec = &OperationRecord{Operation: operation}
		s.ops[operation] = rec
	}
	first := rec.Skipped == 0
	rec.Skipped += count
	if rec.SkipReasons == nil {
		rec.SkipReasons = map[string]int{}
	}
	rec.SkipReasons[bound(reason)] += count
	s.dirty = true
	due := first || !s.flushed || at.Sub(s.lastFlush) >= s.flushEvery
	s.mu.Unlock()
	if due {
		_ = s.Flush()
	}
}

// Flush writes the segment. It is idempotent and safe to call at any time.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.lastErr = err
		return fmt.Errorf("divergence: mkdir %s: %w", s.dir, err)
	}
	// Prune BEFORE rendering, so the segment about to be written already
	// carries the count of what this flush dropped. Pruning after the write
	// would leave the deletion undisclosed until the next flush — and for a
	// process whose last act is a flush, undisclosed forever.
	s.pruned += prune(s.dir, s.path)

	seg := segment{Schema: Schema, PID: os.Getpid(), Pruned: s.pruned, Operations: make([]OperationRecord, 0, len(s.ops))}
	for _, rec := range s.ops {
		seg.Operations = append(seg.Operations, *rec)
	}
	sort.Slice(seg.Operations, func(i, j int) bool { return seg.Operations[i].Operation < seg.Operations[j].Operation })

	data, err := json.MarshalIndent(seg, "", "  ")
	if err != nil {
		s.lastErr = err
		return fmt.Errorf("divergence: marshal segment: %w", err)
	}
	data = append(data, '\n')
	if err := writeAtomic(s.path, data); err != nil {
		s.lastErr = err
		return err
	}
	s.dirty = false
	s.flushed = true
	s.lastFlush = s.now().UTC()
	return nil
}

// LastError reports the most recent write failure, if any. A store that cannot
// write keeps counting in memory rather than failing the dispatch it was
// observing — the seam's job is to answer the request, not to enforce the
// record — but the failure is retained so a caller can surface it.
func (s *Store) LastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// Path is the segment file this store owns.
func (s *Store) Path() string { return s.path }

// writeAtomic writes data to path via a temp file + rename, owner-only, so a
// reader never observes a half-written segment.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".segment-*")
	if err != nil {
		return fmt.Errorf("divergence: temp segment: %w", err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("divergence: chmod segment: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("divergence: write segment: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("divergence: close segment: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("divergence: rename segment: %w", err)
	}
	return nil
}

// prune caps the retained segment count, oldest first, never removing keep —
// the caller's own segment, which is the only writer-liveness guarantee this
// function has (see maxSegments for the exact bound that leaves).
//
// It RETURNS how many segments were dropped, counting a victim's own pruned
// tally rather than only the file, so the disclosure survives being pruned
// itself. The caller records that number in its segment and the read path
// reports the totals as a lower bound.
func prune(dir, keep string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	type aged struct {
		path string
		mod  time.Time
	}
	var files []aged
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if path == keep {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, aged{path: path, mod: info.ModTime()})
	}
	if len(files) < maxSegments {
		return 0
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	dropped := 0
	for i := 0; i <= len(files)-maxSegments; i++ {
		carried := carriedPruneCount(files[i].path)
		if err := os.Remove(files[i].path); err != nil {
			continue
		}
		dropped += 1 + carried
	}
	return dropped
}

// carriedPruneCount reads the pruned tally a segment carried, so deleting it
// does not also delete the disclosure of what IT had pruned. A segment that
// cannot be read contributes nothing — it was already being counted as
// unreadable by the read path, and inventing a number for it would be the
// opposite of the honesty this counter exists for.
func carriedPruneCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var seg segment
	if err := json.Unmarshal(raw, &seg); err != nil || seg.Schema != Schema {
		return 0
	}
	return seg.Pruned
}

// bound truncates a repository-influenced value and marks that it truncated.
func bound(v string) string {
	if len(v) <= MaxValueLength {
		return v
	}
	return v[:MaxValueLength-len("…")] + "…"
}
