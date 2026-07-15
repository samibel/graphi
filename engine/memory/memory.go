// Package memory provides a local-first, CGo-free store for agent memory entries.
//
// Entries are scoped to a Scope and Notebook, carry optional Tags and an opaque
// Payload, and are persisted to a durable JSONL journal. The in-memory index is
// rebuilt from the journal on Open, so the journal is the authoritative source
// of truth. All operations are pure Go and perform no network I/O.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrClosed is returned by operations on a closed Store.
var ErrClosed = errors.New("memory: store closed")

// ErrNotFound is returned when a requested entry does not exist.
var ErrNotFound = errors.New("memory: entry not found")

// EnvAllowSecrets is the explicit LOCAL override for the PRIV-01 (SW-119)
// secret default: set "1" to persist payloads the heuristic flags as
// secret-like. Unset (the default), such payloads are REJECTED with
// ErrSecretRejected instead of being stored with a mere warning flag.
const EnvAllowSecrets = "GRAPHI_MEMORY_ALLOW_SECRETS"

// ErrSecretRejected is returned when a store operation refuses a secret-like
// payload under the default policy. Nothing was persisted.
var ErrSecretRejected = errors.New("memory: payload looks like a secret and was NOT persisted (default policy); set " + EnvAllowSecrets + "=1 to store it anyway")

// ID is a stable identifier for a memory entry. It is assigned by the store on
// StoreMemory and is guaranteed to be unique within that store.
type ID string

// Entry is one durable memory item.
type Entry struct {
	ID            ID       `json:"id"`
	Scope         string   `json:"scope"`
	Notebook      string   `json:"notebook"`
	Tags          []string `json:"tags"`
	Payload       string   `json:"payload"`
	Kind          string   `json:"kind,omitempty"`
	Source        string   `json:"source,omitempty"`
	Confidence    string   `json:"confidence,omitempty"`
	Evidence      string   `json:"evidence,omitempty"`
	SecretSuspect bool     `json:"secret_suspected,omitempty"`
	CreatedAt     int64    `json:"created_at"`           // UnixNano UTC
	UpdatedAt     int64    `json:"updated_at,omitempty"` // UnixNano UTC
}

// Query constrains a memory recall/list. The zero Query matches everything.
type Query struct {
	Scope      string
	Notebook   string
	TagPrefix  string
	CreatedMin int64 // inclusive, UnixNano UTC; 0 disables
	CreatedMax int64 // inclusive, UnixNano UTC; 0 disables
}

// Ledger records avoided token spend when memory is recalled.
type Ledger interface {
	RecordRecall(ctx context.Context, entryCount int, savedTokens int64) error
}

// noopLedger satisfies Ledger without doing anything.
type noopLedger struct{}

func (noopLedger) RecordRecall(ctx context.Context, entryCount int, savedTokens int64) error {
	return nil
}

// Store is the memory store. It is safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	path    string
	f       *os.File
	byID    map[ID]*Entry
	entries []*Entry // sorted by ID ascending (canonical order)
	closed  bool
	ledger  Ledger
}

// NewMemStore opens an ephemeral memory store backed by a temporary JSONL file.
// It is useful for surfaces that do not configure an explicit memory path. The
// file is removed when the store is closed.
func NewMemStore(ledger Ledger) (*Store, error) {
	f, err := os.CreateTemp("", "graphi-memory-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("memory: temp file: %w", err)
	}
	path := f.Name()
	_ = f.Close()
	s, err := Open(path, ledger)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	s.path = path
	return s, nil
}

// Open opens or creates a memory store at path. The journal is a local JSONL
// file holding whatever an agent chose to remember — owner-only by contract
// (PRIV-01 / SW-119): missing parent directories are created 0700, the journal
// is created 0600, and a pre-existing too-wide journal is migrated to 0600 on
// open. Existing parent directories are never chmodded (the caller may point
// into a shared directory it does not own the policy for). If ledger is nil, a
// no-op ledger is used.
func Open(path string, ledger Ledger) (*Store, error) {
	if ledger == nil {
		ledger = noopLedger{}
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("memory: create parent dir %s: %w", dir, err)
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("memory: open %s: %w", path, err)
	}
	if fi, serr := f.Stat(); serr == nil && fi.Mode().Perm() != 0o600 {
		if cerr := f.Chmod(0o600); cerr != nil {
			_ = f.Close()
			return nil, fmt.Errorf("memory: tighten journal mode: %w", cerr)
		}
	}
	s := &Store{path: path, f: f, byID: make(map[ID]*Entry), ledger: ledger}
	if err := s.reload(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return s, nil
}

// reload reads the journal, discards torn/invalid trailing lines, and rebuilds
// the in-memory index in deterministic order.
func (s *Store) reload() error {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("memory: seek: %w", err)
	}
	data, err := io.ReadAll(s.f)
	if err != nil {
		return fmt.Errorf("memory: read journal: %w", err)
	}
	s.byID = make(map[ID]*Entry)
	s.entries = nil

	type lineInfo struct {
		offset int64
		text   string
	}
	var lines []lineInfo
	start := int64(0)
	for i := 0; i <= len(data); i++ {
		if i == len(data) || data[i] == '\n' {
			lines = append(lines, lineInfo{offset: start, text: string(data[start:i])})
			start = int64(i + 1)
		}
	}

	validThrough := int64(0)
	for _, ln := range lines {
		t := strings.TrimSpace(ln.text)
		if t == "" {
			validThrough = ln.offset + int64(len(ln.text)) + 1
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(t), &e); err != nil {
			break
		}
		if e.ID == "" || e.CreatedAt == 0 {
			break
		}
		cp := e
		s.byID[e.ID] = &cp
		validThrough = ln.offset + int64(len(ln.text)) + 1
	}

	if fi, err := s.f.Stat(); err == nil && validThrough < fi.Size() {
		if err := s.f.Truncate(validThrough); err != nil {
			return fmt.Errorf("memory: truncate torn tail: %w", err)
		}
	}

	s.entries = make([]*Entry, 0, len(s.byID))
	for _, e := range s.byID {
		s.entries = append(s.entries, e)
	}
	s.sortEntries()
	return nil
}

func (s *Store) sortEntries() {
	sort.Slice(s.entries, func(i, j int) bool { return s.entries[i].ID < s.entries[j].ID })
}

// StoreMemory persists a new memory entry and returns its assigned ID.
func (s *Store) StoreMemory(ctx context.Context, scope, notebook string, tags []string, payload string) (ID, error) {
	return s.StoreMemoryWithProvenance(ctx, ProvenanceInput{
		Scope:    scope,
		Notebook: notebook,
		Tags:     tags,
		Payload:  payload,
	})
}

// Memory entry kinds — the closed product vocabulary for Entry.Kind. An empty
// kind is allowed (untyped fact); a non-empty kind must be a member of this
// set so agents can rely on the taxonomy.
const (
	KindArchitecture = "architecture"
	KindCommand      = "command"
	KindConvention   = "convention"
	KindDecision     = "decision"
	KindRisk         = "risk"
	KindDependency   = "dependency"
	KindWorkflow     = "workflow"
)

// ValidKinds returns the closed set of memory entry kinds in canonical order.
func ValidKinds() []string {
	return []string{
		KindArchitecture, KindCommand, KindConvention,
		KindDecision, KindRisk, KindDependency, KindWorkflow,
	}
}

// validKindSet is the membership index backing kind validation.
var validKindSet = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range ValidKinds() {
		m[k] = true
	}
	return m
}()

// ErrInvalidKind is wrapped by store operations that receive a kind outside
// the closed vocabulary.
var ErrInvalidKind = errors.New("memory: invalid kind")

// ProvenanceInput carries the optional provenance fields for a memory entry.
type ProvenanceInput struct {
	Scope       string
	Notebook    string
	Tags        []string
	Payload     string
	Kind        string
	Source      string
	Confidence  string
	Evidence    string
	OverwriteID ID // empty -> create new entry
}

// StoreMemoryWithProvenance persists a memory entry with provenance metadata.
// If OverwriteID is set and exists, the old entry is replaced preserving CreatedAt.
func (s *Store) StoreMemoryWithProvenance(ctx context.Context, p ProvenanceInput) (ID, error) {
	if p.Kind != "" && !validKindSet[p.Kind] {
		return "", fmt.Errorf("%w: %q (valid: %s)", ErrInvalidKind, p.Kind, strings.Join(ValidKinds(), ", "))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", ErrClosed
	}
	now := time.Now().UTC().UnixNano()
	secretSuspect := detectSecret(p.Payload)
	// PRIV-01 (SW-119): reject, don't persist-and-flag. A flagged entry that is
	// stored anyway is already a leak into a durable journal; the default fails
	// closed BEFORE any write, and the override is an explicit local decision.
	// Overrides keep the SecretSuspect flag so the entry stays visibly marked.
	if secretSuspect && os.Getenv(EnvAllowSecrets) != "1" {
		return "", ErrSecretRejected
	}
	var id ID
	var createdAt int64
	if p.OverwriteID != "" {
		old, ok := s.byID[p.OverwriteID]
		if !ok {
			// An explicit overwrite target that does not exist is a caller
			// error — silently creating a new entry would hide typos.
			return "", fmt.Errorf("%w: overwrite target %q", ErrNotFound, p.OverwriteID)
		}
		id = p.OverwriteID
		createdAt = old.CreatedAt
		delete(s.byID, p.OverwriteID)
		filtered := make([]*Entry, 0, len(s.byID))
		for _, e := range s.entries {
			if e.ID != id {
				filtered = append(filtered, e)
			}
		}
		s.entries = filtered
	}
	if id == "" {
		id = s.nextID()
		createdAt = now
	}
	e := Entry{
		ID:            id,
		Scope:         p.Scope,
		Notebook:      p.Notebook,
		Tags:          canonicalTags(p.Tags),
		Payload:       p.Payload,
		Kind:          p.Kind,
		Source:        p.Source,
		Confidence:    p.Confidence,
		Evidence:      p.Evidence,
		SecretSuspect: secretSuspect,
		CreatedAt:     createdAt,
		UpdatedAt:     now,
	}
	if err := s.appendEntry(&e); err != nil {
		return "", err
	}
	cp := e
	s.byID[id] = &cp
	s.entries = append(s.entries, &cp)
	s.sortEntries()
	return id, nil
}

// detectSecret applies a local heuristic to flag secret-like payloads.
func detectSecret(payload string) bool {
	p := strings.TrimSpace(payload)
	if len(p) < 8 {
		return false
	}
	patterns := []string{
		"AKIA", "-----BEGIN", "bearer ", "token=", "password=", "secret=",
		"api_key", "apikey", "private_key",
	}
	lower := strings.ToLower(p)
	for _, pat := range patterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return true
		}
	}
	return false
}

// ListMemory returns entries matching q, bounded by limit (0 = unlimited).
func (s *Store) ListMemory(ctx context.Context, q Query, limit int) ([]Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, ErrClosed
	}
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if matches(e, q) {
			out = append(out, *e)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// ExportMemory serializes all matching entries to the provided writer as JSONL.
func (s *Store) ExportMemory(ctx context.Context, q Query, w io.Writer) error {
	entries, err := s.ListMemory(ctx, q, 0)
	if err != nil {
		return err
	}
	for _, e := range entries {
		line, err := marshalEntry(&e)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// nextID returns a fresh ID. IDs are "mem-<N>" where N is monotonically
// increasing, derived from the existing entries only (no randomness).
func (s *Store) nextID() ID {
	maxN := 0
	for id := range s.byID {
		if n, err := parseIDSeq(id); err == nil && n > maxN {
			maxN = n
		}
	}
	return ID(fmt.Sprintf("mem-%d", maxN+1))
}

func parseIDSeq(id ID) (int, error) {
	var n int
	_, err := fmt.Sscanf(string(id), "mem-%d", &n)
	return n, err
}

func canonicalTags(tags []string) []string {
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (s *Store) appendEntry(e *Entry) error {
	line, err := marshalEntry(e)
	if err != nil {
		return fmt.Errorf("memory: marshal entry: %w", err)
	}
	if _, err := s.f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("memory: write entry: %w", err)
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("memory: fsync entry: %w", err)
	}
	return nil
}

// marshalEntry returns deterministic JSON with keys in fixed order.
func marshalEntry(e *Entry) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	fmt.Fprintf(&buf, `"id":%q,`, e.ID)
	fmt.Fprintf(&buf, `"scope":%q,`, e.Scope)
	fmt.Fprintf(&buf, `"notebook":%q,`, e.Notebook)
	tagsJSON, err := json.Marshal(e.Tags)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&buf, `"tags":%s,`, tagsJSON)
	fmt.Fprintf(&buf, `"payload":%q,`, e.Payload)
	if e.Kind != "" {
		fmt.Fprintf(&buf, `"kind":%q,`, e.Kind)
	}
	if e.Source != "" {
		fmt.Fprintf(&buf, `"source":%q,`, e.Source)
	}
	if e.Confidence != "" {
		fmt.Fprintf(&buf, `"confidence":%q,`, e.Confidence)
	}
	if e.Evidence != "" {
		fmt.Fprintf(&buf, `"evidence":%q,`, e.Evidence)
	}
	if e.SecretSuspect {
		fmt.Fprintf(&buf, `"secret_suspected":true,`)
	}
	fmt.Fprintf(&buf, `"created_at":%d`, e.CreatedAt)
	if e.UpdatedAt != 0 {
		fmt.Fprintf(&buf, `,"updated_at":%d`, e.UpdatedAt)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// RecallMemory returns memory entries matching q. It also records a ledger entry
// for the avoided token spend.
func (s *Store) RecallMemory(ctx context.Context, q Query) ([]Entry, error) {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, ErrClosed
	}
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		if matches(e, q) {
			out = append(out, *e)
		}
	}
	s.mu.RUnlock()
	if err := s.ledger.RecordRecall(ctx, len(out), int64(len(out))*100); err != nil {
		return out, fmt.Errorf("memory: ledger recall: %w", err)
	}
	return out, nil
}

func matches(e *Entry, q Query) bool {
	if q.Scope != "" && e.Scope != q.Scope {
		return false
	}
	if q.Notebook != "" && e.Notebook != q.Notebook {
		return false
	}
	if q.TagPrefix != "" && !hasTagPrefix(e.Tags, q.TagPrefix) {
		return false
	}
	if q.CreatedMin != 0 && e.CreatedAt < q.CreatedMin {
		return false
	}
	if q.CreatedMax != 0 && e.CreatedAt > q.CreatedMax {
		return false
	}
	return true
}

func hasTagPrefix(tags []string, prefix string) bool {
	for _, t := range tags {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// ForgetMemory removes the entry with the given id. It is idempotent: deleting
// a non-existent entry is not an error.
func (s *Store) ForgetMemory(ctx context.Context, id ID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if _, ok := s.byID[id]; !ok {
		return nil
	}
	delete(s.byID, id)
	filtered := make([]*Entry, 0, len(s.byID))
	for _, e := range s.entries {
		if e.ID != id {
			filtered = append(filtered, e)
		}
	}
	s.entries = filtered
	return s.rewriteJournal()
}

func (s *Store) rewriteJournal() error {
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("memory: seek for rewrite: %w", err)
	}
	if err := s.f.Truncate(0); err != nil {
		return fmt.Errorf("memory: truncate for rewrite: %w", err)
	}
	for _, e := range s.entries {
		line, err := marshalEntry(e)
		if err != nil {
			return err
		}
		if _, err := s.f.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("memory: rewrite entry: %w", err)
		}
	}
	if err := s.f.Sync(); err != nil {
		return fmt.Errorf("memory: fsync rewrite: %w", err)
	}
	return nil
}

// Close releases the store and removes the backing file if it was created as an
// ephemeral temp file (path still matches a temp pattern).
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.f.Close(); err != nil {
		return err
	}
	if strings.HasPrefix(filepath.Base(s.path), "graphi-memory-") {
		_ = os.Remove(s.path)
	}
	return nil
}
