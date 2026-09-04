package surfaces_test

// SW-121 (RUN-01, ADR 0002 D5): the daemon HOST PROCESS terminates. Before
// RUN-01, `graphi daemon start` parked in `select {}` forever: `daemon stop`
// tore down the listener + socket, but the started CLI process never exited
// and its deferred cleanups (watcher StopAll, store Close) never ran. This
// subprocess E2E drives the real binary through Start → ready → Stop and
// asserts the PROCESS exits (exit code 0, within the budget) and the socket
// file is gone — for both the `daemon stop` RPC and SIGTERM.
//
// SW-275: "ready" is a SERVED request, and readiness has its own failure.
//
// The SIGTERM half of this file was red under a full parallel `go test ./...`
// and green in isolation. The helper below always waited for the socket, but
// the socket was the wrong readiness signal: `net.Listen` makes it dial-able
// the instant the kernel has the listener, which in `cmd/graphi` was BEFORE the
// process installed its SIGTERM handler. A signal landing in that window is
// delivered with Go's default disposition — the process dies with
// `signal: terminated` — and the test reported that as "daemon mishandled
// SIGTERM". That window is a product ordering defect and is closed in
// cmd/graphi/serve.go (the handler is now installed before the listener
// exists); the test side of the fix is that "ready" now means the daemon has
// served a `status` RPC round trip, the readiness wait is bounded by its own
// budget, and its expiry is reported as a READINESS failure with the daemon's
// output attached, never as a SIGTERM/stop-handling failure.
//
// The `daemon stop` half never had the race: it does not depend on signal
// disposition at all. The stop RPC is served by the listener's accept loop, and
// `srv.Done()` is a closed channel by the time the host process selects on it,
// so it returns whether the select was reached before or after the RPC.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const (
	// daemonReadyBudget bounds the readiness wait. Its expiry is a readiness
	// failure and is reported as one (SW-275 AC-2).
	daemonReadyBudget = 15 * time.Second
	// daemonExitBudget bounds the exit wait after `stop` or SIGTERM. Its expiry
	// is the RUN-01 zombie and is reported as that.
	daemonExitBudget = 5 * time.Second
	// daemonReadyPoll is the interval between readiness probes.
	daemonReadyPoll = 50 * time.Millisecond
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

// daemonProc is a running `graphi daemon start` plus its captured output, so
// every failure in this file can quote what the daemon itself said.
type daemonProc struct {
	cmd *exec.Cmd
	// out is the daemon's combined stdout+stderr. exec copies into it from a
	// goroutine, so it is only read after cmd.Wait has returned.
	out bytes.Buffer
}

// pingDaemon performs one `status` RPC round trip over the socket and returns
// nil only when the daemon SERVED it. A dial alone is not enough: the kernel
// accepts a connection into the listen backlog before the accept loop runs and
// before the host process has finished starting up (SW-275 AC-1).
//
// It deliberately does not use surfaces/daemon.DaemonClient, whose connect path
// auto-starts a daemon on dial failure — a readiness probe must never start the
// thing it is probing for.
func pingDaemon(socket string) error {
	conn, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintln(conn, `{"method":"status"}`); err != nil {
		return fmt.Errorf("write status request: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read status response: %w", err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("decode status response %q: %w", line, err)
	}
	if !resp.OK {
		return fmt.Errorf("status RPC refused: %s", resp.Error)
	}
	return nil
}

// startDaemon launches `graphi daemon start -socket S` and returns once the
// daemon has SERVED a status RPC — an observable readiness signal — or fails the
// test with a readiness failure when daemonReadyBudget expires first. It never
// returns a daemon in an unknown state.
func startDaemon(t *testing.T, bin, socket string) *daemonProc {
	t.Helper()
	d := &daemonProc{cmd: exec.Command(bin, "daemon", "start", "-socket", socket)}
	d.cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	d.cmd.Stdout = &d.out
	d.cmd.Stderr = &d.out
	if err := d.cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	deadline := time.Now().Add(daemonReadyBudget)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = pingDaemon(socket); lastErr == nil {
			return d
		}
		time.Sleep(daemonReadyPoll)
	}
	// AC-2: a distinct failure. This is "the daemon never became ready", and it
	// is reported before any signal or stop RPC has been sent, so it can never
	// be mistaken for "the daemon mishandled SIGTERM/stop".
	_ = d.cmd.Process.Kill()
	_ = d.cmd.Wait()
	t.Fatalf("READINESS FAILURE (nothing was signalled yet, so this is not a SIGTERM/stop-handling "+
		"failure): daemon never served a status RPC on %s within %v; last probe error: %v\n"+
		"--- daemon output ---\n%s", socket, daemonReadyBudget, lastErr, d.out.String())
	return nil
}

// waitExit waits for the process to exit within budget and returns its error.
func waitExit(t *testing.T, d *daemonProc, budget time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(budget):
		_ = d.cmd.Process.Kill()
		<-done
		t.Fatalf("daemon process still running after %v — the select{} zombie is back\n"+
			"--- daemon output ---\n%s", budget, d.out.String())
		return nil
	}
}

// describeExit names HOW a daemon that should have exited 0 actually ended, so a
// death by SIGTERM's default disposition (no handler installed at the moment
// the signal arrived) reads differently from a non-zero exit code.
func describeExit(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return "wait error, not an exit status"
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		if ws.Signal() == syscall.SIGTERM {
			return "the process was KILLED BY SIGTERM's default disposition: no SIGTERM handler " +
				"was installed at the moment the signal arrived (cmd/graphi must install it " +
				"before the daemon is observably ready)"
		}
		return fmt.Sprintf("the process was killed by signal %v", ws.Signal())
	}
	return fmt.Sprintf("the process exited with status %d", exitErr.ExitCode())
}

func TestDaemonLifecycle_StopRPCTerminatesTheProcess(t *testing.T) {
	bin := buildGraphi(t)
	socket := shortSocketPath(t)
	d := startDaemon(t, bin, socket)

	stop := exec.Command(bin, "daemon", "stop", "-socket", socket)
	if out, err := stop.CombinedOutput(); err != nil {
		t.Fatalf("daemon stop: %v\n%s", err, out)
	}

	if err := waitExit(t, d, daemonExitBudget); err != nil {
		t.Fatalf("STOP MISHANDLED: daemon exited non-zero after stop: %v (%s)\n--- daemon output ---\n%s",
			err, describeExit(err), d.out.String())
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket %s still exists after stop (err=%v)", socket, err)
	}

	// The lifecycle is restartable: a fresh start on the same socket succeeds.
	d2 := startDaemon(t, bin, socket)
	stop2 := exec.Command(bin, "daemon", "stop", "-socket", socket)
	if out, err := stop2.CombinedOutput(); err != nil {
		t.Fatalf("second daemon stop: %v\n%s", err, out)
	}
	if err := waitExit(t, d2, daemonExitBudget); err != nil {
		t.Fatalf("STOP MISHANDLED: second daemon exited non-zero: %v (%s)\n--- daemon output ---\n%s",
			err, describeExit(err), d2.out.String())
	}
}

func TestDaemonLifecycle_SIGTERMTerminatesTheProcess(t *testing.T) {
	bin := buildGraphi(t)
	socket := shortSocketPath(t)
	// AC-1: startDaemon returns only after the daemon has served a request, so
	// the signal below is sent to a daemon in a known state.
	d := startDaemon(t, bin, socket)

	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	if err := waitExit(t, d, daemonExitBudget); err != nil {
		t.Fatalf("SIGTERM MISHANDLED: the daemon was ready (it had served a status RPC) and still "+
			"exited non-zero after SIGTERM: %v — %s\n--- daemon output ---\n%s",
			err, describeExit(err), d.out.String())
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("SIGTERM MISHANDLED: socket %s still exists after SIGTERM (err=%v) — the handler "+
			"returned without stopping the server\n--- daemon output ---\n%s", socket, err, d.out.String())
	}
}
