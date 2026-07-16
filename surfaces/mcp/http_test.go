package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestMCP_Stdio_HTTP_EnvelopeParity asserts the SW-098 invariant: the same
// JSON-RPC request produces byte-identical response envelopes over stdio and over
// streamable-HTTP, because both route through the same dispatcher and the same
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

func TestMCP_HTTP_BinderRequiresInlineRepositoryRoot(t *testing.T) {
	bound := false
	srv := NewServerWithBinder(func(context.Context, []string) (Binding, error) {
		bound = true
		return Binding{Client: allToolsClient{}}, nil
	})
	defer srv.Close()
	handler := srv.HTTPHandler()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}}}}`,
	))
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != -32602 || !strings.Contains(response.Error.Message, "requires rootUri or inline roots") {
		t.Fatalf("initialize response = %+v", response)
	}
	if bound {
		t.Fatal("HTTP binder ran without an explicit repository root")
	}
}

func TestMCP_HTTP_BinderAcceptsRootURI(t *testing.T) {
	repo := t.TempDir()
	var gotRoots []string
	srv := NewServerWithBinder(func(_ context.Context, roots []string) (Binding, error) {
		gotRoots = append([]string(nil), roots...)
		return Binding{Client: allToolsClient{}}, nil
	})
	defer srv.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":` + mustJSON(t, "file://"+filepath.ToSlash(repo)) + `,"capabilities":{"roots":{"listChanged":true}}}}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	srv.HTTPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error != nil {
		t.Fatalf("initialize failed: %+v", response.Error)
	}
	if len(gotRoots) != 1 || gotRoots[0] != filepath.Clean(repo) {
		t.Fatalf("binder roots=%v, want %q", gotRoots, filepath.Clean(repo))
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

func TestMCP_HTTP_RequestBodyLimitBoundary(t *testing.T) {
	prefix := `{"jsonrpc":"2.0","id":1,"method":"initialize","padding":"`
	suffix := `"}`
	requestOfSize := func(size int) string {
		t.Helper()
		padding := size - len(prefix) - len(suffix)
		if padding < 0 {
			t.Fatalf("test request framing exceeds target size %d", size)
		}
		return prefix + strings.Repeat("x", padding) + suffix
	}

	for _, tc := range []struct {
		name string
		size int
		want int
	}{
		{name: "exact_limit", size: maxHTTPBody, want: http.StatusOK},
		{name: "limit_plus_one", size: maxHTTPBody + 1, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := NewServerWithClient(allToolsClient{}).HTTPHandler()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(requestOfSize(tc.size)))
			req.Host = "127.0.0.1:8080"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("size=%d: code=%d body=%s, want %d", tc.size, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusRequestEntityTooLarge {
				var response rpcResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode oversized response: %v", err)
				}
				if response.Error == nil || response.Error.Code != mcpHTTPBodyTooLargeCode {
					t.Fatalf("oversized response = %+v", response)
				}
			}
		})
	}
}

func TestMCP_HTTP_RequestHostFailsClosed(t *testing.T) {
	handler := NewServerWithClient(allToolsClient{}).HTTPHandler()
	cases := []struct {
		host string
		want int
	}{
		{host: "127.0.0.1:8080", want: http.StatusOK},
		{host: "127.0.0.2:8080", want: http.StatusOK},
		{host: "LOCALHOST.:8080", want: http.StatusOK},
		{host: "[::1]:8080", want: http.StatusOK},
		{host: "attacker.example:8080", want: http.StatusForbidden},
		{host: "localhost.attacker.example:8080", want: http.StatusForbidden},
		{host: "0.0.0.0:8080", want: http.StatusForbidden},
		{host: "", want: http.StatusForbidden},
		{host: "localhost@attacker.example", want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
			req.Host = tc.host
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("Host %q: code=%d body=%s, want %d", tc.host, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusForbidden {
				assertMCPHTTPForbidden(t, rec)
			}
		})
	}
}

func TestMCP_HTTP_OriginMustBeExactSameOrigin(t *testing.T) {
	handler := NewServerWithClient(allToolsClient{}).HTTPHandler()
	cases := []struct {
		name    string
		origins []string
		want    int
	}{
		{name: "local_client_no_origin", want: http.StatusOK},
		{name: "same_origin", origins: []string{"http://127.0.0.1:8080"}, want: http.StatusOK},
		{name: "different_port", origins: []string{"http://127.0.0.1:3000"}, want: http.StatusForbidden},
		{name: "different_loopback_name", origins: []string{"http://localhost:8080"}, want: http.StatusForbidden},
		{name: "different_scheme", origins: []string{"https://127.0.0.1:8080"}, want: http.StatusForbidden},
		{name: "remote", origins: []string{"https://attacker.example"}, want: http.StatusForbidden},
		{name: "opaque", origins: []string{"null"}, want: http.StatusForbidden},
		{name: "path", origins: []string{"http://127.0.0.1:8080/path"}, want: http.StatusForbidden},
		{name: "multiple", origins: []string{"http://127.0.0.1:8080", "http://127.0.0.1:8080"}, want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
			req.Host = "127.0.0.1:8080"
			for _, origin := range tc.origins {
				req.Header.Add("Origin", origin)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("Origin %v: code=%d body=%s, want %d", tc.origins, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusForbidden {
				assertMCPHTTPForbidden(t, rec)
			}
		})
	}
}

func assertMCPHTTPForbidden(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var response rpcResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode forbidden response: %v (%s)", err, rec.Body.String())
	}
	if response.Error == nil || response.Error.Code != -32003 {
		t.Fatalf("forbidden response = %+v", response)
	}
}
