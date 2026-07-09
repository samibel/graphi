package community

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	corecommunity "github.com/samibel/graphi/core/community"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
)

// LouvainDetector is the default grouping mechanism since SW-103. It projects
// the graphstore into the pure core/community node/edge view, runs deterministic
// modularity-maximizing Louvain in `core`, and returns []Community with stable
// canonical IDs (ordered by each community's smallest member NodeId) and the
// community→members index (members sorted by NodeId).
//
// The graphstore projection lives here, in the engine layer; the core algorithm
// has no graphstore/engine dependency, preserving the cmd→surfaces→engine→core
// layer direction.
type LouvainDetector struct{}

// Detect implements Detector via deterministic Louvain. Edge weight is
// model.Edge.Confidence(); detection is a pure function of the canonical
// post-read graph, so a graph reached incrementally and the same graph built
// from scratch produce identical communities (full-vs-incremental parity).
func (LouvainDetector) Detect(ctx context.Context, reader graphstore.Graphstore) ([]Community, error) {
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}

	// Exclude non-symbol artifact nodes (external / package / file) from the
	// community projection so they never appear as members and never skew the
	// modularity partition; drop any edge incident to an excluded node so the core
	// detector never references a node id outside the projected set (WP-14
	// follow-up E). Full-vs-incremental parity and determinism are preserved: the
	// filter is a pure function of node kind, applied identically on every run.
	ids := make([]model.NodeId, 0, len(nodes))
	kept := make(map[model.NodeId]struct{}, len(nodes))
	for _, n := range nodes {
		if isArtifactKind(n.Kind()) {
			continue
		}
		ids = append(ids, n.ID())
		kept[n.ID()] = struct{}{}
	}
	projEdges := make([]model.Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := kept[e.From()]; !ok {
			continue
		}
		if _, ok := kept[e.To()]; !ok {
			continue
		}
		projEdges = append(projEdges, e)
	}

	res := corecommunity.Detect(ids, projEdges)

	out := make([]Community, 0, len(res.Communities))
	for _, c := range res.Communities {
		key := ""
		if len(c.Members) > 0 {
			key = string(c.Members[0]) // representative NodeId (smallest), stable
		}
		out = append(out, Community{ID: c.ID, Key: key, Members: c.Members})
	}
	return out, nil
}

// Name returns the Louvain detector's stable name.
func (LouvainDetector) Name() string { return "louvain" }

// --- community→members index serialization (byte-stable) ---

type communityWire struct {
	ID      int            `json:"id"`
	Key     string         `json:"key"`
	Members []model.NodeId `json:"members"`
}

type communitiesWire struct {
	FormatVersion int             `json:"format_version"`
	Communities   []communityWire `json:"communities"`
}

const communitiesFormatVersion = 1

// SerializeCommunities renders the community→members index to canonical,
// byte-stable JSON: communities in ID order (== representative order), members
// in NodeId order, fixed field layout. Two detections of the same resulting
// graph state serialize byte-for-byte identically, which is the comparison unit
// for the determinism (AC-2) and full-vs-incremental (AC-4) gates.
func SerializeCommunities(comms []Community) ([]byte, error) {
	w := communitiesWire{
		FormatVersion: communitiesFormatVersion,
		Communities:   make([]communityWire, 0, len(comms)),
	}
	for _, c := range comms {
		members := make([]model.NodeId, len(c.Members))
		copy(members, c.Members)
		w.Communities = append(w.Communities, communityWire{ID: c.ID, Key: c.Key, Members: members})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(w); err != nil {
		return nil, fmt.Errorf("community: serialize: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
