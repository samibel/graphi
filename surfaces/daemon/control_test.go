package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samibel/graphi/surfaces/client"
)

// ctlStub is a minimal Handler for control-plane tests. Query echoes its args so
// proxy parity can be asserted; the rest are unused.
type ctlStub struct{}

func (ctlStub) Query(_ context.Context, op, symbol string, depth int) ([]byte, error) {
	return []byte(fmt.Sprintf(`{"op":%q,"symbol":%q,"depth":%d}`, op, symbol, depth)), nil
}
func (ctlStub) Search(context.Context, string, int) ([]byte, error)            { return nil, nil }
func (ctlStub) Compound(context.Context, string) ([]byte, error)               { return nil, nil }
func (ctlStub) SearchAST(context.Context, string, int) ([]byte, error)         { return nil, nil }
func (ctlStub) FindClones(context.Context, string) ([]byte, error)             { return nil, nil }
func (ctlStub) Savings(context.Context) ([]byte, error)                        { return nil, nil }
func (ctlStub) Memory(context.Context, client.MemoryRequest) ([]byte, error)   { return nil, nil }
func (ctlStub) Distill(context.Context, client.DistillRequest) ([]byte, error) { return nil, nil }
func (ctlStub) SkillGen(context.Context, client.SkillGenRequest) ([]byte, error) {
	return nil, nil
}

func ctlReq(t *testing.T, s *Server, method string, params any) response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return s.dispatch(context.Background(), request{Method: method, Params: raw})
}

func TestControl_TrackStatusUntrack(t *testing.T) {
	s := NewServer(ctlStub{})
	resp := ctlReq(t, s, "track", trackParams{Root: "/repo/a"})
	if !resp.OK {
		t.Fatalf("track failed: %s", resp.Error)
	}
	var got struct{ ID string }
	_ = json.Unmarshal(resp.Body, &got)
	if got.ID == "" {
		t.Fatal("track returned empty id")
	}

	st := ctlStatus(t, s)
	if len(st.Tracked) != 1 || st.Tracked[0].ID != got.ID || st.Tracked[0].Root != "/repo/a" {
		t.Fatalf("status tracked = %+v, want one /repo/a", st.Tracked)
	}

	if r := ctlReq(t, s, "untrack", untrackParams{ID: got.ID}); !r.OK {
		t.Fatalf("untrack failed: %s", r.Error)
	}
	if st := ctlStatus(t, s); len(st.Tracked) != 0 {
		t.Fatalf("status after untrack = %+v, want empty", st.Tracked)
	}
}

func ctlStatus(t *testing.T, s *Server) DaemonStatus {
	t.Helper()
	resp := ctlReq(t, s, "status", struct{}{})
	if !resp.OK {
		t.Fatalf("status failed: %s", resp.Error)
	}
	var st DaemonStatus
	if err := json.Unmarshal(resp.Body, &st); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return st
}

func TestControl_ReloadBumpsGeneration(t *testing.T) {
	s := NewServer(ctlStub{})
	if g := ctlStatus(t, s).Generation; g != 0 {
		t.Fatalf("initial generation = %d, want 0", g)
	}
	ctlReq(t, s, "reload", struct{}{})
	if g := ctlStatus(t, s).Generation; g != 1 {
		t.Fatalf("generation after reload = %d, want 1", g)
	}
	ctlReq(t, s, "reload", struct{}{})
	if g := ctlStatus(t, s).Generation; g != 2 {
		t.Fatalf("generation after 2 reloads = %d, want 2", g)
	}
}

func TestControl_StatusDeterministic(t *testing.T) {
	s := NewServer(ctlStub{})
	// Fixed clock → uptime is stable, so the whole status is deterministic.
	fixed := time.Unix(1700000000, 0).UTC()
	s.ctl = newControl(func() time.Time { return fixed })
	ctlReq(t, s, "track", trackParams{Root: "/repo/b"})
	ctlReq(t, s, "track", trackParams{Root: "/repo/a"})

	a, _ := json.Marshal(ctlStatus(t, s))
	b, _ := json.Marshal(ctlStatus(t, s))
	if string(a) != string(b) {
		t.Fatalf("status not deterministic:\n a=%s\n b=%s", a, b)
	}
	// Tracked entries are sorted by id (no map-iteration nondeterminism).
	st := ctlStatus(t, s)
	for i := 1; i < len(st.Tracked); i++ {
		if st.Tracked[i-1].ID > st.Tracked[i].ID {
			t.Fatalf("tracked not sorted by id: %+v", st.Tracked)
		}
	}
}

func TestControl_ProxyByteIdenticalToCold(t *testing.T) {
	s := NewServer(ctlStub{})
	inner, _ := json.Marshal(queryParams{Op: "callers", Symbol: "X", Depth: 2})
	cold := s.dispatch(context.Background(), request{Method: "query", Params: inner})
	warm := ctlReq(t, s, "proxy", proxyParams{Method: "query", Params: inner})
	if !warm.OK || string(warm.Body) != string(cold.Body) {
		t.Fatalf("proxy body != cold body:\n warm=%s\n cold=%s", warm.Body, cold.Body)
	}
	if r := ctlReq(t, s, "proxy", proxyParams{Method: "proxy"}); r.OK {
		t.Fatal("proxy wrapping proxy must be rejected")
	}
}

func TestControl_DaemonStopOverSocket(t *testing.T) {
	s := NewServer(ctlStub{})
	// Short temp dir to stay within the Unix socket path limit on macOS.
	dir, err := os.MkdirTemp("", "g*.sock")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	if err := s.Start(socket); err != nil {
		t.Fatalf("Start: %v", err)
	}
	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if _, err := conn.Write([]byte(`{"method":"stop"}` + "\n")); err != nil {
		t.Fatalf("write stop: %v", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read ack: %v", err)
	}
	var resp response
	if err := json.Unmarshal(line, &resp); err != nil || !resp.OK {
		t.Fatalf("stop ack = %s (err %v)", line, err)
	}
	_ = conn.Close()
	// After stop, a fresh dial must fail (listener + socket gone).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.Dial("unix", socket); err != nil {
			return // success: daemon stopped
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon still accepting connections after stop")
}

func TestGenerateServiceUnit(t *testing.T) {
	opts := ServiceOptions{Label: "com.graphi.daemon", BinaryPath: "/usr/local/bin/graphi", Args: []string{"daemon", "start"}}

	name, plist, err := GenerateServiceUnit("darwin", opts)
	if err != nil || name != "com.graphi.daemon.plist" {
		t.Fatalf("darwin: name=%q err=%v", name, err)
	}
	if !contains(plist, "com.graphi.daemon") || !contains(plist, "/usr/local/bin/graphi") {
		t.Fatalf("darwin plist missing label/binary:\n%s", plist)
	}

	name, unit, err := GenerateServiceUnit("linux", opts)
	if err != nil || name != "com.graphi.daemon.service" {
		t.Fatalf("linux: name=%q err=%v", name, err)
	}
	if !contains(unit, "ExecStart=/usr/local/bin/graphi daemon start") {
		t.Fatalf("linux unit missing ExecStart:\n%s", unit)
	}

	if _, _, err := GenerateServiceUnit("plan9", opts); err == nil {
		t.Fatal("unsupported OS must error")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
