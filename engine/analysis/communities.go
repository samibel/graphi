package analysis

// SW-104 (EP-017 capstone): surface the SW-103 community-detection capability as
// the canonical `communities` operation behind the ONE dispatch table. The
// analyzer holds no detection logic — it consumes engine/community's Detector
// (the grouping seam) over the read-only graph and maps the result into the
// canonical envelope. Determinism is enforced at the encoder boundary
// (serialize.go sortCommunities): communities by deterministic id, members by
// node id. No analysis logic lives in surfaces.

import (
	"context"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/community"
	"github.com/samibel/graphi/engine/query"
)

// CommunitiesAnalyzerName is the canonical dispatch key for the community
// detection operation (kebab-case, like the other operation keys).
const CommunitiesAnalyzerName = "communities"

// CommunitiesReport is the canonical, byte-stable envelope payload for the
// `communities` operation. Communities are ordered by their deterministic id and
// members by node id at the encoder, so the same resulting graph state serializes
// identically regardless of the path (full vs incremental parallel parse) taken
// to reach it.
type CommunitiesReport struct {
	Detector    string           `json:"detector"`
	Communities []CommunityEntry `json:"communities"`
}

// CommunityEntry is one detected community: its stable id, representative key,
// and member node ids (sorted by node id at the encoder).
type CommunityEntry struct {
	ID      int      `json:"id"`
	Key     string   `json:"key"`
	Members []string `json:"members"`
}

// communitiesAnalyzer routes the `communities` operation to engine/community's
// Detector. It is stateless per call and safe for concurrent use.
type communitiesAnalyzer struct {
	detector community.Detector
}

// Name implements Analyzer.
func (communitiesAnalyzer) Name() string { return CommunitiesAnalyzerName }

// Analyze runs community detection over the read-only graph and maps the result
// into the canonical envelope. The dispatch Reader is the graphstore the service
// was constructed over (graphstore.Graphstore satisfies query.Reader); when it is
// not a full graphstore (no graph), an empty report is returned (never an error).
func (a communitiesAnalyzer) Analyze(ctx context.Context, r query.Reader, p Params) (Analysis, error) {
	d := a.detector
	if d == nil {
		d = community.DefaultDetector()
	}
	report := &CommunitiesReport{Detector: d.Name(), Communities: []CommunityEntry{}}
	if gs, ok := r.(graphstore.Graphstore); ok {
		comms, err := d.Detect(ctx, gs)
		if err != nil {
			return Analysis{}, err
		}
		for _, c := range comms {
			members := make([]string, len(c.Members))
			for i, m := range c.Members {
				members[i] = string(m)
			}
			report.Communities = append(report.Communities, CommunityEntry{
				ID:      c.ID,
				Key:     c.Key,
				Members: members,
			})
		}
	}
	outcome := query.OutcomeFound
	if len(report.Communities) == 0 {
		outcome = query.OutcomeEmpty
	}
	return Analysis{
		Analyzer:    CommunitiesAnalyzerName,
		Outcome:     outcome,
		Symbol:      p.Symbol,
		Communities: report,
	}, nil
}
