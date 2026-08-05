// Package perfbaseline records an A/B-comparable performance baseline for the
// P2 architecture refactor (ARCH-P0).
//
// The refactor inserts an application layer and a contract-mapping step between
// the surfaces and the engine. That is exactly the kind of change that costs a
// few percent per call without anyone noticing, so the PRD gates it: full-index
// p95 may not regress by more than 3%, warm-query p95 by more than 5%. A gate
// like that is meaningless without a recorded "before", measured the same way on
// the same machine.
//
// This package is that "before". It deliberately does NOT replace bench/: the
// bench budget in bench/bench-budget.yml stays the release gate for cold start,
// full index, freshness, and binary size. What bench cannot answer is the
// question this refactor raises — it reports a median for one metric and a p95
// for another, never both, and it does not measure warm query latency or the
// trust report at all. So this harness samples each metric itself and reports
// median AND p95 for every one, reusing bench's statistics helpers and canonical
// build contract rather than reimplementing them.
//
// Like internal/bench, this is UNRANKED CI tooling: it is not part of any shipped
// runtime import graph.
package perfbaseline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/internal/bench"
)

// SchemaVersion versions the artifact envelope.
const SchemaVersion = 1

// MinSamples is the floor the PRD sets: a performance claim resting on fewer
// runs than this is not a measurement, it is an anecdote.
const MinSamples = 5

// DefaultSamples matches the bench harness so both report comparable statistics.
const DefaultSamples = 15

// DefaultWarmup discards this many initial runs before recording.
const DefaultWarmup = 2

// Config parameterizes a baseline run.
type Config struct {
	// FixtureDir is the workload. Empty means <module root>/bench/fixture, the
	// same digest-pinned corpus the release benchmark uses.
	FixtureDir string
	// Samples is the recorded sample count per metric (default DefaultSamples).
	Samples int
	// Warmup is the number of discarded leading runs (default DefaultWarmup).
	Warmup int
	// Commit is the revision this baseline describes. Required: a measurement
	// that cannot name what it measured cannot be compared against anything.
	Commit string
	// SkipBinarySize omits the canonical release build, which dominates runtime.
	// Intended for fast self-tests, never for a recorded baseline.
	SkipBinarySize bool
}

func (c *Config) defaults() {
	if c.Samples == 0 {
		c.Samples = DefaultSamples
	}
	if c.Warmup == 0 {
		c.Warmup = DefaultWarmup
	}
}

// Stat is one measured metric: the full sample-set statistics, not a single
// number. Median and p95 are both recorded because a mean improvement hiding a
// tail regression is the exact failure mode the phase gates care about.
type Stat struct {
	MedianMS float64 `json:"median_ms"`
	P95MS    float64 `json:"p95_ms"`
	MinMS    float64 `json:"min_ms"`
	MaxMS    float64 `json:"max_ms"`
	Samples  int     `json:"samples"`
}

// Report is the recorded baseline artifact.
type Report struct {
	SchemaVersion int    `json:"schema_version"`
	Commit        string `json:"commit"`
	Toolchain     string `json:"toolchain"`
	// FixtureDigest pins the workload, so a later comparison run cannot silently
	// measure a different corpus.
	FixtureDigest string `json:"fixture_digest"`
	// TrustFixture names the separate workload the trust measurement uses, since
	// the trust composition needs a resolvable module and the bench corpus is not
	// one. Recording it keeps the artifact from implying a single workload.
	TrustFixture string `json:"trust_fixture"`
	Samples      int    `json:"samples"`
	Warmup       int    `json:"warmup"`

	// FullIndex is engine/ingest IngestAll over the whole fixture, per sample.
	FullIndex Stat `json:"full_index"`
	// WarmQuery holds one entry per read operation, measured against an already
	// warm store — the latency an interactive user actually experiences, and the
	// path the application layer inserts a mapping step into.
	WarmQuery map[string]Stat `json:"warm_query"`
	// TrustReport is the P1 trust composition end to end. It has no benchmark
	// anywhere else in the repo, and its encoder lives at surface rank, so the
	// refactor moves it — which makes an unmeasured baseline a blind spot.
	TrustReport Stat `json:"trust_report"`
	// TrustVerdict and TrustState record what the timed calls actually produced,
	// so a reader can confirm the numbers describe a real report.
	TrustVerdict string `json:"trust_verdict"`
	TrustState   string `json:"trust_state"`

	// BinarySizeBytes is the canonical release build; 0 when skipped.
	BinarySizeBytes int64 `json:"binary_size_bytes"`
	// BuildContract records whether the measured binary really was the canonical
	// release build, so a size comparison cannot be made against a custom build.
	BuildContract string `json:"build_contract,omitempty"`

	// Environment is free-form provenance: these numbers are only comparable
	// against another run on the same machine, and the artifact should say so.
	Environment string `json:"environment"`
}

// statOf computes the recorded statistics for one sample set.
func statOf(samples []time.Duration) Stat {
	if len(samples) == 0 {
		return Stat{}
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return Stat{
		MedianMS: toMS(bench.Median(samples)),
		P95MS:    toMS(bench.P95(samples)),
		MinMS:    toMS(sorted[0]),
		MaxMS:    toMS(sorted[len(sorted)-1]),
		Samples:  len(samples),
	}
}

func toMS(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }

// Render encodes the report as canonical, reviewable JSON.
func Render(r Report) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return nil, fmt.Errorf("perfbaseline: encode report: %w", err)
	}
	return buf.Bytes(), nil
}

// Parse decodes a recorded report.
func Parse(raw []byte) (Report, error) {
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return Report{}, fmt.Errorf("perfbaseline: decode report: %w", err)
	}
	return r, nil
}

// environment describes the machine, for the provenance field.
func environment() string {
	return fmt.Sprintf("%s/%s, %d cpu", runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
}

// toolchain records the Go version the measurement ran under.
func toolchain() string {
	return fmt.Sprintf("%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// ModuleRoot resolves the module root once via `go env GOMOD` and caches it.
var ModuleRoot = sync.OnceValues(func() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == "/dev/null" {
		return "", fmt.Errorf("perfbaseline: no go.mod found (GOMOD=%q)", gomod)
	}
	return filepath.Dir(gomod), nil
})

// Summary renders a short human-readable digest.
func Summary(r Report) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "performance baseline @ %s (%s, %s)\n", r.Commit, r.Toolchain, r.Environment)
	fmt.Fprintf(&b, "  samples=%d warmup=%d fixture=%s\n", r.Samples, r.Warmup, short(r.FixtureDigest))
	fmt.Fprintf(&b, "  %-24s median %8.2f ms   p95 %8.2f ms\n", "full_index", r.FullIndex.MedianMS, r.FullIndex.P95MS)
	for _, op := range sortedKeys(r.WarmQuery) {
		stat := r.WarmQuery[op]
		fmt.Fprintf(&b, "  %-24s median %8.2f ms   p95 %8.2f ms\n", "warm_query/"+op, stat.MedianMS, stat.P95MS)
	}
	fmt.Fprintf(&b, "  %-24s median %8.2f ms   p95 %8.2f ms  (verdict %s, state %s)\n",
		"trust_report", r.TrustReport.MedianMS, r.TrustReport.P95MS, r.TrustVerdict, r.TrustState)
	if r.BinarySizeBytes > 0 {
		fmt.Fprintf(&b, "  %-24s %d bytes (%s)\n", "binary_size", r.BinarySizeBytes, r.BuildContract)
	}
	return b.String()
}

func sortedKeys(m map[string]Stat) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func short(digest string) string {
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
