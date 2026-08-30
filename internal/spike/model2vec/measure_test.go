package model2vec

import (
	"fmt"
	"runtime"
	"testing"
	"time"
)

// measureBatchSize is the AC-4 batch: 10k texts.
const measureBatchSize = 10_000

// measureTexts generates a deterministic mixed batch in the shape of what the
// product embeds: NodeText v1 documents (kind + qualified name), path
// segments, doc-comment prose and short code lines.
func measureTexts(n int) []string {
	shapes := []string{
		"function engine/agenttools/hybridsearch.Search%d",
		"method surfaces/client.Direct.TrustReport%d",
		"type core/graphstore.SQLiteStore%d",
		"file internal/eval/retrieval/runner_%d.go",
		"// ValidateToken%d checks the bearer token signature and expiry before the request reaches the store.",
		"func (h *Handler) authenticate%d(r *http.Request) (auth.Claims, error) { raw := strings.TrimPrefix(r.Header.Get(\"Authorization\"), \"Bearer \") }",
		"variable config.DefaultTokenTTL%d",
		"where is the auth token validated %d",
	}
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(shapes[i%len(shapes)], i)
	}
	return out
}

// TestSpikeMeasure records (never asserts) AC-4: model load time, peak RSS and
// Go heap during load and during a 10k-text batch, texts/second, token count
// and artifact size. The HEADLINE figures are the production path
// (EmbedEach, batch-invariant, no pad rows pooled); the reference-batch
// throughput is recorded separately with its pad rows counted. Run it alone
// so the process-lifetime MAXRSS is the spike's own:
//
//	CGO_ENABLED=0 go test ./internal/spike/model2vec -run TestSpikeMeasure -count=1 -v
func TestSpikeMeasure(t *testing.T) {
	dir := artifactDir()
	if !artifactPresent(dir) {
		t.Skip(skipMessage)
	}
	var ms runtime.MemStats
	runtime.GC()
	rss0, _ := peakRSSBytes()
	runtime.ReadMemStats(&ms)
	t.Logf("before load: MaxRSS %.1f MiB, HeapSys %.1f MiB, Sys %.1f MiB", mib(rss0), mib(int64(ms.HeapSys)), mib(int64(ms.Sys)))

	start := time.Now()
	m, err := LoadModel(dir)
	if err != nil {
		t.Fatal(err)
	}
	loadDur := time.Since(start)
	rssLoad, _ := peakRSSBytes()
	runtime.ReadMemStats(&ms)
	t.Logf("load: %s for %d×%d (artifact %.2f MiB = %d bytes); after load: MaxRSS %.1f MiB, HeapSys %.1f MiB, Sys %.1f MiB, HeapAlloc %.1f MiB",
		loadDur, m.VocabSize(), m.Dim(), mib(m.ArtifactBytes), m.ArtifactBytes, mib(rssLoad), mib(int64(ms.HeapSys)), mib(int64(ms.Sys)), mib(int64(ms.HeapAlloc)))

	texts := measureTexts(measureBatchSize)
	// AC-4 measures the production path: EmbedEach is batch-invariant
	// (record §2.1, SW-262 candidate) and pools exactly the ids InferenceIDs
	// returns per text — no [PAD] rows are pooled because each text is its
	// own batch of one.
	var tokens int
	for _, txt := range texts {
		tokens += len(m.InferenceIDs(txt))
	}
	start = time.Now()
	vecs := m.EmbedEach(texts)
	embedDur := time.Since(start)
	rssBatch, _ := peakRSSBytes()
	runtime.ReadMemStats(&ms)
	if len(vecs) != len(texts) {
		t.Fatalf("%d vectors for %d texts", len(vecs), len(texts))
	}
	perSec := float64(len(texts)) / embedDur.Seconds()
	t.Logf("batch (EmbedEach, production path): %d texts, %d pooled tokens (%.1f/text) in %s → %.0f texts/s, %.1f µs/text; after batch: MaxRSS %.1f MiB, HeapSys %.1f MiB, Sys %.1f MiB, HeapAlloc %.1f MiB",
		len(texts), tokens, float64(tokens)/float64(len(texts)), embedDur, perSec, float64(embedDur.Microseconds())/float64(len(texts)),
		mib(rssBatch), mib(int64(ms.HeapSys)), mib(int64(ms.Sys)), mib(int64(ms.HeapAlloc)))

	// Second pass, warm: the steady-state figure for the production path.
	start = time.Now()
	_ = m.EmbedEach(texts)
	warm := time.Since(start)
	t.Logf("batch (EmbedEach, warm repeat): %s → %.0f texts/s", warm, float64(len(texts))/warm.Seconds())

	// Reference-batch throughput: the SAME texts through Embed (BatchLongest
	// padded, pad id read from tokenizer.json). This is the path the oracle
	// batch fixture requires for max |Δ| = 0 (oracle_test.go:TestOracle_Batch
	// WithinEpsilon); it pools the [PAD] row into every shorter text's mean.
	// Token count uses InferenceIDsBatch so the padding rows are counted.
	var padTokens int
	for _, ids := range m.InferenceIDsBatch(texts) {
		padTokens += len(ids)
	}
	start = time.Now()
	refVecs := m.Embed(texts)
	refDur := time.Since(start)
	if len(refVecs) != len(texts) {
		t.Fatalf("reference batch: %d vectors for %d texts", len(refVecs), len(texts))
	}
	t.Logf("batch (Embed reference, BatchLongest padded): %d texts, %d pooled tokens incl. pad rows (%d pad rows, %.1f/text) in %s → %.0f texts/s — reference-comparison path, NOT the production path",
		len(texts), padTokens, padTokens-tokens, float64(padTokens-tokens)/float64(len(texts)), refDur, float64(len(texts))/refDur.Seconds())
	t.Logf("machine: %s/%s, %d CPUs, %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
}

func mib(b int64) float64 { return float64(b) / (1 << 20) }
