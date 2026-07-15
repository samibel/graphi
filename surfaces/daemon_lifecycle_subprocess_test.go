package surfaces_test

// SW-121 (RUN-01, ADR 0002 D5): the daemon HOST PROCESS terminates. Before
// RUN-01, `graphi daemon start` parked in `select {}` forever: `daemon stop`
// tore down the listener + socket, but the started CLI process never exited
// and its deferred cleanups (watcher StopAll, store Close) never ran. This
// subprocess E2E drives the real binary through Start → ready → Stop and
// asserts the PROCESS exits (exit code 0, within the budget) and the socket
// file is gone — for both the `daemon stop` RPC and SIGTERM.

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// buildGraphi builds the real CGo-free binary once per test into a temp dir.
func buildGraphi(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	bin := filepath.Join(t.TempDir(), "graphi")
	build := exec.Command("go", "build", "-o", bin, "./cmd/graphi")
	build.Dir = moduleRoot(t)
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build graphi: %v\n%s", err, out)
	}
	return bin
}

// shortSocketPath returns a socket path short enough for the 108-byte UNIX
// sun_path limit (t.TempDir can exceed it on some runners).
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gd")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "d.sock")
}

// startDaemon launches `graphi daemon start -socket S` and waits until the
// socket accepts a connection. It returns the running command.
func startDaemon(t *testing.T, bin, socket string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin, "daemon", "start", "-socket", socket)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if conn, err := net.Dial("unix", socket); err == nil {
			_ = conn.Close()
			return cmd
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	t.Fatalf("daemon never became ready on %s", socket)
	return nil
}

// waitExit waits for the process to exit within budget and returns its error.
func waitExit(t *testing.T, cmd *exec.Cmd, budget time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(budget):
		_ = cmd.Process.Kill()
		<-done
		t.Fatalf("daemon process still running after %v — the select{} zombie is back", budget)
		return nil
	}
}

func TestDaemonLifecycle_StopRPCTerminatesTheProcess(t *testing.T) {
	bin := buildGraphi(t)
	socket := shortSocketPath(t)
	cmd := startDaemon(t, bin, socket)

	stop := exec.Command(bin, "daemon", "stop", "-socket", socket)
	if out, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("daemon stop: %v\n%s", err, out)
	}

	if err := waitExit(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("daemon exited non-zero after stop: %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket %s still exists after stop (err=%v)", socket, err)
	}

	// The lifecycle is restartable: a fresh start on the same socket succeeds.
	cmd2 := startDaemon(t, bin, socket)
	stop2 := exec.Command(bin, "daemon", "stop", "-socket", socket)
	if out, err := stop2.CombinedOutput(); err != nil {
		t.Fatalf("second daemon stop: %v\n%s", err, out)
	}
	if err := waitExit(t, cmd2, 5*time.Second); err != nil {
		t.Fatalf("second daemon exited non-zero: %v", err)
	}
}

func TestDaemonLifecycle_SIGTERMTerminatesTheProcess(t *testing.T) {
	bin := buildGraphi(t)
	socket := shortSocketPath(t)
	cmd := startDaemon(t, bin, socket)

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := waitExit(t, cmd, 5*time.Second); err != nil {
		t.Fatalf("daemon exited non-zero after SIGTERM: %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket %s still exists after SIGTERM (err=%v)", socket, err)
	}
}
