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
// forever. The count is generous on purpose: pruning loses history, so it
// happens rarely.
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
type OperationRecord struct {
	Operation    string     `json:"operation"`
	Observations int        `json:"observations"`
	Mismatches   int        `json:"mismatches"`
	FirstSeen    *time.Time `json:"first_seen,omitempty"`
	LastSeen     *time.Time `json:"last_seen,omitempty"`
	LastMismatch *Mismatch  `json:"last_mismatch,omitempty"`
}

// segment is the on-disk file one process owns.
type segment struct {
	Schema     string            `json:"schema"`
	PID        int               `json:"pid"`
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

// Flush writes the segment. It is idempotent and safe to call at any time.
func (s *Store) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	seg := segment{Schema: Schema, PID: os.Getpid(), Operations: make([]OperationRecord, 0, len(s.ops))}
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
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		s.lastErr = err
		return fmt.Errorf("divergence: mkdir %s: %w", s.dir, err)
	}
	if err := writeAtomic(s.path, data); err != nil {
		s.lastErr = err
		return err
	}
	s.dirty = false
	s.flushed = true
	s.lastFlush = s.now().UTC()
	prune(s.dir, s.path)
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

// prune caps the retained segment count, oldest first, never removing keep.
func prune(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
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
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for i := 0; i <= len(files)-maxSegments; i++ {
		_ = os.Remove(files[i].path)
	}
}

// bound truncates a repository-influenced value and marks that it truncated.
func bound(v string) string {
	if len(v) <= MaxValueLength {
		return v
	}
	return v[:MaxValueLength-len("…")] + "…"
}
