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
// and artifact size. Run it alone so the process-lifetime MAXRSS is the
// spike's own:
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
	var tokens int
	for _, txt := range texts {
		tokens += len(m.InferenceIDs(txt))
	}
	start = time.Now()
	vecs := m.Embed(texts)
	embedDur := time.Since(start)
	rssBatch, _ := peakRSSBytes()
	runtime.ReadMemStats(&ms)
	if len(vecs) != len(texts) {
		t.Fatalf("%d vectors for %d texts", len(vecs), len(texts))
	}
	perSec := float64(len(texts)) / embedDur.Seconds()
	t.Logf("batch: %d texts, %d pooled tokens (%.1f/text) in %s → %.0f texts/s, %.1f µs/text; after batch: MaxRSS %.1f MiB, HeapSys %.1f MiB, Sys %.1f MiB, HeapAlloc %.1f MiB",
		len(texts), tokens, float64(tokens)/float64(len(texts)), embedDur, perSec, float64(embedDur.Microseconds())/float64(len(texts)),
		mib(rssBatch), mib(int64(ms.HeapSys)), mib(int64(ms.Sys)), mib(int64(ms.HeapAlloc)))

	// Second pass, warm: the steady-state figure.
	start = time.Now()
	_ = m.Embed(texts)
	warm := time.Since(start)
	t.Logf("batch (warm repeat): %s → %.0f texts/s", warm, float64(len(texts))/warm.Seconds())
	t.Logf("machine: %s/%s, %d CPUs, %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
}

func mib(b int64) float64 { return float64(b) / (1 << 20) }
