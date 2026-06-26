package mcp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCP_Stdio_HTTP_EnvelopeParity asserts the SW-098 invariant: the same
// JSON-RPC request produces byte-identical response envelopes over stdio and over
// streamable-HTTP, because both route through the same handle() seam and the same
// encoder. Covers initialize, tools/list, a tools/call, and a method-not-found
// error envelope.
func TestMCP_Stdio_HTTP_EnvelopeParity(t *testing.T) {
	srv := NewServerWithClient(allToolsClient{})
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()

	cases := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"query":"x"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"bogus_method"}`,
	}
	for _, req := range cases {
		// stdio framing
		var out bytes.Buffer
		if err := srv.Serve(context.Background(), strings.NewReader(req+"\n"), &out); err != nil {
			t.Fatalf("stdio Serve: %v", err)
		}
		stdioBytes := out.Bytes()

		// streamable-HTTP framing
		resp, err := http.Post(ts.URL, "application/json", strings.NewReader(req))
		if err != nil {
			t.Fatalf("http POST: %v", err)
		}
		httpBytes, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if !bytes.Equal(stdioBytes, httpBytes) {
			t.Fatalf("envelope parity broken for %q:\n stdio=%q\n http =%q", req, stdioBytes, httpBytes)
		}
	}
}

func TestMCP_HTTP_NotificationNoBody(t *testing.T) {
	srv := NewServerWithClient(allToolsClient{})
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	// A notification (no id) yields 202 Accepted with no JSON-RPC body.
	resp, err := http.Post(ts.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification status = %d, want 202", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(bytes.TrimSpace(body)) != 0 {
		t.Fatalf("notification must have no body, got %q", body)
	}
}

func TestMCP_HTTP_LoopbackOnly(t *testing.T) {
	if _, err := ListenLoopbackMCP("0.0.0.0:0"); err == nil {
		t.Fatal("must refuse wildcard bind 0.0.0.0")
	}
	if _, err := ListenLoopbackMCP("192.168.1.5:9000"); err == nil {
		t.Fatal("must refuse routable bind")
	}
	ln, err := ListenLoopbackMCP("127.0.0.1:0")
	if err != nil {
		t.Fatalf("loopback bind should succeed: %v", err)
	}
	_ = ln.Close()
}

func TestMCP_HTTP_RejectsNonPost(t *testing.T) {
	srv := NewServerWithClient(allToolsClient{})
	ts := httptest.NewServer(srv.HTTPHandler())
	defer ts.Close()
	resp, err := http.Get(ts.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want 405", resp.StatusCode)
	}
}
