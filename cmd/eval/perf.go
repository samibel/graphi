package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/explain"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/evalreport"
)

// Tier-1 performance budgets, from the performance-scale PRD's small-repo
// targets (index < 10 s, query < 100 ms) plus the agent-first response budget
// for MCP tools and a bounded incremental-update target. These gate the tiny
// pinned fixture, so they are deliberately strict and deterministic; the
// larger tier-2/3 trend runs live in the bench harness and the nightly
// workflow, not here.
const (
	budgetIndexMS       = 10_000
	budgetDBSizeBytes   = 5 * 1024 * 1024
	budgetQueryMS       = 100
	budgetMCPToolMS     = 500
	budgetIncrementalMS = 5_000
)

// perfFixture is the pinned tier-1 fixture the budgets run against.
const perfFixture = "corpus/fixtures/go"

// measurePerformance runs the tier-1 performance budget checks: full index
// time into a real SQLite store, resulting DB size, median structural-query
// latency, median agent-tool (explain_symbol) latency, and incremental-update
// latency. Score = passed checks / total checks * 100, with every measured
// value recorded in the report.
func measurePerformance() (evalreport.PerfMetrics, error) {
	return measurePerformanceAt(perfFixture)
}

// measurePerformanceAt is measurePerformance with an explicit fixture path
// (tests run from a different working directory).
func measurePerformanceAt(fixtureDir string) (evalreport.PerfMetrics, error) {
	ctx := context.Background()
	var m evalreport.PerfMetrics

	tmp, err := os.MkdirTemp("", "graphi-eval-perf-*")
	if err != nil {
		return m, err
	}
	defer os.RemoveAll(tmp)

	// Copy the fixture so the incremental-update step can touch a file
	// without dirtying the checkout.
	work := filepath.Join(tmp, "fixture")
	if err := copyTree(fixtureDir, work); err != nil {
		return m, fmt.Errorf("copy fixture: %w", err)
	}

	dbPath := filepath.Join(tmp, "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return m, err
	}
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), "")
	if err != nil {
		_ = store.Close()
		return m, err
	}

	// 1. Full index time.
	start := time.Now()
	if err := ing.IngestAll(ctx, work); err != nil {
		_ = store.Close()
		return m, fmt.Errorf("index: %w", err)
	}
	indexMS := float64(time.Since(start).Milliseconds())
	m.Checks = append(m.Checks, check("index_time", indexMS, budgetIndexMS, "ms"))

	// 2. DB size after checkpoint (the WAL folded back into the main file).
	_ = store.WALCheckpoint(ctx, "TRUNCATE")
	dbSize := 0.0
	if fi, err := os.Stat(dbPath); err == nil {
		dbSize = float64(fi.Size())
	}
	m.Checks = append(m.Checks, check("db_size", dbSize, budgetDBSizeBytes, "bytes"))

	deps := resolve.Deps{Query: query.New(store), Search: search.New(store)}

	// 3. Median structural-query latency (search, the always-available op).
	queryMS, err := medianLatencyMS(7, func() error {
		_, err := deps.Search.Search(ctx, "Hello", 10)
		return err
	})
	if err != nil {
		_ = store.Close()
		return m, fmt.Errorf("query latency: %w", err)
	}
	m.Checks = append(m.Checks, check("query_latency_median", queryMS, budgetQueryMS, "ms"))

	// 4. Median MCP agent-tool latency (explain_symbol end to end).
	toolMS, err := medianLatencyMS(7, func() error {
		_, err := explain.Explain(ctx, deps, "Hello", 10)
		return err
	})
	if err != nil {
		_ = store.Close()
		return m, fmt.Errorf("tool latency: %w", err)
	}
	m.Checks = append(m.Checks, check("mcp_tool_latency_median", toolMS, budgetMCPToolMS, "ms"))

	// 5. Incremental update latency: touch one file, re-ingest it.
	target := filepath.Join(work, "sample.go")
	src, err := os.ReadFile(target)
	if err != nil {
		_ = store.Close()
		return m, err
	}
	if err := os.WriteFile(target, append(src, []byte("\n// touched by eval perf check\n")...), 0o644); err != nil {
		_ = store.Close()
		return m, err
	}
	start = time.Now()
	if err := ing.IngestChanged(ctx, work, []string{"sample.go"}); err != nil {
		_ = store.Close()
		return m, fmt.Errorf("incremental: %w", err)
	}
	incrementalMS := float64(time.Since(start).Milliseconds())
	m.Checks = append(m.Checks, check("incremental_update", incrementalMS, budgetIncrementalMS, "ms"))

	_ = ing.Close()
	_ = store.Close()

	passed := 0
	for _, c := range m.Checks {
		if c.Pass {
			passed++
		}
	}
	m.Score = float64(passed) / float64(len(m.Checks)) * 100
	return m, nil
}

func check(name string, measured, budget float64, unit string) evalreport.PerfCheck {
	return evalreport.PerfCheck{
		Name:     name,
		Measured: measured,
		Budget:   budget,
		Unit:     unit,
		Pass:     measured <= budget,
	}
}

// medianLatencyMS runs fn n times and returns the median duration in ms.
func medianLatencyMS(n int, fn func() error) (float64, error) {
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		if err := fn(); err != nil {
			return 0, err
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	med := samples[len(samples)/2]
	return float64(med.Microseconds()) / 1000.0, nil
}

// copyTree copies a directory tree (regular files only — the pinned fixtures
// contain no symlinks).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, info.Mode().Perm())
	})
}
