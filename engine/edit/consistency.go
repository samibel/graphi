package edit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/ingest"
)

// ConsistencyChecker verifies the post-edit invariant: the incrementally-updated
// graph (the live store, after IngestChanged) must be byte-identical to a graph
// produced by a FULL re-index of the same post-edit source. This is AC-3. It is
// exposed as an interface so SW-036/037 can reuse the default implementation or
// substitute a stricter one, and so tests can inject a divergence.
//
// The comparison is over the CANONICAL serialized graph (model.Graph.Marshal),
// never the ingest FNV content cache: the cache is an ingest-internal
// optimization keyed on file bytes (FNV-1a), whereas the invariant we care about
// is graph equivalence, which model.Graph.Marshal renders deterministically
// (nodes sorted by NodeId, edges by EdgeId, fixed field order). Hashing the
// marshalled graph — not the cache — is the architecturally correct target
// (refinement Architect finding #5).
type ConsistencyChecker interface {
	// Check returns nil when the live store's marshalled graph equals a full
	// re-index of the source under root; a non-nil error describes the divergence.
	Check(ctx context.Context, live graphstore.Graphstore, ingester *ingest.Ingester, root string) error
}

// DefaultConsistencyChecker is the standard incremental-vs-full checker. It
// requires the live Ingester to expose its Parser and meta-dir factory; because
// (*ingest.Ingester) does not export those, DefaultConsistencyChecker rebuilds a
// fresh full store using a caller-supplied builder. To keep the default
// dependency-free for the common case, NewApplier wires a checker that captures
// the parser/meta builder; see NewParserConsistencyChecker.
var DefaultConsistencyChecker ConsistencyChecker = noopUntilWired{}

// noopUntilWired is the zero default: if an Applier is constructed without a
// checker AND none is wired, Check fails closed so a missing invariant gate is
// never silently skipped.
type noopUntilWired struct{}

func (noopUntilWired) Check(context.Context, graphstore.Graphstore, *ingest.Ingester, string) error {
	return fmt.Errorf("edit: no consistency checker configured")
}

// FullReindexFactory builds a fresh, empty graphstore and an Ingester bound to
// it, used to perform a throwaway full re-index for the consistency check. The
// returned cleanup releases both. SW-036/037 reuse this seam with their own
// backend choices.
type FullReindexFactory func() (store graphstore.Graphstore, ingester *ingest.Ingester, cleanup func(), err error)

// NewParserConsistencyChecker returns a ConsistencyChecker that, on each Check,
// uses factory to build a fresh store + ingester, runs IngestAll over the
// post-edit source under root into that fresh store (the full re-index),
// marshals both the live store and the fresh store via model.Graph.Marshal, and
// compares their SHA-256 digests. Equal digests prove the incremental update
// converged with a full rebuild (AC-3).
func NewParserConsistencyChecker(factory FullReindexFactory) ConsistencyChecker {
	return &parserChecker{factory: factory}
}

type parserChecker struct {
	factory FullReindexFactory
}

func (c *parserChecker) Check(ctx context.Context, live graphstore.Graphstore, _ *ingest.Ingester, root string) error {
	fullStore, fullIngester, cleanup, err := c.factory()
	if err != nil {
		return fmt.Errorf("build full re-index store: %w", err)
	}
	defer cleanup()

	if err := fullIngester.IngestAll(ctx, root); err != nil {
		return fmt.Errorf("full re-index: %w", err)
	}

	liveDigest, err := graphDigest(ctx, live)
	if err != nil {
		return fmt.Errorf("digest incremental graph: %w", err)
	}
	fullDigest, err := graphDigest(ctx, fullStore)
	if err != nil {
		return fmt.Errorf("digest full graph: %w", err)
	}
	if liveDigest != fullDigest {
		return fmt.Errorf("incremental graph diverges from full re-index: incremental=%s full=%s", liveDigest, fullDigest)
	}
	return nil
}

// graphDigest reads every node/edge from store in canonical order, marshals them
// via model.Graph.Marshal (the deterministic, byte-stable serialization used by
// the snapshot envelope), and returns the hex SHA-256 of those bytes. Two stores
// with the same logical graph yield the same digest regardless of insertion
// order or backend.
func graphDigest(ctx context.Context, store graphstore.Graphstore) (string, error) {
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return "", fmt.Errorf("read nodes: %w", err)
	}
	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return "", fmt.Errorf("read edges: %w", err)
	}
	b, err := model.NewGraph(nodes, edges).Marshal()
	if err != nil {
		return "", fmt.Errorf("marshal graph: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
