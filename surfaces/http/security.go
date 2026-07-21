package http

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// limitRequestBody enforces one hard limit before routing. Buffering once here
// covers both today's POST handlers and future routes; handlers receive a fresh
// reader containing the complete body only after the limit check succeeds.
func limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Body == http.NoBody {
			next.ServeHTTP(w, r)
			return
		}

		limited := http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		body, err := io.ReadAll(limited)
		_ = limited.Close()
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeErr(w, http.StatusRequestEntityTooLarge, "request_too_large",
					fmt.Sprintf("request body exceeds %d bytes", maxRequestBodyBytes))
				return
			}
			writeErr(w, http.StatusBadRequest, "bad_request", "cannot read request body")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

// requestSecurityGuard protects the loopback HTTP surface against DNS
// rebinding and cross-origin browser access. Host must name localhost or a
// loopback IP. Browser requests that carry Origin must be exactly same-origin
// (scheme, normalized host, and port); requests without Origin remain valid for
// curl, the CLI HTTP client, IDEs, and other non-browser local consumers.
func requestSecurityGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		requestAuthority, err := parseHTTPAuthority(r.Host, scheme)
		if err != nil || !isLoopbackHTTPHost(requestAuthority.host) {
			writeErr(w, http.StatusForbidden, "invalid_host", "request host must be loopback or localhost")
			return
		}

		origins := r.Header.Values("Origin")
		if len(origins) > 1 {
			writeErr(w, http.StatusForbidden, "origin_forbidden", "cross-origin requests are not allowed")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			originAuthority, originScheme, err := parseHTTPOrigin(origin)
			if err != nil || originScheme != scheme || originAuthority != requestAuthority {
				writeErr(w, http.StatusForbidden, "origin_forbidden", "cross-origin requests are not allowed")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type httpAuthority struct {
	host string
	port string
}

func parseHTTPAuthority(raw, scheme string) (httpAuthority, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "/?#@") {
		return httpAuthority{}, errors.New("invalid authority")
	}

	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		switch {
		case net.ParseIP(raw) != nil:
			host = raw // bare IPv6, accepted defensively for in-process clients
		case !strings.Contains(raw, ":"):
			host = raw
		default:
			return httpAuthority{}, fmt.Errorf("invalid authority %q: %w", raw, err)
		}
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" {
		return httpAuthority{}, errors.New("empty authority host")
	}
	if port == "" {
		port = defaultHTTPPort(scheme)
	} else {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return httpAuthority{}, errors.New("invalid authority port")
		}
		port = strconv.Itoa(n)
	}
	return httpAuthority{host: host, port: port}, nil
}

func parseHTTPOrigin(raw string) (httpAuthority, string, error) {
	if raw == "null" {
		return httpAuthority{}, "", errors.New("opaque origin")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Opaque != "" ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return httpAuthority{}, "", errors.New("invalid origin")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return httpAuthority{}, "", errors.New("invalid origin scheme")
	}
	authority, err := parseHTTPAuthority(u.Host, scheme)
	if err != nil || !isLoopbackHTTPHost(authority.host) {
		return httpAuthority{}, "", errors.New("invalid origin authority")
	}
	return authority, scheme, nil
}

func defaultHTTPPort(scheme string) string {
	if scheme == "https" {
		return "443"
	}
	return "80"
}

func isLoopbackHTTPHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// AssertLoopback rejects a non-loopback bind address, enforcing the zero-outbound
// local-first contract at the surface boundary. It is exported so callers that
// build their own listener (e.g. cmd/graphi runHTTP, which prints the bound
// address) can validate BEFORE net.Listen — the surface must never bind a
// non-loopback address regardless of which entry point constructs the listener.
func AssertLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("http: bad address %q: %w", addr, err)
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return fmt.Errorf("http: refusing non-loopback bind %q (local-first, loopback-only)", addr)
	}
	return nil
}

// ListenLoopback asserts addr is loopback (AssertLoopback) and then binds a TCP
// listener on it. The bind lives in this loopback-only surface package — not in
// cmd — so the local-first contract and the single net.Listen egress surface
// stay inside the allowlisted surfaces/http boundary (the zero-telemetry canary
// allowlists this package, like surfaces/daemon and surfaces/client).
func ListenLoopback(addr string) (net.Listener, error) {
	if err := AssertLoopback(addr); err != nil {
		return nil, err
	}
	return net.Listen("tcp", addr)
}
