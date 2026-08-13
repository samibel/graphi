package bench

// P4 / TODO-19: the incremental-indexing benchmark suite. Everything here
// measures the frozen bench/fixture workload — mutations happen ONLY in a
// runtime copy, so the pinned fixture digest is untouched. No fantasy numbers
// anywhere: these measurements feed the same budget gate and bench-report.json
// artifact as the original four metrics, so every published number is
// CI-produced.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
)

// querySamples is the sample count for the fast per-query metrics (median).
const querySamples = 5

// IncrementalMetrics carries the TODO-19 measurements merged into Metrics.
type IncrementalMetrics struct {
	TenFileMS      float64
	BranchSwitchMS float64
	MCPStartupMS   float64
	SymbolLookupMS float64
	CallersQueryMS float64
	ContextQueryMS float64
	HeapAllocBytes int64
}

// measureIncremental runs the incremental-indexing suite over a fixture copy:
//
//  1. full index of the copy + post-GC heap footprint;
//  2. 10-file change (4 modified + 6 created) → IngestChanged + query
//     round-trip;
//  3. simulated branch switch (2 modified + 2 added + 1 deleted — the shape of
//     a checkout delta) → IngestChanged + query round-trip;
//  4. named query latencies on the hot store: lexical symbol lookup, callers
//     on a resolved node id, and the task_context one-call bundle (medians);
//  5. MCP startup: spawn the measured binary with `mcp -db` against the built
//     store and time spawn → initialize response (median).
func measureIncremental(ctx context.Context, srcFixture, binPath string) (IncrementalMetrics, error) {
	var out IncrementalMetrics

	tmp, err := os.MkdirTemp("", "graphi-bench-incr-*")
	if err != nil {
		return out, err
	}
	defer os.RemoveAll(tmp)
	fixtureCopy := filepath.Join(tmp, "fixture")
	if err := copyDir(srcFixture, fixtureCopy); err != nil {
		return out, err
	}

	dbPath := filepath.Join(tmp, "graph.db")
	metaDir := filepath.Join(tmp, "meta")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return out, err
	}
	defer store.Close()
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), metaDir)
	if err != nil {
		return out, err
	}
	defer ing.Close()
	if err := ing.IngestAll(ctx, fixtureCopy); err != nil {
		return out, fmt.Errorf("incremental: full index: %w", err)
	}

	// (1) Post-GC heap after the full index: the resident graph + caches.
	runtime.GC()
	var ms0 runtime.MemStats
	runtime.ReadMemStats(&ms0)
	out.HeapAllocBytes = int64(ms0.HeapAlloc)

	c := client.NewDirect(query.New(store), search.New(store))
	ssvc := search.New(store)

	// (2) Ten-file change: modify every original source file and create new
	// ones until ten paths changed, then absorb + prove visibility.
	changed, probeName, err := mutateTenFiles(fixtureCopy)
	if err != nil {
		return out, err
	}
	t0 := time.Now()
	if err := ing.IngestChanged(ctx, fixtureCopy, changed); err != nil {
		return out, fmt.Errorf("incremental: ten-file IngestChanged: %w", err)
	}
	if _, err := c.Search(ctx, probeName, 10); err != nil {
		return out, fmt.Errorf("incremental: ten-file search: %w", err)
	}
	out.TenFileMS = ms(time.Since(t0))

	// (3) Simulated branch switch: a checkout delta touches, adds, AND
	// removes files. IngestChanged treats the list as a hint and walks the
	// root, so the deletion exercises the same drift-absorption path.
	switched, switchProbe, err := mutateBranchSwitch(fixtureCopy)
	if err != nil {
		return out, err
	}
	t1 := time.Now()
	if err := ing.IngestChanged(ctx, fixtureCopy, switched); err != nil {
		return out, fmt.Errorf("incremental: branch-switch IngestChanged: %w", err)
	}
	if _, err := c.Search(ctx, switchProbe, 10); err != nil {
		return out, fmt.Errorf("incremental: branch-switch search: %w", err)
	}
	out.BranchSwitchMS = ms(time.Since(t1))

	// (4) Named query latencies on the hot store (medians over querySamples).
	resp, err := ssvc.Search(ctx, "delta", 5)
	if err != nil || len(resp.Matches) == 0 {
		return out, fmt.Errorf("incremental: resolve fixture symbol: %v (matches %d)", err, len(resp.Matches))
	}
	nodeID := resp.Matches[0].NodeID

	out.SymbolLookupMS = ms(medianOf(querySamples, func() error {
		_, err := c.Search(ctx, "delta", 10)
		return err
	}, &err))
	if err != nil {
		return out, fmt.Errorf("incremental: symbol lookup: %w", err)
	}
	out.CallersQueryMS = ms(medianOf(querySamples, func() error {
		_, err := c.Query(ctx, "callers", nodeID, 0)
		return err
	}, &err))
	if err != nil {
		return out, fmt.Errorf("incremental: callers query: %w", err)
	}
	out.ContextQueryMS = ms(medianOf(querySamples, func() error {
		_, err := c.TaskContext(ctx, client.TaskContextParams{Task: "delta"})
		return err
	}, &err))
	if err != nil {
		return out, fmt.Errorf("incremental: context query: %w", err)
	}

	// (5) MCP startup over the measured binary — only when the harness built
	// the binary itself (an EXTERNAL binary's MCP capability is unverified,
	// matching the external-binary/unverified build contract; the metric is
	// then omitted from Map() rather than reported as a fake zero).
	if binPath == "" {
		return out, nil
	}
	// Close the store first so the spawned server owns the SQLite file.
	if err := store.Close(); err != nil {
		return out, err
	}
	startup, err := measureMCPStartup(ctx, binPath, dbPath, metaDir)
	if err != nil {
		return out, err
	}
	out.MCPStartupMS = ms(startup)
	return out, nil
}

// medianOf runs fn n times, records each duration, and returns the median.
// The first error aborts and is reported through errOut.
func medianOf(n int, fn func() error, errOut *error) time.Duration {
	samples := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		t0 := time.Now()
		if err := fn(); err != nil {
			*errOut = err
			return 0
		}
		samples = append(samples, time.Since(t0))
	}
	*errOut = nil
	return Median(samples)
}

// mutateTenFiles modifies every .go source in the copy and creates new files
// until exactly ten repo-relative paths changed. Returns the changed paths and
// a probe symbol name proven visible after absorption.
func mutateTenFiles(fixtureCopy string) ([]string, string, error) {
	srcDir := filepath.Join(fixtureCopy, "src")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, "", err
	}
	var changed []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		p := filepath.Join(srcDir, e.Name())
		orig, err := os.ReadFile(p)
		if err != nil {
			return nil, "", err
		}
		stem := strings.TrimSuffix(e.Name(), ".go")
		probe := append(orig, []byte(fmt.Sprintf("\n\n// BenchTenFileProbe appended by the incremental harness.\nfunc benchTenFileProbe_%s() int { return 1 }\n", stem))...)
		if err := os.WriteFile(p, probe, 0o644); err != nil {
			return nil, "", err
		}
		changed = append(changed, filepath.Join("src", e.Name()))
	}
	probeName := ""
	for i := len(changed); i < 10; i++ {
		name := fmt.Sprintf("bench_incr_%d.go", i)
		symbol := fmt.Sprintf("BenchIncrTen%d", i)
		body := fmt.Sprintf("//go:build ignore\n\npackage fixture\n\nfunc %s() int { return %d }\n", symbol, i)
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(body), 0o644); err != nil {
			return nil, "", err
		}
		changed = append(changed, filepath.Join("src", name))
		probeName = symbol
	}
	if len(changed) != 10 {
		return nil, "", fmt.Errorf("incremental: fixture yields %d changed paths, want 10 — re-tune mutateTenFiles", len(changed))
	}
	return changed, probeName, nil
}

// mutateBranchSwitch applies a checkout-shaped delta on top of the ten-file
// state: modify two originals, add two files, delete one of the generated
// files. Returns the touched repo-relative paths (including the deleted one)
// and a probe symbol from an added file.
func mutateBranchSwitch(fixtureCopy string) ([]string, string, error) {
	srcDir := filepath.Join(fixtureCopy, "src")
	var touched []string
	for i, name := range []string{"alpha.go", "beta.go"} {
		p := filepath.Join(srcDir, name)
		orig, err := os.ReadFile(p)
		if err != nil {
			return nil, "", err
		}
		probe := append(orig, []byte(fmt.Sprintf("\n\nfunc benchSwitchTouch%d() int { return %d }\n", i, i))...)
		if err := os.WriteFile(p, probe, 0o644); err != nil {
			return nil, "", err
		}
		touched = append(touched, filepath.Join("src", name))
	}
	probeName := ""
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("bench_switch_%d.go", i)
		symbol := fmt.Sprintf("BenchSwitchAdd%d", i)
		body := fmt.Sprintf("//go:build ignore\n\npackage fixture\n\nfunc %s() int { return %d }\n", symbol, i)
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(body), 0o644); err != nil {
			return nil, "", err
		}
		touched = append(touched, filepath.Join("src", name))
		probeName = symbol
	}
	deleted := filepath.Join(srcDir, "bench_incr_9.go")
	if err := os.Remove(deleted); err != nil {
		return nil, "", err
	}
	touched = append(touched, filepath.Join("src", "bench_incr_9.go"))
	return touched, probeName, nil
}

// measureMCPStartup spawns `<binary> mcp -db <db> -meta <meta>`, sends an MCP
// initialize request, and measures spawn → response for querySamples runs
// (median). This is the real agent-visible startup: process launch, store
// open, and the first JSON-RPC round-trip.
func measureMCPStartup(ctx context.Context, binPath, dbPath, metaDir string) (time.Duration, error) {
	samples := make([]time.Duration, 0, querySamples)
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"bench","version":"0"}}}` + "\n"
	for i := 0; i < querySamples; i++ {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(cctx, binPath, "mcp", "-db", dbPath, "-meta", metaDir)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			return 0, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			return 0, err
		}
		cmd.Stderr = io.Discard
		t0 := time.Now()
		if err := cmd.Start(); err != nil {
			cancel()
			return 0, fmt.Errorf("mcp startup: start: %w", err)
		}
		if _, err := io.WriteString(stdin, initReq); err != nil {
			_ = cmd.Process.Kill()
			cancel()
			return 0, fmt.Errorf("mcp startup: write: %w", err)
		}
		line, err := bufio.NewReader(stdout).ReadBytes('\n')
		elapsed := time.Since(t0)
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		cancel()
		if err != nil {
			return 0, fmt.Errorf("mcp startup: read response: %w", err)
		}
		var resp struct {
			ID any `json:"id"`
		}
		if jerr := json.Unmarshal(line, &resp); jerr != nil || resp.ID == nil {
			return 0, fmt.Errorf("mcp startup: unexpected response %q (%v)", strings.TrimSpace(string(line)), jerr)
		}
		samples = append(samples, elapsed)
	}
	return Median(samples), nil
}
