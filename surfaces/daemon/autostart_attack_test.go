package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAutoStartRefusesTestBinaryAttack pins the fail-closed guard that ended
// the 2026-08-27 fork-recursion incident.
//
// The attack shape: DaemonClient.connect auto-starts the daemon on a failed
// dial, and NewClient(socket, "") defaults the daemon binary to os.Args[0].
// Inside `go test`, os.Args[0] is the *.test binary — whose entry point is the
// test suite. Before the guard, any test that dialed an absent socket (the
// SW-227 attach-daemon start-path characterization was the first) spawned the
// test binary as a "daemon"; Go's flag parsing stops at the positional
// "daemon start" arguments, so the child silently re-ran the whole suite,
// dialed the absent socket again, and spawned the next copy — hundreds of
// recursive test processes, ending in kernel watchdog panics on the host.
//
// The guard must therefore refuse BEFORE exec: a binary path ending in .test
// is never a daemon host.
func TestAutoStartRefusesTestBinaryAttack(t *testing.T) {
	if !strings.HasSuffix(os.Args[0], ".test") {
		t.Skipf("test binary is %q, not *.test; the default-path premise does not hold here", os.Args[0])
	}

	socket := filepath.Join(t.TempDir(), "absent.sock")
	c := NewClient(socket, "") // binaryPath defaults to os.Args[0] — this test binary

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.connect(ctx)
	if err == nil {
		t.Fatal("connect to an absent socket succeeded; expected the auto-start refusal")
	}
	if !strings.Contains(err.Error(), "refusing to spawn") {
		t.Fatalf("connect failed, but not via the fail-closed refusal — the spawn branch may have run: %v", err)
	}

	// The refusal must have prevented the spawn: the socket must still be
	// absent (a spawned daemon would create it).
	if _, statErr := os.Stat(socket); statErr == nil {
		t.Fatal("socket exists after the refusal — something was spawned anyway")
	}
}

// TestAutoStartExplicitTestBinaryRefusedAttack closes the explicit variant:
// even a caller that deliberately configures a *.test path must be refused —
// the guard is on the binary path, not on "are we currently inside go test".
func TestAutoStartExplicitTestBinaryRefusedAttack(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "absent.sock")
	c := NewClient(socket, filepath.Join(t.TempDir(), "fake.test"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.connect(ctx)
	if err == nil || !strings.Contains(err.Error(), "refusing to spawn") {
		t.Fatalf("expected the auto-start refusal for an explicit *.test binary, got: %v", err)
	}
}
