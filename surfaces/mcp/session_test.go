package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionBinding_InitializeRootURI(t *testing.T) {
	repo := t.TempDir()
	var gotRoots []string
	server := NewServerWithBinder(func(_ context.Context, roots []string) (Binding, error) {
		gotRoots = append([]string(nil), roots...)
		return Binding{Client: allToolsClient{}}, nil
	})
	defer server.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":` + mustJSON(t, "file://"+filepath.ToSlash(repo)) + `}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if len(gotRoots) != 1 || gotRoots[0] != filepath.Clean(repo) {
		t.Fatalf("binder roots = %#v, want [%q]", gotRoots, filepath.Clean(repo))
	}
	responses := sessionResponses(t, out.Bytes())
	if responses[1].Error != nil {
		t.Fatalf("initialize error: %+v", responses[1].Error)
	}
	if responses[2].Error != nil || len(responses[2].Result) == 0 {
		t.Fatalf("bound tools/call did not succeed: %+v", responses[2])
	}
}

func TestSessionBinding_RootsListAndFailClosedUntilBound(t *testing.T) {
	repo := t.TempDir()
	var gotRoots []string
	closed := false
	server := NewServerWithBinder(func(_ context.Context, roots []string) (Binding, error) {
		gotRoots = append([]string(nil), roots...)
		return Binding{Client: allToolsClient{}, Close: func() { closed = true }}, nil
	})

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{"roots":{"listChanged":true}}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		// A tool arriving before roots/list is answered must fail, never query an
		// empty placeholder client and return a superficially successful result.
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
		`{"jsonrpc":"2.0","id":"graphi-roots-1","result":{"roots":[{"uri":` + mustJSON(t, "file://"+filepath.ToSlash(repo)) + `,"name":"fixture"}]}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"method":"roots/list"`)) {
		t.Fatalf("server never requested roots/list:\n%s", out.Bytes())
	}
	responses := sessionResponses(t, out.Bytes())
	if responses[2].Error == nil || !strings.Contains(responses[2].Error.Message, "waiting") {
		t.Fatalf("pre-bind tool must fail closed, got %+v", responses[2])
	}
	if responses[3].Error != nil || len(responses[3].Result) == 0 {
		t.Fatalf("post-bind tool call failed: %+v", responses[3])
	}
	if len(gotRoots) != 1 || gotRoots[0] != filepath.Clean(repo) {
		t.Fatalf("binder roots = %#v, want [%q]", gotRoots, filepath.Clean(repo))
	}
	server.Close()
	if !closed {
		t.Fatal("server Close did not release the bound session")
	}
}

func TestSessionBinding_NoRepositoryIsInitializeError(t *testing.T) {
	server := NewServerWithBinder(func(context.Context, []string) (Binding, error) {
		return Binding{}, errors.New("no repository could be bound")
	})
	defer server.Close()

	var out bytes.Buffer
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	responses := sessionResponses(t, out.Bytes())
	if responses[1].Error == nil || responses[1].Error.Code != -32002 {
		t.Fatalf("initialize must expose binding failure: %+v", responses[1])
	}
	if responses[2].Error == nil || len(responses[2].Result) != 0 {
		t.Fatalf("tool after failed binding must fail closed: %+v", responses[2])
	}
}

func TestSessionBinding_RootsListChangedFailsClosedAndClosesSession(t *testing.T) {
	first := t.TempDir()
	var bindings [][]string
	closed := 0
	server := NewServerWithBinder(func(_ context.Context, roots []string) (Binding, error) {
		bindings = append(bindings, append([]string(nil), roots...))
		return Binding{Client: allToolsClient{}, Close: func() { closed++ }}, nil
	})
	defer server.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":` + mustJSON(t, "file://"+filepath.ToSlash(first)) + `,"capabilities":{"roots":{"listChanged":true}}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"symbol":"Hello"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	responses := sessionResponses(t, out.Bytes())
	if responses[2].Error == nil || !strings.Contains(responses[2].Error.Message, "restart") {
		t.Fatalf("tool after roots change must require a fresh session: %+v", responses[2])
	}
	if len(bindings) != 1 || len(bindings[0]) != 1 || bindings[0][0] != filepath.Clean(first) {
		t.Fatalf("roots change must not hop repositories; bindings = %#v", bindings)
	}
	if closed != 1 {
		t.Fatalf("old binding close count = %d, want 1 before final Server.Close", closed)
	}
}

func TestLocalRootPathRejectsNonLocalURI(t *testing.T) {
	for _, raw := range []string{"https://example.com/repo", "file://remote-host/repo", "relative/repo"} {
		if _, err := localRootPath(raw); err == nil {
			t.Errorf("localRootPath(%q) succeeded, want error", raw)
		}
	}
}

func TestRejectNonLocalWindowsRootIsCrossPlatform(t *testing.T) {
	for _, local := range []string{
		`C:\Users\alice\repo`,
		`D:/src/graphi`,
		`/Users/alice/repo`,
		`/var/lib/graphi`,
	} {
		if err := rejectNonLocalWindowsRoot(local); err != nil {
			t.Errorf("rejectNonLocalWindowsRoot(%q) rejected local path: %v", local, err)
		}
	}

	for _, nonLocal := range []string{
		`\\server\share\repo`,
		`//server/share/repo`,
		`\\?\C:\repo`,
		`\\?\UNC\server\share\repo`,
		`\\.\PhysicalDrive0`,
		`//./pipe/graphi`,
		`\??\C:\repo`,
		`\Device\HarddiskVolume1\repo`,
		`/??/UNC/server/share/repo`,
		`/Device/HarddiskVolume1/repo`,
		`/GLOBAL??/C:`,
		`\GLOBAL??\C:`,
		`\DosDevices\C:`,
		`\SystemRoot\System32`,
	} {
		if err := rejectNonLocalWindowsRoot(nonLocal); err == nil {
			t.Errorf("rejectNonLocalWindowsRoot(%q) succeeded, want error", nonLocal)
		}
	}
}

func TestSessionBindingRejectsWindowsNetworkAndDeviceRootsBeforeBinder(t *testing.T) {
	for _, root := range []string{
		`\\server\share\repo`,
		`//server/share/repo`,
		`file:////server/share/repo`,
		`file:////?/C:/repo`,
		`file:////./pipe/graphi`,
		`file:///%5C%5Cserver%5Cshare%5Crepo`,
		`file:///%5CDevice%5CHarddiskVolume1%5Crepo`,
		`file://localhost/%3F%3F/UNC/server/share/repo`,
		`file://localhost/Device/HarddiskVolume1/repo`,
	} {
		t.Run(root, func(t *testing.T) {
			bindCalls := 0
			server := NewServerWithBinder(func(context.Context, []string) (Binding, error) {
				bindCalls++
				return Binding{Client: allToolsClient{}}, nil
			})
			defer server.Close()

			input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"rootUri":` + mustJSON(t, root) + `}}` + "\n"
			var out bytes.Buffer
			if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
				t.Fatal(err)
			}
			if bindCalls != 0 {
				t.Fatalf("binder called %d time(s) for rejected root", bindCalls)
			}
			response := sessionResponses(t, out.Bytes())[1]
			if response.Error == nil || response.Error.Code != -32602 {
				t.Fatalf("initialize error = %+v, want invalid params", response.Error)
			}
		})
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type sessionResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func sessionResponses(t *testing.T, out []byte) map[int]sessionResponse {
	t.Helper()
	responses := make(map[int]sessionResponse)
	for _, line := range bytes.Split(out, []byte("\n")) {
		var response sessionResponse
		if json.Unmarshal(line, &response) == nil && response.ID != 0 {
			responses[response.ID] = response
		}
	}
	return responses
}
