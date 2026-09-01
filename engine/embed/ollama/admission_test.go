package ollama_test

// SW-267 AC-2 / AC-7 / AC-9 tests for the Ollama adapter's
// fail-closed admission surface and explicitly-optional backend
// contract.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/ollama"
)

// TestOllama_AdmissionImplementsInterface pins AC-2 / AC-7: the
// Ollama adapter implements embed.Admission and embed.AdmissionProfile.
func TestOllama_AdmissionImplementsInterface(t *testing.T) {
	e, err := newEmbedder(t)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var ei embed.Embedder = e
	if _, ok := ei.(embed.Admission); !ok {
		t.Fatal("ollama.Embedder does not implement embed.Admission; AC-2 requires it")
	}
	if _, ok := ei.(embed.AdmissionProfile); !ok {
		t.Fatal("ollama.Embedder does not implement embed.AdmissionProfile; AC-3 requires it")
	}
}

// TestOllama_AdmitBytesBound pins AC-6 / AC-9: the Ollama adapter's
// Admit enforces the resource byte cap (MaxCapsuleBytes) so an
// oversized payload never reaches the server. The server is the
// final authority via /api/embed with truncate:false (the SW-267
// AC-4 fail-closed posture); Admit here guards the byte budget so a
// misconfigured daemon never sees the request.
func TestOllama_AdmitBytesBound(t *testing.T) {
	e, err := newEmbedder(t)
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	var ei embed.Embedder = e
	adm := ei.(embed.Admission)
	// A small input admits cleanly.
	if _, err := adm.Admit(context.Background(), "hello"); err != nil {
		t.Errorf("Admit(hello) = %v, want nil", err)
	}
	// An oversized payload surfaces a typed AdmissionError so the
	// build fails closed.
	big := strings.Repeat("x", 20*1024) // 20 KiB > MaxCapsuleBytes (16 KiB)
	if _, err := adm.Admit(context.Background(), big); err == nil {
		t.Fatal("Admit(20 KiB) succeeded; AC-4 says oversized payload must surface a typed error")
	} else if !embed.IsAdmissionError(err) {
		t.Errorf("Admit(20 KiB) error type = %T, want *embed.AdmissionError", err)
	}
}

// TestOllama_EmbedUsesApiEmbedWithTruncateFalse pins AC-4: the
// adapter POSTs to /api/embed (not /api/embeddings) with
// `truncate:false` so the daemon is the final authority on input
// admission. The test asserts the request body carries the
// truncate:false field and the URL targets /api/embed.
func TestOllama_EmbedUsesApiEmbedWithTruncateFalse(t *testing.T) {
	var sawTruncateFalse bool
	var sawEndpoint string
	var sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawEndpoint = r.Host
		sawPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err == nil {
			if t, ok := req["truncate"].(bool); ok && !t {
				sawTruncateFalse = true
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2}})
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	e, err := newEmbedderAt(host, "m")
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	if _, err := e.Embed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if !sawTruncateFalse {
		t.Error("server saw request without truncate:false; AC-4 requires truncate:false for fail-closed admission")
	}
	if sawPath != "/api/embed" {
		t.Errorf("server saw path %q, want /api/embed (AC-4: switch from /api/embeddings)", sawPath)
	}
	if sawEndpoint == "" {
		t.Error("server endpoint missing; loopback allowlist must hold")
	}
}

// TestOllama_EmbedFailsClosedOn400 pins AC-4: the daemon's
// "input exceeds context length" rejection surfaces as a typed
// error so the calling build aborts rather than publishes a
// partial generation as ready (AC-5).
func TestOllama_EmbedFailsClosedOn400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"input exceeds context length"}`))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	e, err := newEmbedderAt(host, "m")
	if err != nil {
		t.Fatalf("constructor: %v", err)
	}
	_, err = e.Embed(context.Background(), []string{"hello"})
	if err == nil {
		t.Fatal("Embed succeeded against a 400 daemon; AC-4 says the embedder must surface the typed error")
	}
	if !strings.Contains(err.Error(), "input exceeds context length") {
		t.Errorf("Embed error = %q, want it to surface the daemon's failure message verbatim", err.Error())
	}
}

// newEmbedder is a small constructor helper for the AC tests; the
// existing TestOllama_OptInViaSelector covers the standard loopback
// path. Pulled into a shared helper to keep the AC tests focused on
// the contract under test.
func newEmbedder(t *testing.T) (*ollama.Embedder, error) {
	t.Helper()
	return newEmbedderAt("127.0.0.1:11434", "m")
}

func newEmbedderAt(host, model string) (*ollama.Embedder, error) {
	return ollama.New(host, model)
}
