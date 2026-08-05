package perfbaseline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/release"
	"github.com/samibel/graphi/surfaces/client"
)

// warmQueryOps are the read operations measured warm. They are the interactive
// path — the one an application service inserts a contract-mapping step into —
// and the reason the PRD budgets warm-query p95 separately from full index.
var warmQueryOps = []string{"definition", "callers", "callees", "references", "neighborhood"}

// neighborhoodDepth is fixed so the measured work cannot drift with a default.
const neighborhoodDepth = 2

// trustFixtureName describes the purpose-built trust workload in the artifact.
const trustFixtureName = "perfbaseline/trust-module (generated: 3-file Go module, one confirmed cross-package call)"

// trustPolicy is the policy the timed trust report evaluates under. review-v1 is
// the PR-review policy — the realistic caller — so the measurement covers scope
// resolution and policy assessment, not just snapshot reading.
const trustPolicy = "review-v1"

// Measure runs the full baseline and returns the recorded report.
func Measure(ctx context.Context, cfg Config) (Report, error) {
	cfg.defaults()
	if cfg.Commit == "" {
		return Report{}, fmt.Errorf("perfbaseline: a commit is required (a measurement that cannot name what it measured cannot be compared)")
	}
	if cfg.Samples < MinSamples {
		return Report{}, fmt.Errorf("perfbaseline: %d samples is below the %d-sample floor", cfg.Samples, MinSamples)
	}

	moduleRoot, err := ModuleRoot()
	if err != nil {
		return Report{}, err
	}
	fixture := cfg.FixtureDir
	if fixture == "" {
		fixture = filepath.Join(moduleRoot, "bench", "fixture")
	}
	if _, err := os.Stat(fixture); err != nil {
		return Report{}, fmt.Errorf("perfbaseline: fixture dir: %w", err)
	}
	digest, err := FixtureDigest(fixture)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		SchemaVersion: SchemaVersion,
		Commit:        cfg.Commit,
		Toolchain:     toolchain(),
		FixtureDigest: digest,
		TrustFixture:  trustFixtureName,
		Samples:       cfg.Samples,
		Warmup:        cfg.Warmup,
		Environment:   environment(),
	}

	indexSamples, err := measureFullIndex(ctx, fixture, cfg.Warmup+cfg.Samples)
	if err != nil {
		return Report{}, fmt.Errorf("perfbaseline: full index: %w", err)
	}
	report.FullIndex = statOf(indexSamples[cfg.Warmup:])

	warm, err := measureWarmQuery(ctx, fixture, cfg.Warmup+cfg.Samples)
	if err != nil {
		return Report{}, fmt.Errorf("perfbaseline: warm query: %w", err)
	}
	report.WarmQuery = map[string]Stat{}
	for op, samples := range warm {
		report.WarmQuery[op] = statOf(samples[cfg.Warmup:])
	}

	trustSamples, verdict, state, err := measureTrustReport(ctx, cfg.Warmup+cfg.Samples)
	if err != nil {
		return Report{}, fmt.Errorf("perfbaseline: trust report: %w", err)
	}
	report.TrustReport = statOf(trustSamples[cfg.Warmup:])
	report.TrustVerdict = string(verdict)
	report.TrustState = string(state)

	if !cfg.SkipBinarySize {
		size, contract, err := measureBinarySize(ctx, moduleRoot)
		if err != nil {
			return Report{}, fmt.Errorf("perfbaseline: binary size: %w", err)
		}
		report.BinarySizeBytes = size
		report.BuildContract = contract
	}
	return report, nil
}

// measureFullIndex times a complete IngestAll into a fresh durable store, the
// same work bench measures — but keeping every raw sample so both the median and
// the tail are recorded.
func measureFullIndex(ctx context.Context, fixture string, runs int) ([]time.Duration, error) {
	samples := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		elapsed, err := oneFullIndex(ctx, fixture)
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i, err)
		}
		samples = append(samples, elapsed)
	}
	return samples, nil
}

func oneFullIndex(ctx context.Context, fixture string) (time.Duration, error) {
	tmp, err := os.MkdirTemp("", "graphi-perfbaseline-index-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)

	store, err := graphstore.OpenSQLite(filepath.Join(tmp, "graph.db"))
	if err != nil {
		return 0, err
	}
	defer store.Close()
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), filepath.Join(tmp, "meta"))
	if err != nil {
		return 0, err
	}
	defer ing.Close()

	start := time.Now()
	if err := ing.IngestAll(ctx, fixture); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// measureWarmQuery indexes once, then times repeated reads against the warm
// store. The store is deliberately reused across samples: a cold read would
// measure the store opening, which measureFullIndex already covers.
func measureWarmQuery(ctx context.Context, fixture string, runs int) (map[string][]time.Duration, error) {
	tmp, err := os.MkdirTemp("", "graphi-perfbaseline-warm-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	store, err := graphstore.OpenSQLite(filepath.Join(tmp, "graph.db"))
	if err != nil {
		return nil, err
	}
	defer store.Close()
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), filepath.Join(tmp, "meta"))
	if err != nil {
		return nil, err
	}
	if err := ing.IngestAll(ctx, fixture); err != nil {
		_ = ing.Close()
		return nil, err
	}
	if err := ing.Close(); err != nil {
		return nil, err
	}

	symbol, err := lowestFunctionID(ctx, store)
	if err != nil {
		return nil, err
	}
	c := client.NewDirect(query.New(store), search.New(store))

	out := map[string][]time.Duration{}
	for _, op := range warmQueryOps {
		depth := 0
		if op == "neighborhood" {
			depth = neighborhoodDepth
		}
		samples := make([]time.Duration, 0, runs)
		for i := 0; i < runs; i++ {
			start := time.Now()
			if _, err := c.Query(ctx, op, symbol, depth); err != nil {
				return nil, fmt.Errorf("op %s sample %d: %w", op, i, err)
			}
			samples = append(samples, time.Since(start))
		}
		out[op] = samples
	}

	searchSamples := make([]time.Duration, 0, runs)
	for i := 0; i < runs; i++ {
		start := time.Now()
		if _, err := c.Search(ctx, "delta", 20); err != nil {
			return nil, fmt.Errorf("op search sample %d: %w", i, err)
		}
		searchSamples = append(searchSamples, time.Since(start))
	}
	out["search"] = searchSamples
	return out, nil
}

// lowestFunctionID picks the function node with the lowest canonical id, so the
// measured symbol never depends on map iteration order and stays stable as long
// as the fixture digest does.
func lowestFunctionID(ctx context.Context, store graphstore.Graphstore) (string, error) {
	nodes, err := store.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return "", err
	}
	var ids []string
	for _, n := range nodes {
		if n.Kind() == "function" {
			ids = append(ids, string(n.ID()))
		}
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("fixture produced no function nodes to query")
	}
	sort.Strings(ids)
	return ids[0], nil
}

// measureTrustReport times the P1 trust composition end to end.
//
// It builds its own fixture rather than reusing the bench corpus: the trust
// report's cost depends on observing a resolvable module, and the bench fixture
// is loose source files with no go.mod. Timing it there would measure the
// fail-closed path, which is fast, meaningless, and would quietly under-report
// the real cost the refactor has to preserve.
func measureTrustReport(ctx context.Context, runs int) ([]time.Duration, trust.Verdict, trust.State, error) {
	root, err := os.MkdirTemp("", "graphi-perfbaseline-trust-*")
	if err != nil {
		return nil, "", "", err
	}
	defer os.RemoveAll(root)

	repo := filepath.Join(root, "repo")
	files := map[string]string{
		"go.mod":       "module example.com/perfbaseline\n\ngo 1.26\n",
		"util/util.go": "package util\n\nfunc Answer() int { return 42 }\n",
		"main.go":      "package main\n\nimport \"example.com/perfbaseline/util\"\n\nfunc main() { x := util.Answer(); _ = x }\n",
	}
	for name, content := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, "", "", err
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return nil, "", "", err
		}
	}

	metaDir := filepath.Join(root, "meta")
	dbPath := filepath.Join(root, "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return nil, "", "", err
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		_ = store.Close()
		return nil, "", "", err
	}
	if err := ing.IngestAll(ctx, repo); err != nil {
		_ = ing.Close()
		_ = store.Close()
		return nil, "", "", err
	}
	if err := ing.Close(); err != nil {
		_ = store.Close()
		return nil, "", "", err
	}
	if err := store.Close(); err != nil {
		return nil, "", "", err
	}

	// The trust composition observes read-only, so it is wired without a store.
	// A policy is set on purpose: with no policy the composition skips assessment
	// and returns an empty verdict, which would time only part of the work the
	// refactor has to preserve.
	c := client.NewDirect(nil, nil)
	opts := client.TrustReportOptions{
		Root: repo, DBPath: dbPath, MetaDir: metaDir, Policy: trustPolicy,
	}

	samples := make([]time.Duration, 0, runs)
	var verdict trust.Verdict
	var state trust.State
	for i := 0; i < runs; i++ {
		start := time.Now()
		_, v, s, err := c.TrustReport(ctx, opts)
		elapsed := time.Since(start)
		if err != nil {
			return nil, "", "", fmt.Errorf("sample %d: %w", i, err)
		}
		samples = append(samples, elapsed)
		verdict, state = v, s
	}

	// Guard against silently timing the degraded path: an UNAVAILABLE state means
	// the composition found nothing to assess, so the numbers would describe a
	// no-op and the later A/B comparison would be worthless.
	if state == trust.StateUnavailable {
		return nil, "", "", fmt.Errorf("trust report returned state %q — the fixture did not produce an observable graph, so the timing would describe the fail-closed path", state)
	}
	if verdict == "" {
		return nil, "", "", fmt.Errorf("trust report under policy %q produced no verdict — the policy assessment path was not exercised, so the timing understates the real work", trustPolicy)
	}
	return samples, verdict, state, nil
}

// measureBinarySize builds the canonical release binary and stats it, reusing
// internal/release's build contract so the number is comparable with the release
// size budget rather than being some ad-hoc build.
func measureBinarySize(ctx context.Context, moduleRoot string) (int64, string, error) {
	tmp, err := os.MkdirTemp("", "graphi-perfbaseline-bin-*")
	if err != nil {
		return 0, "", err
	}
	defer os.RemoveAll(tmp)

	out := filepath.Join(tmp, "graphi")
	args := release.CanonicalBuildArgs(release.BuildConfig{
		Target:  "./cmd/graphi/",
		Version: "dev",
		Tags:    release.DefaultGrammarSubsetTags,
	}, out)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = moduleRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if combined, err := cmd.CombinedOutput(); err != nil {
		return 0, "", fmt.Errorf("build: %w (%s)", err, strings.TrimSpace(string(combined)))
	}
	info, err := os.Stat(out)
	if err != nil {
		return 0, "", err
	}
	return info.Size(), release.CanonicalBuildContract, nil
}

// FixtureDigest returns the hex sha256 over the fixture's file contents, sorted
// by relative path. It mirrors internal/bench's digest byte for byte, so a
// recorded baseline can be checked against the pinned bench workload.
func FixtureDigest(dir string) (string, error) {
	h := sha256.New()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, rel := range paths {
		b, rerr := os.ReadFile(filepath.Join(dir, rel))
		if rerr != nil {
			return "", rerr
		}
		h.Write([]byte(rel))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
