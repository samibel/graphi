package main

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The TUI's self-contained default is the whole point of `graphi tui` working
// without arguments, so these pin the backend it starts. The Bubble Tea render
// loop itself needs a TTY and is not exercised here; what is exercised is
// everything that has to be right BEFORE it can render — a real listener on a
// free loopback port, a served contract, and a clean teardown.

// TestStartTUIBackend_BindsAFreePortAndServes pins the default that replaced
// the old hard-coded :8080. Nothing may assume a port number: the kernel
// assigns one, and the caller learns it from the returned URL.
func TestStartTUIBackend_BindsAFreePortAndServes(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	stop, url, err := startTUIBackend(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("startTUIBackend: %v", err)
	}
	defer stop()

	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("url = %q, want a loopback http URL", url)
	}
	_, port, err := net.SplitHostPort(strings.TrimPrefix(url, "http://"))
	if err != nil {
		t.Fatalf("url %q has no host:port: %v", url, err)
	}
	if port == "0" || port == "" {
		t.Errorf("port = %q — the URL must carry the port the kernel actually assigned, not the :0 request", port)
	}
	if port == "8080" {
		t.Errorf("port 8080 is the pre-0.9.0 hard-coded value; the backend must not claim a well-known port")
	}

	// The server is really serving: /contract is the version handshake every
	// client makes first.
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(url + "/contract")
	if err != nil {
		t.Fatalf("GET /contract: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /contract = %d, want 200", resp.StatusCode)
	}
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("/contract is not JSON: %v", err)
	}
	if _, ok := doc["schema_version"]; !ok {
		t.Errorf("/contract carries no schema_version: %v", doc)
	}
}

// TestStartTUIBackend_StopReleasesThePort pins teardown. A TUI session that
// left its listener behind would leak a port per run — and the leak would be
// invisible, since the port was never named by the user.
func TestStartTUIBackend_StopReleasesThePort(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	stop, url, err := startTUIBackend(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("startTUIBackend: %v", err)
	}
	hostPort := strings.TrimPrefix(url, "http://")
	stop()

	// Re-binding the same address proves the listener is gone. Retry briefly:
	// the OS may hold the socket for a moment after close.
	var lastErr error
	for i := 0; i < 50; i++ {
		ln, err := net.Listen("tcp", hostPort)
		if err == nil {
			_ = ln.Close()
			return
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("port %s still held one second after stop(): %v", hostPort, lastErr)
}

// TestStartTUIBackend_EachSessionGetsItsOwnPort pins that two concurrent TUI
// sessions do not fight. With a fixed port the second would simply fail to
// start; with :0 they must both come up.
func TestStartTUIBackend_EachSessionGetsItsOwnPort(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	stopA, urlA, err := startTUIBackend(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("first backend: %v", err)
	}
	defer stopA()
	stopB, urlB, err := startTUIBackend(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatalf("second backend: %v — concurrent TUI sessions must not collide", err)
	}
	defer stopB()

	if urlA == urlB {
		t.Errorf("both sessions bound %s", urlA)
	}
}
