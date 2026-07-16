package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newLocalRequest models the only authority accepted by the production HTTP
// surface. httptest.NewRequest defaults Host to example.com, which is useful for
// generic handlers but intentionally rejected by graphi's DNS-rebinding guard.
func newLocalRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = "127.0.0.1"
	return req
}

func TestRequestSecurityGuard_HostFailClosed(t *testing.T) {
	srv := New(&stubClient{}, nil)
	cases := []struct {
		name string
		host string
		want int
	}{
		{name: "ipv4", host: "127.0.0.1:8080", want: http.StatusOK},
		{name: "ipv4_loopback_range", host: "127.0.0.2:8080", want: http.StatusOK},
		{name: "localhost", host: "LOCALHOST.:8080", want: http.StatusOK},
		{name: "ipv6", host: "[::1]:8080", want: http.StatusOK},
		{name: "rebinding_domain", host: "attacker.example:8080", want: http.StatusForbidden},
		{name: "localhost_suffix", host: "localhost.attacker.example:8080", want: http.StatusForbidden},
		{name: "unspecified", host: "0.0.0.0:8080", want: http.StatusForbidden},
		{name: "empty", host: "", want: http.StatusForbidden},
		{name: "userinfo", host: "localhost@attacker.example", want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			req.Host = tc.host
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("Host %q: code=%d body=%s, want %d", tc.host, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusForbidden {
				assertResponseErrorCode(t, rec, "invalid_host")
			}
		})
	}
}

func TestRequestSecurityGuard_OriginMustBeExactSameOrigin(t *testing.T) {
	srv := New(&stubClient{}, nil)
	cases := []struct {
		name    string
		origins []string
		want    int
	}{
		{name: "cli_no_origin", want: http.StatusOK},
		{name: "same_origin", origins: []string{"http://127.0.0.1:8080"}, want: http.StatusOK},
		{name: "different_port", origins: []string{"http://127.0.0.1:3000"}, want: http.StatusForbidden},
		{name: "different_loopback_name", origins: []string{"http://localhost:8080"}, want: http.StatusForbidden},
		{name: "different_scheme", origins: []string{"https://127.0.0.1:8080"}, want: http.StatusForbidden},
		{name: "remote_origin", origins: []string{"https://attacker.example"}, want: http.StatusForbidden},
		{name: "opaque_origin", origins: []string{"null"}, want: http.StatusForbidden},
		{name: "origin_with_path", origins: []string{"http://127.0.0.1:8080/path"}, want: http.StatusForbidden},
		{name: "multiple_origins", origins: []string{"http://127.0.0.1:8080", "http://127.0.0.1:8080"}, want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			req.Host = "127.0.0.1:8080"
			for _, origin := range tc.origins {
				req.Header.Add("Origin", origin)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("Origin %v: code=%d body=%s, want %d", tc.origins, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusForbidden {
				assertResponseErrorCode(t, rec, "origin_forbidden")
			}
		})
	}
}

func TestRequestBodyLimitBoundary(t *testing.T) {
	t.Setenv(LabsEnvVar, "1")
	srv := New(&stubClient{queryBytes: []byte(`{"ok":true}`)}, nil)
	handler := srv.Handler()

	for _, tc := range []struct {
		name string
		size int
		want int
	}{
		{name: "exact_limit", size: int(maxRequestBodyBytes), want: http.StatusOK},
		{name: "limit_plus_one", size: int(maxRequestBodyBytes) + 1, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newLocalRequest(http.MethodPost, "/compound", strings.NewReader(strings.Repeat("x", tc.size)))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("size=%d: code=%d body=%s, want %d", tc.size, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusRequestEntityTooLarge {
				assertResponseErrorCode(t, rec, "request_too_large")
			}
		})
	}
}

func assertResponseErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var envelope errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if envelope.Error.Code != want {
		t.Fatalf("error code=%q, want %q", envelope.Error.Code, want)
	}
}
