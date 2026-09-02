package embed

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/samibel/graphi/core/model"
)

// MemGenerationStore is the in-memory reference implementation of
// GenerationStore. It is used by tests and is byte-for-byte comparable with
// SQLiteGenerationStore against the shared conformance suite — every test
// in generationstore_conformance_test.go runs against both adapters.
//
// Concurrency: a single mutex serialises Begin/Active/Load and the in-flight
// Build's Upsert/Commit/Abort. The Build's Upsert/Commit/Abort run on the
// same mutex as the store, so a second Begin blocks until Commit/Abort.
//
// Generation identity: each Begin mints a unique opaque id (a 16-hex
// nonce). The id is independent of the fingerprint; fingerprint equality
// is what identifies "the" active generation for a given Fingerprint.
// This matches the SQLite store's design so the conformance suite asserts
// the same shape on both adapters.
type MemGenerationStore struct {
	mu sync.Mutex

	generations map[GenerationID]memGeneration // by id, for Load + Commit
	active      GenerationID                   // "" when none
	staging     GenerationID                   // "" when none
	history     []GenerationID                 // successful commits, oldest first
}

type memGeneration struct {
	fingerprint Fingerprint
	rows        map[model.NodeId]Row // keyed by node id; one row per node
	// committedAt is the RFC3339 UTC timestamp Build.Commit stamped the
	// generation with. Empty for builds predating SW-265.
	committedAt string
}

// NewMemGenerationStore returns an empty store with no active generation.
func NewMemGenerationStore() *MemGenerationStore {
	return &MemGenerationStore{generations: map[GenerationID]memGeneration{}}
}

// mintID produces an opaque 16-hex-char id for a new build. It is unique
// across the store's lifetime (16 random bytes ⇒ collision-safe) and is
// independent of the fingerprint so a re-build under the same fingerprint
// gets a distinct id.
func mintID() GenerationID {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return GenerationID("g-" + hex.EncodeToString(buf[:]))
}

// Begin implements GenerationStore. A second Begin on the same store before
// Commit/Abort of the first returns ErrBuildInProgress; a stale staging row
// from a crashed prior process is discarded.
func (s *MemGenerationStore) Begin(_ context.Context, fp Fingerprint) (Build, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staging != "" {
		return nil, ErrBuildInProgress
	}
	id := mintID()
	s.staging = id
	s.generations[id] = memGeneration{fingerprint: fp, rows: map[model.NodeId]Row{}}
	return &memBuild{store: s, id: id}, nil
}

// memBuild is the in-memory Build. It holds a reference to its parent store
// so Upsert/Commit/Abort can update the shared map; the store's mutex
// guarantees the build is the sole writer while it is open.
type memBuild struct {
	store *MemGenerationStore
	id    GenerationID
}

func (b *memBuild) ID() GenerationID { return b.id }

func (b *memBuild) Upsert(_ context.Context, r Row) error {
	if r.GenerationID != "" && r.GenerationID != b.id {
		return &ValidationFailedError{Reason: "row belongs to a different generation: " + string(r.GenerationID)}
	}
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	gen := b.store.generations[b.id]
	if r.NodeID == "" {
		return &ValidationFailedError{Reason: "row has empty NodeID"}
	}
	if r.Vector == nil {
		return &ValidationFailedError{Reason: "row has nil vector"}
	}
	if gen.fingerprint.Dim > 0 && len(r.Vector) != gen.fingerprint.Dim {
		return &ValidationFailedError{Reason: "row vector dim does not match fingerprint dim"}
	}
	cp := make([]float32, len(r.Vector))
	copy(cp, r.Vector)
	r.GenerationID = b.id
	r.Vector = cp
	gen.rows[r.NodeID] = r
	b.store.generations[b.id] = gen
	return nil
}

func (b *memBuild) Commit(_ context.Context) error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	gen, ok := b.store.generations[b.id]
	if !ok {
		return &ValidationFailedError{Reason: "staging generation vanished before Commit"}
	}
	// SW-261 review round 2 (MAJOR 3): validate every row BEFORE the
	// pointer moves. The pre-fix shape validated in Active, after the
	// pointer had moved, so a wrong-dim row could land and serve as
	// ready until the next Active call discovered it. The validate-
	// then-publish contract is what AC-6 / AC-7 require.
	fp := gen.fingerprint
	if fp.Dim > 0 && len(gen.rows) > 0 {
		for id, r := range gen.rows {
			if len(r.Vector) != fp.Dim {
				return &ValidationFailedError{Reason: fmt.Sprintf("vector dim drift at node %s: persisted=%d expected=%d", id, len(r.Vector), fp.Dim)}
			}
		}
	}
	for id := range gen.rows {
		if id == "" {
			return &ValidationFailedError{Reason: "row has empty NodeID"}
		}
	}
	// A zero-row build is a legitimate state (a reindex over an emptied
	// graph). The prior active generation's rows are kept in the map for
	// diagnostics; the new active id takes over.
	_ = gen
	gen.committedAt = commitTimestamp()
	b.store.generations[b.id] = gen
	b.store.active = b.id
	b.store.history = append(b.store.history, b.id)
	b.store.staging = ""
	return nil
}

// Previous returns the newest committed generation other than activeID.
func (s *MemGenerationStore) Previous(_ context.Context, activeID GenerationID) (Generation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.history) - 1; i >= 0; i-- {
		id := s.history[i]
		if id == activeID {
			continue
		}
		gen, ok := s.generations[id]
		if !ok {
			continue
		}
		return Generation{ID: id, Fingerprint: gen.fingerprint, RowCount: len(gen.rows), Dim: gen.fingerprint.Dim, CommittedAt: gen.committedAt}, true, nil
	}
	return Generation{}, false, nil
}

func (b *memBuild) Abort(_ context.Context) error {
	b.store.mu.Lock()
	defer b.store.mu.Unlock()
	if b.store.staging == b.id {
		delete(b.store.generations, b.id)
		b.store.staging = ""
	}
	return nil
}

// Active implements GenerationStore. The active generation is the one
// whose canonical fingerprint matches the requested fingerprint AND has
// is_active=1.
func (s *MemGenerationStore) Active(_ context.Context, fp Fingerprint, nodes NodeReferencer) (Generation, State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == "" {
		return Generation{ID: "", Fingerprint: fp}, StateMissing, nil
	}
	gen, ok := s.generations[s.active]
	if !ok {
		// The active pointer names a row that vanished — treat as missing
		// rather than corrupt; this is a state the schema should never
		// produce, but the typed answer makes it diagnosable.
		return Generation{ID: "", Fingerprint: fp}, StateMissing, nil
	}
	if gen.fingerprint.Canonical() != fp.Canonical() {
		return Generation{
			ID:          s.active,
			Fingerprint: gen.fingerprint,
			RowCount:    len(gen.rows),
			Dim:         gen.fingerprint.Dim,
			CommittedAt: gen.committedAt,
		}, StateStale, nil
	}
	// Same fingerprint: run the consistency checks (AC-7 corrupt case).
	if err := validateRows(gen.rows, fp, nodes, genRows(gen)); err != nil {
		return Generation{
			ID:          s.active,
			Fingerprint: gen.fingerprint,
			RowCount:    len(gen.rows),
			Dim:         gen.fingerprint.Dim,
			CommittedAt: gen.committedAt,
		}, StateCorrupt, err
	}
	return Generation{
		ID:          s.active,
		Fingerprint: gen.fingerprint,
		RowCount:    len(gen.rows),
		Dim:         gen.fingerprint.Dim,
		CommittedAt: gen.committedAt,
	}, StateReady, nil
}

// Load implements GenerationStore, returning rows in canonical
// (node_id, document_id) order. An unknown id is an empty slice (so a
// caller can Load the prior id post-Commit without nil-checking).
func (s *MemGenerationStore) Load(_ context.Context, id GenerationID) ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen, ok := s.generations[id]
	if !ok {
		return nil, nil
	}
	out := make([]Row, 0, len(gen.rows))
	for _, r := range gen.rows {
		// Defensive copy of the vector so a caller cannot mutate the
		// store's row.
		cp := make([]float32, len(r.Vector))
		copy(cp, r.Vector)
		r.Vector = cp
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NodeID != out[j].NodeID {
			return out[i].NodeID < out[j].NodeID
		}
		return out[i].DocumentID < out[j].DocumentID
	})
	return out, nil
}

// LoadRow implements the GenerationStore point-lookup seam (AC-4
// carry-forward). ok=false when the (generation, node id) pair is
// absent. The vector is defensively copied so a caller cannot mutate
// the store's row through the returned Row.
// DimForModel implements GenerationStore over the in-memory active generation,
// with the same contract as the SQLite adapter: the dimension is returned only
// when the active generation belongs to modelID.
func (s *MemGenerationStore) DimForModel(_ context.Context, modelID string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if modelID == "" || s.active == "" {
		return 0, false, nil
	}
	gen, ok := s.generations[s.active]
	if !ok || gen.fingerprint.ModelID != modelID || gen.fingerprint.Dim <= 0 {
		return 0, false, nil
	}
	return gen.fingerprint.Dim, true, nil
}

func (s *MemGenerationStore) LoadRow(_ context.Context, id GenerationID, nodeID model.NodeId) (Row, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	gen, ok := s.generations[id]
	if !ok {
		return Row{}, false, nil
	}
	r, ok := gen.rows[nodeID]
	if !ok {
		return Row{}, false, nil
	}
	cp := make([]float32, len(r.Vector))
	copy(cp, r.Vector)
	r.Vector = cp
	return r, true, nil
}

// genRows returns the dim of every row in gen (so validateRows can confirm
// the row-level dim matches the fingerprint). For the in-memory adapter the
// dim is fixed at upsert time and is therefore always consistent, but the
// helper is here so the same check runs in both adapters without divergence.
func genRows(g memGeneration) []int {
	dims := make([]int, 0, len(g.rows))
	for _, r := range g.rows {
		dims = append(dims, len(r.Vector))
	}
	return dims
}

// validateRows runs the AC-7 `corrupt` checks shared by both adapters:
// every row's vector dim matches the fingerprint dim, and (when a
// NodeReferencer is supplied) every NodeID references a known node. Any
// mismatch yields a non-nil error; the caller (Active) maps that to
// StateCorrupt.
func validateRows(rows map[model.NodeId]Row, fp Fingerprint, nodes NodeReferencer, dims []int) error {
	if fp.Dim > 0 {
		for _, d := range dims {
			if d != fp.Dim {
				return &ValidationFailedError{Reason: "row vector dim does not match fingerprint dim"}
			}
		}
	}
	if nodes == nil {
		return nil
	}
	for id := range rows {
		exists, err := nodes.NodeExists(context.Background(), id)
		if err != nil {
			return err
		}
		if !exists {
			return &ValidationFailedError{Reason: "row references unknown node: " + string(id)}
		}
	}
	return nil
}
