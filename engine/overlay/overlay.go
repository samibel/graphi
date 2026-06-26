// Package overlay implements graphi's G8 editor-overlay subsystem: a pure
// in-memory view layer that lets the graph reflect the CURRENT editor buffer
// (including unsaved edits) rather than only the last file persisted to disk.
//
// An overlay holds an in-memory buffer for a path; push applies byte-span edits
// to that buffer and re-derives the affected graph fragment by re-parsing the
// buffer — byte-identical to a full re-index of the same buffer content, because
// it is literally the same parser over the same bytes. fork creates an isolated
// sibling (edits never cross), switch selects the active overlay, and merge folds
// an overlay's buffer back onto its parent deterministically.
//
// Invariants: overlays are PURE IN-MEMORY — no operation writes to disk, mutates
// the workspace, or touches the on-disk index (this package imports no os write
// path). The base graph is never mutated here. Operations are deterministic and
// order-stable. The package lives in the engine layer (cmd→surfaces→engine→core):
// it imports core/model + core/parse only; core stays overlay-agnostic. Surface
// transport (loopback-only/zero-egress) is the caller's concern.
package overlay

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
)

// Parser is the minimal parse seam overlays re-derive fragments through — the
// same shape as ingest.Parser, kept local to avoid an engine→engine import.
type Parser interface {
	Parse(ctx context.Context, path string, src []byte) (*parse.ParseResult, error)
}

// ID is a stable overlay identifier, content-derived at registration.
type ID string

// Edit is an in-memory byte-span replacement on an overlay buffer: the half-open
// [Start,End) range is replaced by Replacement. Start==End is an insertion.
type Edit struct {
	Start       int
	End         int
	Replacement string
}

type overlay struct {
	id     ID
	path   string
	buffer []byte
	parent ID // "" for a root overlay
}

// Set manages a collection of overlays and the active one. It is NOT safe for
// concurrent use; callers serialize overlay operations per session.
type Set struct {
	parser   Parser
	overlays map[ID]*overlay
	active   ID
	forkSeq  uint64 // deterministic fork disambiguator (advances by call order)
}

// NewSet constructs an overlay Set that re-derives fragments through parser.
func NewSet(parser Parser) *Set {
	return &Set{parser: parser, overlays: map[ID]*overlay{}}
}

// Register opens a new overlay for path seeded with baseContent (typically the
// on-disk bytes). The ID is stable: the same (path, content) always yields the
// same ID. No disk write occurs and the base graph is untouched.
func (s *Set) Register(path string, baseContent []byte) (ID, error) {
	if path == "" {
		return "", fmt.Errorf("overlay: empty path")
	}
	id := contentID(path, baseContent, "")
	buf := make([]byte, len(baseContent))
	copy(buf, baseContent)
	s.overlays[id] = &overlay{id: id, path: path, buffer: buf}
	return id, nil
}

// Push applies edits to the overlay's buffer and returns the re-derived graph
// fragment bytes. The result is byte-identical to a full re-index of the
// resulting buffer (same parser, same bytes) and deterministic for a given edit
// sequence.
func (s *Set) Push(ctx context.Context, id ID, edits []Edit) ([]byte, error) {
	ov, ok := s.overlays[id]
	if !ok {
		return nil, fmt.Errorf("overlay: unknown overlay %q", id)
	}
	next, err := applyEdits(ov.buffer, edits)
	if err != nil {
		return nil, err
	}
	ov.buffer = next
	return s.fragment(ctx, ov)
}

// Fork creates an isolated sibling overlay copying id's current buffer. Pushes to
// either overlay leave the other unaffected (the buffers are independent copies).
func (s *Set) Fork(id ID) (ID, error) {
	parent, ok := s.overlays[id]
	if !ok {
		return "", fmt.Errorf("overlay: unknown overlay %q", id)
	}
	s.forkSeq++
	childID := forkID(id, parent.buffer, s.forkSeq)
	buf := make([]byte, len(parent.buffer))
	copy(buf, parent.buffer)
	s.overlays[childID] = &overlay{id: childID, path: parent.path, buffer: buf, parent: id}
	return childID, nil
}

// Switch sets the active overlay. Subsequent ActiveFragment calls resolve against
// it; switching back to a prior overlay exactly restores its view (buffers are
// retained per overlay).
func (s *Set) Switch(id ID) error {
	if _, ok := s.overlays[id]; !ok {
		return fmt.Errorf("overlay: unknown overlay %q", id)
	}
	s.active = id
	return nil
}

// Active returns the active overlay ID (empty if none selected).
func (s *Set) Active() ID { return s.active }

// Merge folds id's buffer onto its parent (the parent adopts the child's buffer)
// and removes the child. It returns the parent ID. The operation is deterministic
// and order-stable: the merged parent buffer equals the child's buffer exactly,
// so re-deriving the parent fragment is byte-identical to re-indexing that buffer.
func (s *Set) Merge(id ID) (ID, error) {
	child, ok := s.overlays[id]
	if !ok {
		return "", fmt.Errorf("overlay: unknown overlay %q", id)
	}
	if child.parent == "" {
		return "", fmt.Errorf("overlay: %q has no parent to merge into", id)
	}
	parent, ok := s.overlays[child.parent]
	if !ok {
		return "", fmt.Errorf("overlay: parent %q of %q is gone", child.parent, id)
	}
	parent.buffer = make([]byte, len(child.buffer))
	copy(parent.buffer, child.buffer)
	delete(s.overlays, id)
	if s.active == id {
		s.active = parent.id
	}
	return parent.id, nil
}

// Fragment derives the graph fragment bytes for id's current buffer.
func (s *Set) Fragment(ctx context.Context, id ID) ([]byte, error) {
	ov, ok := s.overlays[id]
	if !ok {
		return nil, fmt.Errorf("overlay: unknown overlay %q", id)
	}
	return s.fragment(ctx, ov)
}

// ActiveFragment derives the fragment for the active overlay.
func (s *Set) ActiveFragment(ctx context.Context) ([]byte, error) {
	if s.active == "" {
		return nil, fmt.Errorf("overlay: no active overlay")
	}
	return s.Fragment(ctx, s.active)
}

// Buffer returns a copy of id's current buffer (for inspection/tests).
func (s *Set) Buffer(id ID) ([]byte, bool) {
	ov, ok := s.overlays[id]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(ov.buffer))
	copy(out, ov.buffer)
	return out, true
}

// fragment parses the overlay's buffer and serializes the resulting nodes/edges
// through the canonical model.Graph marshaller (deterministic, sorted). It is the
// single derivation path, so push/fragment/full-re-index all agree byte-for-byte.
func (s *Set) fragment(ctx context.Context, ov *overlay) ([]byte, error) {
	res, err := s.parser.Parse(ctx, ov.path, ov.buffer)
	if err != nil {
		return nil, fmt.Errorf("overlay: derive fragment for %q: %w", ov.path, err)
	}
	var nodes []model.Node
	var edges []model.Edge
	if res != nil {
		nodes, edges = res.Nodes, res.Edges
	}
	b, err := model.NewGraph(nodes, edges).Marshal()
	if err != nil {
		return nil, fmt.Errorf("overlay: marshal fragment: %w", err)
	}
	return b, nil
}

// applyEdits applies all edits to src and returns a fresh buffer. Edits are
// applied from the highest Start first so earlier offsets stay valid; overlapping
// edits are rejected (their combined result would be order-dependent). src is not
// mutated.
func applyEdits(src []byte, edits []Edit) ([]byte, error) {
	if len(edits) == 0 {
		out := make([]byte, len(src))
		copy(out, src)
		return out, nil
	}
	sorted := make([]Edit, len(edits))
	copy(sorted, edits)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start > sorted[j].Start })
	for i, e := range sorted {
		if e.Start < 0 || e.End < e.Start || e.End > len(src) {
			return nil, fmt.Errorf("overlay: invalid edit span [%d,%d) over %d bytes", e.Start, e.End, len(src))
		}
		if i+1 < len(sorted) && sorted[i+1].End > e.Start {
			return nil, fmt.Errorf("overlay: overlapping edits [%d,%d) and [%d,%d)",
				sorted[i+1].Start, sorted[i+1].End, e.Start, e.End)
		}
	}
	out := make([]byte, len(src))
	copy(out, src)
	for _, e := range sorted {
		next := make([]byte, 0, len(out)-(e.End-e.Start)+len(e.Replacement))
		next = append(next, out[:e.Start]...)
		next = append(next, e.Replacement...)
		next = append(next, out[e.End:]...)
		out = next
	}
	return out, nil
}

// contentID derives a stable overlay ID from (path, content, salt) via FNV-1a.
func contentID(path string, content []byte, salt string) ID {
	h := fnv.New64a()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(salt))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(content)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], h.Sum64())
	return ID(fmt.Sprintf("ov_%x", b))
}

// forkID derives a distinct, deterministic child ID from the parent ID, its
// buffer, and the monotonic fork sequence (so two forks of identical content get
// distinct IDs in call order).
func forkID(parent ID, buffer []byte, seq uint64) ID {
	return contentID(string(parent), buffer, fmt.Sprintf("fork:%d", seq))
}
