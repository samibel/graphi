package githistory

import (
	"context"
	"time"
)

// AnalyzerName is the dispatch key for the git-history analyzer in the
// analysis registry. It mirrors the taint.AnalyzerName pattern.
const AnalyzerName = "git-history"

// DefaultMaxCommits is the default upper bound on commits traversed.
const DefaultMaxCommits = 1000

// DefaultMaxAge is the default maximum age window (6 months).
const DefaultMaxAge = 180 * 24 * time.Hour

// DefaultBusFactorThreshold is the inclusive upper bound of unique authors that
// is considered risky. 1 = single author = risky.
const DefaultBusFactorThreshold = 1

// DefaultMinCoCommits is the minimum number of co-occurring commits for a file
// pair to be reported as a co-change group.
const DefaultMinCoCommits = 2

// Config holds the tunable parameters for the git-history analyzer.
type Config struct {
	// MaxCommits bounds the number of commits traversed (0 = DefaultMaxCommits).
	MaxCommits int
	// MaxAge bounds the age of commits considered. Zero means DefaultMaxAge.
	MaxAge time.Duration
	// BusFactorThreshold is the inclusive upper bound of unique authors considered
	// risky. Zero means DefaultBusFactorThreshold (1).
	BusFactorThreshold int
	// MinCoCommits is the minimum co-occurrence count for a file pair to be
	// reported. Zero means DefaultMinCoCommits (2).
	MinCoCommits int
	// Now overrides the reference time for age computation. Zero means
	// time.Now() at analysis start. Exposed for deterministic testing.
	Now time.Time
}

// effective returns a Config with zero-valued fields replaced by defaults.
func (c Config) effective() Config {
	if c.MaxCommits <= 0 {
		c.MaxCommits = DefaultMaxCommits
	}
	if c.MaxAge <= 0 {
		c.MaxAge = DefaultMaxAge
	}
	if c.BusFactorThreshold <= 0 {
		c.BusFactorThreshold = DefaultBusFactorThreshold
	}
	if c.MinCoCommits <= 0 {
		c.MinCoCommits = DefaultMinCoCommits
	}
	if c.Now.IsZero() {
		c.Now = time.Now()
	}
	return c
}

// GitHistoryResult is the complete output of a git-history analysis run.
type GitHistoryResult struct {
	// ChurnScores holds per-file churn signals sorted by path.
	ChurnScores []ChurnScore `json:"churn_scores"`
	// BusFactors holds per-file bus-factor signals sorted by path.
	BusFactors []BusFactor `json:"bus_factors"`
	// CoChangeGroups holds file-pair co-change groups sorted by files.
	CoChangeGroups []CoChangeGroup `json:"co_change_groups"`
	// Diagnostics carries human-readable warnings or informational messages
	// (e.g. "window truncated by max-commits before max-age reached").
	Diagnostics []string `json:"diagnostics,omitempty"`
}

// Analyzer is the git-history signal analyzer. It is exported so the parent
// analysis package can wrap it with a thin adapter for registry dispatch,
// avoiding an import cycle (githistory cannot import analysis).
type Analyzer struct {
	provider GitProvider
	config   Config
}

// New creates a git-history Analyzer with the given provider and config.
// Pass nil for provider to create a no-op analyzer that returns empty results.
func New(provider GitProvider, cfg Config) *Analyzer {
	return &Analyzer{provider: provider, config: cfg}
}

// Name returns the analyzer dispatch key.
func (a *Analyzer) Name() string { return AnalyzerName }

// Run executes the full git-history analysis: fetches commits from the
// provider within the bounded window, then computes churn, bus-factor, and
// co-change signals.
func (a *Analyzer) Run(ctx context.Context) (GitHistoryResult, error) {
	// Nil provider returns empty results gracefully (used when the analyzer is
	// registered in the default service but no git repo is available).
	if a.provider == nil {
		return GitHistoryResult{
			ChurnScores:    []ChurnScore{},
			BusFactors:     []BusFactor{},
			CoChangeGroups: []CoChangeGroup{},
			Diagnostics:    []string{"no git provider configured"},
		}, nil
	}

	cfg := a.config.effective()

	// Compute the "since" cutoff from cfg.Now - cfg.MaxAge.
	since := cfg.Now.Add(-cfg.MaxAge)

	commits, err := a.provider.Log(ctx, cfg.MaxCommits, since)
	if err != nil {
		return GitHistoryResult{}, err
	}

	var diags []string
	if len(commits) == 0 {
		return GitHistoryResult{
			ChurnScores:    []ChurnScore{},
			BusFactors:     []BusFactor{},
			CoChangeGroups: []CoChangeGroup{},
			Diagnostics:    []string{"no commits in window"},
		}, nil
	}

	// Check if we hit the commit cap before the age boundary.
	if len(commits) >= cfg.MaxCommits {
		oldest := commits[len(commits)-1]
		if oldest.Timestamp.After(since) {
			diags = append(diags, "window truncated by max-commits before max-age reached")
		}
	}

	churn := computeChurn(commits)
	bus := computeBusFactors(commits, cfg.BusFactorThreshold)
	cochange := computeCoChangeGroups(commits, cfg.MinCoCommits)

	return GitHistoryResult{
		ChurnScores:    churn,
		BusFactors:     bus,
		CoChangeGroups: cochange,
		Diagnostics:    diags,
	}, nil
}
