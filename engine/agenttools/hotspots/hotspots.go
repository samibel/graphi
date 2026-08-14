// Package hotspots implements the hotspots agent tool (labs, P2 git
// intelligence): the files that change constantly AND that the graph depends
// on. The score is churn × dependency centrality — "this file changed 12
// times in the window and has 43 edge endpoints" — which is a far better
// where-to-refactor signal than cyclomatic complexity alone.
//
// History comes from the surface-boundary git provider (bounded `git log`,
// engine stays exec-free); centrality comes from the compact BriefStats
// aggregate (per-file edge-endpoint counts). Integer scoring, breakdown in
// every reason. Without a provider the tool degrades to a typed unavailable
// outcome — never a guess.
package hotspots

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/analysis/githistory"
)

const tool = "hotspots"

// MethodVersion stamps the scoring logic version into the summary.
const MethodVersion = "hotspots/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandHotspot   = 9
	bandBusFactor = 3
	bandNext      = 1
)

// Section caps.
const (
	hotspotRows   = 15
	busFactorRows = 8
)

// Params carries the hotspots inputs.
type Params struct {
	// Provider is the surface-boundary git-history source. Nil degrades to
	// the typed unavailable outcome.
	Provider githistory.GitProvider
	// MaxCommits bounds the history window (0 = githistory default, 1000).
	MaxCommits int
	// MaxItems caps the item list (0 selects shape.DefaultMaxItems).
	MaxItems int
	// Now overrides the reference time for the age window (zero = wall clock;
	// tests pass a fixed time for byte determinism).
	Now time.Time
	// Deps are the shared engine services.
	Deps resolve.Deps
}

// Assemble computes the churn × centrality hotspot ranking.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}
	if p.Provider == nil {
		return &contract.Result{
			Outcome: contract.OutcomeUnavailable,
			Summary: tool + ": no git history available on this surface (attach mode or no repository root); open the session from a git repository",
			Confidence: contract.Confidence{
				Distribution: map[string]float64{"unknown": 1},
				Top:          "unknown",
				Method:       "no_git_provider",
			},
		}, nil
	}

	hist, err := githistory.New(p.Provider, githistory.Config{MaxCommits: p.MaxCommits, Now: p.Now}).Run(ctx)
	if err != nil {
		return nil, err
	}
	if len(hist.ChurnScores) == 0 {
		return &contract.Result{
			Outcome: contract.OutcomeEmpty,
			Summary: tool + ": no commits in the bounded history window",
			Confidence: contract.Confidence{
				Distribution: map[string]float64{"unknown": 1},
				Top:          "unknown",
				Method:       "empty_history",
			},
		}, nil
	}

	// Dependency centrality per file from the compact aggregate.
	endpoints := map[string]int{}
	if agg, ok := p.Deps.Query.Reader().(graphstore.BriefAggregatePort); ok {
		stats, err := agg.BriefStats(ctx, 1)
		if err != nil {
			return nil, err
		}
		for _, f := range stats.Files {
			endpoints[f.Path] = f.EdgeEndpoints
		}
	}

	// Integer composite: commits × (1 + edge endpoints). ChurnScores arrive
	// sorted by path, so accumulation order is canonical.
	type row struct {
		churn githistory.ChurnScore
		ends  int
		score int
	}
	rows := make([]row, 0, len(hist.ChurnScores))
	for _, c := range hist.ChurnScores {
		e := endpoints[c.Path]
		s := c.Commits * (1 + e)
		if s >= 1<<20 {
			s = 1<<20 - 1
		}
		rows = append(rows, row{churn: c, ends: e, score: s})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].churn.Path < rows[j].churn.Path
	})

	ev := shape.NewEvidenceSet()
	var items []contract.Item
	top := map[string]struct{}{}
	for i, r := range rows {
		if i >= hotspotRows {
			break
		}
		top[r.churn.Path] = struct{}{}
		evID := ev.Add(r.churn.Path, 0, "hotspot")
		items = append(items, contract.Item{
			RefID:          "hot:" + r.churn.Path,
			Rank:           bandHotspot<<20 + r.score,
			Reason:         fmt.Sprintf("hotspot: %s score %d [%d commit(s) in window × (1+%d edge endpoint(s)); last touched by %s]", r.churn.Path, r.score, r.churn.Commits, r.ends, r.churn.LastAuthor),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Bus-factor warnings for the ranked hotspots (BusFactors arrive sorted
	// by path).
	busFactor := 0
	for _, b := range hist.BusFactors {
		if !b.Risky {
			continue
		}
		if _, ranked := top[b.Path]; !ranked {
			continue
		}
		busFactor++
		if busFactor > busFactorRows {
			continue
		}
		evID := ev.Add(b.Path, 0, "bus_factor")
		items = append(items, contract.Item{
			RefID:          "bus:" + b.Path,
			Rank:           bandBusFactor<<20 + (busFactorRows - busFactor + 1),
			Reason:         fmt.Sprintf("bus_factor: %s — %d unique author(s) in window (%s)", b.Path, b.UniqueAuthors, joinCapped(b.Authors, 3)),
			EvidenceRefIDs: []string{evID},
		})
	}

	if len(rows) > 0 {
		items = append(items, contract.Item{
			RefID:  "next-1",
			Rank:   bandNext<<20 + 1,
			Reason: fmt.Sprintf("next: graphi change-impact %s — assess the top hotspot before touching it", rows[0].churn.Path),
		})
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("hotspots: %d churned file(s) in window — top %s (score %d), %d single-author hotspot(s) (%s)",
			len(rows), rows[0].churn.Path, rows[0].score, busFactor, MethodVersion),
		Items:    items,
		Evidence: ev.List(),
		// Churn is commit-derived fact, centrality is graph-derived fact; the
		// composite itself is a heuristic ranking and says so.
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"heuristic": 1},
			Top:          "heuristic",
			Method:       "churn_x_centrality",
		},
	}
	return shape.FinishLabs(r, p.MaxItems)
}

// joinCapped joins up to n entries, appending an ellipsis marker beyond that.
func joinCapped(vals []string, n int) string {
	if len(vals) <= n {
		out := ""
		for i, v := range vals {
			if i > 0 {
				out += ", "
			}
			out += v
		}
		return out
	}
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += ", "
		}
		out += vals[i]
	}
	return fmt.Sprintf("%s, +%d more", out, len(vals)-n)
}
