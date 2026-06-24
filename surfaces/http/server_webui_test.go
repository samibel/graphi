//go:build webui_embed

package http

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/http/webui"
)

// TestSPA_ServesEmbeddedUI exercises the embedded SPA surface end-to-end under
// -tags webui_embed: "/" and unknown client-side routes serve index.html, real
// assets serve with a non-HTML content type, and an existing API route is still
// routed to its handler (ServeMux specificity wins — invariant #6).
func TestSPA_ServesEmbeddedUI(t *testing.T) {
	if !webui.Enabled() {
		t.Fatal("webui not enabled under -tags webui_embed; build wiring broken")
	}
	indexBytes, err := fs.ReadFile(webui.FS, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	indexBody := string(indexBytes)

	srv := New(nil, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	get := func(path string) (*http.Response, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, string(b)
	}

	// GET / → 200, body is index.html.
	if resp, body := get("/"); resp.StatusCode != 200 || body != indexBody {
		t.Fatalf("GET / = %d (want 200) and body index match=%v", resp.StatusCode, body == indexBody)
	}

	// GET /<spa route> → 200 + index.html (history fallback).
	if resp, body := get("/some/spa/route"); resp.StatusCode != 200 || body != indexBody {
		t.Fatalf("GET /some/spa/route = %d (want 200) body index match=%v", resp.StatusCode, body == indexBody)
	}

	// GET /assets/<real file> → 200, non-HTML content type (a real served asset).
	assetName := firstAsset(t)
	if resp, _ := get("/assets/" + assetName); resp.StatusCode != 200 {
		t.Fatalf("GET /assets/%s = %d; want 200", assetName, resp.StatusCode)
	} else if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET /assets/%s Content-Type = %q; want non-HTML asset type", assetName, ct)
	}

	// GET /healthz → still the health handler (NOT the SPA index/notice).
	if resp, body := get("/healthz"); resp.StatusCode != 200 {
		t.Fatalf("GET /healthz = %d; want 200", resp.StatusCode)
	} else if body == indexBody || !strings.Contains(body, "\"status\"") {
		t.Fatalf("GET /healthz did not hit the health handler; body=%q", body)
	}
}

// firstAsset returns the name of one real file under the embedded assets/ dir.
func firstAsset(t *testing.T) string {
	t.Helper()
	entries, err := fs.ReadDir(webui.FS, "assets")
	if err != nil {
		t.Fatalf("read embedded assets dir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			return e.Name()
		}
	}
	t.Fatal("no files in embedded assets/ dir")
	return ""
}
