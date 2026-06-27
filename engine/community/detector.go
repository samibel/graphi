package community

import (
	"context"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// Detector is the grouping seam (formalized in SW-103). It abstracts how the
// code graph is partitioned into communities so that interchangeable
// implementations — the structural package-prefix baseline and the
// modularity-maximizing Louvain detector — sit behind one boundary. Engine-layer
// consumers (wiki generation, context-assembly cluster resolution) resolve a
// grouping exclusively through this interface; surfaces and cmd never reach
// detection directly.
//
// Determinism contract: for the same resulting graph state every Detector must
// return byte-identical community IDs and membership (sorted by NodeId), with no
// dependence on map iteration, arrival order, wall-clock, or RNG.
type Detector interface {
	// Detect computes the community partition of the graph reachable from
	// reader, deterministically and read-only.
	Detect(ctx context.Context, reader graphstore.Graphstore) ([]Community, error)
	// Name identifies the active grouping mechanism (e.g. "package-prefix",
	// "louvain") for diagnostics and seam assertions.
	Name() string
}

// PackagePrefixDetector is the retained structural baseline: it groups symbols
// by the prefix of their qualified name before the final "." (see Detect). It is
// kept as a fallback and — crucially — as the measurable modularity baseline for
// the AC-1 comparison; it is no longer the default grouping mechanism.
type PackagePrefixDetector struct{}

// Detect implements Detector via the package-prefix pass.
func (PackagePrefixDetector) Detect(ctx context.Context, reader graphstore.Graphstore) ([]Community, error) {
	return Detect(ctx, reader)
}

// Name returns the baseline detector's stable name.
func (PackagePrefixDetector) Name() string { return "package-prefix" }

// DefaultDetector returns the active grouping mechanism. Since SW-103 the
// default is the deterministic Louvain detector; package-prefix is retained
// behind the seam as a baseline/fallback. This is the single place the default
// is selected, so swapping the grouping mechanism is one change behind the seam.
func DefaultDetector() Detector { return LouvainDetector{} }

// Cluster resolves the community that contains target, computed via detector d
// (the grouping seam that context assembly consumes). It returns the community
// and true when target is a member, or a zero Community and false otherwise.
// Resolution is a pure read over reader; with the default detector this returns
// the Louvain community a target entity belongs to.
func Cluster(ctx context.Context, reader graphstore.Graphstore, d Detector, target model.NodeId) (Community, bool, error) {
	comms, err := d.Detect(ctx, reader)
	if err != nil {
		return Community{}, false, err
	}
	for _, c := range comms {
		for _, m := range c.Members {
			if m == target {
				return c, true, nil
			}
		}
	}
	return Community{}, false, nil
}
