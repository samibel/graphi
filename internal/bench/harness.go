package bench

import (
	"context"
	"crypto/sha256"
	buildinfo "debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/release"
	"github.com/samibel/graphi/surfaces/client"
)

// HarnessConfig parameterizes a benchmark run.
type HarnessConfig struct {
	FixtureDir   string   // default: <module root>/bench/fixture
	Samples      int      // default 15 (plus warmup)
	Warmup       int      // default 2 (discarded cold samples)
	BinaryTarget string   // default ./cmd/graphi/
	BinaryPath   string   // if set, skip the build; provenance is read from this external Go binary
	CGOEnabled   string   // default "0"
	BuildTags    []string // build tags for the measured binary; default release.DefaultGrammarSubsetTags
}

func (c *HarnessConfig) defaults() {
	if c.Samples == 0 {
		c.Samples = 15
	}
	if c.Warmup == 0 {
		c.Warmup = 2
	}
	if c.BinaryTarget == "" {
		c.BinaryTarget = "./cmd/graphi/"
	}
	if c.CGOEnabled == "" {
		c.CGOEnabled = "0"
	}
	// SW-057: the gated binary_size_bytes metric MUST measure the SHIPPED default
	// build, which is subset-tagged (internal/release.DefaultGrammarSubsetTags) so
	// only the registered grammar blobs are embedded — never the all-206 default
	// embed (+~24.5 MiB). A nil BuildTags defaults to the subset-tag set so the
	// budget gate enforces the corrected runtime + per-blob size model, not the
	// prohibited all-206 envelope. Pass an explicit (possibly empty) slice to
	// override (e.g. to measure the all-206 contrast).
	if c.BuildTags == nil {
		c.BuildTags = release.DefaultGrammarSubsetTags
	}
}

type preparedHarness struct {
	cfg             HarnessConfig
	fixture         string
	digest          string
	binPath         string
	builtInternally bool
	binarySize      int64
	provenance      binaryBuildProvenance
	buildContract   string
}

func prepareHarness(ctx context.Context, cfg HarnessConfig) (preparedHarness, func(), error) {
	cfg.defaults()
	cleanup := func() {}
	modRoot, err := moduleRoot()
	if err != nil {
		return preparedHarness{}, cleanup, err
	}
	fixture := cfg.FixtureDir
	if !filepath.IsAbs(fixture) {
		fixture = filepath.Join(modRoot, "bench", "fixture")
	}
	if _, err := os.Stat(fixture); err != nil {
		return preparedHarness{}, cleanup, fmt.Errorf("bench: fixture dir: %w", err)
	}
	digest, err := fixtureDigestSHA256(fixture)
	if err != nil {
		return preparedHarness{}, cleanup, err
	}

	binPath := cfg.BinaryPath
	builtInternally := binPath == ""
	if builtInternally {
		tmp, err := os.MkdirTemp("", "graphi-bench-bin-*")
		if err != nil {
			return preparedHarness{}, cleanup, err
		}
		cleanup = func() { _ = os.RemoveAll(tmp) }
		binPath = filepath.Join(tmp, "graphi")
		if out, err := buildBinary(ctx, cfg.BinaryTarget, binPath, cfg.CGOEnabled, modRoot, cfg.BuildTags); err != nil {
			cleanup()
			return preparedHarness{}, func() {}, fmt.Errorf("bench: build binary: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	info, err := os.Stat(binPath)
	if err != nil {
		cleanup()
		return preparedHarness{}, func() {}, fmt.Errorf("bench: stat binary: %w", err)
	}
	provenance, err := readBinaryBuildProvenance(binPath)
	if err != nil {
		cleanup()
		return preparedHarness{}, func() {}, fmt.Errorf("bench: read binary build provenance: %w", err)
	}
	return preparedHarness{
		cfg:             cfg,
		fixture:         fixture,
		digest:          digest,
		binPath:         binPath,
		builtInternally: builtInternally,
		binarySize:      info.Size(),
		provenance:      provenance,
		buildContract:   classifyBuildContract(cfg, provenance, builtInternally),
	}, cleanup, nil
}

func (h preparedHarness) baseMetrics() Metrics {
	return Metrics{
		BinarySizeBytes:  h.binarySize,
		BuildContract:    h.buildContract,
		BuildGoVersion:   h.provenance.goVersion,
		BuildGOOS:        h.provenance.settings["GOOS"],
		BuildGOARCH:      h.provenance.settings["GOARCH"],
		BuildGOAMD64:     h.provenance.settings["GOAMD64"],
		BuildCGOEnabled:  h.provenance.settings["CGO_ENABLED"],
		BuildVCSRevision: h.provenance.settings["vcs.revision"],
		BuildVCSModified: h.provenance.settings["vcs.modified"],
		FixtureDigest:    h.digest,
	}
}

// fixtureDigestSHA256 returns the hex sha256 of the concatenation of all fixture
// file contents (sorted by relative path), pinning the frozen workload.
func fixtureDigestSHA256(dir string) (string, error) {
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

// Run executes the full benchmark harness and returns the measured Metrics. It
// is hermetic: every store/meta dir is a temp dir removed at the end; the binary
// is built under CGO_ENABLED=0; no network I/O is performed.
func Run(ctx context.Context, cfg HarnessConfig) (Metrics, error) {
	h, cleanup, err := prepareHarness(ctx, cfg)
	if err != nil {
		return Metrics{}, err
	}
	defer cleanup()
	cfg = h.cfg
	fixture := h.fixture

	// (2) Cold-start P95 + full-index median over N samples (warmup discarded).
	coldSamples := make([]time.Duration, 0, cfg.Samples)
	idxSamples := make([]time.Duration, 0, cfg.Samples)
	for i := 0; i < cfg.Warmup+cfg.Samples; i++ {
		cold, idx, mErr := oneColdStart(ctx, fixture)
		if mErr != nil {
			return Metrics{}, fmt.Errorf("bench: sample %d: %w", i, mErr)
		}
		if i >= cfg.Warmup {
			coldSamples = append(coldSamples, cold)
			idxSamples = append(idxSamples, idx)
		}
	}

	// (3) Freshness lag: hot-index incremental update + query round-trip.
	fresh, err := measureFreshness(ctx, fixture)
	if err != nil {
		return Metrics{}, fmt.Errorf("bench: freshness: %w", err)
	}

	// (4) P4 / TODO-19 incremental-indexing suite (ten-file change, simulated
	// branch switch, named query latencies, heap footprint, MCP startup over
	// the measured binary). MCP startup is skipped for external binaries —
	// their MCP capability is unverified, like their build contract.
	mcpBinary := h.binPath
	if !h.builtInternally {
		mcpBinary = ""
	}
	incr, err := measureIncremental(ctx, fixture, mcpBinary)
	if err != nil {
		return Metrics{}, fmt.Errorf("bench: incremental suite: %w", err)
	}

	metrics := h.baseMetrics()
	metrics.ColdStartP95MS = ms(P95(coldSamples))
	metrics.FullIndexMS = ms(Median(idxSamples))
	metrics.FreshnessLagMS = ms(fresh)
	metrics.IncrementalTenFileMS = incr.TenFileMS
	metrics.BranchSwitchSimMS = incr.BranchSwitchMS
	metrics.MCPStartupMS = incr.MCPStartupMS
	metrics.SymbolLookupMS = incr.SymbolLookupMS
	metrics.CallersQueryMS = incr.CallersQueryMS
	metrics.ContextQueryMS = incr.ContextQueryMS
	metrics.IndexHeapAllocBytes = incr.HeapAllocBytes
	metrics.Samples = cfg.Samples
	metrics.ProfileMetrics = measureProfileMetrics(ctx, fixture, true)
	return metrics, nil
}

// RunEnvironmentIndependent measures only canonical binary size plus profile
// database sizes and edge counts. It performs no wall-clock or live-heap
// measurement; the dedicated benchmark job remains the sole timing authority.
func RunEnvironmentIndependent(ctx context.Context, cfg HarnessConfig) (Metrics, error) {
	h, cleanup, err := prepareHarness(ctx, cfg)
	if err != nil {
		return Metrics{}, err
	}
	defer cleanup()
	metrics := h.baseMetrics()
	metrics.ProfileMetrics = measureProfileMetrics(ctx, h.fixture, false)
	return metrics, nil
}

// measureProfileMetrics indexes the fixture once per profile and always
// collects DB size and edge count. The full benchmark additionally collects
// index and query timings. It is best-effort: any individual profile failure
// is logged and skipped so the overall bench run is not aborted.
func measureProfileMetrics(ctx context.Context, fixture string, includeTimings bool) map[string]ProfileMetric {
	out := make(map[string]ProfileMetric)
	for _, p := range []profile.Profile{profile.Fast, profile.Balanced, profile.Deep} {
		pm, err := measureOneProfile(ctx, fixture, p, includeTimings)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: profile %s metrics skipped: %v\n", p, err)
			continue
		}
		out[string(p)] = pm
	}
	return out
}

func measureOneProfile(ctx context.Context, fixture string, p profile.Profile, includeTimings bool) (ProfileMetric, error) {
	tmp, err := os.MkdirTemp("", "graphi-bench-profile-*")
	if err != nil {
		return ProfileMetric{}, err
	}
	defer os.RemoveAll(tmp)

	dbPath := filepath.Join(tmp, "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return ProfileMetric{}, err
	}
	defer store.Close()

	ing, err := ingest.New(store, parse.NewDefaultRegistry(), filepath.Join(tmp, "meta"))
	if err != nil {
		return ProfileMetric{}, err
	}
	defer ing.Close()
	ing.WithProfile(p)

	var ti0 time.Time
	if includeTimings {
		ti0 = time.Now()
	}
	if err := ing.IngestAll(ctx, fixture); err != nil {
		return ProfileMetric{}, err
	}
	var index, queryLatency time.Duration
	if includeTimings {
		index = time.Since(ti0)
		c := client.NewDirect(query.New(store), search.New(store))
		q0 := time.Now()
		if _, err := c.Query(ctx, "callers", "", 0); err != nil {
			return ProfileMetric{}, err
		}
		queryLatency = time.Since(q0)
	}

	edges, err := store.Edges(ctx, graphstore.Query{})
	if err != nil {
		return ProfileMetric{}, err
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		return ProfileMetric{}, err
	}

	return ProfileMetric{
		IndexMS:        ms(index),
		DBSizeBytes:    info.Size(),
		EdgeCount:      int64(len(edges)),
		QueryLatencyMS: ms(queryLatency),
	}, nil
}

// oneColdStart measures a single cold start: open a fresh durable store, build
// the ingester, fully ingest the fixture, wire the engine services, and serve
// the first query. Returns (cold-start duration, full-index duration).
func oneColdStart(ctx context.Context, fixture string) (cold, index time.Duration, err error) {
	tmp, err := os.MkdirTemp("", "graphi-bench-cold-*")
	if err != nil {
		return 0, 0, err
	}
	defer os.RemoveAll(tmp)

	dbPath := filepath.Join(tmp, "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return 0, 0, fmt.Errorf("open store: %w", err)
	}
	defer store.Close()

	t0 := time.Now()
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), filepath.Join(tmp, "meta"))
	if err != nil {
		return 0, 0, fmt.Errorf("new ingester: %w", err)
	}
	defer ing.Close()

	ti0 := time.Now()
	if err := ing.IngestAll(ctx, fixture); err != nil {
		return 0, 0, fmt.Errorf("ingest all: %w", err)
	}
	index = time.Since(ti0)

	c := client.NewDirect(query.New(store), search.New(store))
	// First served query at the engine boundary (exercises the query service
	// path; an empty symbol yields no result but completes the round-trip).
	if _, qerr := c.Query(ctx, "callers", "", 0); qerr != nil {
		return 0, 0, fmt.Errorf("first query: %w", qerr)
	}
	cold = time.Since(t0)
	return cold, index, nil
}

// measureFreshness measures the hot-index freshness lag: with a hot index, the
// latency from initiating an incremental update (IngestChanged) to a subsequent
// query completing. The current Go/JSON parsers return AST roots but do not yet
// populate graph nodes, so reflection is measured at the hot-index absorption +
// query round-trip level — the real propagation path that exists today. Once a
// future extraction pass populates nodes, the same harness measures end-to-end
// reflection with no structural change.
func measureFreshness(ctx context.Context, srcFixture string) (time.Duration, error) {
	tmp, err := os.MkdirTemp("", "graphi-bench-fresh-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(tmp)
	fixtureCopy := filepath.Join(tmp, "fixture")
	if err := copyDir(srcFixture, fixtureCopy); err != nil {
		return 0, err
	}
	dbPath := filepath.Join(tmp, "graph.db")
	store, err := graphstore.OpenSQLite(dbPath)
	if err != nil {
		return 0, err
	}
	defer store.Close()
	ing, err := ingest.New(store, parse.NewDefaultRegistry(), filepath.Join(tmp, "meta"))
	if err != nil {
		return 0, err
	}
	defer ing.Close()
	if err := ing.IngestAll(ctx, fixtureCopy); err != nil {
		return 0, err
	}

	// Mutate one fixture file so IngestChanged has real work to absorb.
	target := filepath.Join(fixtureCopy, "src", "beta.go")
	orig, err := os.ReadFile(target)
	if err != nil {
		return 0, err
	}
	probe := append(orig, []byte("\n\n// BenchFreshnessProbe is appended by the benchmark harness.\nfunc BenchFreshnessProbe() int { return 1 }\n")...)
	if err := os.WriteFile(target, probe, 0o644); err != nil {
		return 0, err
	}

	c := client.NewDirect(query.New(store), search.New(store))
	t0 := time.Now()
	if err := ing.IngestChanged(ctx, fixtureCopy, []string{filepath.Join("src", "beta.go")}); err != nil {
		return 0, fmt.Errorf("ingest changed: %w", err)
	}
	if _, err := c.Search(ctx, "benchfreshnessprobe", 10); err != nil {
		return 0, fmt.Errorf("freshness search: %w", err)
	}
	return time.Since(t0), nil
}

// copyDir recursively copies src into dst (which must not exist / be empty).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, in, 0o644)
	})
}

func buildBinary(ctx context.Context, target, out, cgo, modRoot string, tags []string) ([]byte, error) {
	if cgo != "0" {
		return nil, fmt.Errorf("bench: canonical release build requires CGO_ENABLED=0, got %q", cgo)
	}
	err := release.BuildInModule(ctx, release.BuildConfig{
		Target:  target,
		Version: "dev",
		Tags:    tags,
	}, out, modRoot)
	return nil, err
}

type binaryBuildProvenance struct {
	path      string
	goVersion string
	settings  map[string]string
}

func readBinaryBuildProvenance(path string) (binaryBuildProvenance, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return binaryBuildProvenance{}, err
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return binaryBuildProvenance{path: info.Path, goVersion: info.GoVersion, settings: settings}, nil
}

func classifyBuildContract(cfg HarnessConfig, provenance binaryBuildProvenance, builtInternally bool) string {
	if !builtInternally {
		return ExternalBinaryBuildContract
	}
	settings := provenance.settings
	if filepath.Clean(cfg.BinaryTarget) != filepath.Clean("./cmd/graphi/") ||
		!slices.Equal(cfg.BuildTags, release.DefaultGrammarSubsetTags) ||
		provenance.path != "github.com/samibel/graphi/cmd/graphi" ||
		provenance.goVersion == "" ||
		settings["-trimpath"] != "true" ||
		settings["-tags"] != strings.Join(release.DefaultGrammarSubsetTags, ",") ||
		settings["CGO_ENABLED"] != "0" ||
		settings["GOOS"] == "" || settings["GOARCH"] == "" {
		return CustomBuildContract
	}
	return release.CanonicalBuildContract
}

func withCgo(env []string, cgo string) []string {
	prefix := "CGO_ENABLED="
	out := make([]string, 0, len(env)+1)
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			out = append(out, prefix+cgo)
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		out = append(out, prefix+cgo)
	}
	return out
}

// moduleRoot resolves the module root once via `go env GOMOD` and caches it.
var moduleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})
