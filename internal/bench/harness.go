package bench

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
	BinaryPath   string   // if set, skip the build and stat this path
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
	cfg.defaults()
	modRoot, err := moduleRoot()
	if err != nil {
		return Metrics{}, err
	}
	fixture := cfg.FixtureDir
	if !filepath.IsAbs(fixture) {
		fixture = filepath.Join(modRoot, "bench", "fixture")
	}
	if _, err := os.Stat(fixture); err != nil {
		return Metrics{}, fmt.Errorf("bench: fixture dir: %w", err)
	}
	digest, err := fixtureDigestSHA256(fixture)
	if err != nil {
		return Metrics{}, err
	}

	// (1) Binary size: build the default binary under CGO_ENABLED=0 and stat it.
	binPath := cfg.BinaryPath
	ownedBin := ""
	if binPath == "" {
		tmp, err := os.MkdirTemp("", "graphi-bench-bin-*")
		if err != nil {
			return Metrics{}, err
		}
		defer os.RemoveAll(tmp)
		ownedBin = filepath.Join(tmp, "graphi")
		if out, err := buildBinary(ctx, cfg.BinaryTarget, ownedBin, cfg.CGOEnabled, modRoot, cfg.BuildTags); err != nil {
			return Metrics{}, fmt.Errorf("bench: build binary: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		binPath = ownedBin
	}
	info, err := os.Stat(binPath)
	if err != nil {
		return Metrics{}, fmt.Errorf("bench: stat binary: %w", err)
	}

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

	return Metrics{
		ColdStartP95MS:  ms(P95(coldSamples)),
		FullIndexMS:     ms(Median(idxSamples)),
		FreshnessLagMS:  ms(fresh),
		BinarySizeBytes: info.Size(),
		FixtureDigest:   digest,
		Samples:         cfg.Samples,
		ProfileMetrics:  measureProfileMetrics(ctx, fixture),
	}, nil
}

// measureProfileMetrics indexes the fixture once per profile and collects
// index time, DB size, edge count, and a simple query latency. It is best-effort:
// any individual profile failure is logged and skipped so the overall bench run
// is not aborted.
func measureProfileMetrics(ctx context.Context, fixture string) map[string]ProfileMetric {
	out := make(map[string]ProfileMetric)
	for _, p := range []profile.Profile{profile.Fast, profile.Balanced, profile.Deep} {
		pm, err := measureOneProfile(ctx, fixture, p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: profile %s metrics skipped: %v\n", p, err)
			continue
		}
		out[string(p)] = pm
	}
	return out
}

func measureOneProfile(ctx context.Context, fixture string, p profile.Profile) (ProfileMetric, error) {
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

	ti0 := time.Now()
	if err := ing.IngestAll(ctx, fixture); err != nil {
		return ProfileMetric{}, err
	}
	index := time.Since(ti0)

	c := client.NewDirect(query.New(store), search.New(store))
	q0 := time.Now()
	if _, err := c.Query(ctx, "callers", "", 0); err != nil {
		return ProfileMetric{}, err
	}
	ql := time.Since(q0)

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
		QueryLatencyMS: ms(ql),
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
	args := []string{"build"}
	if len(tags) > 0 {
		// Subset-tag the measured binary so binary_size_bytes reflects the SHIPPED
		// default build (only the registered grammar blobs embedded), matching the
		// canonical cmd/release build. Space-joined `-tags 'a b c'` form.
		args = append(args, "-tags", strings.Join(tags, " "))
	}
	args = append(args, "-o", out, target)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Env = withCgo(os.Environ(), cgo)
	cmd.Dir = modRoot
	return cmd.CombinedOutput()
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
