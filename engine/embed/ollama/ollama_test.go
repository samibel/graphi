package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/embed/ollama"
)

// AC-G: construction FAILS CLOSED on a non-loopback host.
func TestNew_RejectsNonLoopback(t *testing.T) {
	bad := []string{
		"8.8.8.8:11434",
		"example.com:11434",
		"10.0.0.5:11434",
		"203.0.113.7:80",
	}
	for _, host := range bad {
		if _, err := ollama.New(host, ""); err == nil {
			t.Fatalf("New(%q) succeeded; want fail-closed error", host)
		}
	}
}

// Loopback hosts are accepted at construction.
func TestNew_AcceptsLoopback(t *testing.T) {
	for _, host := range []string{"", "127.0.0.1:11434", "localhost:11434", "[::1]:11434", "127.0.0.5:9999"} {
		e, err := ollama.New(host, "m")
		if err != nil {
			t.Fatalf("New(%q) failed: %v", host, err)
		}
		if e == nil {
			t.Fatalf("New(%q) returned nil embedder", host)
		}
	}
}

// The default constructor path (empty GRAPHI_EMBEDDER) never constructs Ollama.
func TestOllama_NotConstructedOnDefaultPath(t *testing.T) {
	// Constructor with empty selector ⇒ graceful skip, nothing constructed.
	e, err := embed.Constructor("", embed.DefaultConstructors())
	if err != nil || e != nil {
		t.Fatalf("default path constructed an embedder: e=%v err=%v", e, err)
	}
}

// Opt-in: an explicit "ollama" selector constructs a loopback embedder through
// the registered scheme (importing this package registered it).
func TestOllama_OptInViaSelector(t *testing.T) {
	e, err := embed.Constructor("ollama:127.0.0.1:11434", embed.DefaultConstructors())
	if err != nil {
		t.Fatalf("Constructor(ollama:...): %v", err)
	}
	if e == nil {
		t.Fatal("explicit ollama selector returned nil embedder")
	}
	if !strings.HasPrefix(e.ID(), "ollama:") {
		t.Fatalf("ID = %q, want ollama: prefix", e.ID())
	}
}

// A non-loopback selector still fails closed through the Constructor seam.
func TestOllama_OptInNonLoopbackFailsClosed(t *testing.T) {
	if _, err := embed.Constructor("ollama:8.8.8.8:11434", embed.DefaultConstructors()); err == nil {
		t.Fatal("Constructor(ollama:8.8.8.8:11434) succeeded; want fail-closed error")
	}
}

// When configured, Embed dials the loopback endpoint only (asserted by pointing
// the embedder at a loopback httptest server and confirming it is reached).
func TestOllama_DialsLoopbackOnly(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"embedding": []float32{0.1, 0.2, 0.3}})
	}))
	defer srv.Close()
	// httptest binds 127.0.0.1; strip scheme for the host:port.
	host := strings.TrimPrefix(srv.URL, "http://")
	e, err := ollama.New(host, "m")
	if err != nil {
		t.Fatalf("New(%q): %v", host, err)
	}
	vecs, err := e.Embed(context.Background(), []string{"hello"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if hits != 1 {
		t.Fatalf("server hits = %d, want 1", hits)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("vecs = %v, want one 3-dim vector", vecs)
	}
	if e.Dim() != 3 {
		t.Fatalf("Dim = %d, want 3 after first embed", e.Dim())
	}
}
