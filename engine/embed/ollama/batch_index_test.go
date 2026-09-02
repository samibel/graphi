package ollama_test

// SW-267 reviewer fix Critical 3: Ollama Embed reads `embeddings[0]`
// for every per-text response. The previous shape read `embeddings[i]`
// against the OUTER batch index; with one request per text the
// response always has its vector at `embeddings[0]`. Any chunk of
// two or more documents failed on the second request because
// `decoded.Embeddings[1]` was out-of-range.
//
// The test below is the red-then-green evidence. It sends two
// distinct texts and asserts that BOTH vectors come back in input
// order. Without the fix (decoded.Embeddings[i] with i>0), the second
// request would index out of range or read the wrong vector.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/engine/embed/ollama"
)

// TestOllama_EmbedBatchReadsEmbeddingsZero is the regression test for
// Critical 3: a two-text batch must round-trip. A single-text test
// cannot catch the bug. The mock reproduces the real /api/embed
// wire shape (`input` field; `embeddings: number[][]`, ONE vector
// per request).
func TestOllama_EmbedBatchReadsEmbeddingsZero(t *testing.T) {
	var calls atomic.Int32
	var sawSecond bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		// Each request sends one input. The response carries exactly
		// one vector for that input. The previous shape read
		// `embeddings[i]` with i=outer batch index, which broke on
		// request 2.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{float32(n), 0, 0}}, // unique per request
		})
		if n >= 2 {
			sawSecond = true
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	e, err := ollama.New(host, "m")
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	vecs, err := e.Embed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("server saw %d calls, want 2 (one per text)", calls.Load())
	}
	if !sawSecond {
		t.Error("server never received the second text; the loop must index the OUTER request count correctly")
	}
	if len(vecs) != 2 {
		t.Fatalf("got %d vectors, want 2", len(vecs))
	}
	// Each response carries [n, 0, 0]; the first request's n is 1, the
	// second's is 2. The previous shape (embeddings[i] with i=outer
	// index) would read embeddings[1] from request 2's response —
	// which is the second vector (also [2,0,0]) and look like the
	// answer, hiding the bug. To prove the bug, we also assert a
	// DIFFERENT shape: the response is keyed by request, so the
	// returned vectors are [1,0,0] and [2,0,0] in input order. The
	// fix reads embeddings[0] for both, getting the same [1,0,0]
	// and [2,0,0]. The buggy shape (embeddings[i]) would read
	// embeddings[1] for the first response, getting nothing.
	if len(vecs[0]) != 3 || vecs[0][0] != 1 {
		t.Errorf("vecs[0] = %v, want [1, 0, 0]", vecs[0])
	}
	if len(vecs[1]) != 3 || vecs[1][0] != 2 {
		t.Errorf("vecs[1] = %v, want [2, 0, 0] — the second request's embeddings[0]", vecs[1])
	}
}
