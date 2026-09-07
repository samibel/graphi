package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
)

func TestPinnedSelectorAndQueryPreparation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	var inputs []string
	requests := 0
	wrongDim := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch r.URL.Path {
		case "/api/tags":
			json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"name": "qwen3-embedding:0.6b", "digest": digest}}})
		case "/api/version":
			json.NewEncoder(w).Encode(map[string]any{"version": "fixture-1"})
		case "/api/show":
			json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{"general.architecture": "qwen3", "qwen3.context_length": 32768, "qwen3.embedding_length": 3}, "capabilities": []string{"embedding"}})
		case "/api/embed":
			var req struct {
				Model, Input string
				Truncate     bool
				Options      map[string]int
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req.Model != "qwen3-embedding:0.6b" || req.Truncate || req.Options["num_ctx"] != 8192 {
				t.Errorf("wrong request: %+v", req)
			}
			gpu, hasGPU := req.Options["num_gpu"]
			if !hasGPU || gpu != 0 || req.Options["num_thread"] != 1 {
				t.Errorf("CPU policy missing from request: %+v", req.Options)
			}
			inputs = append(inputs, req.Input)
			v := []float32{1, 0, 0}
			if wrongDim {
				v = v[:2]
			}
			json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{v}})
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	selector := "ollama:" + strings.TrimPrefix(srv.URL, "http://") + "?model=qwen3-embedding:0.6b&digest=" + digest + "&context=8192&runtime=fixture-1&profile=qwen3-code-v1&compute=cpu"
	e, err := embed.Constructor(selector, embed.DefaultConstructors())
	if err != nil {
		t.Fatal(err)
	}
	id := e.ID()
	if requests != 0 {
		t.Fatal("constructor dialed")
	}
	if err := e.(embed.DimDiscoverer).ProbeDim(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.ID() != id || e.Dim() != 3 {
		t.Fatal("unstable identity or wrong dimension")
	}
	inputs = nil
	doc := "func Example() {}"
	if _, err := e.Embed(context.Background(), []string{doc}); err != nil {
		t.Fatal(err)
	}
	if _, err := embed.EmbedQuery(context.Background(), e, "where is the implementation"); err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0] != doc || !strings.HasPrefix(inputs[1], "Instruct: ") || !strings.HasSuffix(inputs[1], "\nQuery: where is the implementation") {
		t.Fatalf("wrong preparation: %q", inputs)
	}
	profile := e.(embed.AdmissionProfile).Profile()
	if profile.TokenizerSHA256 != digest || profile.MaxTokens != 8192 {
		t.Fatalf("unbound profile: %+v", profile)
	}
	wrongDim = true
	if _, err := e.Embed(context.Background(), []string{doc}); err == nil {
		t.Fatal("dimension drift accepted")
	}
	wrongDim = false
	digest = strings.Repeat("b", 64)
	before := len(inputs)
	if _, err := e.Embed(context.Background(), []string{doc}); err == nil {
		t.Fatal("model tag drift accepted")
	}
	if len(inputs) != before {
		t.Fatal("sent source after digest mismatch")
	}
}

func TestPinnedSelectorIdentityBindsEveryChoice(t *testing.T) {
	base := "ollama:127.0.0.1:11434?model=fixture&digest=" + strings.Repeat("a", 64) + "&context=8192&runtime=fixture-1&profile=plain-v1"
	identity := func(selector string) string {
		t.Helper()
		e, err := embed.Constructor(selector, embed.DefaultConstructors())
		if err != nil || e == nil {
			t.Fatalf("constructor: %v", err)
		}
		return embed.FingerprintFor(e, "same-graph").Canonical()
	}
	id := identity(base)
	if identity(base+"&compute=auto") != id {
		t.Fatal("explicit auto placement differs from its default")
	}
	for _, different := range []string{
		strings.Replace(base, "model=fixture", "model=another", 1),
		strings.Replace(base, strings.Repeat("a", 64), strings.Repeat("b", 64), 1),
		strings.Replace(base, "context=8192", "context=4096", 1),
		strings.Replace(base, "runtime=fixture-1", "runtime=fixture-2", 1),
		strings.Replace(base, "profile=plain-v1", "profile=qwen3-code-v1", 1),
		base + "&compute=cpu",
	} {
		if identity(different) == id {
			t.Fatalf("different provider choice shares an identity: %s", different)
		}
	}
}

func TestPinnedSelectorFailsClosed(t *testing.T) {
	base := "ollama:127.0.0.1:11434?model=qwen3-embedding:0.6b&digest=" + strings.Repeat("a", 64) + "&context=8192&runtime=fixture-1&profile=qwen3-code-v1"
	for _, selector := range []string{
		"ollama:127.0.0.1:11434?model=qwen3-embedding:0.6b",
		strings.Replace(base, "127.0.0.1", "example.com", 1),
		base + "&context=4096", base + "&typo=value",
		base + "&compute=unknown", base + "&compute=cpu&compute=auto",
		strings.Replace(base, "context=8192", "context=-1", 1),
		strings.Replace(base, "qwen3-code-v1", "unknown", 1),
	} {
		if _, err := embed.Constructor(selector, embed.DefaultConstructors()); err == nil {
			t.Fatalf("accepted %q", selector)
		}
	}
}

func TestPinnedSelectorRejectsPostEmbeddingDrift(t *testing.T) {
	for _, field := range []string{"digest", "runtime"} {
		t.Run(field, func(t *testing.T) {
			digest, runtime := strings.Repeat("a", 64), "fixture-1"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/tags":
					json.NewEncoder(w).Encode(map[string]any{"models": []any{map[string]any{"name": "fixture", "digest": digest}}})
				case "/api/version":
					json.NewEncoder(w).Encode(map[string]any{"version": runtime})
				case "/api/show":
					json.NewEncoder(w).Encode(map[string]any{"model_info": map[string]any{"general.architecture": "fixture", "fixture.context_length": 8192, "fixture.embedding_length": 3}, "capabilities": []string{"embedding"}})
				case "/api/embed":
					if field == "digest" {
						digest = strings.Repeat("b", 64)
					} else {
						runtime = "fixture-2"
					}
					json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0, 0}}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			selector := "ollama:" + strings.TrimPrefix(srv.URL, "http://") + "?model=fixture&digest=" + digest + "&context=8192&runtime=" + runtime + "&profile=plain-v1"
			e, err := embed.Constructor(selector, embed.DefaultConstructors())
			if err != nil {
				t.Fatal(err)
			}
			vectors, err := e.Embed(context.Background(), []string{"source"})
			if err == nil || !strings.Contains(err.Error(), field) || vectors != nil {
				t.Fatalf("published vectors after %s drift: vectors=%v err=%v", field, vectors, err)
			}
		})
	}
}

func TestOllamaRejectsRedirectBeforeSendingSource(t *testing.T) {
	destinationCalls := 0
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls++
		t.Error("source followed a redirect")
	}))
	defer destination.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	e, err := embed.Constructor("ollama:"+strings.TrimPrefix(srv.URL, "http://"), embed.DefaultConstructors())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"source"}); err == nil || !strings.Contains(err.Error(), "redirects are forbidden") {
		t.Fatalf("redirect not rejected: %v", err)
	}
	if destinationCalls != 0 {
		t.Fatal("redirect destination was contacted")
	}
}
